---
status: implementado
ultima-verificacao: 2026-05-15
codigo-relacionado:
  - workers/internal/supervisor/reconcile.go
  - workers/internal/supervisor/supervisor.go
  - workers/internal/supervisor/station_changes.go
  - workers/internal/ingestor/worker.go
  - workers/internal/index/loader.go
  - workers/internal/api/handlers/campaigns.go
  - workers/internal/api/handlers/stations.go
  - workers/internal/metrics/metrics.go
---

# Worker reconciler

> **Em uma linha:** o supervisor checa a cada 30s se a lista de comerciais
> **e** a `stream_url` que cada worker está usando batem com o que está no
> banco. Se algum dos dois divergiu, reinicia o worker. Existe pra impedir
> que o sistema fique cego para um comercial novo ou pra uma URL atualizada
> sem ninguém perceber.
>
> O nome do arquivo é histórico ("commercial-reconciler"): a versão original
> só reconciliava comerciais; a partir de 2026-05-15 também reconcilia
> `stream_url`. A goroutine no código se chama `runWorkerReconciler`.

## Por que isso existe

Em **2026-05-08**, dois jingles veiculados na 89 FM Joinville (06:43 e 07:02
local) não foram detectados pelo Radiocheck. A causa raiz foi que o worker da
estação rodava com uma lista de `CommercialShortIDs` desatualizada — o
comercial existia, tinha fingerprint pronto, tinha `target_stations` apontando
para a estação, e suas hashes estavam no índice em memória — mas o worker
nunca tinha sido reiniciado depois que esses comerciais foram vinculados,
então não tinha máquina de estado para confirmar a detecção.

A arquitetura tinha três fragilidades encadeadas:

1. **`WorkerConfig.CommercialShortIDs` é congelado em `startStationWorker`.**
   O worker matcheia só contra os shorts que estavam na lista no momento em
   que ele subiu — qualquer comercial vinculado depois fica invisível para
   ele até alguém disparar um restart.
2. **O caminho que deveria disparar esse restart (`PUT /commercials/{id}/stations`
   → `Supervisor.Reload`) tinha o erro silenciado por `_ = h.Supervisor.Reload(...)`.**
   Qualquer falha (DB hiccup, supervisor mid-restart, race com lifecycle
   scheduler) deixava o worker velho rodando sem nenhum sinal.
3. **Sem métrica.** Não havia como saber, nem em logs nem em Grafana, que um
   worker estava rodando com lista vazia ou parcial.

## O que o reconciler faz

A cada 30 segundos, para cada worker em execução:

1. Chama `stations.Get(stationID)` pra obter a `stream_url` atual.
2. Chama `ListReadyByCampaignsForStation(activeCampaigns, station)` pra obter
   o set de `short_id` (comerciais + materiais) que o worker deveria estar
   matcheando.
3. Compara `(station.StreamURL, wantedIDs)` com
   `(worker.StreamURL(), worker.CommercialShortIDs())` via
   [`reconcileReason`](../../workers/internal/supervisor/reconcile.go).
4. Se a função retornar string vazia → atualiza a métrica
   `radiocheck_worker_commercials` com a contagem corrente e dorme até o
   próximo tick.
5. Caso contrário → loga `supervisor.reconcile: drift detected — restarting
   worker` com o motivo (`"stream_url changed"`, `"commercial list changed"`
   ou `"stream_url and commercial list changed"`), cancela o context do
   worker, remove do mapa e respawna via `startStationWorker(stationID)`. O
   reconciler atual termina; o worker novo spawna o seu próprio.

A comparação de comerciais é insensível a ordem (os SELECTs não têm
`ORDER BY`) e a duplicatas (são tratadas como o mesmo elemento). Veja
[`reconcile.go::commercialSetEqual`](../../workers/internal/supervisor/reconcile.go).

### Por que a URL drift também vive aqui

A `stream_url` é congelada na `WorkerConfig` no momento em que
`startStationWorker` lê o banco — exatamente o mesmo padrão de snapshot que
deu origem ao incidente 2026-05-08 com comerciais. Quando um operador edita
`stream_url` via `PUT /v1/internal/stations/{id}`, o handler escreve no
banco mas não dispara reload do worker. O ffmpeg continua tentando reconnect
na URL antiga indefinidamente, nunca produz PCM válido e o sistema mostra a
emissora vermelha em `/monitoring` enquanto o botão de play em `/stations`
(que lê a URL fresca do banco) funciona normalmente.

Estender o reconciler em vez de criar um caminho push novo no handler é a
escolha arquitetural deliberada — o reconciler já é a **rede de segurança
de convergência banco↔worker** e o comentário histórico em [`reconcile.go`](../../workers/internal/supervisor/reconcile.go)
documenta que tentativas anteriores de "Reload via handler" silenciaram
erros e mascararam bugs. Aceitar latência de até 30s pra uma operação que é
raríssima (operador editando URL) é um trade-off explícito.

## Janela de detecção perdida

No pior caso, um comercial recém-vinculado a uma estação cuja chamada de
`Reload` falhou silenciosamente fica invisível por **até 30 segundos**. É o
trade-off explícito documentado em
[`reconcile.go::reconcileInterval`](../../workers/internal/supervisor/reconcile.go).
Antes do reconciler, essa janela era infinita até o próximo restart do worker
(que podia nunca acontecer).

Reduzir o intervalo barateia mais detecções perdidas mas custa em QPS no
Postgres: 200 estações × `1/intervalo` queries por segundo. Em 30s isso é
~6,7 QPS, confortavelmente baixo. Em 5s seriam 40 QPS — começa a competir
com tráfego real.

## Métricas Prometheus

Duas métricas novas vivem em
[`internal/metrics/metrics.go`](../../workers/internal/metrics/metrics.go):

| Métrica | Tipo | Significado |
|---------|------|-------------|
| `radiocheck_worker_commercials{station_id}` | gauge | Quantos comerciais o worker tem carregados agora. |
| `radiocheck_worker_reconcile_runs_total{station_id, outcome}` | counter | `unchanged` \| `restarted` \| `error` por tick. |

### Alerta recomendado

```promql
# Worker rodando há mais de 2min sem nenhum comercial — algo está furado.
# Filtra por estações que pertencem a campanhas ativas (todas com worker).
radiocheck_worker_commercials == 0
```

A presença de série já implica que o worker está vivo (a métrica é setada
no `startStationWorker` e limpa quando o worker para). Se chegou a 0 e ficou
mais que 2 ticks do reconciler (60s), tem desincronia que o reconciler
não consegue resolver sozinho — provavelmente comercial sem fingerprint
ready, ou `target_stations` realmente vazio.

### Sinal de drift contínuo

```promql
# Uma estação está restartando todo tick — Reload está sendo chamado em loop
# por algum job, ou o set tá oscilando.
rate(radiocheck_worker_reconcile_runs_total{outcome="restarted"}[5m]) > 0.05
```

## Onde a regra mora

- **Constante de intervalo:**
  [`reconcileInterval` em `reconcile.go`](../../workers/internal/supervisor/reconcile.go).
  Pinned por teste em `reconcile_test.go::TestReconcileInterval_Constant`.
- **Decisão de restart:**
  [`reconcileReason` em `reconcile.go`](../../workers/internal/supervisor/reconcile.go).
  Função pura que recebe `(currentURL, wantedURL, currentIDs, wantedIDs)` e
  retorna string com o motivo (vazia = não restartar). Coberto por
  `reconcile_test.go::TestReconcileReason`.
- **Comparador de comerciais:**
  [`commercialSetEqual` em `reconcile.go`](../../workers/internal/supervisor/reconcile.go).
  Coberto por `reconcile_test.go::TestCommercialSetEqual` (10 cenários
  incluindo nil/empty, ordem, duplicata, sub/super-set).
- **Goroutine spawnada por worker:**
  `runWorkerReconciler` é chamada dentro de `startStationWorker`
  ([`supervisor.go`](../../workers/internal/supervisor/supervisor.go)) ao lado
  do stall watchdog e do threshold refresher. Mesmo padrão de cancelamento:
  `workerCtx.Done()` → exit.
- **Snapshots da configuração para comparação:**
  [`Worker.CommercialShortIDs()`](../../workers/internal/ingestor/worker.go)
  e [`Worker.StreamURL()`](../../workers/internal/ingestor/worker.go) em
  `ingestor/worker.go`. Os getters retornam cópia/string imutável — o
  reconciler pode mutar livremente.

## Defesas em cima do reconciler

O reconciler é a **rede de segurança**, não a primeira linha. Os caminhos
"síncronos" continuam:

1. `PUT /v1/internal/commercials/{id}/stations` → `Supervisor.Reload(campaignID)`.
2. `PUT /v1/internal/campaigns/{id}/stations` → `Pause` + `UpdateTargetStations` + `Start`.
3. `POST /v1/internal/campaigns/{id}/start` → `Supervisor.Start`.
4. Lifecycle scheduler `programada → ativa` → `OnActivated` → `Supervisor.Start`.
5. `RestoreActive` no startup → `Supervisor.Start` para cada campanha ativa.

Pós-incidente, todos os call-sites do `Reload`/`Pause`/`Start` nos handlers
agora **logam erros explicitamente** ([`commercials.go`](../../workers/internal/api/handlers/commercials.go),
[`campaigns.go`](../../workers/internal/api/handlers/campaigns.go)) — antes
estavam com `_ = h.Supervisor.X(...)`. Se o reconciler estiver consertando
algo silenciosamente, o log de error correspondente deve aparecer minutos
antes; é assim que se diferencia "Reload tá com bug" de "Reload está OK,
foi um caso de borda novo".

## Como verificar manualmente

```bash
# 1. Quantos comerciais cada worker tem agora.
curl -s http://localhost:9090/api/v1/query \
  --data-urlencode 'query=radiocheck_worker_commercials' | jq

# 2. Logs do reconciler (warnings = drift detectado, info = startup).
docker compose -f infra/docker/docker-compose.yml logs api --since 30m \
  | grep "supervisor.reconcile"

# 3. Forçar drift de propósito (sanidade): troque target_stations de um
# comercial via SQL direto (bypassa o Reload do handler), espere 30s e
# verifique nos logs:
#
#   supervisor.reconcile: commercial list drift — restarting worker
#     current_short_ids=[8 9] wanted_short_ids=[8]
```

## Bugs adjacentes corrigidos no mesmo lote

O reconciler endereça a falha de hot-reload, mas o incidente 2026-05-08
expôs duas armadilhas correlatas que valem registro:

### "Todas" no upload em massa enviava `target_stations=[]`

[`frontend/src/pages/CampaignsPage.jsx`](../../frontend/src/pages/CampaignsPage.jsx)
representa o estado da seleção como:

- `null` → não escolhido (botão Enviar fica desabilitado)
- `[]`   → "Todas" (UI)
- `[ids]` → específicas

O `submitAll` antigo só chamava `PUT /commercials/{id}/stations` se
`stationIds.length > 0`. Resultado: ao escolher "Todas", o material era
criado com `target_stations=[]`, e o backend
([`workers/internal/catalog/commercials.go`](../../workers/internal/catalog/commercials.go)
em `ListReadyByCampaignsForStation`) trata isso como **inativo** — material
não detectável em nenhuma estação. UI exibia "Inativo" no card, mas era
fácil não notar.

Fix: ao submeter, se `entry.stations` estiver vazio e a campanha tem
emissoras, expandimos para a lista atual de `campaignStations` antes de
mandar o PUT. Comentário inline marca o link com este incidente.

### Filtro de status do loader era exclusivamente `ativa`

O loader que popula o índice em memória (em
[`workers/internal/index/loader.go`](../../workers/internal/index/loader.go))
filtrava commercials por `ca.status = 'ativa'` tanto no `LoadAll` (boot)
quanto no subscriber `index.reload`. Consequência: se um fingerprint
terminava ANTES da campanha transicionar para `ativa`, a publicação de
reload era silenciosamente rejeitada. A rede de segurança era
`Supervisor.Start` republicar reload no momento da ativação — mas se essa
publicação falhasse (NATS hiccup, race com lifecycle scheduler), as hashes
nunca chegavam à memória e o worker, quando subia, matcheia contra um
índice incompleto.

Fix: a constante
[`indexEligibleStatuses`](../../workers/internal/index/loader.go) agora é
`('programada', 'ativa')`. Hashes são pré-carregadas para campanhas
programadas, então quando o lifecycle scheduler vira a chave
`programada → ativa` o índice já está quente. `concluida` e `cancelada`
continuam excluídas (terminais — nenhum worker matcheia contra elas).
Pinned por
[`loader_test.go::TestIndexEligibleStatuses`](../../workers/internal/index/loader_test.go).

### `Pause+Start` ao editar `target_stations` da campanha

`PUT /campaigns/{id}/stations` antigo fazia `Supervisor.Pause(id)` →
`UpdateTargetStations` → `Supervisor.Start(id)`. `Pause` flipa a campanha
para `cancelada` (estado terminal). Qualquer falha entre Pause e Start
deixava a campanha permanentemente cancelada, e qualquer subscriber
concorrente (lifecycle scheduler, audit log, dashboards) via a campanha
como `cancelada` por uma janela curta porém visível.

Fix: novo método `Supervisor.UpdateStations(campaignID, newStations)` que
faz UPDATE + diff + start/stop incremental sem nunca mudar o status. Ver
[`workers/internal/supervisor/station_changes.go`](../../workers/internal/supervisor/station_changes.go)
e [`docs/campaign-lifecycle.md`](../architecture/campaign-lifecycle.md#edição-de-target_stations-em-campanha-ativa).

## Stall watchdog complementar

O reconciler resolve drift de **configuração** (URL ou lista de comerciais).
O stall watchdog (no mesmo `supervisor.go`) resolve drift de **execução** —
um worker vivo mas mudo. Os dois andam juntos: sem stall watchdog, um worker
que nunca conseguiu pegar PCM (URL morta no boot, DNS NXDOMAIN, 410 Gone)
ficaria zombificado sem nada pra cutucar.

A regra é a função pura
[`isStalled`](../../workers/internal/supervisor/supervisor.go):

| Cenário | Decisão |
|---------|---------|
| `last` definido, `time.Since(last) > 60s` | stalled — restart |
| `last` zero, `time.Since(startedAt) > stallStartupGrace` (2min) | stalled — restart |
| `last` zero, dentro da grace | não stalled |
| `last` recente (≤60s) | não stalled |

`stallStartupGrace = 2 * time.Minute` é o critério novo (2026-05-15). Antes
desse fix, a watchdog tinha `if last.IsZero() || time.Since(last) <= 60*time.Second { continue }`
— qualquer worker que nunca produziu PCM era ignorado pra sempre. Isso
combinou com a snapshot de URL pra criar zumbis silenciosos: worker subia,
ffmpeg falhava em conectar na URL antiga, `LastPCMAt` permanecia zero, o
watchdog pulava o restart eternamente, o reconciler de comerciais não tinha
motivo pra restart (lista não mudou), e a URL nova no banco era invisível.
Pinned por `stall_watchdog_test.go::TestIsStalled` e
`TestStallStartupGrace_Constant`.

Cooldown de 2 minutos entre restarts consecutivos do mesmo worker continua
existindo no `runStallWatchdog` — protege contra restart loops quando o
stream tá legitimamente fora do ar (aí o reconnect backoff interno do
worker é o mecanismo certo, não o restart).

## Não objetivos

O reconciler **não** corrige:

- Threshold desincronizado com calibração (é o threshold refresher).
- Comercial com `fingerprint_status != 'ready'` — esses ficam fora de
  `ListReadyByCampaignsForStation` por design; é a calibração/fingerprint
  worker que precisa concluir antes que o reconciler veja.
- Campanha em estado errado (`programada` ou `cancelada`) — se a campanha
  não tá `ativa`, o worker simplesmente não deveria estar rodando, e não
  é o reconciler que decide isso.
