---
status: planejado
ultima-verificacao: 2026-07-17
codigo-relacionado:
  - workers/internal/storage/s3.go
  - workers/internal/api/handlers/detections.go
  - workers/internal/api/handlers/detections_manual_batch.go
  - workers/internal/api/handlers/suggestions.go
  - infra/docker/docker-compose.yml
---

# Presign público de evidência — tirar o download do processo Go

> **Status: planejado (runbook para o Dereck executar em prod).** Nenhum código deste
> repo muda — é configuração de infra (Cloudflare Tunnel + `.env` da VM). Claude não tem
> acesso a prod (regra 7 do CLAUDE.md); quem executa é o Dereck.

## Problema

Os handlers que servem evidência (`detections.go` Evidence, `detections_manual_batch.go`
Proof, `suggestions.go` ProxyAttachment) **proxeiam** o objeto do MinIO pelo processo Go.
A Task 16 (commit `727a4c5`) já melhorou isso trocando `io.ReadAll` por `io.Copy`
(streaming, sem bufferizar o objeto inteiro no heap) — mas os bytes **ainda passam pelo
processo Go e atravessam o Cloudflare Tunnel duas vezes** (MinIO→API→Tunnel→browser),
competindo por CPU/banda com os ~200 ffmpeg de captura no mesmo container.

A infra **já suporta** presign direto (`storage/s3.go` `PresignGet`), mas o
`S3_PUBLIC_ENDPOINT` em prod é `http://localhost:9000` — inalcançável pelo browser — então
a presigned URL não serve e o frontend caiu no proxy. Este runbook expõe o MinIO num
hostname público e faz a presigned valer, tirando o download do processo Go de vez.

## Runbook (Dereck, em prod)

**Pré-condição:** a Task 16 (streaming) já está em prod — então mesmo sem este runbook o
download não bufferiza mais no heap. Isto aqui é o passo seguinte (tirar do processo Go
inteiramente), não urgente. Fazer em janela tranquila.

- [ ] **1. Rota no Cloudflare Tunnel da VM.** No config do `cloudflared` (o mesmo tunnel
  que já serve a API), adicionar um hostname `evidence.<dominio>` apontando para
  `http://minio:9000` (nome do service no compose; o cloudflared roda na mesma rede
  docker). Não expor o console do MinIO (`:9001`), só a API S3 (`:9000`).

- [ ] **2. `.env` da VM:** trocar `S3_PUBLIC_ENDPOINT=http://localhost:9000` por
  `S3_PUBLIC_ENDPOINT=https://evidence.<dominio>`. (Confirmar que o bucket e o path-style
  batem — presigned URL do MinIO é path-style `https://host/bucket/key?X-Amz-...`.)

- [ ] **3. Recreate da API** (regra 4.1 — `--no-deps` obrigatório, nunca propagar recreate
  pro postgres):
  ```bash
  ./scripts/deploy.sh    # já inclui override + env-file (regra 4.7)
  # OU, manual:
  docker compose -f infra/docker/docker-compose.yml \
                 -f infra/docker/docker-compose.override.yml \
                 --env-file infra/docker/.env up -d --force-recreate --no-deps api
  ```

- [ ] **4. Validar:** abrir uma detecção no frontend. O endpoint que devolve a URL de
  evidência deve retornar `https://evidence.<dominio>/...` (presigned), e essa URL tem que
  **tocar direto no browser** (abrir num aba anônima, sem cookie de sessão — a assinatura
  já autentica). Se voltar `localhost:9000`, o `.env` não pegou (recreate com binário
  velho? regra 4.2) ou o `S3_PUBLIC_ENDPOINT` está errado.

- [ ] **5. Testar expiração:** a presigned URL expira (TTL definido em `storage/s3.go`).
  Confirmar que uma URL velha dá 403 do MinIO e o frontend re-pede uma nova.

## Follow-up (frontend — fora deste runbook)

Depois que a presigned pública estiver validada, o frontend deve trocar os componentes que
hoje batem no **proxy** (`api.get('/detections/{id}/evidence', {responseType:'blob'})`)
pela **presigned URL** direta. Só aí o download some 100% do processo Go. Registrado em
[docs/roadmap/follow-ups-fase2.md](../roadmap/follow-ups-fase2.md).

Componentes que hoje usam o proxy (blob): `DetectionDetailPage.jsx`, `AirtimeDetectionRow.jsx`,
`LiveAiringRow.jsx`, `DayDetailModal.jsx`, `suggestions/AttachmentImage.jsx`. Migrar cada um
pra pedir a presigned URL e apontar o `<audio>`/`<img>`/link direto pra ela.

> **Nota sobre CORS/Range:** ao servir direto do MinIO, o player HTML5 PODE voltar a mandar
> `Range` requests (o MinIO suporta 206) — o que é até melhor que o blob atual. Mas confirmar
> o CORS do bucket MinIO pra `<dominio>` do frontend, senão o browser bloqueia. Isso é uma
> mudança de comportamento vs. o blob de hoje (Task 16), então testar playback e seek antes
> de remover o proxy.
