# WorkerStallLoop

**Severidade:** critical · **Categoria:** Ingestão
**Métrica:** `radiocheck_worker_stall_restarts_total{station_id}` (≥10 restarts em 30min)
**Origem:** incidente [2026-06-12](../incidents/incident-2026-06-12-detection-recall-gaps.md) — Jovem Pan flapou ~1 dia em loop de restart (geo-block) sem ninguém perceber.

## Sintomas

- Worker da emissora reinicia a cada ~120s (stall watchdog: 2min de grace sem PCM + cooldown de 2min entre restarts).
- Log do `api` repete `supervisor: worker started` pra mesma `station_id` em intervalos exatos.
- A emissora está efetivamente **sem captura** — toda veiculação nela é perdida enquanto durar.

## Causas Comuns

1. **URL do stream morta** (404) — rádio trocou de endpoint.
2. **Geo-bloqueio** (`403 Country Not Allowed`) — o provedor do stream barra o IP do GCP como "não-Brasil". Pode ser intermitente (nós de CDN/base geo-IP divergentes).
3. **Bloqueio por user-agent ou IP** — servidor corta o ffmpeg mas aceita navegador.
4. **Mudança de codec/protocolo** — ffprobe falha no probe.

## Diagnóstico

```bash
STATION_URL="<stream_url da emissora>"   # pegar do cadastro ou do log "worker started"

# 1. O stream responde? (GET, não HEAD — geo-block às vezes só age no GET)
curl -s --max-time 10 -o /dev/null -w "%{http_code}\n" "$STATION_URL"

# 2. O erro real do ffmpeg, de dentro do container:
docker compose -f infra/docker/docker-compose.yml \
               -f infra/docker/docker-compose.override.yml \
               --env-file infra/docker/.env \
  exec api ffprobe -v error "$STATION_URL" 2>&1 | head -5

# 3. Histórico de up/down da emissora:
#    SELECT event_type, event_at, duration_seconds FROM stream_health_events
#    WHERE station_id='<uuid>' ORDER BY event_at DESC LIMIT 20;
```

Decision tree: `404` → causa 1 · `403 Country Not Allowed` → causa 2 · curl ok mas ffprobe falha → causa 3/4.

## Correção

- **404 (URL morta):** achar a URL nova (site/player da rádio) e atualizar o cadastro da emissora — o teste de conexão do Step 3 do wizard valida antes de salvar. Reconciler aplica em ≤30s.
- **403 geo-block:** na ordem — (a) pedir allowlist do IP da VM ao provedor/emissora (parceria), (b) URL alternativa da mesma rádio, (c) egress BR (§14.3 do plano).
- **User-agent:** testar `ffmpeg -user_agent "Mozilla/5.0..." -t 5 -i "$URL" -f null -` dentro do container; se passar, abrir task pra UA configurável no ingestor.

## Escalação

Emissora de campanha ativa sem captura >2h → avisar a operação (slots daquele período não são cobráveis sem cruzar com fonte externa; ver postmortem 2026-06-12).

## Circuit breaker (desde jun/2026)

Para o sabor **"nunca conectou"** (IP bloqueado / URL morta — `connect` TCP em
timeout, `LastPCMAt` nunca sai do zero), o supervisor **não respawna mais a cada
~120s**. Ele aplica **backoff exponencial** (1→2→4→…→30min, com jitter), matando
o worker durante a espera para parar de martelar o firmware do painel. Detalhes e
schedule: [docs/features/connect-backoff-circuit-breaker.md](../features/connect-backoff-circuit-breaker.md).

Implicações para este runbook:
- **Sintoma novo:** a emissora bloqueada não loga `worker started` a cada 2min —
  o intervalo cresce até ~32min. Olhe a métrica `radiocheck_worker_connect_backoff_seconds{station_id}`
  (no `/operations`): colada em `1800` = IP bloqueado ou URL morta.
- O breaker **não desbloqueia** nada — é só anti-sangria. Siga a Correção acima
  (allowlist do IP / URL nova / egress BR) para recuperar de fato.
- Após conceder um allowlist, a reconexão é automática em ≤~32min (ou reinicie o
  `api` para forçar agora — não há kick manual ainda).

## Prevenção

Este alerta É a prevenção (antes dele, o loop era silencioso). Complemento: a página `/operations` mostra stall restarts por worker em tempo real, e o `radiocheck_worker_connect_backoff_seconds` denuncia IP bloqueado/URL morta.
