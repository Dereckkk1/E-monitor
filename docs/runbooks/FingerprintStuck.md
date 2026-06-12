# FingerprintStuck

**Severidade:** warning · **Categoria:** Pipeline de materiais
**Métrica:** `radiocheck_fingerprint_stuck{status}` (gauge, setada pelo reconciler a cada tick de 5min)
**Origem:** incidente [2026-06-12](../incidents/incident-2026-06-12-detection-recall-gaps.md) — materiais ficaram 1 mês presos em `pending` sem alerta.

## Sintomas

- Alerta `FingerprintStuck` disparado: existem materiais presos em `pending`/`generating` (>15min) ou `failed` há mais de 30min, e o retry automático do reconciler não resolveu.
- Consequência: esses materiais NÃO estão no índice de matching — veiculações deles não são detectadas.

## Causas Comuns

1. **Serviço `fingerprint` fora do ar** — o retry publica mas ninguém consome.
2. **Arquivo de áudio corrompido/inválido** — `failed` re-falha a cada tentativa (o reconciler desiste após 5).
3. **Master ausente no storage** — upload gravou a linha mas o arquivo não chegou ao MinIO/disco.
4. **NATS fora/particionado** — nem o publish do retry sai.

## Diagnóstico

```bash
# 1. Quem está preso?
docker compose -f infra/docker/docker-compose.yml \
               -f infra/docker/docker-compose.override.yml \
               --env-file infra/docker/.env \
  exec postgres psql -U radiocheck -d radiocheck -c "
SELECT short_id, title, fingerprint_status, updated_at
FROM materials WHERE fingerprint_status <> 'ready' ORDER BY updated_at;"

# 2. O serviço fingerprint está vivo e processando?
docker compose ... ps fingerprint
docker compose ... logs --since 30m fingerprint | tail -30

# 3. O reconciler está re-tentando? (log do api)
docker compose ... logs --since 30m api | grep "fingerprint-queue"
```

## Correção

- **Serviço morto:** `docker compose ... up -d fingerprint` — o reconciler re-publica no próximo tick, nada manual.
- **Failed re-falhando:** ler o erro no log do `fingerprint` (traceback). Arquivo corrompido → re-subir o material pela UI. Master ausente → verificar storage (`mastersdata`).
- **NATS:** verificar `docker compose ... ps nats` e logs.

## Escalação

Se após corrigir a causa o material seguir preso por mais 30min (2 ticks de retry), abrir incidente — pode ser bug novo no pipeline.

## Prevenção

O reconciler (`workers/internal/fingerprintqueue/`) já cobre mensagem perdida e falha transitória. Este alerta é a camada pra falha permanente. Ao subir material importante, conferir na UI que virou "pronto" antes do início da campanha (o email diário de "campanha sem material" cobre o caso de zero materiais, não o de material preso).
