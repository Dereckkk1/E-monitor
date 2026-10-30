package handlers

// detections_acoes_lock_test.go — TRAVA o que as rotas de veiculação manual,
// ignorar e restaurar fazem hoje (status, texto, linha no banco, efeito na cota
// do dia), ANTES de a lógica sair do handler para internal/acoes (Fatia 4,
// Task 11). Passa no código antigo e no novo.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/auth"
	"radiocheck/internal/catalog"
	"radiocheck/internal/mcp/mcptest"
)

type cenarioVeic struct {
	ctx      context.Context
	pool     *pgxpool.Pool
	admin    uuid.UUID
	emissora uuid.UUID
	material uuid.UUID
	campanha uuid.UUID
	h        *DetectionsHandler
	r        chi.Router
}

// Regra: cota 1 por dia, faixa 10:00-12:00, dia 10/06/2026 (quarta).
func novoCenarioVeic(t *testing.T) *cenarioVeic {
	t.Helper()
	ctx, pool := mcptest.DBCatalogo(t)
	c := &cenarioVeic{ctx: ctx, pool: pool}
	c.admin = mcptest.Usuario(t, ctx, pool, "admin", true)
	cliente := mcptest.Cliente(t, ctx, pool, "Cliente Veic")
	c.emissora = mcptest.Emissora(t, ctx, pool, "Rádio Veic", 1000)
	tipo := mcptest.TipoMaterial(t, ctx, pool, "Spot Veic")
	c.material = mcptest.Material(t, ctx, pool, cliente, tipo, "Spot Veic")
	c.campanha = mcptest.Campanha(t, ctx, pool, cliente, "Campanha Veic", "2026-06-01", "2026-06-30", c.emissora)
	mcptest.Vincular(t, ctx, pool, c.campanha, c.material, c.emissora)
	mcptest.Regra(t, ctx, pool, c.campanha, tipo, c.emissora, "2026-06-01", "2026-06-30", 127, "10:00", "12:00", 1)

	c.h = &DetectionsHandler{Repo: catalog.NewDetections(pool)}
	c.r = chi.NewRouter()
	c.r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if req.Header.Get("X-Sem-Claims") == "" {
				req = req.WithContext(auth.WithClaims(req.Context(), &auth.Claims{UserID: c.admin, Role: "admin"}))
			}
			next.ServeHTTP(w, req)
		})
	})
	c.r.Post("/detections/manual", c.h.CreateManual)
	c.r.Post("/detections/{id}/ignore", c.h.Ignore)
	c.r.Post("/detections/{id}/restore", c.h.Restore)
	return c
}

func (c *cenarioVeic) postar(t *testing.T, alvo string, corpo any, semClaims bool) *httptest.ResponseRecorder {
	t.Helper()
	var rd *strings.Reader
	switch v := corpo.(type) {
	case nil:
		rd = strings.NewReader("")
	case string:
		rd = strings.NewReader(v)
	default:
		b, err := json.Marshal(v)
		require.NoError(t, err)
		rd = strings.NewReader(string(b))
	}
	req := httptest.NewRequest(http.MethodPost, alvo, rd)
	req.Header.Set("Content-Type", "application/json")
	if semClaims {
		req.Header.Set("X-Sem-Claims", "1")
	}
	rec := httptest.NewRecorder()
	c.r.ServeHTTP(rec, req)
	return rec
}

func (c *cenarioVeic) corpoManual(quando time.Time) map[string]any {
	return map[string]any{
		"campaign_id":   c.campanha.String(),
		"station_id":    c.emissora.String(),
		"commercial_id": c.material.String(),
		"detected_at":   quando.Format(time.RFC3339),
		"note":          "  censura da emissora ",
	}
}

func (c *cenarioVeic) criar(t *testing.T, quando time.Time) catalog.Detection {
	t.Helper()
	rec := c.postar(t, "/detections/manual", c.corpoManual(quando), false)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var det catalog.Detection
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &det))
	return det
}

func (c *cenarioVeic) categoria(t *testing.T, id uuid.UUID) string {
	t.Helper()
	var cat string
	require.NoError(t, c.pool.QueryRow(c.ctx,
		`SELECT category FROM detection_campaigns WHERE detection_id = $1`, id).Scan(&cat))
	return cat
}

func TestCriarManualHTTP_201GravaManualByNotaECategoria(t *testing.T) {
	c := novoCenarioVeic(t)

	det := c.criar(t, mcptest.Quando("2026-06-10", "10:30"))

	var manualBy *uuid.UUID
	var nota *string
	var cat string
	require.NoError(t, c.pool.QueryRow(c.ctx,
		`SELECT manual_by, manual_note, category FROM detections WHERE id = $1`, det.ID).Scan(&manualBy, &nota, &cat))
	require.NotNil(t, manualBy)
	require.Equal(t, c.admin, *manualBy)
	require.NotNil(t, nota)
	require.Equal(t, "censura da emissora", *nota)
	require.Equal(t, "in_slot", cat)
	require.Equal(t, "in_slot", c.categoria(t, det.ID))
}

func TestCriarManualHTTP_Recusas(t *testing.T) {
	c := novoCenarioVeic(t)
	bom := func() map[string]any { return c.corpoManual(mcptest.Quando("2026-06-10", "10:30")) }
	com := func(m map[string]any, k string, v any) map[string]any { m[k] = v; return m }

	casos := []struct {
		nome   string
		corpo  any
		semCl  bool
		status int
		texto  string
	}{
		{"sem claims", bom(), true, 401, "unauthorized\n"},
		{"json invalido", "{", false, 400, "invalid request\n"},
		{"ids ausentes", map[string]any{"detected_at": time.Now().Add(-time.Hour).Format(time.RFC3339)}, false, 400, "campaign_id, station_id and commercial_id are required\n"},
		{"sem detected_at", com(bom(), "detected_at", nil), false, 400, "detected_at is required\n"},
		{"futuro", com(bom(), "detected_at", time.Now().Add(10*time.Minute).Format(time.RFC3339)), false, 400, "detected_at cannot be in the future\n"},
		{"material nao vinculado", com(bom(), "commercial_id", uuid.NewString()), false, 422, "material is not linked to this station in this campaign\n"},
	}
	for _, tc := range casos {
		t.Run(tc.nome, func(t *testing.T) {
			rec := c.postar(t, "/detections/manual", tc.corpo, tc.semCl)
			require.Equal(t, tc.status, rec.Code, rec.Body.String())
			require.Equal(t, tc.texto, rec.Body.String())
		})
	}
	var n int
	require.NoError(t, c.pool.QueryRow(c.ctx,
		`SELECT count(*) FROM detections WHERE campaign_id = $1 AND station_id = $2`, c.campanha, c.emissora).Scan(&n))
	require.Zero(t, n, "nenhuma recusa grava linha na campanha/emissora do cenário")
}

func TestCriarManualHTTP_FuturoDentroDaToleranciaPassa(t *testing.T) {
	c := novoCenarioVeic(t)
	// 2 min à frente está dentro dos 5 min de tolerância: não é "futuro".
	rec := c.postar(t, "/detections/manual", c.corpoManual(time.Now().Add(2*time.Minute)), false)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
}

func TestCriarManualHTTP_MultipartComCamposInvalidos(t *testing.T) {
	c := novoCenarioVeic(t)
	const fronteira = "xyz"
	multipart := func(campos map[string]string) *httptest.ResponseRecorder {
		var b strings.Builder
		for k, v := range campos {
			b.WriteString("--" + fronteira + "\r\nContent-Disposition: form-data; name=\"" + k + "\"\r\n\r\n" + v + "\r\n")
		}
		b.WriteString("--" + fronteira + "--\r\n")
		req := httptest.NewRequest(http.MethodPost, "/detections/manual", strings.NewReader(b.String()))
		req.Header.Set("Content-Type", "multipart/form-data; boundary="+fronteira)
		rec := httptest.NewRecorder()
		c.r.ServeHTTP(rec, req)
		return rec
	}
	ok := map[string]string{
		"campaign_id": c.campanha.String(), "station_id": c.emissora.String(),
		"commercial_id": c.material.String(), "detected_at": mcptest.Quando("2026-06-10", "10:30").Format(time.RFC3339),
	}
	copia := func(k, v string) map[string]string {
		m := map[string]string{}
		for a, b := range ok {
			m[a] = b
		}
		m[k] = v
		return m
	}
	rec := multipart(copia("campaign_id", "x"))
	require.Equal(t, 400, rec.Code)
	require.Equal(t, "invalid campaign_id\n", rec.Body.String())
	rec = multipart(copia("station_id", "x"))
	require.Equal(t, "invalid station_id\n", rec.Body.String())
	rec = multipart(copia("commercial_id", "x"))
	require.Equal(t, "invalid commercial_id\n", rec.Body.String())
	rec = multipart(copia("detected_at", "ontem"))
	require.Equal(t, "invalid detected_at (use RFC3339)\n", rec.Body.String())
	rec = multipart(ok) // sem áudio: cria
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
}

func TestIgnorarRestaurarHTTP_GravaEApagaAQuemIgnorou(t *testing.T) {
	c := novoCenarioVeic(t)
	det := c.criar(t, mcptest.Quando("2026-06-10", "10:30"))

	rec := c.postar(t, "/detections/"+det.ID.String()+"/ignore", nil, false)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var out catalog.Detection
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.NotNil(t, out.IgnoredAt)
	require.NotNil(t, out.IgnoredBy)
	require.Equal(t, c.admin, *out.IgnoredBy)

	rec = c.postar(t, "/detections/"+det.ID.String()+"/restore", nil, false)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	out = catalog.Detection{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Nil(t, out.IgnoredAt)
	require.Nil(t, out.IgnoredBy)
}

func TestIgnorarRestaurarHTTP_Recusas(t *testing.T) {
	c := novoCenarioVeic(t)
	inexistente := uuid.NewString()

	rec := c.postar(t, "/detections/"+inexistente+"/ignore", nil, false)
	require.Equal(t, 404, rec.Code)
	require.Equal(t, "not found\n", rec.Body.String())
	rec = c.postar(t, "/detections/"+inexistente+"/restore", nil, false)
	require.Equal(t, 404, rec.Code)
	require.Equal(t, "not found\n", rec.Body.String())
	rec = c.postar(t, "/detections/xyz/ignore", nil, false)
	require.Equal(t, 400, rec.Code)
	require.Equal(t, "invalid id\n", rec.Body.String())
	rec = c.postar(t, "/detections/xyz/restore", nil, false)
	require.Equal(t, "invalid id\n", rec.Body.String())
	rec = c.postar(t, "/detections/"+inexistente+"/ignore", nil, true)
	require.Equal(t, 401, rec.Code)
	require.Equal(t, "unauthorized\n", rec.Body.String())
}

// Cota 1/dia: a primeira é in_slot, a segunda bonus. Ignorar a primeira promove
// a segunda; restaurar devolve a vaga à primeira (cronológica).
func TestIgnorarRestaurarHTTP_ReabreECedeAVagaDaCota(t *testing.T) {
	c := novoCenarioVeic(t)
	primeira := c.criar(t, mcptest.Quando("2026-06-10", "10:30"))
	segunda := c.criar(t, mcptest.Quando("2026-06-10", "11:00"))
	require.Equal(t, "in_slot", c.categoria(t, primeira.ID))
	require.Equal(t, "bonus", c.categoria(t, segunda.ID))

	require.Equal(t, 200, c.postar(t, "/detections/"+primeira.ID.String()+"/ignore", nil, false).Code)
	require.Equal(t, "in_slot", c.categoria(t, segunda.ID), "ignorar a primeira promove a segunda")

	require.Equal(t, 200, c.postar(t, "/detections/"+primeira.ID.String()+"/restore", nil, false).Code)
	require.Equal(t, "in_slot", c.categoria(t, primeira.ID))
	require.Equal(t, "bonus", c.categoria(t, segunda.ID), "restaurar devolve a vaga à primeira")
}
