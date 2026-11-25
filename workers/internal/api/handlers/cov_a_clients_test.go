package handlers

// cov_a_clients_test.go — clientes, chaves de API, PMM no target e o card de
// assertividade. Banco real (exceto assertividade, que usa um repo falso).

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/assertiveness"
	"radiocheck/internal/auth"
	"radiocheck/internal/catalog"
	"radiocheck/internal/mcp/mcptest"
)

// ── Clientes ─────────────────────────────────────────────────────────────────

func covAClientesRouter(h *ClientsHandler, claims *auth.Claims) chi.Router {
	return covARouter(claims, func(r chi.Router) {
		r.Get("/clients", h.List)
		r.Post("/clients", h.Create)
		r.Put("/clients/{id}", h.Update)
		r.Delete("/clients/{id}", h.Delete)
		r.Post("/clients/{id}/deactivate", h.Deactivate)
		r.Post("/clients/{id}/activate", h.Activate)
	})
}

func TestCovA_Clients_List(t *testing.T) {
	ctx, pool := covADB(t)
	h := &ClientsHandler{Repo: catalog.NewClients(pool)}
	nomes := []string{"Ana Teste", "Bruno Teste", "Carla Exemplo"}
	ids := make([]uuid.UUID, len(nomes))
	for i, n := range nomes {
		ids[i] = mcptest.Cliente(t, ctx, pool, n)
	}
	r := covAClientesRouter(h, covAClaims("admin", uuid.New()))
	nomesDe := func(rec *httptest.ResponseRecorder) []string {
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var out []string
		for _, it := range covADecode(t, rec)["data"].([]any) {
			out = append(out, it.(map[string]any)["name"].(string))
		}
		return out
	}

	t.Run("sem paginação devolve o catálogo inteiro ordenado por nome", func(t *testing.T) {
		got := nomesDe(covAServe(r, httptest.NewRequest(http.MethodGet, "/clients", nil)))
		assert.Equal(t, nomes, got)
	})

	t.Run("paginado: página, tamanho e total", func(t *testing.T) {
		rec := covAServe(r, httptest.NewRequest(http.MethodGet, "/clients?page=2&page_size=2", nil))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		body := covADecode(t, rec)
		assert.EqualValues(t, 3, body["total"])
		assert.EqualValues(t, 2, body["total_pages"])
		assert.EqualValues(t, 2, body["page"])
		assert.EqualValues(t, 2, body["page_size"])
		assert.Equal(t, []string{"Carla Exemplo"}, nomesDe(rec))
	})

	t.Run("busca por texto", func(t *testing.T) {
		rec := covAServe(r, httptest.NewRequest(http.MethodGet, "/clients?q=exemplo", nil))
		assert.Equal(t, []string{"Carla Exemplo"}, nomesDe(rec))
		assert.EqualValues(t, 1, covADecode(t, rec)["page"], "page ausente vira 1")
		assert.EqualValues(t, 20, covADecode(t, rec)["page_size"], "page_size ausente vira 20")
	})

	t.Run("page_size é limitado a 200 e page inválido vira 1", func(t *testing.T) {
		rec := covAServe(r, httptest.NewRequest(http.MethodGet, "/clients?page=-3&page_size=9999", nil))
		body := covADecode(t, rec)
		assert.EqualValues(t, 200, body["page_size"])
		assert.EqualValues(t, 1, body["page"])
	})

	t.Run("inativos só aparecem com include_inactive", func(t *testing.T) {
		_, err := pool.Exec(ctx, `UPDATE clients SET is_active = FALSE WHERE id = $1`, ids[0])
		require.NoError(t, err)
		assert.Len(t, nomesDe(covAServe(r, httptest.NewRequest(http.MethodGet, "/clients?page=1", nil))), 2)
		assert.Len(t, nomesDe(covAServe(r, httptest.NewRequest(http.MethodGet, "/clients?page=1&include_inactive=1", nil))), 3)
		assert.Len(t, nomesDe(covAServe(r, httptest.NewRequest(http.MethodGet, "/clients?page=1&include_inactive=true", nil))), 3)
	})

	t.Run("viewer enxerga só a própria carteira (mesmo envelope)", func(t *testing.T) {
		vr := covAClientesRouter(h, covAClaims("viewer", uuid.New(), ids[1], ids[2]))
		got := nomesDe(covAServe(vr, httptest.NewRequest(http.MethodGet, "/clients?page=1&page_size=1", nil)))
		assert.Equal(t, []string{"Bruno Teste", "Carla Exemplo"}, got, "paginação não se aplica ao viewer")
	})

	t.Run("falha do banco 500 nos três caminhos", func(t *testing.T) {
		bad := &ClientsHandler{Repo: catalog.NewClients(covAClosedPool(t))}
		for _, claims := range []*auth.Claims{covAClaims("admin", uuid.New()), covAClaims("viewer", uuid.New(), ids[1])} {
			for _, alvo := range []string{"/clients", "/clients?page=1"} {
				rec := covAServe(covAClientesRouter(bad, claims), httptest.NewRequest(http.MethodGet, alvo, nil))
				assert.Equal(t, http.StatusInternalServerError, rec.Code, "%s %s", claims.Role, alvo)
			}
		}
	})
}

func TestCovA_Clients_EscritaRecusasDeEntrada(t *testing.T) {
	ctx, pool := covADB(t)
	h := &ClientsHandler{Repo: catalog.NewClients(pool)}
	r := covAClientesRouter(h, covAClaims("admin", uuid.New()))
	cli := mcptest.Cliente(t, ctx, pool, "Cliente Teste")

	casos := []struct {
		nome, metodo, alvo string
		corpo              any
		status             int
	}{
		{"create json inválido", http.MethodPost, "/clients", "{x", 400},
		{"update id inválido", http.MethodPut, "/clients/nao-uuid", map[string]any{"name": "x"}, 400},
		{"update json inválido", http.MethodPut, "/clients/" + cli.String(), "{x", 400},
		{"delete id inválido", http.MethodDelete, "/clients/nao-uuid", nil, 400},
		{"deactivate id inválido", http.MethodPost, "/clients/nao-uuid/deactivate", nil, 400},
		{"activate id inválido", http.MethodPost, "/clients/nao-uuid/activate", nil, 400},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			rec := covAServe(r, covAJSON(t, c.metodo, c.alvo, c.corpo))
			assert.Equal(t, c.status, rec.Code, rec.Body.String())
		})
	}
}

func TestCovA_Clients_DesativarEReativar(t *testing.T) {
	ctx, pool := covADB(t)
	h := &ClientsHandler{Repo: catalog.NewClients(pool)}
	r := covAClientesRouter(h, covAClaims("admin", uuid.New()))
	cli := mcptest.Cliente(t, ctx, pool, "Cliente Teste")
	ativo := func() bool {
		var a bool
		require.NoError(t, pool.QueryRow(ctx, `SELECT is_active FROM clients WHERE id=$1`, cli).Scan(&a))
		return a
	}

	rec := covAServe(r, covAJSON(t, http.MethodPost, "/clients/"+cli.String()+"/deactivate", nil))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, false, covADecode(t, rec)["is_active"])
	assert.False(t, ativo())

	rec = covAServe(r, covAJSON(t, http.MethodPost, "/clients/"+cli.String()+"/activate", nil))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, true, covADecode(t, rec)["is_active"])
	assert.True(t, ativo())

	rec = covAServe(r, covAJSON(t, http.MethodPost, "/clients/"+uuid.NewString()+"/deactivate", nil))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestCovA_Clients_ApagarComVinculosDa409ComContagens(t *testing.T) {
	ctx, pool := covADB(t)
	h := &ClientsHandler{Repo: catalog.NewClients(pool)}
	r := covAClientesRouter(h, covAClaims("admin", uuid.New()))
	cli := mcptest.Cliente(t, ctx, pool, "Cliente Teste")
	em := mcptest.Emissora(t, ctx, pool, "Rádio Teste FM", 0)
	mcptest.Campanha(t, ctx, pool, cli, "Campanha Teste", "2026-06-01", "2026-06-30", em)

	rec := covAServe(r, covAJSON(t, http.MethodDelete, "/clients/"+cli.String(), nil))
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	body := covADecode(t, rec)
	assert.Equal(t, "client_has_dependents", body["error"])
	assert.EqualValues(t, 1, body["campaigns"])
	assert.EqualValues(t, 0, body["materials"])
	assert.EqualValues(t, 0, body["users"])

	// Sem vínculos, apaga (204).
	livre := mcptest.Cliente(t, ctx, pool, "Cliente Livre")
	rec = covAServe(r, covAJSON(t, http.MethodDelete, "/clients/"+livre.String(), nil))
	assert.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	var n int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM clients WHERE id=$1`, livre).Scan(&n))
	assert.Zero(t, n)
}

func TestCovA_Clients_FalhaDoBancoDa500(t *testing.T) {
	bad := &ClientsHandler{Repo: catalog.NewClients(covAClosedPool(t))}
	r := covAClientesRouter(bad, covAClaims("admin", uuid.New()))
	id := uuid.NewString()
	casos := []struct {
		metodo, alvo string
		corpo        any
	}{
		{http.MethodPost, "/clients", map[string]any{"name": "Cliente Teste"}},
		{http.MethodPut, "/clients/" + id, map[string]any{"name": "Cliente Teste"}},
		{http.MethodDelete, "/clients/" + id, nil},
		{http.MethodPost, "/clients/" + id + "/deactivate", nil},
	}
	for _, c := range casos {
		rec := covAServe(r, covAJSON(t, c.metodo, c.alvo, c.corpo))
		assert.Equal(t, http.StatusInternalServerError, rec.Code, "%s %s: %s", c.metodo, c.alvo, rec.Body.String())
	}
}

// ── Chaves de API ────────────────────────────────────────────────────────────

func TestCovA_APIKeys_CicloCompleto(t *testing.T) {
	ctx, pool := covADB(t)
	h := NewAPIKeysHandler(pool)
	r := covARouter(nil, func(r chi.Router) {
		r.Get("/clients/{clientID}/api-keys", h.List)
		r.Post("/clients/{clientID}/api-keys", h.Create)
		r.Delete("/clients/{clientID}/api-keys/{keyID}", h.Revoke)
	})
	cli := mcptest.Cliente(t, ctx, pool, "Cliente Teste")
	base := "/clients/" + cli.String() + "/api-keys"

	// Lista vazia é [] e não null.
	rec := covAServe(r, httptest.NewRequest(http.MethodGet, base, nil))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "[]", covATrim(rec.Body.String()))

	// Cria: a chave crua aparece UMA vez; no banco fica só o hash dela.
	rec = covAServe(r, httptest.NewRequest(http.MethodPost, base, nil))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	criada := covADecode(t, rec)
	raw := criada["key"].(string)
	id := criada["id"].(string)
	require.Len(t, raw, 64)
	sum := sha256.Sum256([]byte(raw))
	var hash string
	require.NoError(t, pool.QueryRow(ctx, `SELECT key_hash FROM api_keys WHERE id=$1`, id).Scan(&hash))
	assert.Equal(t, hex.EncodeToString(sum[:]), hash, "o banco guarda sha256 da chave, nunca a chave")
	assert.NotContains(t, hash, raw)

	// Lista mascarada (8 primeiros do hash + reticências).
	rec = covAServe(r, httptest.NewRequest(http.MethodGet, base, nil))
	require.Equal(t, http.StatusOK, rec.Code)
	var lista []map[string]any
	covADecodeInto(t, rec, &lista)
	require.Len(t, lista, 1)
	assert.Equal(t, id, lista[0]["id"])
	assert.Equal(t, hash[:8]+"...", lista[0]["key_masked"])
	assert.NotContains(t, rec.Body.String(), raw, "a listagem nunca devolve a chave crua")

	// Revoga: 204, grava revoked_at e some da lista.
	rec = covAServe(r, httptest.NewRequest(http.MethodDelete, base+"/"+id, nil))
	require.Equal(t, http.StatusNoContent, rec.Code)
	var revogada *time.Time
	require.NoError(t, pool.QueryRow(ctx, `SELECT revoked_at FROM api_keys WHERE id=$1`, id).Scan(&revogada))
	require.NotNil(t, revogada)
	rec = covAServe(r, httptest.NewRequest(http.MethodGet, base, nil))
	assert.Equal(t, "[]", covATrim(rec.Body.String()))

	// Revogar de novo / chave desconhecida é inofensivo (204).
	assert.Equal(t, http.StatusNoContent, covAServe(r, httptest.NewRequest(http.MethodDelete, base+"/"+uuid.NewString(), nil)).Code)
}

func TestCovA_APIKeys_Recusas(t *testing.T) {
	ctx, pool := covADB(t)
	h := NewAPIKeysHandler(pool)
	r := covARouter(nil, func(r chi.Router) {
		r.Get("/clients/{clientID}/api-keys", h.List)
		r.Post("/clients/{clientID}/api-keys", h.Create)
		r.Delete("/clients/{clientID}/api-keys/{keyID}", h.Revoke)
	})
	_ = ctx

	t.Run("listar com id malformado sempre foi 500", func(t *testing.T) {
		assert.Equal(t, http.StatusInternalServerError, covAServe(r, httptest.NewRequest(http.MethodGet, "/clients/xx/api-keys", nil)).Code)
	})
	t.Run("criar para cliente inexistente 500 e nada é gravado", func(t *testing.T) {
		rec := covAServe(r, httptest.NewRequest(http.MethodPost, "/clients/"+uuid.NewString()+"/api-keys", nil))
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		var n int
		require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM api_keys`).Scan(&n))
		assert.Zero(t, n)
	})
	t.Run("revogar com id malformado 500", func(t *testing.T) {
		assert.Equal(t, http.StatusInternalServerError, covAServe(r, httptest.NewRequest(http.MethodDelete, "/clients/"+uuid.NewString()+"/api-keys/xx", nil)).Code)
	})
	t.Run("falha do banco 500 na lista", func(t *testing.T) {
		bad := NewAPIKeysHandler(covAClosedPool(t))
		rt := covARouter(nil, func(r chi.Router) { r.Get("/clients/{clientID}/api-keys", bad.List) })
		assert.Equal(t, http.StatusInternalServerError, covAServe(rt, httptest.NewRequest(http.MethodGet, "/clients/"+uuid.NewString()+"/api-keys", nil)).Code)
	})
}

// ── PMM no target ────────────────────────────────────────────────────────────

func TestCovA_TargetPMM(t *testing.T) {
	ctx, pool := covADB(t)
	h := &ClientTargetPmmHandler{Repo: catalog.NewClientStationPMM(pool)}
	cli := mcptest.Cliente(t, ctx, pool, "Cliente Teste")
	outro := mcptest.Cliente(t, ctx, pool, "Outro Cliente")
	e1 := mcptest.Emissora(t, ctx, pool, "Rádio Um FM", 1000)
	e2 := mcptest.Emissora(t, ctx, pool, "Rádio Dois FM", 2000)
	mcptest.Campanha(t, ctx, pool, cli, "Campanha Teste", "2026-06-01", "2026-06-30", e1, e2)

	rotas := func(claims *auth.Claims) chi.Router {
		return covARouter(claims, func(r chi.Router) {
			r.Get("/clients/{clientID}/target-pmm", h.List)
			r.Put("/clients/{clientID}/target-pmm", h.Bulk)
		})
	}
	admin := rotas(covAClaims("admin", uuid.New()))
	alvo := "/clients/" + cli.String() + "/target-pmm"
	itens := func(rec *httptest.ResponseRecorder) map[string]any {
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		out := map[string]any{}
		for _, it := range covADecode(t, rec)["data"].([]any) {
			m := it.(map[string]any)
			out[m["name"].(string)] = m["pmm_target"]
		}
		return out
	}

	t.Run("lista as emissoras-alvo sem cadastro (target null)", func(t *testing.T) {
		got := itens(covAServe(admin, httptest.NewRequest(http.MethodGet, alvo, nil)))
		assert.Equal(t, map[string]any{"Rádio Um FM": nil, "Rádio Dois FM": nil}, got)
	})

	t.Run("recusas de entrada no PUT", func(t *testing.T) {
		assert.Equal(t, http.StatusBadRequest, covAServe(admin, covAJSON(t, http.MethodPut, "/clients/xx/target-pmm", map[string]any{})).Code)
		assert.Equal(t, http.StatusBadRequest, covAServe(admin, covAJSON(t, http.MethodPut, alvo, "{x")).Code)

		neg := -5
		rec := covAServe(admin, covAJSON(t, http.MethodPut, alvo, map[string]any{"entries": []catalog.TargetPMMEntry{{StationID: e1, PMMTarget: &neg}}}))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "pmm_target must be >= 0")

		muitos := make([]catalog.TargetPMMEntry, 5001)
		rec = covAServe(admin, covAJSON(t, http.MethodPut, alvo, map[string]any{"entries": muitos}))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "too many entries")
	})

	t.Run("grava, zero é diferente de ausente, null apaga", func(t *testing.T) {
		v1, zero := 12000, 0
		rec := covAServe(admin, covAJSON(t, http.MethodPut, alvo, map[string]any{"entries": []catalog.TargetPMMEntry{
			{StationID: e1, PMMTarget: &v1}, {StationID: e2, PMMTarget: &zero},
		}}))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		body := covADecode(t, rec)
		assert.EqualValues(t, 2, body["updated"])
		assert.EqualValues(t, 0, body["deleted"])
		got := itens(covAServe(admin, httptest.NewRequest(http.MethodGet, alvo, nil)))
		assert.EqualValues(t, 12000, got["Rádio Um FM"])
		assert.EqualValues(t, 0, got["Rádio Dois FM"], "zero cadastrado não vira ausente")

		rec = covAServe(admin, covAJSON(t, http.MethodPut, alvo, map[string]any{"entries": []map[string]any{{"station_id": e1.String(), "pmm_target": nil}}}))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.EqualValues(t, 1, covADecode(t, rec)["deleted"])
		got = itens(covAServe(admin, httptest.NewRequest(http.MethodGet, alvo, nil)))
		assert.Nil(t, got["Rádio Um FM"])
	})

	t.Run("GET: id inválido 400 e escopo anti-oracle", func(t *testing.T) {
		assert.Equal(t, http.StatusBadRequest, covAServe(admin, httptest.NewRequest(http.MethodGet, "/clients/xx/target-pmm", nil)).Code)

		dono := rotas(covAClaims("viewer", uuid.New(), cli))
		assert.Equal(t, http.StatusOK, covAServe(dono, httptest.NewRequest(http.MethodGet, alvo, nil)).Code)

		alheio := rotas(covAClaims("viewer", uuid.New(), outro))
		rec := covAServe(alheio, httptest.NewRequest(http.MethodGet, alvo, nil))
		assert.Equal(t, http.StatusNotFound, rec.Code, "viewer de outro cliente leva 404, não 403")

		assert.Equal(t, http.StatusNotFound, covAServe(admin, httptest.NewRequest(http.MethodGet, "/clients/"+uuid.Nil.String()+"/target-pmm", nil)).Code,
			"uuid zerado nunca é autorizado")
	})

	t.Run("falha do banco 500", func(t *testing.T) {
		bad := &ClientTargetPmmHandler{Repo: catalog.NewClientStationPMM(covAClosedPool(t))}
		rt := covARouter(nil, func(r chi.Router) {
			r.Get("/clients/{clientID}/target-pmm", bad.List)
			r.Put("/clients/{clientID}/target-pmm", bad.Bulk)
		})
		assert.Equal(t, http.StatusInternalServerError, covAServe(rt, httptest.NewRequest(http.MethodGet, alvo, nil)).Code)
		assert.Equal(t, http.StatusInternalServerError, covAServe(rt, covAJSON(t, http.MethodPut, alvo, map[string]any{"entries": []any{}})).Code)
	})
}

// ── Assertividade ────────────────────────────────────────────────────────────

type covAAssertRepo struct {
	res    assertiveness.Result
	err    error
	gotNow time.Time
	gotF   assertiveness.Filter
}

func (f *covAAssertRepo) Get(_ context.Context, now time.Time, fl assertiveness.Filter) (assertiveness.Result, error) {
	f.gotNow, f.gotF = now, fl
	return f.res, f.err
}

func TestCovA_Assertiveness_Get(t *testing.T) {
	agora := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	repo := &covAAssertRepo{}
	h := NewAssertivenessHandler(repo)
	require.NotNil(t, h.NowFn, "o construtor injeta o relógio")
	h.NowFn = func() time.Time { return agora }
	get := func(qs string) *httptest.ResponseRecorder {
		return covAServe(http.HandlerFunc(h.Get), httptest.NewRequest(http.MethodGet, "/assertiveness?"+qs, nil))
	}

	t.Run("recusas", func(t *testing.T) {
		rec := get("client_id=xx")
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "invalid client_id")
		rec = get("campaigns=a,b")
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "invalid campaigns")

		ids := make([]string, 201)
		for i := range ids {
			ids[i] = uuid.NewString()
		}
		rec = get("campaigns=" + covAJoin(ids))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "campaigns max=200")
	})

	t.Run("filtros chegam ao repo junto com o relógio injetado", func(t *testing.T) {
		cli, c1, c2 := uuid.New(), uuid.New(), uuid.New()
		repo.res = assertiveness.Result{}
		rec := get("client_id=" + cli.String() + "&campaigns=" + c1.String() + "," + c2.String())
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Equal(t, agora, repo.gotNow)
		require.NotNil(t, repo.gotF.ClientID)
		assert.Equal(t, cli, *repo.gotF.ClientID)
		assert.Equal(t, []uuid.UUID{c1, c2}, repo.gotF.CampaignIDs)
	})

	t.Run("sem filtros: o repo recebe filtro vazio", func(t *testing.T) {
		rec := get("")
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Nil(t, repo.gotF.ClientID)
		assert.Empty(t, repo.gotF.CampaignIDs)
	})

	t.Run("sem NowFn usa o relógio real", func(t *testing.T) {
		h2 := &AssertivenessHandler{Repo: repo}
		antes := time.Now()
		rec := covAServe(http.HandlerFunc(h2.Get), httptest.NewRequest(http.MethodGet, "/assertiveness", nil))
		require.Equal(t, http.StatusOK, rec.Code)
		assert.False(t, repo.gotNow.Before(antes))
	})

	t.Run("erro do repo 500", func(t *testing.T) {
		repo.err = errors.New("db")
		defer func() { repo.err = nil }()
		rec := get("")
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		assert.Contains(t, rec.Body.String(), "failed to load assertiveness")
	})
}
