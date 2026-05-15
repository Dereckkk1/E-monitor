---
status: implementado
ultima-verificacao: 2026-05-15
codigo-relacionado:
  - workers/internal/storage/s3.go
  - workers/internal/api/handlers/detections.go
  - workers/internal/config/config.go
  - frontend/src/components/DayDetailModal.jsx
  - frontend/src/components/AudioPlayer.jsx
---

# Evidence — URLs pré-assinadas

Como o frontend interno entrega áudios de evidência ao navegador sem precisar
proxiar o arquivo pela API.

## Por que existe

`<audio>` e `<a download>` não anexam o header `Authorization` em GETs. Como o
endpoint `GET /v1/internal/detections/{id}/evidence` exige JWT, o player do
calendário falhava com 401 mesmo com o usuário logado. Em vez de aceitar token
em query string (vaza em logs / referer) ou de baixar o arquivo via fetch + Blob
(rouba memória, quebra seek), o frontend pede uma URL pré-assinada do bucket e
usa essa URL diretamente no `<audio src>`.

Aderente ao §11.3 e §13 do `plano_implementacao.md`.

## Endpoint

```
GET /v1/internal/detections/{id}/evidence/url
Authorization: Bearer <jwt>
```

**200 OK**

```json
{
  "url": "http://localhost:9000/radiocheck-evidence/evidences/2026/05/07/<station>/<id>.m4a?X-Amz-...",
  "expires_at": "2026-05-08T11:13:24Z"
}
```

**404** — detecção não existe ou `evidence_status != "available"`.

A URL é válida por **5 minutos** (constante em
[handlers/detections.go](../../workers/internal/api/handlers/detections.go),
função `EvidenceURL`). O frontend deve cachear e refazer a chamada perto do
expiry — a implementação atual reaproveita a URL enquanto o expires_at estiver
a mais de 30 s do agora.

A URL externa carrega assinatura AWS SigV4 e não exige nenhum header — pode ser
usada como `src` de qualquer tag de mídia.

## Configuração

A URL gerada precisa ter um host **acessível pelo navegador do usuário**, não
pelo container da API. Em docker-compose o serviço `api` fala com o MinIO via
`http://minio:9000`, mas esse hostname não resolve fora da rede docker.

Por isso o storage client passou a aceitar dois endpoints:

| Variável             | Onde é usada                                  | Default em dev          |
|----------------------|-----------------------------------------------|-------------------------|
| `S3_ENDPOINT`        | I/O de objetos pelo backend                   | `http://minio:9000`     |
| `S3_PUBLIC_ENDPOINT` | Host das URLs pré-assinadas vistas pelo browser | `http://localhost:9000` |

Quando `S3_PUBLIC_ENDPOINT` está vazio, presigning cai de volta para
`S3_ENDPOINT` — comportamento correto em produção, onde o bucket público
(R2 / S3) tem o mesmo host pelos dois lados.

Em produção, defina `S3_PUBLIC_ENDPOINT` para o domínio público do bucket
(ex.: `https://evidence.radiocheck.example.com`).

## Fluxo no frontend

[DayDetailModal.jsx](../../frontend/src/components/DayDetailModal.jsx) mantém um
mapa `{detectionId -> {url, expiresAt}}` em estado local.

- **Click em Play:** `ensureEvidenceUrl(id)` → seta no estado → AudioPlayer
  renderiza com `src=url` → `useEffect` dispara `audio.play()`.
- **Click em Download:** `ensureEvidenceUrl(id)` → cria âncora temporária com
  `download` attribute, força click programático, remove. A URL pré-assinada
  é seguida diretamente pelo navegador.
- **Cache:** primeira chamada para uma detecção dispara fetch; chamadas
  subsequentes reaproveitam até 30 s antes do expiry.
- **Erro:** 4xx/5xx é silenciosamente engolido — próximo click tenta de novo.

## O endpoint legado continua existindo

`GET /v1/internal/detections/{id}/evidence` (proxia o arquivo) e
`GET /v1/detections/{id}/evidence` (API key, externo) seguem funcionando. São
úteis para:

- Clientes externos via API key (não passam por SigV4).
- Testes via `curl` com header `Authorization`.
- Cenários onde o storage não suporta presigning ou está atrás de um proxy
  que invalida assinaturas.

## Por que TTL curto

5 minutos é tempo de sobra para o navegador buscar o clip (clipes têm 15–30 s).
TTL menor reduz a janela em que uma URL vazada fica utilizável — relevante porque
URLs pré-assinadas entregam o objeto sem nenhuma autorização adicional. O
frontend recarrega antes de expirar; o usuário não percebe.

## O que NÃO está coberto

- Rate limiting — presign em si não custa nada (não é call ao S3), mas chamadas
  em massa enchem o log. Se virar problema, acrescentar limit por usuário.
- Range requests — `<audio>` faz Range na URL pré-assinada e MinIO suporta.
  Não foi necessário ajuste extra.
- Refresh proativo — só refazemos quando alguém clica de novo. Para áudios
  longos onde 5 min não dá, aumentar o TTL ou agendar refresh com `setTimeout`.
