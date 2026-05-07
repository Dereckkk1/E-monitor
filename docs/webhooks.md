# Webhooks

Documentação operacional do subsistema de webhooks do Radiocheck (§13.1.4 do plano).

## Como funciona

```
ingestor.Worker  →  NATS "detections.confirmed"
                    │
                    ├─→ evidence.Service   (grava clip + insere detection row)
                    └─→ webhook.Deliverer  (resolve client_id, enfileira na outbox)
                                            │
                                            ▼
                                  Tabela webhook_deliveries (status='pending')
                                            │
                                            ▼
                                  webhook.Worker (loop, FOR UPDATE SKIP LOCKED)
                                            │
                                            ├─→ POST cliente
                                            └─→ retry/dead-letter
```

O worker roda como goroutine no processo `cmd/api`. Não é necessário processo
separado. Múltiplas réplicas podem rodar simultaneamente; o `SKIP LOCKED` evita
disputa pela mesma linha.

## Configuração por cliente

Cada `clients` row tem 4 colunas:

| Coluna            | Tipo      | Default                    | Descrição                                     |
|-------------------|-----------|----------------------------|-----------------------------------------------|
| `webhook_url`     | TEXT      | NULL                       | Endpoint que receberá o POST                  |
| `webhook_secret`  | TEXT      | NULL                       | Chave HMAC (>= 16 chars). Plaintext no PoC.   |
| `webhook_enabled` | BOOLEAN   | FALSE                      | Liga/desliga sem perder a config              |
| `webhook_events`  | TEXT[]    | `{detection.confirmed}`    | Lista de eventos aos quais o cliente assina   |

### Endpoints de administração (operador, JWT)

```
GET    /v1/internal/clients/{id}/webhook
PATCH  /v1/internal/clients/{id}/webhook
        body: { webhook_url?, webhook_secret?, webhook_enabled?, webhook_events? }
GET    /v1/internal/clients/{id}/webhook-deliveries?status=&limit=
POST   /v1/internal/clients/{id}/webhook-test
```

A tela de Clientes na UI expõe um modal "Webhook" (ícone na linha do cliente)
que cobre os quatro endpoints.

`GET /webhook` nunca devolve o secret completo — apenas `webhook_secret_masked`
(primeiros 4 chars + `...`) e o flag `has_secret`.

## Formato do payload

Toda requisição POST tem `Content-Type: application/json` e o body abaixo:

```json
{
  "event_id":    "9f8c6e2b-...uuid...",
  "type":        "detection.confirmed",
  "occurred_at": "2026-05-07T18:34:21.123Z",
  "data": {
    "detection": {
      "detected_at":   "2026-05-07T18:34:18Z",
      "confidence":    0.84,
      "offset_frames": 1024
    },
    "station": {
      "id":   "uuid",
      "name": "Massa FM Joinville"
    },
    "commercial": {
      "id":       "uuid",
      "short_id": 4711,
      "title":    "Sazonal Outono 30s"
    }
  }
}
```

Eventos extras (hoje só `webhook.test`) usam o mesmo envelope; apenas `data`
muda.

## Verificação da assinatura HMAC

Cabeçalhos enviados:

```
Content-Type: application/json
User-Agent: Radiocheck-Webhook/1.0
X-Radiocheck-Event: detection.confirmed
X-Radiocheck-Signature: sha256=<hex>
```

`<hex>` é `HMAC_SHA256(secret, raw_body)` em lowercase hex.

### Go

```go
import (
    "crypto/hmac"
    "crypto/sha256"
    "encoding/hex"
    "io"
)

func verify(req *http.Request, secret string) (bool, []byte, error) {
    body, err := io.ReadAll(req.Body)
    if err != nil { return false, nil, err }
    sig := req.Header.Get("X-Radiocheck-Signature")
    if !strings.HasPrefix(sig, "sha256=") { return false, body, nil }
    mac := hmac.New(sha256.New, []byte(secret))
    mac.Write(body)
    want := hex.EncodeToString(mac.Sum(nil))
    return hmac.Equal([]byte(sig[7:]), []byte(want)), body, nil
}
```

### Node.js

```js
import crypto from "crypto"

export function verify(req, secret, rawBody) {
  const sig = req.headers["x-radiocheck-signature"] || ""
  if (!sig.startsWith("sha256=")) return false
  const want = crypto.createHmac("sha256", secret).update(rawBody).digest("hex")
  return crypto.timingSafeEqual(Buffer.from(sig.slice(7)), Buffer.from(want))
}
```

### curl (geração local)

```bash
BODY='{"event_id":"abc","type":"webhook.test","occurred_at":"2026-05-07T00:00:00Z","data":{}}'
SIG=$(printf '%s' "$BODY" | openssl dgst -sha256 -hmac "$SECRET" -hex | awk '{print $2}')
curl -X POST "$URL" \
  -H "Content-Type: application/json" \
  -H "X-Radiocheck-Signature: sha256=$SIG" \
  -H "X-Radiocheck-Event: webhook.test" \
  -d "$BODY"
```

## Política de retry / DLQ

| Tentativa | Ação se ainda falhar          |
|-----------|-------------------------------|
| #1        | esperar 1m e tentar de novo   |
| #2        | esperar 5m                    |
| #3        | esperar 15m                   |
| #4        | esperar 1h                    |
| #5        | esperar 4h, então `dead`      |

- **2xx** → `delivered`.
- **4xx** (exceto 408 e 429) → `failed` (não retentamos: erros do cliente).
- **5xx, 408, 429, timeout, erro de rede** → retentar até 5 vezes, depois `dead`.

Linhas em `dead` ficam na tabela para inspeção via UI ou:

```sql
SELECT * FROM webhook_deliveries WHERE status='dead' ORDER BY updated_at DESC;
```

A política é codificada em `workers/internal/webhook/worker.go`
(`backoffSchedule`, `maxAttempts`).

## Métricas

Expostas em `/metrics` (Prometheus):

- `radiocheck_webhook_deliveries_total{status}` — counter, status ∈
  `{enqueued, delivered, failed, retry, dead}`.
- `radiocheck_webhook_delivery_duration_seconds` — histogram do POST.
- `radiocheck_webhook_queue_size` — gauge: `pending`.
- `radiocheck_webhook_dlq_size` — gauge: `dead`.

## Endpoint de teste

`POST /v1/internal/clients/{id}/webhook-test` enfileira um evento
`webhook.test` com `data: { message, sent_at, environment }`. Mesmo pipeline,
mesmo HMAC — útil para validar a integração antes de uma campanha real.

## Eventos suportados

Hoje só `detection.confirmed`. Novos eventos devem ser:

1. Adicionados à constante `EventType` em
   `workers/internal/webhook/outbox.go`.
2. Listados no validador `validEvent` em
   `workers/internal/api/handlers/webhooks.go`.
3. Ofertados no multiselect em `frontend/src/components/WebhookModal.jsx`.

## Migrations relacionadas

- `0008_fase2_webhooks.up.sql` — colunas `webhook_url`, `webhook_secret`
  (legado).
- `0012_webhooks_complete.up.sql` — `webhook_enabled`, `webhook_events`,
  tabela `webhook_deliveries`, índices.
