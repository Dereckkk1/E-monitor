---
status: implementado
ultima-verificacao: 2026-05-15
codigo-relacionado:
  - workers/internal/ingestor/worker.go
  - workers/internal/ingestor/ffmpeg.go
  - workers/internal/supervisor/reconcile.go
  - workers/internal/supervisor/supervisor.go
  - workers/internal/supervisor/stall_watchdog_test.go
  - workers/internal/supervisor/reconcile_test.go
  - workers/internal/ingestor/worker_test.go
  # data-do-incidente: 2026-05-15
  # contexto: relatado pelo usuário durante operação, sem ticket externo
  # ondas: 1) snapshot pattern + stall watchdog ignorava zumbi (manhã)
  #        2) ADTS segment muxer rejeita streams não-AAC (tarde, mesmo dia)
  #        3) -reconnect_at_eof 1 quebra HLS (fim de tarde, mesmo dia)
---

# Workers presos na URL antiga após edição de emissora — snapshot pattern + zumbi sem PCM

Operador editou `stream_url` em algumas emissoras via `PUT /v1/internal/stations/{id}`.
O banco refletiu a mudança, o botão "play" em `/stations` (que lê a URL
fresca do banco no browser) tocou normalmente, mas os workers
correspondentes ficaram presos tentando a URL antiga e o painel `/monitoring`
exibiu essas emissoras com bolinha vermelha "como se tivessem acabado de
cair". Estado durou desde a edição até a investigação (várias horas).

Sem perda de detecção real porque, em paralelo, ninguém detectaria contra
URLs mortas — mas qualquer comercial que veiculasse nessas emissoras
durante o período teria sido perdido silenciosamente.

## Causa raiz

Três bugs encadeados, todos do mesmo padrão arquitetural ("snapshot na
inicialização sem reconciliação posterior"):

### 1. `WorkerConfig.StreamURL` é congelada em `startStationWorker`

`s.stations.Get(stationID)` lê o banco uma vez no boot do worker e enfia o
valor em [`WorkerConfig.StreamURL`](../../workers/internal/ingestor/worker.go).
O loop interno de `Worker.Run()` passa essa string fixa pro ffmpeg, que
mantém seu próprio backoff de reconnect (`-reconnect 1
-reconnect_delay_max 5`). Nada relê o banco enquanto o worker viver.

Mesmo padrão que detonou o incidente 2026-05-08 com comerciais — só que
naquela vez foi tratado pontualmente pra `CommercialShortIDs` via
reconciler de 30s, sem generalizar pra `stream_url` (vinha junto no
mesmo `WorkerConfig` mas ninguém percebeu).

### 2. Handlers de edição não disparam reload

`StationsHandler.Update` em [`workers/internal/api/handlers/stations.go`](../../workers/internal/api/handlers/stations.go)
faz `Repo.Update()` e retorna 200. Não chama o supervisor, não publica
evento NATS, não tem nenhuma "post-write hook". A intenção provavelmente
era confiar no reconciler — mas o reconciler de 30s só cobria comerciais.

O comentário histórico no topo de [`reconcile.go`](../../workers/internal/supervisor/reconcile.go)
deixa explícito que a equipe já decidiu confiar no reconciler como rede
de segurança em vez de Reload-via-handler ("The handler-level Reload call
that is supposed to cover this path silently swallows errors"). Pra
manter coerência arquitetural, a extensão certa era no reconciler, não
no handler.

### 3. Stall watchdog ignorava workers que nunca produziram PCM

`runStallWatchdog` em [`supervisor.go`](../../workers/internal/supervisor/supervisor.go)
era o único mecanismo no sistema que conseguiria reiniciar o worker
"transitivamente" (porque `startStationWorker` relê o banco no respawn).
Mas tinha o guard:

```go
if last.IsZero() || time.Since(last) <= 60*time.Second {
    continue
}
```

A intenção do `last.IsZero()` era "worker recém-criado, dá tempo de subir
antes de declarar stall". Na prática, esse guard era **permanente** —
worker cuja primeira tentativa de ffmpeg falhasse (URL morta, DNS
NXDOMAIN, 410 Gone) tinha `LastPCMAt` zero pra sempre, e o watchdog
ignorava pra sempre. Combinado com a snapshot da URL, isso cria zumbis
silenciosos: vivo, sem PCM, sem reload, sem watchdog.

## Sintomas

- `/monitoring`: bolinha vermelha "is_currently_down" pra emissoras com
  URL editada. Não diferencia "caiu há 5min" de "caiu há 5 horas" porque
  o `is_currently_down` em [`health_events.go::computeSummary`](../../workers/internal/catalog/health_events.go)
  só checa "é o último evento e está sem `duration_seconds`?". Como o
  watchdog nunca reiniciou o worker, o `OnStreamUp` nunca disparou
  `RecordUp`, então o `down` ficou aberto eternamente.
- `/stations` play button: tocava normal — frontend lê
  `station.stream_url` direto e usa no `RadioPlayerContext` no browser,
  bypassando totalmente o pipeline do worker.
- Logs do worker: ffmpeg em loop de reconnect na URL antiga, sem mensagem
  de erro visível pro operador (nível debug; supervisor não eleva).

## Fix

Branch local atual (não publicado em master ainda). Pacote em três
componentes complementares, com TDD:

### A. Reconciler de URL (mesmo padrão dos comerciais)

[`reconcile.go::reconcileReason`](../../workers/internal/supervisor/reconcile.go)
é uma função pura nova que recebe `(currentURL, wantedURL, currentIDs, wantedIDs)`
e retorna string com o motivo do restart (ou vazia se nada mudou).
`reconcileOnce` agora chama `s.stations.Get()` no início do tick e
compara `station.StreamURL` com `entry.worker.StreamURL()` (getter novo
em [`ingestor/worker.go`](../../workers/internal/ingestor/worker.go)).
Restart segue exatamente o mesmo caminho do drift de comerciais —
cancela context, deleta do mapa, respawna via `startStationWorker`.

Goroutine renomeada `runCommercialReconciler` → `runWorkerReconciler` pra
refletir que o escopo cobre URL+comerciais.

Coberto por `reconcile_test.go::TestReconcileReason` (5 cenários,
incluindo o caso defensivo de `wantedURL` vazio).

### B. Stall watchdog robusto

`isStalled(last, startedAt, now)` em [`supervisor.go`](../../workers/internal/supervisor/supervisor.go)
substitui o predicate antigo. Regra nova: `last.IsZero() && time.Since(startedAt) > 2min`
também conta como stalled. `startedAt` é setado no `workerEntry` no
momento do `go w.Run(workerCtx)` e zerado quando o worker entra no mapa
mas ainda não foi marcado como "iniciado" (caso defensivo: zero
`startedAt` → não stalled, evita restart durante a janela curta do
`startStationWorker`).

Coberto por `stall_watchdog_test.go::TestIsStalled` (7 cenários, ambas
flavors de stall + bordas) e `TestStallStartupGrace_Constant`.

### C. Não tocou no handler

Decisão deliberada: o handler `PUT /stations/{id}` continua só escrevendo
no banco. O reconciler garante convergência em até 30s. Adicionar push do
handler dobraria o caminho de invalidação e historicamente esse padrão
silenciou erros (vide comentário histórico em `reconcile.go`). Pra a
operação real (operador edita URL raramente), 30s de latência é trivial.

## Verificação manual

Quem precisar reproduzir/verificar:

```bash
# 1. Identificar emissoras zumbis (red dot + worker antigo no logs)
curl -s http://localhost:9090/api/v1/query \
  --data-urlencode 'query=radiocheck_worker_active' | jq '.data.result[]'

# 2. Forçar drift de URL (após o fix, espera 30s e veja o restart)
psql "$DATABASE_URL" -c "UPDATE stations SET stream_url='https://novo.example/stream' WHERE id='<UUID>'"
docker compose -f infra/docker/docker-compose.yml \
               -f infra/docker/docker-compose.override.yml \
               --env-file infra/docker/.env logs api --since 1m \
  | grep "supervisor.reconcile: drift detected"

# Esperado, em até 30s:
# supervisor.reconcile: drift detected — restarting worker
#   reason="stream_url changed"
#   current_stream_url="https://antigo.example/stream"
#   wanted_stream_url="https://novo.example/stream"
```

## Timeline

- **Antes de 2026-05-15**: operador editou stream_url de N emissoras via
  UI/API. Workers continuaram com URL antiga.
- **2026-05-15, manhã**: usuário reportou em conversa que `/monitoring`
  mostra workers em alerta enquanto `/stations` play funciona.
- **2026-05-15, ~13:00**: investigação confirma causa raiz tripla
  (snapshot + handler sem reload + watchdog ignora zumbi).
- **2026-05-15, ~13:30**: fix implementado com TDD, todos os testes
  passando localmente.

## Segunda onda — ADTS segment muxer rejeita streams não-AAC

Após o deploy da primeira onda, o usuário relatou que 9 emissoras
continuavam vermelhas em `/monitoring` (com `LastPCMAt` zerado) embora o
botão de play em `/stations` tocasse normal. Inspeção dos logs mostrou que
o stall watchdog estava reiniciando essas 9 workers a cada 2 minutos —
provando que a primeira onda funcionou (zumbis não ficam mais presos), mas
revelando que cada restart caía no mesmo erro de inicialização.

### Causa raiz

O pipeline ffmpeg do worker emite dois outputs em paralelo: PCM via
`pipe:3` (matcher) e ADTS-AAC via segment muxer em disco (evidência §11).
A flag `-segment_format adts` instrui o muxer a usar o container ADTS, que
é específico do codec AAC. Streams **MP3** (que são a maioria do parque de
rádios brasileiro) crashavam imediatamente na inicialização:

```
[adts @ 0x...] Only AAC streams can be muxed by the ADTS muxer
[out#0/segment @ 0x...] Could not write header (incorrect codec parameters?)
Error opening output file ...
```

Confirmado em `2026-05-15` testando manualmente com o comando exato do
worker contra `https://playerservices.streamtheworld.com/api/livestream-redirect/MIXFM_JOAOPESSOA.mp3`
(MP3 96k). O `-c:a copy` propaga o MP3 pro muxer, que rejeita o codec.

Por que demorou pra aparecer: o sistema sempre teve esse bug, mas ficou
latente até a primeira onda (snapshot pattern) expor zumbis em massa. Antes
disso, as estações eram pacientemente reiniciadas pelo stall watchdog…
exceto que o stall watchdog ignorava workers com `LastPCMAt.IsZero()`, e
esses workers MP3 nunca conseguiam emitir um único frame PCM antes do
ffmpeg morrer. Resultado: ficavam zumbis indefinidos, e ninguém via a
correlação.

### Fix

Adicionado em [`workers/internal/ingestor/ffmpeg.go`](../../workers/internal/ingestor/ffmpeg.go):

1. **`ProbeAudioCodec(ctx, url)`** — chama `ffprobe -show_streams -select_streams a:0`
   com timeout de 8s e User-Agent espelhando o do live para detectar o
   codec antes do `ffmpeg`. Falha de probe (timeout, parse, sem stream)
   retorna string vazia, tratada como "unknown".
2. **`pickSegmentAudioArgs(codec)`** — função pura, testada por tabela:
   - `aac` → `-c:a copy` (comportamento anterior, zero overhead)
   - qualquer outro (mp3, opus, vorbis, …, ou `""` unknown) →
     `-c:a aac -b:a 128k` (re-encode pra AAC LC, que o ADTS muxer aceita)
3. `StartFFmpeg` chama o probe pré-flight e splica `audioCodecArgs` no
   comando. Mantém TUDO o resto inalterado — extensão `.aac` dos
   arquivos de evidência, audit §9.9, presigned URLs e retenção continuam
   funcionando idênticos porque a saída em disco sempre é ADTS-AAC.

Custo CPU: ~3% de 1 core por worker MP3 (encoder AAC LC a 128k é leve).
Streams AAC nativas continuam zero overhead via `-c:a copy`.

Pinned por `worker_test.go::TestPickSegmentAudioArgs` (6 cenários: AAC
em ambos os cases, MP3, Opus, Vorbis, codec vazio/desconhecido).

### Por que NÃO trocar pra mpegts

A opção alternativa era `-segment_format mpegts` (TS aceita qualquer
codec). Foi descartada porque:

- Mudaria a extensão dos arquivos de evidência de `.aac` pra `.ts`,
  quebrando glob/leitura em audit §9.9, presigned URLs e — pior — o
  player de evidência do frontend (browsers reproduzem `<audio src=".aac">`
  nativamente; `.ts` precisa de hls.js).
- Adicionaria 5-10% de overhead de container (TS é mais "gordo" que ADTS).
- Re-encode pra AAC LC é a abordagem padrão da indústria pra esse exato
  caso e tem custo aceitável.

A opção de detectar formato e usar muxer dinâmico (`adts` p/ AAC,
`mp3` p/ MP3, `mpegts` p/ resto) foi rejeitada pelo mesmo motivo: força
mudanças em ≥5 outros lugares (segments, presigned URLs, retention, audit,
frontend player) pra uma optimização CPU que economiza ~3% por worker MP3.
Trade-off ruim — fiquei no caminho cirúrgico de re-encode condicional.

## Terceira onda — `-reconnect_at_eof 1` quebra HLS

Após o deploy da segunda onda, restavam ainda 2 estações em CAÍDO. Uma
delas — **Atlântida Joinville** (`c03a724a`, URL
`https://playerservices.streamtheworld.com/api/livestream-redirect/ATL_JOIAAC.m3u8`)
— tocava no `/stations` e era confirmadamente AAC. A outra, **Mix João
Pessoa**, tinha o stream da própria emissora quebrado (fora do nosso
controle).

Para a Atlântida (HLS), smoke test local com as flags exatas do worker
produziu o sintoma:

```
[https @ ...] Will reconnect at 188 in 0 second(s), error=End of file.
exit=124 (timeout)
ls /tmp/hls/  → vazio
```

### Causa raiz

A flag `-reconnect_at_eof 1` instrui o ffmpeg a re-estabelecer a conexão
HTTP sempre que a leitura atinge EOF. Faz sentido pra **streams
progressivos** (direct .mp3/.aac, Icecast, SHOUTcast): uma única resposta
HTTP de duração indefinida, EOF = servidor fechou.

Mas **HLS é diferente**: a playlist `.m3u8` aponta pra uma sequência de
segmentos finitos (`.ts` ou `.aac`), e cada segmento naturalmente termina
em EOF. Com a flag ativa, ffmpeg trata cada limite de segmento como
desconexão, reconecta imediatamente, re-busca a playlist, e nunca
chega a decodificar conteúdo. O worker fica vivo mas produz zero PCM.

Por que demorou pra aparecer: a flag está no `ffmpeg.go` desde sempre,
mas até as ondas 1 e 2 serem corrigidas, os workers HLS eram zumbis
silenciosos junto com os MP3. Depois do fix da 2ª onda, sobraram as
estações HLS expondo o bug.

### Fix

Adicionado em [`ffmpeg.go`](../../workers/internal/ingestor/ffmpeg.go):

1. **`pickReconnectArgs(streamURL)`** — função pura que retorna o conjunto
   de flags `-reconnect*`. Sempre inclui o trio base (`-reconnect`,
   `-reconnect_streamed`, `-reconnect_delay_max`) que cobre hiccups
   transientes de TCP/TLS em qualquer tipo de stream. Adiciona
   `-reconnect_at_eof 1` apenas se a URL **não** for HLS.
2. **`isHLS(streamURL)`** — detector por substring case-insensitive
   contra `.m3u8`. Cobre query string, path com `.m3u8` em qualquer
   posição, e MAIÚSCULAS.
3. `StartFFmpeg` chama `pickReconnectArgs(streamURL)` e spliça o
   resultado no comando ffmpeg em vez do trio anterior + flag fixa.

Pinned por `worker_test.go::TestPickReconnectArgs` (8 cenários: 4
progressivos com flag, 4 HLS sem flag).

### Por que não desabilitar `-reconnect_at_eof` globalmente

Os streams progressivos brasileiros (Icecast/SHOUTcast com IP fixo do
servidor) **fecham conexão periodicamente** (timeout de socket após X horas,
restart de servidor, etc.). Sem a flag, o worker desses streams precisaria
de uma camada extra de retry — exatamente o que a flag dá pronto. Ficar
sem ela degradaria a robustez de 80%+ do parque (todas as não-HLS) só
pra resolver as poucas HLS. Detecção dirigida pela URL é o trade-off
certo.

## Follow-ups

- **F-122 (recomendado)**: cobrir o bug correlato em
  [`health_events.go::computeSummary`](../../workers/internal/catalog/health_events.go) —
  `is_currently_down` não considera idade do `down`. Após este fix o
  problema é mitigado transitivamente (worker volta a produzir PCM →
  `OnStreamUp` fecha o `down`), mas se em algum cenário futuro o worker
  ficar travado de novo o sintoma "vermelho recente" enganoso volta.
  Sugestão: separar `is_currently_down` (booleano) de
  `current_down_started_at` (timestamp) e deixar o frontend decidir o
  estilo da bolinha em função da idade.

- **F-123 (recomendado)**: alerta Prometheus pra worker com `LastPCMAt`
  zero por mais que `stallStartupGrace`. Hoje o watchdog reinicia mas
  ninguém é notificado se isso vira loop (URL morta de verdade) —
  expor `radiocheck_worker_zero_pcm_total` ou alertar em
  `rate(radiocheck_worker_stall_restarts_total[5m]) > 0.01`.

- **F-125 (cosmético)**: o painel "Workers em alerta" mostra "último PCM
  há 739750d" pra workers com `LastPCMAt` zerado. Isso é o frontend
  interpretando `time.Time{}` (year 0001) como timestamp absoluto e
  calculando `Date.now() - lastPcmAt` = ~739k dias. Deveria detectar
  timestamp zerado e mostrar "Aguardando primeiro sinal" ou similar.

- **F-126 (qualidade)**: validação na criação/edição de emissora —
  fazer um `ffprobe` pré-cadastro pra (a) detectar URLs que retornam 403
  (URL stale do streamtheworld ou User-Agent gate) e (b) registrar o
  codec do stream junto com a row da emissora. Permite que o operador
  veja na UI "essa URL não responde" antes de salvar e gerar zumbi.

- **Teste pré-existente flaky (não relacionado)**:
  `internal/catalog/TestBuildDailySummary_WithDowntime` falha quando
  rodado antes das ~13h UTC porque `time.Now().Add(-13h)` cruza o
  boundary do dia. Pré-existe a esse incidente; vale corrigir em PR
  separado.
