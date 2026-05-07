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
X-Radiocheck-Timestamp: 1715123456
X-Radiocheck-Signature: sha256=<hex>
X-Radiocheck-Delivery-Id: <uuid>
```

`<hex>` é `HMAC_SHA256(secret, "<X-Radiocheck-Timestamp>.<raw_body>")` em
lowercase hex (formato Stripe-style). **A assinatura cobre o timestamp +
body**, não apenas o body — isso impede replay de requisições antigas.

### O que o receiver DEVE verificar (obrigatório)

1. `X-Radiocheck-Timestamp` está dentro de uma janela aceitável (recomendado:
   ±5 minutos do seu relógio). Rejeitar fora da janela com 401, mesmo que a
   assinatura seja válida — a freshness é defesa contra replay.
2. `HMAC_SHA256(secret, ts + "." + raw_body)` é igual à assinatura recebida,
   usando comparação de tempo constante (`hmac.Equal`/`timingSafeEqual`).

`X-Radiocheck-Delivery-Id` é o UUID da row em `webhook_deliveries`. É **estável
entre tentativas** — se o mesmo evento for re-tentado após backoff, o cliente
recebe o mesmo `delivery_id`. Use-o para correlacionar logs e deduplicar
entregas idempotentemente. (Não confundir com `event_id` no payload, que é o
identificador do evento de domínio.)

### Go

```go
import (
    "crypto/hmac"
    "crypto/sha256"
    "encoding/hex"
    "fmt"
    "io"
    "strconv"
    "strings"
    "time"
)

func verify(req *http.Request, secret string) (bool, []byte, error) {
    body, err := io.ReadAll(req.Body)
    if err != nil { return false, nil, err }

    // 1. Freshness check.
    tsStr := req.Header.Get("X-Radiocheck-Timestamp")
    ts, err := strconv.ParseInt(tsStr, 10, 64)
    if err != nil { return false, body, nil }
    if d := time.Since(time.Unix(ts, 0)); d > 5*time.Minute || d < -5*time.Minute {
        return false, body, nil // outside replay window
    }

    // 2. Signature check (constant time).
    sig := req.Header.Get("X-Radiocheck-Signature")
    if !strings.HasPrefix(sig, "sha256=") { return false, body, nil }
    mac := hmac.New(sha256.New, []byte(secret))
    mac.Write([]byte(fmt.Sprintf("%d.", ts)))
    mac.Write(body)
    want := hex.EncodeToString(mac.Sum(nil))
    return hmac.Equal([]byte(sig[7:]), []byte(want)), body, nil
}
```

### Node.js

```js
import crypto from "crypto"

const TOLERANCE_MS = 5 * 60 * 1000

export function verify(req, secret, rawBody) {
  const tsStr = req.headers["x-radiocheck-timestamp"] || ""
  const ts = Number.parseInt(tsStr, 10)
  if (!Number.isFinite(ts)) return false
  if (Math.abs(Date.now() - ts * 1000) > TOLERANCE_MS) return false

  const sig = req.headers["x-radiocheck-signature"] || ""
  if (!sig.startsWith("sha256=")) return false
  const want = crypto
    .createHmac("sha256", secret)
    .update(`${ts}.`)
    .update(rawBody)
    .digest("hex")
  return crypto.timingSafeEqual(Buffer.from(sig.slice(7)), Buffer.from(want))
}
```

### curl (geração local)

```bash
BODY='{"event_id":"abc","type":"webhook.test","occurred_at":"2026-05-07T00:00:00Z","data":{}}'
TS=$(date +%s)
SIG=$(printf '%s.%s' "$TS" "$BODY" | openssl dgst -sha256 -hmac "$SECRET" -hex | awk '{print $2}')
curl -X POST "$URL" \
  -H "Content-Type: application/json" \
  -H "X-Radiocheck-Timestamp: $TS" \
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

## Trade-offs e dívida técnica

Decisões deliberadas que aceitamos para entregar a Etapa 2C dentro do prazo.
Cada item tem o caminho de evolução documentado para quando virarmos
produção.

### Outbox não-transacional vs detecção

A inserção em `detections` (feita por `evidence.Service` ao consumir
`detections.confirmed` no NATS) e a inserção em `webhook_deliveries` (feita
por `webhook.Deliverer` no mesmo subject) acontecem em **transações
distintas, em processos distintos**. Se o processo de webhook crashar
**entre o publish do NATS e o insert na outbox**, o evento é perdido — não
há replay automático.

Para produção: migrar para um **outbox transacional**, onde a row em
`webhook_deliveries` é inserida na mesma `BEGIN/COMMIT` que `detections`
(no caminho `evidence.Service`), garantindo "ou ambos commitam, ou nenhum".
O NATS deixa de ser o gatilho de enfileiramento e vira só notificação de
"tem trabalho novo".

### Worker serial dentro do batch

`batchSize = 10` é o `LIMIT` do `SELECT ... FOR UPDATE SKIP LOCKED`. As 10
deliveries claimadas são processadas **sequencialmente** dentro do tick —
não há fan-out paralelo. Para o volume da PoC isso é amplamente suficiente
(latência típica < 500ms por POST × 10 = 5s, dentro do `pollInterval`).

Para escala (>50 detections/s): adicionar `errgroup` + semáforo dentro de
`tick()` para fazer N POSTs em paralelo, com `N` configurável (`maxInflight`
separado de `batchSize`). O `SKIP LOCKED` já permite múltiplas réplicas do
worker, então fan-out horizontal por processo também é uma alternativa.

### Secret armazenado em plaintext

`clients.webhook_secret` é `TEXT` plain. Quem tem `SELECT` na tabela
`clients` lê o segredo. O endpoint `GET /webhook` mascara para
`webhook_secret_masked` na API, mas o DB não está protegido.

Para produção: column encryption (`pgcrypto` com chave fora do DB) ou
KMS-wrapping (AWS KMS, GCP KMS, HashiCorp Vault) com decrypt no caminho do
worker. Migrar com migration que roda
`UPDATE clients SET webhook_secret = pgp_sym_encrypt(webhook_secret, key)`.

### Sem `Idempotency-Key` no envelope

Não enviamos `Idempotency-Key` separado — o `event_id` no payload já é
único por evento de domínio, e o `X-Radiocheck-Delivery-Id` é único por
tentativa de delivery (mas estável entre retries de uma mesma row em
`webhook_deliveries`). Clientes que precisam idempotência forte devem
indexar `event_id` localmente e ignorar duplicatas — duplicatas raras
podem acontecer quando uma resposta 2xx do cliente é perdida na rede e o
worker reagenda retry.

Não há plano de adicionar `Idempotency-Key` enquanto o contrato com `event_id`
estiver atendendo. Se um cliente reportar problema, reavaliar.

### HTTPS obrigatório

`PatchConfig` agora rejeita `http://` por default. A única exceção é
`RADIOCHECK_ENV=development` **somado a** host loopback (`localhost`,
`127.0.0.1`, `::1`) — combinacao usada apenas em testes locais. URL com
qualquer outro host em http retorna 400 com mensagem
`http URLs are not allowed in production; set RADIOCHECK_ENV=development for local testing`.

Resolvido em commit do security review (2026-05-07). Webhook receivers em
producao **devem** servir HTTPS valido, senao a config falha antes de salvar.

### Refresh de gauges com `COUNT(*)`

`refreshGauges()` roda dois `SELECT COUNT(*)` a cada 5s. Em volumes da PoC
(<10k rows em `webhook_deliveries`) isso é < 1ms mesmo sem índice especial.
Para escala (>1M rows histórico), considerar índice parcial por status
ativo, ou cache em `webhook_stats` atualizado por trigger. TODO está no
código (`worker.go::refreshGauges`).
