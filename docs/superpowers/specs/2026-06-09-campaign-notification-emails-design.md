# Design — Emails diários de alerta de campanha para admins

**Data:** 2026-06-09
**Status:** spec aprovada para implementação (pendente revisão do usuário)
**Autor:** brainstorming Claude + Dereck

## 1. Objetivo

Disparar automaticamente **3 tipos de email diários** para todos os usuários internos (admin/operator) sobre o ciclo de vida das campanhas:

1. **Disparo 1 — Campanhas iniciando SEM material:** lembrar a equipe de cadastrar materiais antes do início.
2. **Disparo 2 — Campanhas iniciando:** lista das campanhas que vão começar.
3. **Disparo 3 — Campanhas terminando:** lista das campanhas que vão acabar.

Substitui o controle manual/mental do time. Reenvio diário enquanto a condição persistir (ex.: disparo 1 volta no dia seguinte se ninguém cadastrou material).

Não é um objetivo do `plano_implementacao.md` (§1.3) — é feature operacional interna; documentação final vai em `docs/features/`.

## 2. Decisões travadas (brainstorming 2026-06-09)

| Tema | Decisão |
|---|---|
| Transporte de email | SMTP do Google Workspace/Gmail (`smtp.gmail.com:587` + STARTTLS) |
| Confiabilidade | **Abordagem A**: envio direto com retry curto + `notification_log` para idempotência (sem outbox/worker) |
| "Sem material" | `MaterialCount == 0` (zero linhas em `campaign_materials`) |
| Dias úteis | Seg–Sex, **sem feriados** |
| Janela de fim de semana | Job nunca envia em sábado/domingo |
| Destinatários | `users` ativos com role `admin` **ou** `operator` |
| Horário de envio | 08:00 `America/Sao_Paulo` |
| Formato | 3 emails separados; disparo 2 lista TODAS que iniciam (com ou sem material) |
| Base URL (links/CTA) | `https://e-monitor.online/` (config `NOTIFICATIONS_BASE_URL`) |
| Logo do email | `https://e-monitor.online/E-monitor%20logo.png` (mesmo asset da sidebar) |
| Disparo 1 escopo | só campanhas `programada` (ainda não iniciaram) |
| Disparo 3 escopo | só campanhas `ativa` |

## 3. Regra de janela (compartilhada)

O job roda diariamente mas **só envia em dia útil**. Para a data de hoje (`hoje`, em BRT) e uma `data-alvo` (start_date para disparos 1/2, end_date para disparo 3):

```
alerta = (diasCorridos(hoje, alvo) < 3) OU (diasÚteis(hoje, alvo) ≤ 2)
```

- `diasCorridos(hoje, alvo)` = diferença em dias de calendário (`alvo − hoje`).
- `diasÚteis(hoje, alvo)` = número de dias úteis (seg–sex) no intervalo **semiaberto `(hoje, alvo]`** — conta o dia-alvo se for útil, não conta hoje.
- Campanhas com `alvo < hoje` (já passou) **não** entram (janela é prospectiva). Campanhas com `alvo == hoje` entram (`diasCorridos == 0 < 3`).

### Validação contra cenários do usuário

| Cenário | hoje | alvo | diasCorridos | diasÚteis(hoje,alvo] | Alerta? |
|---|---|---|---|---|---|
| 1 | Sexta | Segunda | 3 (não<3) | 1 (≤2) | ✅ sexta. Sáb/dom não envia. |
| 2 | Quinta | Sábado | 2 (<3) | 1 | ✅ quinta |
| 3 | Segunda | Terça | 1 (<3) | 1 | ✅ |

Outros casos cobertos pela cláusula de dias úteis (ponte sobre o fim de semana): Sexta→Terça (úteis=2 ✅ alerta sexta), Quinta→Segunda (úteis=2 ✅ alerta quinta).

## 4. Arquitetura

### 4.1 Componentes novos

- **`workers/internal/mailer/`** — cliente SMTP reusável.
  - `type Mailer interface { Send(ctx, to []string, subject, htmlBody, textBody string) error }`
  - Impl `smtpMailer` usando `net/smtp` com STARTTLS para `smtp.gmail.com:587`. Auth `PlainAuth`.
  - Construído a partir de config; se `NOTIFICATIONS_ENABLED=false` ou credenciais ausentes → `noopMailer` que loga e não envia (dev local).
- **`workers/internal/calendar/`** — utilitário de datas úteis (puro, sem deps externas).
  - `func IsBusinessDay(d time.Time) bool` — false para Sat/Sun.
  - `func BusinessDaysUntil(today, target time.Time) int` — conta dias úteis em `(today, target]`. Negativo/zero se `target <= today`.
  - `func CalendarDaysUntil(today, target time.Time) int`.
  - `func InAlertWindow(today, target time.Time) bool` — `CalendarDaysUntil < 3 || BusinessDaysUntil <= 2`, e `target >= today`.
  - Todas operam em datas truncadas para `America/Sao_Paulo`.
- **`workers/internal/campaignalerts/`** — o miolo.
  - `queries.go`: 3 funções de consulta (ver §4.3).
  - `render.go`: renderização dos 3 templates HTML+texto (`html/template` + `text/template`), embutindo logo/CTA/dados.
  - `templates/`: `starting_no_material.html`, `starting.html`, `ending.html` (+ versões `.txt`). HTML construído via skill `/impeccable` (shape → critique → craft).
  - `scheduler.go`: ticker + lógica de janela diária + advisory lock + dedup (ver §4.4).
  - `service.go`: orquestra (busca destinatários, monta cada disparo, chama mailer, grava log).

### 4.2 Migration

`migrations/00NN_notification_log.up.sql`:

```sql
CREATE TABLE notification_log (
    notification_date DATE NOT NULL,
    type              TEXT NOT NULL CHECK (type IN ('starting_no_material','starting','ending')),
    sent_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    recipient_count   INT NOT NULL DEFAULT 0,
    campaign_count    INT NOT NULL DEFAULT 0,
    status            TEXT NOT NULL DEFAULT 'sent' CHECK (status IN ('sent','partial','failed','skipped_empty')),
    error             TEXT,
    PRIMARY KEY (notification_date, type)
);
```

A PK `(notification_date, type)` garante **um envio por tipo por dia**. `status='skipped_empty'` registra dias sem campanhas (não manda email, mas marca como processado para não re-tentar no mesmo dia). Migration `.down.sql` faz `DROP TABLE`.

> Ler `docs/operations/migrations.md` antes de criar a migration (regra do projeto).

### 4.3 Consultas

Todas filtram `clients`/`campaigns` não deletados e usam `MaterialCount` via subquery existente (`workers/internal/catalog/campaigns.go`).

- **Disparo 1 (starting_no_material):** `status='programada'` E `InAlertWindow(hoje, start_date)` E `MaterialCount == 0`.
- **Disparo 2 (starting):** `status='programada'` E `InAlertWindow(hoje, start_date)`.
- **Disparo 3 (ending):** `status='ativa'` E `InAlertWindow(hoje, end_date)`.

A filtragem de janela pode ser feita em SQL (faixa de datas) com refinamento de dias úteis em Go, ou inteira em Go sobre o conjunto de candidatas (volume baixo — poucas campanhas/dia). **Decisão:** buscar candidatas por faixa larga de `start_date/end_date` (`BETWEEN hoje AND hoje+4`) em SQL e aplicar `InAlertWindow` em Go (testável, sem replicar a regra de dias úteis no SQL).

Cada campanha no email mostra: nome, cliente, start/end date, nº de emissoras-alvo, e (disparo 1) destaque "0 materiais".

### 4.4 Scheduler

```
ticker a cada 5 min:
  agora := time.Now() em America/Sao_Paulo
  se !IsBusinessDay(agora): continue
  se hora(agora) < 08:00: continue
  para cada tipo em [starting_no_material, starting, ending]:
    se já existe notification_log(hoje, tipo): continue   // dedup
    tenta pg_try_advisory_lock(hash(tipo))                // multi-instância
      calcula campanhas; busca admins
      se campanhas vazias: grava log status='skipped_empty'; continue
      renderiza + envia (1 email por destinatário, retry 3x backoff)
      grava notification_log com recipient_count/campaign_count/status
    libera lock
```

Robusto a restart e a downtime: se a VM cair às 08:00, o primeiro tick após voltar (ainda no mesmo dia útil) processa. O `notification_log` impede reenvio no mesmo dia. No dia seguinte, nova `notification_date` ⇒ reenvio natural.

**Idempotência fina:** o `INSERT` no `notification_log` é a barreira; combinado com advisory lock, evita corrida entre instâncias. Inserir o log **antes** de enviar (status inicial `sent`/atualizado depois) vs **depois**: inserimos **depois** do envio para refletir resultado real, mas adquirimos o advisory lock **antes** para serializar. Risco de duplo-envio só existe se o processo morrer entre enviar e gravar log — aceitável (raro, e o pior caso é um email repetido, não perda).

### 4.5 Config nova (`workers/internal/config/config.go`)

| Env var | Default | Uso |
|---|---|---|
| `NOTIFICATIONS_ENABLED` | `false` | liga/desliga o job inteiro |
| `SMTP_HOST` | `smtp.gmail.com` | host SMTP |
| `SMTP_PORT` | `587` | porta STARTTLS |
| `SMTP_USER` | — | usuário/login SMTP |
| `SMTP_PASS` | — | senha (App Password do Workspace) |
| `MAIL_FROM` | `SMTP_USER` | remetente (nome + email) |
| `NOTIFICATIONS_BASE_URL` | `https://e-monitor.online` | base dos links/CTA |
| `NOTIFICATIONS_SEND_HOUR` | `8` | hora local de corte (BRT) |

Sem credenciais → mailer noop (dev local não envia).

### 4.6 Wiring (`workers/cmd/api/main.go`)

Seguindo o padrão dos outros jobs periódicos:

```go
if cfg.NotificationsEnabled {
    alertSvc := campaignalerts.NewService(pool, mailer, usersRepo, campaignsRepo, cfg, logger)
    go alertSvc.RunScheduler(ctx)
}
```

## 5. Design visual dos emails (`/impeccable`)

HTML de email: table-based layout, CSS inline, largura ≤ 600px, dark-mode-safe, testado em Gmail/Outlook web.

Anatomia comum:
- **Header:** logo E-monitor (`https://e-monitor.online/E-monitor%20logo.png`) sobre fundo claro.
- **Headline** forte por tipo de disparo (ex.: "3 campanhas começam sem material").
- **Intro** curta personalizada ("Olá {nome}, …").
- **Corpo:** campanhas como cards/linhas — nome, cliente, datas, nº emissoras; disparo 1 destaca "0 materiais".
- **CTA primário** deep-link: disparo 1 → `…/campaigns/{id}` (ou wizard de materiais); disparos 2/3 → `…/campaigns`.
- **Footer:** identidade E-monitor + nota de que é alerta automático.

Inspiração nos refs anexados (Webflow Conf / Mobbin / Semrush): tipografia bold, hierarquia limpa, um CTA primário, cards com borda fina.

> O HTML será produzido com a skill `/impeccable` em 3 passos — **shape** (estrutura/wireframe inspirado nos refs), **critique** (auditoria), **craft** (HTML final inline). Esta etapa acontece na implementação.

## 6. Testes

- **`calendar`:** unit tests cobrindo os 3 cenários do usuário + bordas (alvo==hoje, alvo passado, sex→ter, qui→seg, fim de semana como alvo).
- **`campaignalerts/queries`:** testes de integração (ou com fixtures) validando os 3 filtros de seleção e o corte de status.
- **`scheduler`:** teste de dedup (segundo tick no mesmo dia não reenvia) e de skip em fim de semana / antes das 08:00, injetando relógio.
- **`mailer`:** noop em dev; teste manual end-to-end com SMTP real antes de confiar (regra cultural do projeto — "backup que nunca rodou").
- **render:** golden test do HTML/texto de cada disparo.

## 7. Observabilidade

- Métricas Prometheus: `notifications_sent_total{type}`, `notifications_failed_total{type}`, `notifications_recipients{type}`.
- Log estruturado por execução (tipo, nº campanhas, nº destinatários, resultado). Falha de SMTP loga em nível `error` — sem falha silenciosa.

## 8. Documentação

Ao implementar: criar `docs/features/campaign-notification-emails.md` com header YAML (`status: implementado`, `codigo-relacionado`), e adicionar linha no `docs/README.md` e na tabela de consulta do `CLAUDE.md`. Operação do SMTP (App Password, env vars) em `docs/operations/`.

## 9. Suposições confirmadas

- Disparo 1 = só `programada`; campanha já `ativa` sem material está **fora** deste escopo.
- Disparo 3 = só `ativa`.
- Reenvio diário é desejado e é o comportamento natural (nova `notification_date`).
- Volume de campanhas/dia é baixo → filtragem em Go é aceitável.

## 10. Fora de escopo (YAGNI)

- Outbox/worker persistente de email (Abordagem B).
- Calendário de feriados.
- Preferências por-usuário de opt-out/digest.
- Emails para clientes (só admins/operators internos).
- Alerta de campanha já ativa sem material.
