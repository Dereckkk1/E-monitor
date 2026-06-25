---
status: implementado
ultima-verificacao: 2026-06-25
codigo-relacionado:
  - workers/internal/supervisor/connect_backoff.go
  - workers/internal/supervisor/connect_backoff_test.go
  - workers/internal/supervisor/supervisor.go
  - workers/internal/metrics/metrics.go
---

# Circuit breaker de conexão (backoff para streams que nunca conectam)

## Problema

Quando o IP de saída do worker é **bloqueado no firewall** de uma emissora/painel
(o `connect` TCP dá **timeout silencioso**, não `403`), o ffmpeg fica retentando
internamente via `-reconnect` **para sempre** — ele nunca sai. Logo o loop de
reconexão do próprio worker ([worker.go](../../workers/internal/ingestor/worker.go),
backoff até 60s) **nunca itera**: `runPCMReader` bloqueia em `ReadFull`. A única
coisa que quebra o ciclo é o **stall watchdog**, que respawna o worker a cada
~2min (grace de startup) e, no respawn, **reseta** o backoff.

Resultado antes desta feature: **~9 connects TCP a cada 2min, 24/7, por emissora
bloqueada**. Esse martelo contínuo, vindo de um **IP de datacenter estático
único**, construiu reputação de abuso em vários painéis brasileiros.

### Incidente jun/2026

Diagnóstico que originou a feature (região GCP `southamerica-east1`, IP estático
`34.39.163.110`):

- 5 emissoras com `connect` TCP em timeout silencioso, em **2 provedores**
  diferentes (livespanel ×4 + streamingdevideo ×1) → **ban de abuso direcionado
  ao nosso IP**, não geo-block (geo daria `403`; SP geolocaliza como BR).
- A `Piatã FM` (livespanel:9430) ficou saudável por semanas e caiu de uma vez em
  `2026-06-20 11:28 UTC`, sem mais voltar — assinatura de bloqueio deliberado, e
  não de flapping nosso.
- A mesma URL respondia `200 OK` de um IP residencial → confirmou que o alvo é o
  **IP**, não UA/URL/codec.

Conclusão: o martelo sem backoff é causa-raiz **parcial e controlável** do ban.
Esta feature ataca essa parte; o desbloqueio em si depende de allowlist do IP
e/ou egress BR (plano §14.3).

## Como funciona

O breaker vive na decisão de respawn do stall watchdog
([supervisor.go](../../workers/internal/supervisor/supervisor.go),
`runStallWatchdog`). O `isStalled` já distingue dois sabores de stall:

| Sabor | Condição | Significado | Tratamento |
|-------|----------|-------------|------------|
| **A** | `LastPCMAt` setado mas >60s velho | tocava e parou (flap/transitório) | cooldown fixo de **2min** (inalterado) |
| **B** | `LastPCMAt` zero além do grace (2min) | **nunca conectou** (IP bloqueado / URL morta) | **backoff exponencial** ← esta feature |

Para o sabor B, a cada respawn:

1. **Mata o worker agora** (`cancel()`) → ffmpeg morre → para de martelar o
   firewall durante a espera.
2. Incrementa `connectFailures[station]` e calcula
   `delay = jitterDelay(backoffFor(failures))`.
3. Estaciona um **placeholder de backoff** em `workers[station]` (um `workerEntry`
   com `worker==nil` e `cancel = boCancel`) para que `Pause` / `StopWorkersForCampaign`
   / restart preventivo possam **abortar** o respawn pendente. Como `worker==nil`,
   o placeholder não conta como ativo (`WorkerStatuses` o ignora) e não
   incrementa/decrementa `WorkerActive` indevidamente (os `Dec` foram guardados
   com `if entry.worker != nil`).
4. Agenda o `startStationWorker` para depois de `delay`. **Durante a espera não há
   ffmpeg rodando** = zero martelada.

O `onStreamUp` zera `connectFailures[station]` no instante em que a emissora
produz PCM de novo — então uma emissora que recupera (ou recebe allowlist) volta
à cadência normal imediatamente.

### Schedule (`backoffFor`)

Exponencial a partir de `connectBackoffBase = 1min`, dobrando, com teto
`connectBackoffMax = 30min`:

| Falhas consecutivas | Delay |
|---------------------|-------|
| 0 | 0 (restart imediato — comportamento de hoje p/ o 1º stall) |
| 1 | 1min |
| 2 | 2min |
| 3 | 4min |
| 4 | 8min |
| 5 | 16min |
| 6+ | 30min (teto) |

Mais `±20%` de jitter (`jitterDelay`) para emissoras co-bloqueadas pelo mesmo
painel não retentarem em lockstep.

**Duty cycle no teto:** ~2min de tentativa (o grace de startup) a cada ~32min →
corte de **~16×** versus o respawn-a-cada-2min antigo.

**Latência de recuperação:** ≤ teto + grace (~32min) depois que a emissora é
liberada. Veja Follow-ups para o "kick" manual.

## Observabilidade

- **Métrica:** `radiocheck_worker_connect_backoff_seconds{station_id}` (gauge) — o
  delay atual; `0` = conectado/normal. Uma emissora colada no teto (1800s) está
  com IP bloqueado ou URL morta. Exposta no `/operations`.
- **Log:** `supervisor: worker stall detected, restarting` agora carrega
  `never_connected=true/false` e `backoff=<dur>`.
- O contador cumulativo `radiocheck_worker_stall_restarts_total` continua somando
  (um respawn com backoff ainda é um stall restart).

## Relação com o resto da solução

Esta feature **não desbloqueia** a emissora — só para a sangria e é
**pré-requisito** para qualquer unban "colar" (sem ela, o worker re-dispara o ban
assim que o painel libera). O desbloqueio em si:

1. **Allowlist** do IP estático (`34.39.163.110`) com o painel — tático.
2. **Egress BR / proxy** (plano §14.3) — durável, só se o padrão multi-painel
   reincidir mesmo com a gente "comportada".

## Follow-ups (não implementados)

- **Kick manual:** endpoint admin para forçar respawn imediato (abortando o
  placeholder de backoff) quando um allowlist for concedido, evitando esperar o
  teto de 30min. Hoje, reiniciar o processo `api` (`RestoreActive`) é o atalho
  bruto.
- **Detecção de não-conexão mais rápida:** hoje ainda há ~2min de grace (≈8
  connects ffmpeg) por ciclo antes do breaker agir. Encurtar isso sem matar
  startups lentos legítimos é um ganho secundário.

## Testes

`workers/internal/supervisor/connect_backoff_test.go`: schedule exato,
monotonicidade + teto, constantes fixadas, e limites do jitter. O wiring no
`runStallWatchdog` segue o padrão do pacote (lógica pura testada; orquestração
revisada por leitura — ver `stall_watchdog_test.go`).
