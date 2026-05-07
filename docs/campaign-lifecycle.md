# Ciclo de vida de campanha

> Documentação operacional da feature §18.2.1 do `plano_implementacao.md`.
> O blueprint arquitetural fica no plano; este arquivo descreve o que foi
> efetivamente entregue, como rodar, como testar e como reagir a alertas.

## Estados

| Estado       | Significado                                            | Worker roda? | Como entra                             |
| ------------ | ------------------------------------------------------ | ------------ | -------------------------------------- |
| `programada` | Cadastrada, ainda não chegou em `start_date`           | Não          | Default ao criar (migration 0011)      |
| `ativa`      | Hoje (TZ `America/Sao_Paulo`) ∈ `[start_date, end_date]` | **Sim**    | Auto: scheduler `programada → ativa`   |
| `concluida`  | Hoje > `end_date`                                      | Não          | Auto: scheduler `ativa → concluida`    |
| `cancelada`  | Encerrada manualmente antes do fim                     | Não          | `POST /v1/internal/campaigns/{id}/cancel` |

**Regra dura:** workers só rodam para campanhas em `ativa`. Os outros três
estados são equivalentes para o supervisor (worker desligado).

`cancelada` é terminal — não volta para `programada`/`ativa`. Para retomar uma
campanha cancelada por engano, criar uma nova.

## Transições

```
[criar]  ──►  programada
                  │ start_date <= today (America/Sao_Paulo)
                  ▼
                ativa
                  │ end_date < today (America/Sao_Paulo)
                  ▼
              concluida

programada / ativa  ── operador chama POST /cancel ──►  cancelada
```

## Componentes

### `migrations/0011_campaign_lifecycle.up.sql`

- Renomeia o `CHECK` de `campaigns.status` do modelo manual em inglês
  (`'planned','active','paused','ended'`) para o modelo automático em
  português (`'programada','ativa','concluida','cancelada'`).
- Atualiza dados existentes:
  - `planned` → `programada`
  - `active` → `ativa` (ou `concluida` se `end_date < hoje TZ SP`)
  - `paused` → `cancelada` (R-D: pausa nunca era retomada na prática)
  - `ended` → `concluida`
- Atualiza o `DEFAULT` da coluna para `'programada'`.
- DDL transacional. `down.sql` é melhor esforço (`cancelada → ended`).

### `workers/internal/supervisor/lifecycle_scheduler.go`

Goroutine única, criada por `Supervisor.StartLifecycle(ctx)` e disparada uma
vez no `cmd/api/main.go` após o `RestoreActive`. Roda imediatamente e depois
a cada `DefaultSchedulerInterval = 60s`.

A cada tick:

1. Em uma única transação executa os dois UPDATEs (ver §SQL abaixo) e captura
   os IDs em `RETURNING`.
2. Para cada ID em `activated`:
   - Incrementa `radiocheck_campaign_lifecycle_transitions_total{from="programada",to="ativa"}`.
   - Publica `campaign.activated` em NATS.
   - Invoca o callback in-process `Supervisor.Start(id)` (reusa o caminho
     manual e idempotente — a 2ª chamada de `UpdateStatus("ativa")` é no-op).
3. Para cada ID em `ended`:
   - Incrementa `radiocheck_campaign_lifecycle_transitions_total{from="ativa",to="concluida"}`.
   - Publica `campaign.ended` em NATS.
   - Invoca `Supervisor.stopWorkersForCampaign(id)` (apenas a metade que
     desliga workers — o status já foi movido para `concluida`).
4. Atualiza o gauge `radiocheck_campaigns_by_status{status=...}` com o snapshot
   atual (mesmo sem transições, para não ficar stale).

#### SQL principal

```sql
-- programada → ativa
UPDATE campaigns
   SET status = 'ativa', updated_at = now()
 WHERE status = 'programada'
   AND start_date <= (now() AT TIME ZONE 'America/Sao_Paulo')::date
RETURNING id;

-- ativa → concluida
UPDATE campaigns
   SET status = 'concluida', updated_at = now()
 WHERE status = 'ativa'
   AND end_date < (now() AT TIME ZONE 'America/Sao_Paulo')::date
RETURNING id;
```

Ambos rodam em uma única transação (`PromoteScheduledLifecycle` em
`internal/catalog/campaigns.go`).

#### Eventos NATS

| Subject               | Payload                                                  |
| --------------------- | -------------------------------------------------------- |
| `campaign.activated`  | `{ "campaign_id": "<uuid>", "at": "<RFC3339>" }`         |
| `campaign.ended`      | `{ "campaign_id": "<uuid>", "at": "<RFC3339>" }`         |

Quando `nats.Conn` é nulo (modo testes/dev sem NATS), um stub
(`noopBus`) só registra a transição em log — o callback in-process
continua sendo invocado, então o sistema funciona end-to-end.

### Endpoints

#### `POST /v1/internal/campaigns/{id}/cancel`

Única transição manual restante. Body: vazio.

| Resposta | Significado                                                                |
| -------- | -------------------------------------------------------------------------- |
| `204`    | Cancelamento aplicado. Workers parados. Campanha está em `cancelada`.      |
| `404`    | Campanha não encontrada.                                                   |
| `409`    | Campanha já em estado terminal (`concluida` / `cancelada`). Body: `{"error":"...","current_status":"<estado>"}`. |

A operação é atômica via CTE em `Campaigns.CancelCampaign` — leitura do estado
atual e o UPDATE acontecem na mesma query, com `FOR UPDATE`.

#### Outros endpoints relevantes

- `GET /v1/internal/campaigns?status=programada,ativa,concluida,cancelada`:
  filtro CSV opcional. Status inválido → `400`. Ordenação retornada pela API:
  `ativa` → `programada` (start_date ASC) → `concluida` → `cancelada`.
- `PUT /v1/internal/campaigns/{id}/start`: **mantido como admin/debug**
  (forçar ativação fora da janela). Use com parcimônia.
- `PUT /v1/internal/campaigns/{id}/pause`: **deprecated**, alias funcional
  de `cancel`. Mantido só para compatibilidade dos clients antigos.

### Frontend

`frontend/src/pages/CampaignsPage.jsx`:

- 4 cores fixas (`badge-ativa` verde `#10b981`, `badge-programada` cinza
  `#6b7280`, `badge-concluida` azul `#3b82f6`, `badge-cancelada` vermelho-sutil
  `#ef4444 a 60% de opacidade`).
- Botão único **Cancelar campanha** visível só em `programada`/`ativa`.
  Confirmação via `window.confirm` (provider `ConfirmModal.jsx`).
- Listagem ordenada ativa → programada (próximas a entrar) → concluida →
  cancelada.
- Tooltip nas datas: "começa em X dias" / "termina em Y dias" / "encerrou
  há Y dias".

`frontend/src/api/hooks.js`:

- `useCancelCampaign()` (`POST /campaigns/{id}/cancel`).
- `useStartCampaign` / `usePauseCampaign` foram removidos do uso público da
  página (mas permanecem no arquivo caso outras telas dependam — verificar
  antes de remover).

## Métricas Prometheus

| Métrica                                                  | Tipo    | Descrição                                                |
| -------------------------------------------------------- | ------- | -------------------------------------------------------- |
| `radiocheck_campaigns_by_status{status="..."}`           | Gauge   | Snapshot atual de campanhas por estado.                  |
| `radiocheck_campaign_lifecycle_transitions_total{from,to}` | Counter | Transições observadas pelo scheduler (cumulativo).       |

**Sugestão de alerta** (não implementado em código — basta adicionar à regra
Prometheus do projeto): scheduler que não roda há mais de 5 minutos.
Exemplo:

```promql
# Sem transição em 5 min E gauge não muda E o processo não morreu →
# o ticker travou. (Para detecção rápida usar um heartbeat dedicado.)
absent_over_time(radiocheck_campaigns_by_status[5m])
```

## Como rodar

A goroutine sobe automaticamente quando `cmd/api` inicia:

```go
sup := supervisor.New(...)
sup.RestoreActive(ctx)
sup.StartLifecycle(ctx) // <- aqui
```

Sem flag, sem variável de ambiente. O intervalo é constante; se for preciso
ajustar para testes, use `LifecycleScheduler.SetInterval(d)`.

## Como testar

### Manual ponta-a-ponta

1. Aplicar `migrations/0011_campaign_lifecycle.up.sql` em uma base de teste.
2. Subir `cmd/api`.
3. Criar uma campanha com `start_date = hoje`, `end_date = hoje + 1d`.
4. Em até 60s o scheduler deve mover a campanha para `ativa` e o supervisor
   deve subir os workers. Confirmar:
   - `SELECT status FROM campaigns ...` → `ativa`.
   - `radiocheck_campaign_lifecycle_transitions_total{from="programada",to="ativa"}`
     deve ter incrementado.
   - Em `/v1/internal/workers` os workers devem aparecer.
5. No dia seguinte, a campanha deve cair para `concluida` automaticamente.

### Crítério de aceite (§18.2.1)

- Campanha criada com `start_date = amanhã` fica em `programada` até a
  meia-noite (TZ SP), sobe automaticamente para `ativa`, monitora normalmente,
  e cai para `concluida` no fim do `end_date` sem intervenção. **OK**.
- Estado `ativa` é precondição estrita pra worker rodar — `Supervisor.Start`,
  `RestoreActive`, `ActiveCampaignsForStation`, `index.loader` e `cmd/diag`
  filtram por `'ativa'`. **OK**.

## Riscos e cuidados (§18.2.1)

### R-A — Janela de tempo crítica

Se uma campanha entra em vigor às 23:59:59 e o scheduler roda a cada 60s,
pode haver até ~1 min de "ar perdido" no início. Aceitável para a fase atual.
Em produção, considerar adiantar a ativação para `start_date 00:00 - 5min`.

### R-B — Campanhas em massa expirando juntas

50 campanhas terminando à meia-noite causam 50 chamadas a
`stopWorkersForCampaign` em sequência. Hoje é serializado dentro do callback;
se virar gargalo, introduzir um throttle (máx 5 reload concorrentes).

### R-C — Time zone

`start_date` e `end_date` são `DATE` (sem hora). Todas as comparações
usam `(now() AT TIME ZONE 'America/Sao_Paulo')::date`. **Não dependa do TZ
do servidor** — a expressão é literal nos UPDATEs do scheduler.

### R-D — Compatibilidade com dados existentes

A migration 0011 mapeia `paused` → `cancelada` (semântica mais próxima do
uso real, conforme §18.2.1). Antes de aplicar em produção, listar
campanhas em `paused` e validar com o operador que nenhuma delas era
"pausa temporária com retomada planejada" — esse caso de uso não é mais
suportado pelo modelo (cancelada é terminal).

```sql
-- Dry-run: identificar campanhas pausadas que serão coladas em 'cancelada'.
SELECT id, name, start_date, end_date, updated_at
  FROM campaigns
 WHERE status = 'paused';
```
