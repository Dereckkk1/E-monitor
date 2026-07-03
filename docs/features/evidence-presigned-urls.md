---
status: parcialmente-implementado
ultima-verificacao: 2026-07-03
codigo-relacionado:
  - workers/internal/storage/s3.go
  - workers/internal/api/handlers/detections.go
  - workers/internal/config/config.go
  - frontend/src/components/DayDetailModal.jsx
  - frontend/src/pages/DetectionDetailPage.jsx
  - frontend/src/components/AudioPlayer.jsx
---

# Evidence — URLs pré-assinadas

Mecanismo de URL pré-assinada (SigV4) do bucket de evidência.

> **Estado atual (2026-07-03):** o **frontend interno NÃO usa mais** URLs
> pré-assinadas para o áudio — ele **proxia os bytes pela API** (ver
> [Fluxo no frontend](#fluxo-no-frontend)). O motivo é que a URL pré-assinada
> assa o host do MinIO (`localhost:9000` em prod) na própria URL, e o navegador
> em `https://e-monitor.online` (origem pública) **não consegue seguir um
> endereço loopback** — o Chrome bloqueia com *"Permission was denied for this
> request to access the `loopback` address space"* (Private Network Access).
> Isso quebrou a reprodução na `DetectionDetailPage` em 2026-07-03; a
> `DayDetailModal` já tinha migrado pro proxy antes. O endpoint `/evidence/url`
> continua existindo, mas hoje só o **comprovante PDF** (`/proof/url`, admin)
> ainda depende de presigned no browser — e por isso ainda sofre o mesmo
> bloqueio (ver [Pendências](#pendências)).

## Por que existe

`<audio>` e `<a download>` não anexam o header `Authorization` em GETs. Como o
endpoint `GET /v1/internal/detections/{id}/evidence` exige JWT, o player do
calendário falhava com 401. As duas saídas para isso são: (a) uma URL
pré-assinada do bucket (SigV4, sem header) usada direto no `<audio src>`, ou
(b) buscar os bytes via `fetch` autenticado e tocar um `blob:` object URL. A
opção (a) foi a original, mas depende de o bucket ter um host público
alcançável pelo browser — que não é o caso do MinIO atrás do tunnel. O
frontend interno usa hoje a opção (b).

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

Ambos os consumidores de áudio buscam os bytes pelo **proxy autenticado**
`GET /v1/internal/detections/{id}/evidence` (que anexa o JWT do axios) com
`responseType: 'blob'` e tocam um `blob:` object URL — **não** uma URL
pré-assinada.

- [DayDetailModal.jsx](../../frontend/src/components/DayDetailModal.jsx) —
  fetch on-click (`ensureEvidenceUrl`), cacheia o object URL por detecção num
  ref, revoga todos no unmount.
- [DetectionDetailPage.jsx](../../frontend/src/pages/DetectionDetailPage.jsx) —
  o player carrega sozinho, então busca o blob via `useQuery`
  (`['detection-evidence-blob', id]`, `staleTime: Infinity`) e deriva o object
  URL com `useMemo`, revogando no cleanup. `isLoading`/`isError` da query
  alimentam os estados do painel.
- **Download:** âncora com `download` apontando pro mesmo `blob:` URL.
- Um `blob:` object URL não expira como a antiga presigned (TTL 5 min), então
  não há lógica de refresh.

## Pendências

- **Comprovante PDF (`GET /v1/internal/detections/{id}/proof/url`, admin)**
  ainda devolve uma URL pré-assinada aberta direto no browser
  ([DetectionDetailPage `ProofCard`](../../frontend/src/pages/DetectionDetailPage.jsx)).
  Em prod isso sofre exatamente o mesmo bloqueio de loopback do áudio. Correção
  simétrica: criar um proxy `GET /detections/{id}/proof` (espelhando o de
  evidência) e buscar o PDF como blob. Não feito ainda — a decisão de 2026-07-03
  foi migrar só o áudio (o que estava reportado quebrado) e deixar o comprovante
  como follow-up.

## O endpoint de proxy (caminho atual do browser)

`GET /v1/internal/detections/{id}/evidence` (proxia o arquivo com JWT) é o
caminho que o frontend interno usa hoje. `GET /v1/detections/{id}/evidence`
(API key, externo) também existe. São úteis para:

- Frontend interno tocar áudio sem depender de host público do bucket.
- Clientes externos via API key (não passam por SigV4).
- Testes via `curl` com header `Authorization`.

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
