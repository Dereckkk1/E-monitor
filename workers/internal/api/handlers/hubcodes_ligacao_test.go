package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/catalog"
	"radiocheck/internal/hub"
)

const hubDonoTeste = "6ad27357e7da87aa5b274b11"

// Responde a conferência (by-code) e o ligar. Conta as chamadas ao ligar.
func hubDaLigacao(t *testing.T, idNaPlataforma string, statusLigar int, corpoLigar string) (*hub.Client, *int32) {
	t.Helper()
	var ligou int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/api/platform/campaigns/by-code/"):
			_, _ = w.Write([]byte(`{"hubCampaignId":"66f0","nome":"Verão 2026","inicio":"2026-12-01","fim":"2027-02-28",` +
				`"cliente":{"id":"` + hubDonoTeste + `","nome":"Lojas Renner","idNaPlataforma":"` + idNaPlataforma + `"}}`))
		case strings.HasSuffix(r.URL.Path, "/api/platform/clientes/ligar"):
			atomic.AddInt32(&ligou, 1)
			w.WriteHeader(statusLigar)
			_, _ = w.Write([]byte(corpoLigar))
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"acao":"atualiza"}`))
		}
	}))
	t.Cleanup(srv.Close)
	return hub.New(srv.URL, "chave"), &ligou
}

func clienteComHubID(t *testing.T, ctx context.Context, pool *pgxpool.Pool, nome string, hubID *string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO clients (name, hub_id) VALUES ($1, $2) RETURNING id`, nome, hubID).Scan(&id))
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM audit_log WHERE resource_id = $1`, id)
		_, _ = pool.Exec(ctx, `DELETE FROM campaigns WHERE client_id = $1`, id)
		_, _ = pool.Exec(ctx, `DELETE FROM clients WHERE id = $1`, id)
	})
	return id
}

func getConferencia(t *testing.T, h *HubCodesHandler, cliente uuid.UUID) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/internal/hub-codes/EH-7K4M2X?client_id="+cliente.String(), nil)
	req = comParamDeRota(req, "code", "EH-7K4M2X")
	rec := httptest.NewRecorder()
	h.Get(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	return out
}

func postLigar(t *testing.T, h *HubCodesHandler, cliente uuid.UUID) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"client_id": cliente.String()})
	req := httptest.NewRequest(http.MethodPost, "/v1/internal/hub-codes/EH-7K4M2X/ligar", bytes.NewReader(b))
	req = comParamDeRota(req, "code", "EH-7K4M2X")
	rec := httptest.NewRecorder()
	h.Ligar(rec, req)
	return rec
}

func estadoDe(out map[string]any) string {
	l, _ := out["ligacao"].(map[string]any)
	s, _ := l["estado"].(string)
	return s
}

func TestConferencia_SemClientIDNaoTrazLigacao(t *testing.T) {
	hb, _ := hubDaLigacao(t, "", 200, `{}`)
	rec := getHubCode(t, &HubCodesHandler{Hub: hb}, "EH-7K4M2X")
	require.NotContains(t, rec.Body.String(), "ligacao")
}

func TestConferencia_FaltaLigar(t *testing.T) {
	ctx, pool := poolHubCode(t)
	c := clienteComHubID(t, ctx, pool, "LOJAS RENNER", nil)
	hb, _ := hubDaLigacao(t, "", 200, `{}`)
	out := getConferencia(t, &HubCodesHandler{Hub: hb, Ligacoes: catalog.NewLigacoes(pool)}, c)
	require.Equal(t, catalog.EstadoFaltaLigar, estadoDe(out))
	require.NotContains(t, mustJSON(out), hubDonoTeste, "o id do hub não desce para o navegador")
}

func TestConferencia_Ligado(t *testing.T) {
	ctx, pool := poolHubCode(t)
	h := hubDonoTeste
	c := clienteComHubID(t, ctx, pool, "LOJAS RENNER", &h)
	hb, _ := hubDaLigacao(t, c.String(), 200, `{}`)
	out := getConferencia(t, &HubCodesHandler{Hub: hb, Ligacoes: catalog.NewLigacoes(pool)}, c)
	require.Equal(t, catalog.EstadoLigado, estadoDe(out))
}

func TestLigar_LigaOsDoisLados(t *testing.T) {
	ctx, pool := poolHubCode(t)
	c := clienteComHubID(t, ctx, pool, "LOJAS RENNER", nil)
	hb, ligou := hubDaLigacao(t, "", 200, `{"acao":"ligado"}`)
	h := &HubCodesHandler{Hub: hb, Ligacoes: catalog.NewLigacoes(pool), Campanhas: catalog.NewCampaigns(pool)}

	rec := postLigar(t, h, c)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Equal(t, catalog.EstadoLigado, estadoDe(out))
	require.Equal(t, int32(1), atomic.LoadInt32(ligou))
	var hubID string
	require.NoError(t, pool.QueryRow(ctx, `SELECT hub_id FROM clients WHERE id = $1`, c).Scan(&hubID))
	require.Equal(t, hubDonoTeste, hubID)
}

func TestLigar_HubForaDeixaOLadoLocalGravadoE503(t *testing.T) {
	ctx, pool := poolHubCode(t)
	c := clienteComHubID(t, ctx, pool, "LOJAS RENNER", nil)
	hb, _ := hubDaLigacao(t, "", 500, `{}`)
	h := &HubCodesHandler{Hub: hb, Ligacoes: catalog.NewLigacoes(pool)}

	rec := postLigar(t, h, c)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Equal(t, catalog.EstadoFaltaLigar, estadoDe(out))
	var hubID string
	require.NoError(t, pool.QueryRow(ctx, `SELECT hub_id FROM clients WHERE id = $1`, c).Scan(&hubID))
	require.Equal(t, hubDonoTeste, hubID)
}

func TestLigar_Hub409ViraBloqueado(t *testing.T) {
	ctx, pool := poolHubCode(t)
	c := clienteComHubID(t, ctx, pool, "LOJAS RENNER", nil)
	hb, _ := hubDaLigacao(t, "", 409, `{"error":{"code":"id_em_uso"},"cliente":"LOJAS RENNER"}`)
	rec := postLigar(t, &HubCodesHandler{Hub: hb, Ligacoes: catalog.NewLigacoes(pool)}, c)

	require.Equal(t, http.StatusConflict, rec.Code)
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Equal(t, catalog.EstadoBloqueado, estadoDe(out))
}

func TestLigar_BloqueadoNaoChamaOHubNemGrava(t *testing.T) {
	ctx, pool := poolHubCode(t)
	outro := "hub-" + uuid.NewString()
	c := clienteComHubID(t, ctx, pool, "LOJAS RENNER", &outro)
	hb, ligou := hubDaLigacao(t, "", 200, `{}`)

	rec := postLigar(t, &HubCodesHandler{Hub: hb, Ligacoes: catalog.NewLigacoes(pool)}, c)

	require.Equal(t, http.StatusConflict, rec.Code)
	require.Equal(t, int32(0), atomic.LoadInt32(ligou))
}

func TestLigar_JaLigadoNaoChamaOHub(t *testing.T) {
	ctx, pool := poolHubCode(t)
	h := hubDonoTeste
	c := clienteComHubID(t, ctx, pool, "LOJAS RENNER", &h)
	hb, ligou := hubDaLigacao(t, c.String(), 200, `{}`)

	rec := postLigar(t, &HubCodesHandler{Hub: hb, Ligacoes: catalog.NewLigacoes(pool)}, c)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, int32(0), atomic.LoadInt32(ligou))
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }
