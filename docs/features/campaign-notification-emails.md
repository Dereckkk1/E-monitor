---
status: implementado
ultima-verificacao: 2026-06-12
codigo-relacionado:
  - workers/internal/campaignalerts/scheduler.go
  - workers/internal/campaignalerts/service.go
  - workers/internal/campaignalerts/queries.go
  - workers/internal/campaignalerts/outages.go
  - workers/internal/campaignalerts/logstore.go
  - workers/internal/campaignalerts/render.go
  - workers/internal/campaignalerts/templates/
  - workers/internal/calendar/businessdays.go
  - workers/internal/mailer/mailer.go
  - workers/internal/users/users.go
  - workers/cmd/api/main.go
  - migrations/0036_notification_log.up.sql
  - migrations/0037_stations_offline_email.up.sql
---

# Emails diários de alerta (campanhas + emissoras)

Quatro emails transacionais diários para usuários internos (role `admin` ou
`operator`, ativos, com **"Receber emails de alerta"** ligado — toggle por
usuário em `/admin/users`, default ligado). Disparo só em **dia útil**, a
partir das **08:00 BRT**, **uma vez por dia por tipo** (dedup via
`notification_log`). Reenvio diário é automático: a `notification_date` muda a
cada dia, então um alerta não resolvido reaparece no dia seguinte.

Spec de design: [docs/superpowers/specs/2026-06-09-campaign-notification-emails-design.md](../superpowers/specs/2026-06-09-campaign-notification-emails-design.md).

## Os 4 disparos

| Tipo (`notification_log.type`) | Seleção | Escopo |
|---|---|---|
| `starting_no_material` | start_date na janela **e** zero materiais (`MaterialCount == 0`) | campanha `programada` |
| `starting` | start_date na janela (todas, com ou sem material) | campanha `programada` |
| `ending` | end_date na janela | campanha `ativa` |
| `stations_offline` | emissoras com **>2h de downtime acumulado** num dia civil do período coberto | `stream_health_events` |

Cada disparo vira **um email por destinatário** (saudação personalizada). Se um
disparo não tem itens no dia, o email não é enviado, mas o dia é marcado
como `skipped_empty` para não re-tentar.

### Disparo 4 — emissoras fora do ar

Período coberto = **[meia-noite do dia útil anterior, meia-noite de hoje)** em
BRT: terça a sexta cobrem "ontem"; **segunda cobre sex+sáb+dom** — nenhuma
queda fica sem relatório, nenhuma é reportada duas vezes. O downtime soma os
eventos `down` de `stream_health_events` com **merge de intervalos
sobrepostos** (o histórico real tem downs aninhados) e fatia na meia-noite
(queda que atravessa o dia conta em cada data com sua fração). Eventos ainda
abertos contam até `now()`. Limiar fixo: 2h (`campaignalerts.OfflineThreshold`).
Visual: mesmo shell dos demais, acento âmbar, uma linha por (emissora, dia)
com a duração ("9h32 fora do ar"), CTA pra `/operations`.

## Janela de alerta

Compartilhada pelos 3 disparos, para a data-alvo (start ou end):

```
alerta = (diasCorridos(hoje, alvo) < 3) OU (diasÚteis(hoje, alvo) ≤ 2)
```

- Dias úteis = seg–sex, **sem feriados**. Implementação: `internal/calendar`.
- `diasÚteis` conta o intervalo semiaberto `(hoje, alvo]`.
- Datas no passado nunca entram.
- O job nunca envia em sábado/domingo (decisão no scheduler, `shouldRun`).

Exemplos: sexta→segunda alerta na sexta (1 dia útil); quinta→sábado alerta na
quinta (2 dias corridos); sexta→terça alerta na sexta (2 dias úteis, ponte de
fim de semana).

> Cuidado de timezone: colunas `DATE` do Postgres voltam como meia-noite UTC.
> `calendar.CivilDate` trata-as como datas civis (não converte fuso, senão UTC-3
> desloca o dia pra trás). `calendar.Today` converte o instante atual para o dia
> no Brasil. Ver comentários em `businessdays.go`.

## Configuração (env)

| Var | Default | Descrição |
|---|---|---|
| `NOTIFICATIONS_ENABLED` | `false` | liga o job (opt-in) |
| `SMTP_HOST` | `smtp.gmail.com` | host SMTP |
| `SMTP_PORT` | `587` | porta STARTTLS |
| `SMTP_USER` | — | login SMTP (email da conta Workspace) |
| `SMTP_PASS` | — | App Password do Google Workspace |
| `MAIL_FROM` | `=SMTP_USER` | header From (ex.: `E-monitor <ops@e-monitor.online>`) |
| `NOTIFICATIONS_BASE_URL` | `https://e-monitor.online` | base dos links/CTA |
| `NOTIFICATIONS_SEND_HOUR` | `8` | hora BRT de corte |
| `NOTIFICATIONS_INTERVAL` | `5m` | (opcional) intervalo do ticker; útil em teste |

Sem `SMTP_USER`/`SMTP_PASS`, o `mailer.New` retorna um **noop** que só loga — é
o comportamento de dev local (nenhum email sai). O job inteiro só sobe se
`NOTIFICATIONS_ENABLED=true`.

## Operação do SMTP (Google Workspace)

1. Conta Google da empresa → Segurança → Verificação em duas etapas → **Senhas
   de app**. Gere uma senha de 16 caracteres.
2. Setar `SMTP_USER` (o email) e `SMTP_PASS` (a App Password) no `.env` da VM.
3. Validar **end-to-end** antes de confiar (regra cultural — não confiar em
   config no papel): com uma campanha programada sem material na janela, rodar o
   `api` com `NOTIFICATIONS_ENABLED=true` e confirmar a chegada do email.

## Design visual dos emails

HTML table-based, CSS inline, ≤600px, testado em desktop e mobile. Segue o
design system E-radios (clean-light): títulos navy `#06055B` em Space Grotesk,
corpo Fira Sans Condensed (com fallback robusto pra Gmail), CTA em rosa de ação
`#E81E75`. O disparo 1 (sem material) ganha um acento âmbar de urgência (faixa
no topo + badge "0 materiais"); os disparos 2/3 são neutros/informativos.
Templates em `workers/internal/campaignalerts/templates/`. Logo:
`{BASE_URL}/E-monitor%20logo.png` (mesmo asset da sidebar).

## Idempotência e multi-réplica

- `notification_log` PK `(notification_date, type)` garante um envio por tipo/dia.
- O scheduler adquire `pg_try_advisory_lock` (chave FNV de
  `radiocheck:campaign-alerts-scheduler`) antes de processar — só uma réplica
  dispara por tick. Mesmo padrão do calibration scheduler.
- Robusto a restart/downtime: se a VM cair às 08:00, o primeiro tick após voltar
  (no mesmo dia útil) processa.

## Observabilidade

- `radiocheck_notifications_sent_total{type}`
- `radiocheck_notifications_failed_total{type}`
- `radiocheck_notifications_recipients{type}`

Falha de SMTP loga em nível `error` (sem falha silenciosa). Cada execução loga
tipo, nº de campanhas, destinatários e resultado.

## Fora de escopo

Outbox persistente de email; calendário de feriados; opt-out por usuário; emails
para clientes; alerta de campanha já `ativa` sem material.
