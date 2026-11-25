package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"radiocheck/internal/metrics"
)

// covWVerifyLikeReceiver reproduz o procedimento de verificação documentado em
// docs/features/webhooks.md (janela de ±5min + HMAC de "<ts>.<corpo>"), de forma
// independente do código de assinatura de produção.
func covWVerifyLikeReceiver(t *testing.T, h covWHit, secret string) {
	t.Helper()
	tsStr := h.Header.Get("X-Radiocheck-Timestamp")
	ts, err := strconv.ParseInt(tsStr, 10, 64)
	require.NoError(t, err, "timestamp precisa ser inteiro unix")
	d := time.Since(time.Unix(ts, 0))
	assert.Less(t, d, 5*time.Minute, "timestamp dentro da janela de replay")
	assert.Greater(t, d, -5*time.Minute)

	sig := h.Header.Get("X-Radiocheck-Signature")
	require.True(t, strings.HasPrefix(sig, "sha256="), "assinatura no formato sha256=<hex>: %q", sig)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(fmt.Sprintf("%d.", ts)))
	mac.Write(h.Body)
	want := hex.EncodeToString(mac.Sum(nil))
	assert.True(t, hmac.Equal([]byte(sig[len("sha256="):]), []byte(want)), "HMAC de ts.corpo confere")
}

const covWSecret = "segredo-de-teste-123456"

func TestCovW_Worker_Tick_DeliversSignedRequestAndMarksDelivered(t *testing.T) {
	ctx, pool := covWDB(t)
	recv := covWNewReceiver(t, 200, "recebido")
	client := covWSeedClient(t, ctx, pool, recv.URL(), covWSecret, true, nil)
	id := covWSeedDelivery(t, ctx, pool, client, "detection.confirmed",
		`{"event_id":"e-1","type":"detection.confirmed","data":{"station":{"name":"Rádio Teste FM"}}}`,
		"pending", 0, -time.Second)

	delivered0 := covWMetricValue(t, metrics.WebhookDeliveriesTotal.WithLabelValues("delivered"))
	covWWorker(pool).tick(ctx)

	hits := recv.Hits()
	require.Len(t, hits, 1)
	h := hits[0]
	assert.Equal(t, http.MethodPost, h.Method)
	assert.Equal(t, "application/json", h.Header.Get("Content-Type"))
	assert.Equal(t, "Radiocheck-Webhook/1.0", h.Header.Get("User-Agent"))
	assert.Equal(t, "detection.confirmed", h.Header.Get("X-Radiocheck-Event"))
	assert.Equal(t, id.String(), h.Header.Get("X-Radiocheck-Delivery-Id"), "delivery id é o id da linha da outbox")
	covWVerifyLikeReceiver(t, h, covWSecret)

	var body map[string]any
	require.NoError(t, json.Unmarshal(h.Body, &body))
	assert.Equal(t, "e-1", body["event_id"])
	assert.Equal(t, "detection.confirmed", body["type"])

	// Assinatura com OUTRO segredo não confere (a verificação não é trivial).
	badMac := hmac.New(sha256.New, []byte("outro-segredo-qualquer"))
	badMac.Write([]byte(h.Header.Get("X-Radiocheck-Timestamp") + "."))
	badMac.Write(h.Body)
	assert.NotEqual(t, "sha256="+hex.EncodeToString(badMac.Sum(nil)), h.Header.Get("X-Radiocheck-Signature"))

	row := covWReadDelivery(t, ctx, pool, id)
	assert.Equal(t, "delivered", row.Status)
	assert.Equal(t, 1, row.Attempts)
	require.NotNil(t, row.ResponseCode)
	assert.Equal(t, 200, *row.ResponseCode)
	require.NotNil(t, row.ResponseBody)
	assert.Equal(t, "recebido", *row.ResponseBody)
	assert.Nil(t, row.LastError)
	assert.NotNil(t, row.DeliveredAt)
	assert.InDelta(t, delivered0+1, covWMetricValue(t, metrics.WebhookDeliveriesTotal.WithLabelValues("delivered")), 0.001)

	// Já entregue: um novo tick não reenvia.
	covWWorker(pool).tick(ctx)
	assert.Len(t, recv.Hits(), 1)
}

// Contrato de status (docs/features/webhooks.md): 2xx entrega; 4xx (exceto
// 408/429) é falha definitiva; 408/429/5xx reagenda.
func TestCovW_Worker_Deliver_StatusCodePolicy(t *testing.T) {
	ctx, pool := covWDB(t)
	cases := []struct {
		code       int
		wantStatus string
	}{
		{200, "delivered"}, {201, "delivered"}, {204, "delivered"}, {299, "delivered"},
		{400, "failed"}, {401, "failed"}, {403, "failed"}, {404, "failed"}, {410, "failed"}, {422, "failed"},
		{408, "pending"}, {429, "pending"}, {500, "pending"}, {502, "pending"}, {503, "pending"},
	}
	for _, c := range cases {
		t.Run(strconv.Itoa(c.code), func(t *testing.T) {
			recv := covWNewReceiver(t, c.code, "corpo-"+strconv.Itoa(c.code))
			client := covWSeedClient(t, ctx, pool, recv.URL(), covWSecret, true, nil)
			id := covWSeedDelivery(t, ctx, pool, client, "detection.confirmed", `{"type":"x"}`, "pending", 0, -time.Second)

			covWWorker(pool).tick(ctx)

			row := covWReadDelivery(t, ctx, pool, id)
			assert.Equal(t, c.wantStatus, row.Status)
			assert.Equal(t, 1, row.Attempts, "toda tentativa incrementa attempt_count")
			require.NotNil(t, row.ResponseCode)
			assert.Equal(t, c.code, *row.ResponseCode)
			require.NotNil(t, row.ResponseBody)
			if c.code == 204 {
				assert.Empty(t, *row.ResponseBody, "204 não tem corpo")
			} else {
				assert.Equal(t, "corpo-"+strconv.Itoa(c.code), *row.ResponseBody)
			}
			switch c.wantStatus {
			case "delivered":
				assert.Nil(t, row.LastError)
				assert.NotNil(t, row.DeliveredAt)
			case "failed":
				require.NotNil(t, row.LastError)
				assert.Equal(t, "client error (non-retryable)", *row.LastError)
			case "pending":
				require.NotNil(t, row.LastError)
				assert.Equal(t, "non-2xx", *row.LastError)
				assert.Greater(t, row.NextAttempt, row.DBNow, "retry agendado no futuro")
			}
		})
	}
}

// Backoff 1m, 5m, 15m, 1h; na 5ª falha a linha vai pra DLQ (status dead).
func TestCovW_Worker_Retry_BackoffScheduleThenDead(t *testing.T) {
	ctx, pool := covWDB(t)
	recv := covWNewReceiver(t, 500, "erro")
	client := covWSeedClient(t, ctx, pool, recv.URL(), covWSecret, true, nil)

	cases := []struct {
		prevAttempts int
		wantDelay    time.Duration
	}{
		{0, 1 * time.Minute},
		{1, 5 * time.Minute},
		{2, 15 * time.Minute},
		{3, 1 * time.Hour},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("falha_%d", c.prevAttempts+1), func(t *testing.T) {
			id := covWSeedDelivery(t, ctx, pool, client, "detection.confirmed", `{"type":"x"}`, "pending", c.prevAttempts, -time.Second)
			covWWorker(pool).tick(ctx)
			row := covWReadDelivery(t, ctx, pool, id)
			assert.Equal(t, "pending", row.Status)
			assert.Equal(t, c.prevAttempts+1, row.Attempts)
			wait := row.NextAttempt.Sub(row.DBNow)
			assert.LessOrEqual(t, wait, c.wantDelay+2*time.Second)
			assert.GreaterOrEqual(t, wait, c.wantDelay-30*time.Second, "atraso do backoff para a falha %d", c.prevAttempts+1)
		})
	}

	t.Run("quinta falha vai para a DLQ", func(t *testing.T) {
		dead0 := covWMetricValue(t, metrics.WebhookDeliveriesTotal.WithLabelValues("dead"))
		id := covWSeedDelivery(t, ctx, pool, client, "detection.confirmed", `{"type":"x"}`, "pending", 4, -time.Second)
		covWWorker(pool).tick(ctx)
		row := covWReadDelivery(t, ctx, pool, id)
		assert.Equal(t, "dead", row.Status)
		assert.Equal(t, 5, row.Attempts)
		require.NotNil(t, row.LastError)
		assert.Equal(t, "non-2xx", *row.LastError)
		require.NotNil(t, row.ResponseCode)
		assert.Equal(t, 500, *row.ResponseCode)
		assert.InDelta(t, dead0+1, covWMetricValue(t, metrics.WebhookDeliveriesTotal.WithLabelValues("dead")), 0.001)

		// DLQ não é reprocessada.
		hitsBefore := len(recv.Hits())
		covWWorker(pool).tick(ctx)
		assert.Len(t, recv.Hits(), hitsBefore)
	})
}

// Falha de rede/timeout também é retentável e não grava response_code.
func TestCovW_Worker_Deliver_NetworkErrorsAreRetryable(t *testing.T) {
	ctx, pool := covWDB(t)

	t.Run("conexão recusada", func(t *testing.T) {
		client := covWSeedClient(t, ctx, pool, covWClosedURL(t), covWSecret, true, nil)
		id := covWSeedDelivery(t, ctx, pool, client, "detection.confirmed", `{"type":"x"}`, "pending", 0, -time.Second)
		retry0 := covWMetricValue(t, metrics.WebhookDeliveriesTotal.WithLabelValues("retry"))
		covWWorker(pool).tick(ctx)
		row := covWReadDelivery(t, ctx, pool, id)
		assert.Equal(t, "pending", row.Status)
		assert.Equal(t, 1, row.Attempts)
		assert.Nil(t, row.ResponseCode)
		assert.Nil(t, row.ResponseBody)
		require.NotNil(t, row.LastError)
		assert.NotEmpty(t, *row.LastError)
		assert.InDelta(t, retry0+1, covWMetricValue(t, metrics.WebhookDeliveriesTotal.WithLabelValues("retry")), 0.001)
	})

	t.Run("timeout do cliente HTTP", func(t *testing.T) {
		release := make(chan struct{})
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-release }))
		t.Cleanup(func() { close(release); srv.Close() })
		client := covWSeedClient(t, ctx, pool, srv.URL, covWSecret, true, nil)
		id := covWSeedDelivery(t, ctx, pool, client, "detection.confirmed", `{"type":"x"}`, "pending", 0, -time.Second)
		w := NewWorker(pool, zap.NewNop()).WithHTTPClient(&http.Client{Timeout: 300 * time.Millisecond})
		w.tick(ctx)
		row := covWReadDelivery(t, ctx, pool, id)
		assert.Equal(t, "pending", row.Status)
		assert.Nil(t, row.ResponseCode)
		require.NotNil(t, row.LastError)
		assert.Contains(t, strings.ToLower(*row.LastError), "timeout")
	})

	t.Run("URL inválida falha na montagem do request e reagenda", func(t *testing.T) {
		client := covWSeedClient(t, ctx, pool, "http://host:porta-invalida/x", covWSecret, true, nil)
		id := covWSeedDelivery(t, ctx, pool, client, "detection.confirmed", `{"type":"x"}`, "pending", 0, -time.Second)
		covWWorker(pool).tick(ctx)
		row := covWReadDelivery(t, ctx, pool, id)
		assert.Equal(t, "pending", row.Status)
		assert.Equal(t, 1, row.Attempts)
		require.NotNil(t, row.LastError)
		assert.Contains(t, *row.LastError, "invalid port")
	})
}

func TestCovW_Worker_Deliver_ClientWithoutURLGoesStraightToDead(t *testing.T) {
	ctx, pool := covWDB(t)
	client := covWSeedClient(t, ctx, pool, "", "", true, nil)
	id := covWSeedDelivery(t, ctx, pool, client, "detection.confirmed", `{"type":"x"}`, "pending", 0, -time.Second)
	covWWorker(pool).tick(ctx)
	row := covWReadDelivery(t, ctx, pool, id)
	assert.Equal(t, "dead", row.Status)
	assert.Equal(t, 1, row.Attempts)
	require.NotNil(t, row.LastError)
	assert.Equal(t, "client has no webhook_url configured", *row.LastError)
	assert.Nil(t, row.ResponseCode)
}

func TestCovW_Worker_Deliver_TruncatesResponseBodyTo4KB(t *testing.T) {
	ctx, pool := covWDB(t)
	recv := covWNewReceiver(t, 200, strings.Repeat("x", 10_000))
	client := covWSeedClient(t, ctx, pool, recv.URL(), covWSecret, true, nil)
	id := covWSeedDelivery(t, ctx, pool, client, "detection.confirmed", `{"type":"x"}`, "pending", 0, -time.Second)
	covWWorker(pool).tick(ctx)
	row := covWReadDelivery(t, ctx, pool, id)
	require.NotNil(t, row.ResponseBody)
	assert.Len(t, *row.ResponseBody, maxResponseBodyBytes, "corpo da resposta é cortado em 4KB antes de persistir")
}

func TestCovW_Worker_ClaimBatch_OnlyDuePendingRowsOldestFirstWithLimit(t *testing.T) {
	ctx, pool := covWDB(t)
	recv := covWNewReceiver(t, 200, "ok")
	client := covWSeedClient(t, ctx, pool, recv.URL(), covWSecret, true, nil)

	oldest := covWSeedDelivery(t, ctx, pool, client, "detection.confirmed", `{"n":1}`, "pending", 0, -30*time.Second)
	middle := covWSeedDelivery(t, ctx, pool, client, "detection.confirmed", `{"n":2}`, "pending", 0, -20*time.Second)
	newest := covWSeedDelivery(t, ctx, pool, client, "detection.confirmed", `{"n":3}`, "pending", 0, -10*time.Second)
	future := covWSeedDelivery(t, ctx, pool, client, "detection.confirmed", `{"n":4}`, "pending", 1, time.Hour)
	done := covWSeedDelivery(t, ctx, pool, client, "detection.confirmed", `{"n":5}`, "delivered", 1, -time.Hour)
	dead := covWSeedDelivery(t, ctx, pool, client, "detection.confirmed", `{"n":6}`, "dead", 5, -time.Hour)
	failed := covWSeedDelivery(t, ctx, pool, client, "detection.confirmed", `{"n":7}`, "failed", 1, -time.Hour)

	w := covWWorker(pool)
	got, err := w.claimBatch(ctx, 2)
	require.NoError(t, err)
	require.Len(t, got, 2, "LIMIT respeitado")
	assert.Equal(t, oldest, got[0].ID, "mais antigo primeiro")
	assert.Equal(t, middle, got[1].ID)
	assert.Equal(t, recv.URL(), got[0].URL)
	assert.Equal(t, covWSecret, got[0].Secret)
	assert.Equal(t, "detection.confirmed", got[0].EventType)
	assert.JSONEq(t, `{"n":1}`, string(got[0].Payload))

	all, err := w.claimBatch(ctx, 50)
	require.NoError(t, err)
	ids := map[uuid.UUID]bool{}
	for _, d := range all {
		ids[d.ID] = true
	}
	assert.True(t, ids[oldest] && ids[middle] && ids[newest])
	for name, id := range map[string]uuid.UUID{"futuro": future, "delivered": done, "dead": dead, "failed": failed} {
		assert.False(t, ids[id], "linha %s não pode ser reclamada", name)
	}

	// tick com batchSize 2 entrega só as duas mais antigas.
	w.batchSize = 2
	w.tick(ctx)
	assert.Equal(t, "delivered", covWReadDelivery(t, ctx, pool, oldest).Status)
	assert.Equal(t, "delivered", covWReadDelivery(t, ctx, pool, middle).Status)
	assert.Equal(t, "pending", covWReadDelivery(t, ctx, pool, newest).Status)
	assert.Len(t, recv.Hits(), 2)
}

func TestCovW_Worker_RefreshGauges_ReflectQueueAndDLQ(t *testing.T) {
	ctx, pool := covWDB(t)
	client := covWSeedClient(t, ctx, pool, "http://example.com/h", covWSecret, true, nil)
	covWSeedDelivery(t, ctx, pool, client, "detection.confirmed", `{}`, "pending", 0, time.Hour)
	covWSeedDelivery(t, ctx, pool, client, "detection.confirmed", `{}`, "pending", 1, time.Hour)
	covWSeedDelivery(t, ctx, pool, client, "detection.confirmed", `{}`, "dead", 5, -time.Hour)

	var pending, dead int
	require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FILTER (WHERE status='pending'), COUNT(*) FILTER (WHERE status='dead') FROM webhook_deliveries`).Scan(&pending, &dead))

	covWWorker(pool).refreshGauges(ctx)
	assert.InDelta(t, float64(pending), covWMetricValue(t, metrics.WebhookQueueSize), 0.001)
	assert.InDelta(t, float64(dead), covWMetricValue(t, metrics.WebhookDLQSize), 0.001)
	assert.GreaterOrEqual(t, pending, 2)
	assert.GreaterOrEqual(t, dead, 1)
}

func TestCovW_Worker_Run_PollsUntilContextCancelled(t *testing.T) {
	ctx, pool := covWDB(t)
	recv := covWNewReceiver(t, 200, "ok")
	client := covWSeedClient(t, ctx, pool, recv.URL(), covWSecret, true, nil)
	first := covWSeedDelivery(t, ctx, pool, client, "detection.confirmed", `{"n":1}`, "pending", 0, -time.Second)

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	w := covWWorker(pool).WithPollInterval(20 * time.Millisecond)
	done := make(chan error, 1)
	go func() { done <- w.Run(runCtx) }()

	covWEventually(t, func() bool { return covWReadDelivery(t, ctx, pool, first).Status == "delivered" }, "primeira entrega")

	// Linha que aparece DEPOIS do primeiro tick é pega por um tick do ticker.
	second := covWSeedDelivery(t, ctx, pool, client, "detection.confirmed", `{"n":2}`, "pending", 0, -time.Second)
	covWEventually(t, func() bool { return covWReadDelivery(t, ctx, pool, second).Status == "delivered" }, "entrega via ticker")

	cancel()
	select {
	case err := <-done:
		assert.NoError(t, err, "Run devolve nil ao cancelar o contexto")
	case <-time.After(20 * time.Second):
		t.Fatal("Run não terminou após cancelar o contexto")
	}
}

// Com o pool fechado nada pode entrar em pânico: erros de claim/update são logados.
func TestCovW_Worker_DBFailuresDoNotPanic(t *testing.T) {
	ctx, pool := covWDB(t)
	_, other := covWDB(t)
	other.Close()
	w := NewWorker(other, zap.NewNop())

	_, err := w.claimBatch(ctx, 1)
	require.Error(t, err, "claim sem banco devolve erro")

	d := pendingDelivery{ID: uuid.New(), ClientID: uuid.New(), EventType: "x", Payload: []byte(`{}`), URL: "http://example.com", Secret: "s"}
	assert.NotPanics(t, func() {
		w.tick(ctx)
		w.refreshGauges(ctx)
		w.markDelivered(ctx, d, 200, "ok")
		w.markFailed(ctx, d, "x", 400, "b")
		w.markDead(ctx, d, "x", 0, "")
		w.scheduleRetryOrDead(ctx, d, "x", 500, "b")
	})
	_ = pool
}
