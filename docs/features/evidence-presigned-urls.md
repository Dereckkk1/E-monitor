---
status: implementado
ultima-verificacao: 2026-07-13
codigo-relacionado:
  - workers/internal/storage/s3.go
  - workers/internal/api/handlers/detections.go
  - workers/internal/api/handlers/detections_manual_batch.go
  - workers/internal/api/handlers/suggestions.go
  - workers/internal/config/config.go
  - frontend/src/components/DayDetailModal.jsx
  - frontend/src/pages/DetectionDetailPage.jsx
  - frontend/src/pages/suggestions/AttachmentImage.jsx
  - frontend/src/components/AudioPlayer.jsx
---

# Evidence — URLs pré-assinadas

Mecanismo de URL pré-assinada (SigV4) do bucket de evidência.

> **Estado atual (2026-07-13):** o **frontend interno NÃO usa mais** URLs
> pré-assinadas para NENHUMA mídia do bucket — áudio de evidência, comprovante
> PDF e anexos de sugestão **proxiam os bytes pela API** (ver
> [Fluxo no frontend](#fluxo-no-frontend) e a tabela em
> [Todos os consumidores](#todos-os-consumidores-de-mídia-do-bucket-usam-o-proxy-mesmo-motivo)).
> O motivo é que a URL pré-assinada assa o host do MinIO (`localhost:9000` em
> prod) na própria URL, e o navegador em `https://e-monitor.online` (origem
> pública) **não consegue seguir um endereço loopback** — o Chrome bloqueia com
> *"Permission was denied for this request to access the `loopback` address
> space"* (Private Network Access), ou simplesmente `ERR_CONNECTION_REFUSED`.
> Isso quebrou o áudio da `DetectionDetailPage` em 2026-07-03 (a `DayDetailModal`
> já tinha migrado antes), o comprovante PDF e os prints das sugestões (audit de
> 2026-07-13). Os endpoints `*/url` presigned continuam existindo mas ninguém no
> front os chama (ver [Pendências](#pendências)).

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

## Todos os consumidores de mídia do bucket usam o proxy (mesmo motivo)

Audit de 2026-07-13: varremos **todo** produtor de presigned (`Storage.PresignGet`)
e todo consumidor no browser. Só existem 3 superfícies que entregavam bytes do
bucket ao navegador, e as 3 agora proxiam:

| Superfície | Proxy (bytes + JWT) | Endpoint `/url` presigned legado |
|---|---|---|
| Áudio de evidência | `GET /detections/{id}/evidence` (`Evidence`) | `/evidence/url` (`EvidenceURL`) — não usado pelo front |
| Comprovante PDF (lote) | `GET /detections/{id}/proof` (`Proof`) | `/proof/url` (`ProofURL`) — não usado pelo front |
| Anexos de Sugestões (prints) | `GET /suggestions/attachments/{aid}` (`ProxyAttachment`) | `/attachments/{aid}/url` (`AttachmentURL`) — não usado pelo front |

- **Anexos de Sugestões** (`/admin/suggestions`) — corrigido em 2026-07-13 com o
  componente `AttachmentImage`. Ver [suggestions-board.md](suggestions-board.md).
- **Comprovante PDF** (`ProofCard` em [DetectionDetailPage](../../frontend/src/pages/DetectionDetailPage.jsx))
  — corrigido em 2026-07-13: buscava `/proof/url` (presigned) e abria em nova aba
  → em prod caía no `localhost:9000`. Agora busca `/detections/{id}/proof` como
  blob e abre um `blob:` URL. Handler `DetectionsHandler.Proof` espelha o
  `Evidence`.

Reprodutores de material/áudio (MaterialPlaybackList, LiveAiringRow,
AirtimeDetectionRow, SimilarityWarningModal) e os exports CSV/PDF já usavam o
proxy autenticado (`responseType: 'blob'`), nunca presigned — sem ação.

## Pendências

- Os endpoints `*/url` presigned (`EvidenceURL`, `ProofURL`, `AttachmentURL`)
  continuam existindo mas **nenhum é usado pelo frontend interno**. São inócuos
  (nada os chama) e ficam como legado; podem ser removidos numa limpeza futura.

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
