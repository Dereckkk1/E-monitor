package webhook

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"radiocheck/internal/catalog"
	"radiocheck/internal/events"
)

func covWDeliverer(pool *pgxpool.Pool) *Deliverer {
	return New(pool, nil, zap.NewNop(), catalog.NewClients(pool), nil, nil)
}

func covWDetectionJSON(t *testing.T, station uuid.UUID, short int32, detectedAt string) []byte {
	t.Helper()
	b, err := json.Marshal(detectionEvent{
		StationID:         station.String(),
		CommercialShortID: short,
		DetectedAt:        detectedAt,
		OffsetFrames:      12,
		Confidence:        0.92,
	})
	require.NoError(t, err)
	return b
}

func covWRetractedJSON(t *testing.T, station uuid.UUID, short int32, detectedAt string) []byte {
	t.Helper()
	b, err := json.Marshal(retractedEvent{
		StationID:         station.String(),
		CommercialShortID: short,
		DetectedAt:        detectedAt,
		RetractedAt:       "2026-06-15T12:00:05Z",
		Reason:            "longer_cut_detected",
		Confidence:        0.91,
	})
	require.NoError(t, err)
	return b
}

type covWEnvelope struct {
	EventID    string `json:"event_id"`
	Type       string `json:"type"`
	OccurredAt string `json:"occurred_at"`
	Data       struct {
		Detection  map[string]any `json:"detection"`
		Station    map[string]any `json:"station"`
		Commercial map[string]any `json:"commercial"`
	} `json:"data"`
}

func covWLastEnvelope(t *testing.T, ctx context.Context, pool *pgxpool.Pool, clientID uuid.UUID, eventType string) covWEnvelope {
	t.Helper()
	var raw []byte
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT payload FROM webhook_deliveries WHERE client_id = $1 AND event_type = $2
		ORDER BY created_at DESC LIMIT 1`, clientID, eventType).Scan(&raw))
	var env covWEnvelope
	require.NoError(t, json.Unmarshal(raw, &env))
	return env
}

// ───────────────────── Outbox.Enqueue ─────────────────────

func TestCovW_Outbox_Enqueue(t *testing.T) {
	ctx, pool := covWDB(t)
	ob := covWOutbox(pool)

	t.Run("grava a linha pending com o envelope documentado", func(t *testing.T) {
		client := covWSeedClient(t, ctx, pool, "https://example.com/hook", covWSecret, true, nil)
		before := time.Now().UTC().Add(-2 * time.Second)
		require.NoError(t, ob.Enqueue(ctx, &Event{
			Type:     EventDetectionConfirmed,
			ClientID: client,
			Body:     map[string]any{"detection": map[string]any{"confidence": 0.5}},
		}))

		var status string
		var attempts int
		var nextAt time.Time
		require.NoError(t, pool.QueryRow(ctx, `
			SELECT status, attempt_count, next_attempt_at FROM webhook_deliveries
			WHERE client_id = $1`, client).Scan(&status, &attempts, &nextAt))
		assert.Equal(t, "pending", status)
		assert.Zero(t, attempts)
		assert.True(t, nextAt.After(before) && nextAt.Before(time.Now().Add(time.Minute)), "primeira tentativa é imediata")

		env := covWLastEnvelope(t, ctx, pool, client, "detection.confirmed")
		_, err := uuid.Parse(env.EventID)
		assert.NoError(t, err, "event_id é um UUID")
		assert.Equal(t, "detection.confirmed", env.Type)
		occurred, err := time.Parse(time.RFC3339Nano, env.OccurredAt)
		require.NoError(t, err)
		assert.WithinDuration(t, time.Now(), occurred, time.Minute)
		assert.InDelta(t, 0.5, env.Data.Detection["confidence"], 1e-9)
	})

	t.Run("cada evento ganha um event_id próprio", func(t *testing.T) {
		client := covWSeedClient(t, ctx, pool, "https://example.com/hook", covWSecret, true, nil)
		for i := 0; i < 2; i++ {
			require.NoError(t, ob.Enqueue(ctx, &Event{Type: EventDetectionConfirmed, ClientID: client, Body: map[string]any{}}))
		}
		var distinct int
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT COUNT(DISTINCT payload->>'event_id') FROM webhook_deliveries WHERE client_id = $1`, client).Scan(&distinct))
		assert.Equal(t, 2, distinct)
	})

	noopCases := []struct {
		name     string
		url      string
		enabled  bool
		events   []string
		evType   EventType
		wantRows int
	}{
		{"cliente sem URL é descartado em silêncio", "", true, nil, EventDetectionConfirmed, 0},
		{"webhook desligado é descartado em silêncio", "https://example.com/h", false, nil, EventDetectionConfirmed, 0},
		{"evento fora da assinatura é descartado", "https://example.com/h", true, []string{"detection.confirmed"}, EventWebhookTest, 0},
		{"assinatura com curinga aceita qualquer evento", "https://example.com/h", true, []string{"*"}, EventWebhookTest, 1},
		{"assinatura explícita do tipo é aceita", "https://example.com/h", true, []string{"detection.retracted"}, EventDetectionRetracted, 1},
	}
	for _, c := range noopCases {
		t.Run(c.name, func(t *testing.T) {
			client := covWSeedClient(t, ctx, pool, c.url, covWSecret, c.enabled, c.events)
			require.NoError(t, ob.Enqueue(ctx, &Event{Type: c.evType, ClientID: client, Body: map[string]any{}}))
			assert.Equal(t, c.wantRows, covWCountDeliveries(t, ctx, pool, client, string(c.evType)))
		})
	}

	t.Run("cliente inexistente devolve erro de config", func(t *testing.T) {
		err := ob.Enqueue(ctx, &Event{Type: EventDetectionConfirmed, ClientID: uuid.New(), Body: map[string]any{}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "load config")
	})

	t.Run("corpo não serializável devolve erro", func(t *testing.T) {
		client := covWSeedClient(t, ctx, pool, "https://example.com/h", covWSecret, true, nil)
		err := ob.Enqueue(ctx, &Event{Type: EventDetectionConfirmed, ClientID: client, Body: map[string]any{"f": func() {}}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "marshal payload")
		assert.Zero(t, covWCountDeliveries(t, ctx, pool, client, "detection.confirmed"))
	})

	t.Run("falha no INSERT devolve erro", func(t *testing.T) {
		client := covWSeedClient(t, ctx, pool, "https://example.com/h", covWSecret, true, nil)
		_, closed := covWDB(t)
		closed.Close()
		broken := NewOutbox(closed, catalog.NewClients(pool), zap.NewNop())
		err := broken.Enqueue(ctx, &Event{Type: EventDetectionConfirmed, ClientID: client, Body: map[string]any{}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "insert")
	})
}

// ───────────────────── handleDetection / handleRetracted ─────────────────────

func TestCovW_HandleDetection_LegacyCommercial(t *testing.T) {
	ctx, pool := covWDB(t)
	d := covWDeliverer(pool)
	client := covWSeedClient(t, ctx, pool, "https://example.com/h", covWSecret, true,
		[]string{"detection.confirmed", "detection.retracted"})
	camp := covWSeedCampaign(t, ctx, pool, client, "ativa", "2026-06-01", "2026-07-31")
	comID, short := covWSeedCommercial(t, ctx, pool, camp, "Spot Teste 30s")
	station := covWSeedStation(t, ctx, pool, "Rádio Teste FM")

	require.NoError(t, d.handleDetection(ctx, covWDetectionJSON(t, station, short, "2026-06-15T12:00:00Z")))

	env := covWLastEnvelope(t, ctx, pool, client, "detection.confirmed")
	assert.Equal(t, "detection.confirmed", env.Type)
	assert.Equal(t, "2026-06-15T12:00:00Z", env.Data.Detection["detected_at"])
	assert.InDelta(t, 0.92, env.Data.Detection["confidence"], 1e-9)
	assert.InDelta(t, 12, env.Data.Detection["offset_frames"], 1e-9)
	assert.Equal(t, station.String(), env.Data.Station["id"])
	assert.Equal(t, "Rádio Teste FM", env.Data.Station["name"], "nome da emissora vem do cadastro")
	assert.Equal(t, comID.String(), env.Data.Commercial["id"])
	assert.InDelta(t, float64(short), env.Data.Commercial["short_id"], 1e-9)
	assert.Equal(t, "Spot Teste 30s", env.Data.Commercial["title"])

	// retraction
	require.NoError(t, d.handleRetracted(ctx, covWRetractedJSON(t, station, short, "2026-06-15T12:00:00Z")))
	renv := covWLastEnvelope(t, ctx, pool, client, "detection.retracted")
	assert.Equal(t, "detection.retracted", renv.Type)
	assert.Equal(t, "longer_cut_detected", renv.Data.Detection["reason"])
	assert.Equal(t, "2026-06-15T12:00:05Z", renv.Data.Detection["retracted_at"])
	assert.InDelta(t, 0.91, renv.Data.Detection["confidence"], 1e-9)
	assert.Equal(t, comID.String(), renv.Data.Commercial["id"])
	assert.Equal(t, "Rádio Teste FM", renv.Data.Station["name"])
}

func TestCovW_HandleDetection_MaterialFallback(t *testing.T) {
	ctx, pool := covWDB(t)
	d := covWDeliverer(pool)
	client := covWSeedClient(t, ctx, pool, "https://example.com/h", covWSecret, true,
		[]string{"detection.confirmed", "detection.retracted"})
	camp := covWSeedCampaign(t, ctx, pool, client, "ativa", "2026-06-01", "2026-07-31")
	station := covWSeedStation(t, ctx, pool, "Rádio Teste FM")
	other := covWSeedStation(t, ctx, pool, "Outra Rádio FM")
	matID, short := covWSeedMaterialLink(t, ctx, pool, client, camp, station, "Material Teste")

	t.Run("emissora alvo da campanha enfileira", func(t *testing.T) {
		require.NoError(t, d.handleDetection(ctx, covWDetectionJSON(t, station, short, "2026-06-15T12:00:00Z")))
		env := covWLastEnvelope(t, ctx, pool, client, "detection.confirmed")
		assert.Equal(t, matID.String(), env.Data.Commercial["id"])
		assert.Equal(t, "Material Teste", env.Data.Commercial["title"])
	})

	t.Run("retração também resolve pelo material", func(t *testing.T) {
		require.NoError(t, d.handleRetracted(ctx, covWRetractedJSON(t, station, short, "2026-06-15T12:00:00Z")))
		env := covWLastEnvelope(t, ctx, pool, client, "detection.retracted")
		assert.Equal(t, matID.String(), env.Data.Commercial["id"])
	})

	t.Run("emissora fora do alvo é descartada sem erro", func(t *testing.T) {
		n0 := covWCountDeliveries(t, ctx, pool, client, "detection.confirmed")
		require.NoError(t, d.handleDetection(ctx, covWDetectionJSON(t, other, short, "2026-06-15T12:00:00Z")))
		assert.Equal(t, n0, covWCountDeliveries(t, ctx, pool, client, "detection.confirmed"))
	})

	t.Run("fora do período da campanha é descartada", func(t *testing.T) {
		n0 := covWCountDeliveries(t, ctx, pool, client, "detection.confirmed")
		require.NoError(t, d.handleDetection(ctx, covWDetectionJSON(t, station, short, "2026-09-15T12:00:00Z")))
		assert.Equal(t, n0, covWCountDeliveries(t, ctx, pool, client, "detection.confirmed"))
	})

	t.Run("campanha concluída não notifica", func(t *testing.T) {
		_, err := pool.Exec(ctx, `UPDATE campaigns SET status = 'concluida' WHERE id = $1`, camp)
		require.NoError(t, err)
		n0 := covWCountDeliveries(t, ctx, pool, client, "detection.confirmed")
		require.NoError(t, d.handleDetection(ctx, covWDetectionJSON(t, station, short, "2026-06-15T12:00:00Z")))
		assert.Equal(t, n0, covWCountDeliveries(t, ctx, pool, client, "detection.confirmed"))
	})
}

// A atribuição (evidence) e o resto do sistema resolvem "o dia da campanha" em
// America/Sao_Paulo. Uma tocada às 23:30 BRT do último dia da campanha chega em
// UTC como 02:30 do dia seguinte; o webhook tem que enfileirar mesmo assim.
func TestCovW_HandleDetection_LateNightLastDayUsesSaoPauloDate(t *testing.T) {
	ctx, pool := covWDB(t)
	d := covWDeliverer(pool)
	client := covWSeedClient(t, ctx, pool, "https://example.com/h", covWSecret, true,
		[]string{"detection.confirmed", "detection.retracted"})
	camp := covWSeedCampaign(t, ctx, pool, client, "ativa", "2026-07-01", "2026-07-31")
	station := covWSeedStation(t, ctx, pool, "Rádio Teste FM")
	_, short := covWSeedMaterialLink(t, ctx, pool, client, camp, station, "Material Teste")

	// 2026-07-31 23:30 BRT == 2026-08-01 02:30 UTC
	require.NoError(t, d.handleDetection(ctx, covWDetectionJSON(t, station, short, "2026-08-01T02:30:00Z")))
	require.Equal(t, 1, covWCountDeliveries(t, ctx, pool, client, "detection.confirmed"),
		"tocada 23:30 (São Paulo) do último dia da campanha tem de gerar o webhook")

	require.NoError(t, d.handleRetracted(ctx, covWRetractedJSON(t, station, short, "2026-08-01T02:30:00Z")))
	assert.Equal(t, 1, covWCountDeliveries(t, ctx, pool, client, "detection.retracted"))
}

func TestCovW_HandleDetection_DropsAndErrors(t *testing.T) {
	ctx, pool := covWDB(t)
	d := covWDeliverer(pool)
	client := covWSeedClient(t, ctx, pool, "https://example.com/h", covWSecret, true, nil)
	station := covWSeedStation(t, ctx, pool, "Rádio Teste FM")

	t.Run("short_id desconhecido é descartado sem erro", func(t *testing.T) {
		require.NoError(t, d.handleDetection(ctx, covWDetectionJSON(t, station, 2_000_000_001, "2026-06-15T12:00:00Z")))
		require.NoError(t, d.handleRetracted(ctx, covWRetractedJSON(t, station, 2_000_000_001, "2026-06-15T12:00:00Z")))
		assert.Zero(t, covWCountDeliveries(t, ctx, pool, client, "detection.confirmed"))
	})

	t.Run("station_id inválido", func(t *testing.T) {
		raw, _ := json.Marshal(detectionEvent{StationID: "nao-uuid", DetectedAt: "2026-06-15T12:00:00Z"})
		err := d.handleDetection(ctx, raw)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "parse station id")
		rraw, _ := json.Marshal(retractedEvent{StationID: "nao-uuid", DetectedAt: "2026-06-15T12:00:00Z"})
		err = d.handleRetracted(ctx, rraw)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "parse station id")
	})

	t.Run("detected_at inválido", func(t *testing.T) {
		raw, _ := json.Marshal(detectionEvent{StationID: station.String(), DetectedAt: "ontem"})
		err := d.handleDetection(ctx, raw)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "parse detected_at")
		rraw, _ := json.Marshal(retractedEvent{StationID: station.String(), DetectedAt: "ontem"})
		err = d.handleRetracted(ctx, rraw)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "parse detected_at")
	})

	t.Run("cliente sem webhook configurado não gera linha", func(t *testing.T) {
		noHook := covWSeedClient(t, ctx, pool, "", "", false, nil)
		camp := covWSeedCampaign(t, ctx, pool, noHook, "ativa", "2026-06-01", "2026-07-31")
		_, short := covWSeedCommercial(t, ctx, pool, camp, "Spot")
		require.NoError(t, d.handleDetection(ctx, covWDetectionJSON(t, station, short, "2026-06-15T12:00:00Z")))
		assert.Zero(t, covWCountDeliveries(t, ctx, pool, noHook, "detection.confirmed"))
	})

	t.Run("erro de banco na resolução do commercial é propagado", func(t *testing.T) {
		_, closed := covWDB(t)
		closed.Close()
		broken := New(closed, nil, zap.NewNop(), catalog.NewClients(pool), nil, nil)
		err := broken.handleDetection(ctx, covWDetectionJSON(t, station, 1, "2026-06-15T12:00:00Z"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "lookup commercial")
		err = broken.handleRetracted(ctx, covWRetractedJSON(t, station, 1, "2026-06-15T12:00:00Z"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "lookup commercial")
	})
}

// ───────────────────── Deliverer.Start (NATS falso) ─────────────────────

func TestCovW_Deliverer_Start_ConsumesConfirmedAndRetracted(t *testing.T) {
	ctx, pool := covWDB(t)
	srv := covWNewNATS(t)
	nc := covWConnect(t, srv)
	pub := covWConnect(t, srv)

	client := covWSeedClient(t, ctx, pool, "https://example.com/h", covWSecret, true,
		[]string{"detection.confirmed", "detection.retracted"})
	camp := covWSeedCampaign(t, ctx, pool, client, "ativa", "2026-06-01", "2026-07-31")
	_, short := covWSeedCommercial(t, ctx, pool, camp, "Spot Teste 30s")
	station := covWSeedStation(t, ctx, pool, "Rádio Teste FM")

	d := New(pool, nc, zap.NewNop(), catalog.NewClients(pool), nil, nil)
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	require.NoError(t, d.Start(runCtx))
	require.NoError(t, nc.Flush())

	require.NoError(t, pub.Publish(events.SubjectDetectionConfirmed, covWDetectionJSON(t, station, short, "2026-06-15T12:00:00Z")))
	require.NoError(t, pub.Publish(events.SubjectDetectionRetracted, covWRetractedJSON(t, station, short, "2026-06-15T12:00:00Z")))
	// mensagens ruins só viram log (não derrubam a assinatura)
	require.NoError(t, pub.Publish(events.SubjectDetectionConfirmed, []byte("nao-json")))
	require.NoError(t, pub.Publish(events.SubjectDetectionRetracted, []byte("nao-json")))
	require.NoError(t, pub.Flush())

	covWEventually(t, func() bool {
		return covWCountDeliveries(t, ctx, pool, client, "detection.confirmed") == 1 &&
			covWCountDeliveries(t, ctx, pool, client, "detection.retracted") == 1
	}, "as duas assinaturas devem alimentar a outbox")
	assert.Equal(t, "Rádio Teste FM", covWLastEnvelope(t, ctx, pool, client, "detection.confirmed").Data.Station["name"])

	// Cancelar o contexto drena as duas assinaturas.
	cancel()
	covWEventually(t, func() bool { return nc.NumSubscriptions() == 0 }, "ctx cancelado deve drenar as assinaturas")
}

func TestCovW_Deliverer_Start_FailsOnClosedConn(t *testing.T) {
	srv := covWNewNATS(t)
	nc := covWConnect(t, srv)
	nc.Close()
	err := New(nil, nc, zap.NewNop(), nil, nil, nil).Start(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "subscribe confirmed")
}

func TestCovW_Deliverer_NewWiresOutbox(t *testing.T) {
	ctx, pool := covWDB(t)
	d := New(pool, nil, zap.NewNop(), catalog.NewClients(pool), nil, nil)
	require.NotNil(t, d.Outbox())
	client := covWSeedClient(t, ctx, pool, "https://example.com/h", covWSecret, true, []string{"webhook.test"})
	require.NoError(t, d.Outbox().Enqueue(ctx, &Event{Type: EventWebhookTest, ClientID: client, Body: map[string]any{"message": "ping"}}))
	assert.Equal(t, 1, covWCountDeliveries(t, ctx, pool, client, "webhook.test"))
}
