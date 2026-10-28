package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/api/handlers"
	"radiocheck/internal/auth"
	"radiocheck/internal/catalog"
	"radiocheck/internal/mcp/mcptest"
)

// TestReportsSummary_TotaisTravados fixa os totais do GET
// /reports/campaigns/{id}/summary — trava do refactor que levou a conta para
// catalog.TotaisDoResumo (o MCP chama a mesma função).
func TestReportsSummary_TotaisTravados(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	// PMM 1000,2 separa as contas possíveis: por emissora round(1000,2 × 3) =
	// 3001; por material × emissora seria round(2000,4) + round(1000,2) = 3000;
	// o total da emissora repetido por material daria 6002.
	e1 := mcptest.Emissora(t, ctx, pool, "Rádio Um", 1000.2)
	e2 := mcptest.Emissora(t, ctx, pool, "Rádio Dois", 0) // sem PMM e sem target
	e3 := mcptest.Emissora(t, ctx, pool, "Rádio Três", 0) // com target, sem PMM
	tipo := mcptest.TipoMaterial(t, ctx, pool, "Spot")
	mat := mcptest.Material(t, ctx, pool, cli, tipo, "Spot A")
	mat2 := mcptest.Material(t, ctx, pool, cli, tipo, "Spot B")
	camp := mcptest.Campanha(t, ctx, pool, cli, "Camp", "2026-10-01", "2026-10-31", e1, e2, e3)
	mcptest.PMMTarget(t, ctx, pool, cli, e1, 300)
	mcptest.PMMTarget(t, ctx, pool, cli, e3, 500)
	mcptest.Veiculacao(t, ctx, pool, camp, mat, e1, "in_slot", mcptest.Quando("2026-10-10", "08:00"))
	mcptest.Veiculacao(t, ctx, pool, camp, mat, e1, "bonus", mcptest.Quando("2026-10-10", "09:00"))
	mcptest.Veiculacao(t, ctx, pool, camp, mat, e1, "out_slot", mcptest.Quando("2026-10-10", "23:00"))
	mcptest.Veiculacao(t, ctx, pool, camp, mat2, e1, "in_slot", mcptest.Quando("2026-10-10", "10:00"))
	mcptest.Veiculacao(t, ctx, pool, camp, mat, e2, "in_slot", mcptest.Quando("2026-10-11", "08:00"))
	mcptest.Veiculacao(t, ctx, pool, camp, mat, e3, "in_slot", mcptest.Quando("2026-10-11", "09:00"))

	h := &handlers.ReportsHandler{Detections: catalog.NewDetections(pool), CampaignRepo: catalog.NewCampaigns(pool), Pool: pool}
	var r handlers.SummaryResponse
	require.NoError(t, json.Unmarshal(chamarHandlerAdmin(t, h.Summary, "/x", map[string]string{"id": camp.String()}, admin), &r))
	require.Equal(t, 6, r.Totals.Detections)
	require.Equal(t, 2, r.Totals.DistinctMaterials)
	require.Equal(t, 3, r.Totals.DistinctStations)
	require.EqualValues(t, 3001, r.Totals.Impactos,
		"round(1000,2 × 3 [in_slot+bonus de e1, 2 materiais]) — por emissora; out_slot e emissora sem PMM não contam")
	require.EqualValues(t, 1400, r.Totals.ImpactosTarget, "300 × 3 (e1) + 500 × 1 (e3, target sem PMM conta)")
	require.Equal(t, 2, r.Totals.StationsWithTarget)
}

// chamarHandlerAdmin chama um handler HTTP com claims de admin e parâmetros de rota.
func chamarHandlerAdmin(t *testing.T, h http.HandlerFunc, alvo string, rota map[string]string, admin uuid.UUID) []byte {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, alvo, nil)
	rctx := chi.NewRouteContext()
	for k, v := range rota {
		rctx.URLParams.Add(k, v)
	}
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	ctx = auth.WithClaims(ctx, &auth.Claims{UserID: admin, Role: "admin"})
	rec := httptest.NewRecorder()
	h(rec, req.WithContext(ctx))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	return rec.Body.Bytes()
}
