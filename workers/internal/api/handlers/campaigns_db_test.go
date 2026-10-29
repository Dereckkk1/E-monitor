package handlers

// campaigns_db_test.go — TRAVA o que as rotas de campanha fazem hoje (status,
// corpo, linha no banco, chamadas ao supervisor), ANTES de a escrita sair do
// handler para internal/acoes (plano da Fatia 3, Task 8). Complementa o que
// `campaigns_hubcode_test.go`, `campaigns_update_hubcode_test.go` e
// `campaigns_ligacao_test.go` já travam do código do hub. Passa no código antigo
// e no novo.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/catalog"
)

func roteadorCampanha(h *CampaignsHandler) chi.Router {
	r := chi.NewRouter()
	r.Put("/campaigns/{id}", h.Update)
	r.Put("/campaigns/{id}/stations", h.UpdateStations)
	r.Post("/campaigns", h.Create)
	return r
}

func putJSON(t *testing.T, r http.Handler, alvo string, corpo any) *httptest.ResponseRecorder {
	t.Helper()
	var b []byte
	if s, ok := corpo.(string); ok {
		b = []byte(s)
	} else {
		var err error
		b, err = json.Marshal(corpo)
		require.NoError(t, err)
	}
	req := httptest.NewRequest(http.MethodPut, alvo, bytes.NewReader(b))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func emissorasDaCampanha(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) []uuid.UUID {
	t.Helper()
	var ids []uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `SELECT target_stations FROM campaigns WHERE id = $1`, id).Scan(&ids))
	return ids
}

func novaEmissora(t *testing.T, ctx context.Context, pool *pgxpool.Pool, nome string) uuid.UUID {
	t.Helper()
	st, err := catalog.NewStations(pool).Create(ctx, catalog.CreateStationInput{
		Name: nome + " " + uuid.NewString()[:6], Band: "FM", StreamURL: "http://teste.local/" + uuid.NewString(),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM stations WHERE id = $1`, st.ID) })
	return st.ID
}

func TestCampanhaHTTP_Create_201DevolveACampanhaComOStatusDoBanco(t *testing.T) {
	ctx, pool := poolHubCode(t)
	cliente := seedClienteLocal(t, ctx, pool)
	est := novaEmissora(t, ctx, pool, "Rádio Create")
	corpo := campanhaNova(cliente, "")
	corpo["target_stations"] = []string{est.String()}

	rec := postCampanha(t, &CampaignsHandler{Repo: catalog.NewCampaigns(pool)}, corpo)

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var out catalog.Campaign
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Equal(t, "Verão 2026", out.Name)
	require.Equal(t, cliente, out.ClientID)
	require.Equal(t, "programada", out.Status, "nasce programada; quem ativa é o scheduler")
	require.Equal(t, []uuid.UUID{est}, out.TargetStations)
	require.Equal(t, []uuid.UUID{est}, emissorasDaCampanha(t, ctx, pool, out.ID))
}

func TestCampanhaHTTP_Update_200SubstituiNomeEDatasEAparaOEspaco(t *testing.T) {
	ctx, pool := poolHubCode(t)
	cliente := seedClienteLocal(t, ctx, pool)
	c := criaCampanhaDireto(t, ctx, pool, cliente, "")
	h := &CampaignsHandler{Repo: catalog.NewCampaigns(pool)}

	rec := putCampanha(t, h, c.ID, map[string]any{
		"name": "  Nome novo  ", "start_date": "2026-12-05T00:00:00Z", "end_date": "2027-03-01T00:00:00Z",
	})

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var out catalog.Campaign
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Equal(t, "Nome novo", out.Name)
	require.Equal(t, "2026-12-05", out.StartDate.Format("2006-01-02"))
	require.Equal(t, "2027-03-01", out.EndDate.Format("2006-01-02"))
	var nome string
	var ini, fim time.Time
	require.NoError(t, pool.QueryRow(ctx, `SELECT name, start_date, end_date FROM campaigns WHERE id = $1`, c.ID).Scan(&nome, &ini, &fim))
	require.Equal(t, "Nome novo", nome)
	require.Equal(t, "2026-12-05", ini.Format("2006-01-02"))
	require.Equal(t, "2027-03-01", fim.Format("2006-01-02"))
}

func TestCampanhaHTTP_Update_EmUmDiaSoEhValido(t *testing.T) {
	ctx, pool := poolHubCode(t)
	cliente := seedClienteLocal(t, ctx, pool)
	c := criaCampanhaDireto(t, ctx, pool, cliente, "")
	h := &CampaignsHandler{Repo: catalog.NewCampaigns(pool)}

	rec := putCampanha(t, h, c.ID, map[string]any{
		"name": "Um dia", "start_date": "2026-12-05T00:00:00Z", "end_date": "2026-12-05T00:00:00Z",
	})

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

func TestCampanhaHTTP_Update_IdInexistenteDa404ComESemCodigo(t *testing.T) {
	ctx, pool := poolHubCode(t)
	_ = seedClienteLocal(t, ctx, pool)
	h := &CampaignsHandler{Repo: catalog.NewCampaigns(pool)}
	basico := putBasico()

	require.Equal(t, http.StatusNotFound, putCampanha(t, h, uuid.New(), basico).Code, "sem hub_code")
	basico["hub_code"] = ""
	require.Equal(t, http.StatusNotFound, putCampanha(t, h, uuid.New(), basico).Code, "com hub_code (Get antes da barreira)")
}

func TestCampanhaHTTP_Update_Recusas400ComOTextoDeSempre(t *testing.T) {
	ctx, pool := poolHubCode(t)
	cliente := seedClienteLocal(t, ctx, pool)
	c := criaCampanhaDireto(t, ctx, pool, cliente, "")
	r := roteadorCampanha(&CampaignsHandler{Repo: catalog.NewCampaigns(pool)})
	alvo := "/campaigns/" + c.ID.String()

	for nome, caso := range map[string]struct {
		corpo any
		texto string
	}{
		"json inválido":       {`{`, "invalid request"},
		"nome vazio":          {map[string]any{"name": "   ", "start_date": "2026-12-01T00:00:00Z", "end_date": "2027-02-28T00:00:00Z"}, "name is required"},
		"sem datas":           {map[string]any{"name": "x"}, "start_date and end_date are required"},
		"fim antes do início": {map[string]any{"name": "x", "start_date": "2027-02-28T00:00:00Z", "end_date": "2026-12-01T00:00:00Z"}, "end_date must be on or after start_date"},
	} {
		t.Run(nome, func(t *testing.T) {
			rec := putJSON(t, r, alvo, caso.corpo)
			require.Equal(t, http.StatusBadRequest, rec.Code)
			require.Contains(t, rec.Body.String(), caso.texto)
		})
	}
	// Nada foi gravado pelas recusas.
	var nome string
	require.NoError(t, pool.QueryRow(ctx, `SELECT name FROM campaigns WHERE id = $1`, c.ID).Scan(&nome))
	require.Equal(t, "Campanha original", nome)
}

func TestCampanhaHTTP_UpdateStations_SemSupervisorGravaNoBanco(t *testing.T) {
	ctx, pool := poolHubCode(t)
	cliente := seedClienteLocal(t, ctx, pool)
	c := criaCampanhaDireto(t, ctx, pool, cliente, "")
	a, b := novaEmissora(t, ctx, pool, "A"), novaEmissora(t, ctx, pool, "B")
	r := roteadorCampanha(&CampaignsHandler{Repo: catalog.NewCampaigns(pool)})

	rec := putJSON(t, r, "/campaigns/"+c.ID.String()+"/stations", map[string]any{"target_stations": []string{a.String(), b.String()}})

	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	require.Empty(t, rec.Body.String())
	require.ElementsMatch(t, []uuid.UUID{a, b}, emissorasDaCampanha(t, ctx, pool, c.ID))

	// Lista omitida/nula APAGA todas (a rota sempre foi substituição total).
	rec = putJSON(t, r, "/campaigns/"+c.ID.String()+"/stations", `{}`)
	require.Equal(t, http.StatusNoContent, rec.Code)
	require.Empty(t, emissorasDaCampanha(t, ctx, pool, c.ID))
}

func TestCampanhaHTTP_UpdateStations_SemSupervisorCampanhaInexistenteDa404(t *testing.T) {
	ctx, pool := poolHubCode(t)
	_ = seedClienteLocal(t, ctx, pool)
	r := roteadorCampanha(&CampaignsHandler{Repo: catalog.NewCampaigns(pool)})

	rec := putJSON(t, r, "/campaigns/"+uuid.NewString()+"/stations", `{"target_stations":[]}`)

	require.Equal(t, http.StatusNotFound, rec.Code)
}

// fakeSupervisorGravando registra o que o handler pede ao supervisor.
type fakeSupervisorGravando struct {
	fakeSupervisor
	updates []chamadaUpdateStations
	err     error
}

type chamadaUpdateStations struct {
	id       uuid.UUID
	stations []uuid.UUID
}

func (f *fakeSupervisorGravando) UpdateStations(id uuid.UUID, stations []uuid.UUID) error {
	f.updates = append(f.updates, chamadaUpdateStations{id, stations})
	return f.err
}

func TestCampanhaHTTP_UpdateStations_ComSupervisorChamaComAListaENaoGravaPorFora(t *testing.T) {
	ctx, pool := poolHubCode(t)
	cliente := seedClienteLocal(t, ctx, pool)
	c := criaCampanhaDireto(t, ctx, pool, cliente, "")
	est := novaEmissora(t, ctx, pool, "S")
	sup := &fakeSupervisorGravando{}
	r := roteadorCampanha(&CampaignsHandler{Repo: catalog.NewCampaigns(pool), Supervisor: sup})

	rec := putJSON(t, r, "/campaigns/"+c.ID.String()+"/stations", map[string]any{"target_stations": []string{est.String()}})

	require.Equal(t, http.StatusNoContent, rec.Code)
	require.Equal(t, []chamadaUpdateStations{{c.ID, []uuid.UUID{est}}}, sup.updates)
	// Com supervisor, quem grava é o supervisor (aqui, um falso que só registra):
	// o handler não faz um UpdateTargetStations por fora.
	require.Empty(t, emissorasDaCampanha(t, ctx, pool, c.ID))
}

func TestCampanhaHTTP_UpdateStations_ErroDoSupervisorDa500(t *testing.T) {
	ctx, pool := poolHubCode(t)
	cliente := seedClienteLocal(t, ctx, pool)
	c := criaCampanhaDireto(t, ctx, pool, cliente, "")
	sup := &fakeSupervisorGravando{err: errors.New("nats caiu")}
	r := roteadorCampanha(&CampaignsHandler{Repo: catalog.NewCampaigns(pool), Supervisor: sup})

	rec := putJSON(t, r, "/campaigns/"+c.ID.String()+"/stations", `{"target_stations":[]}`)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.Contains(t, rec.Body.String(), "internal error")
}

// Cliente inexistente e datas invertidas eram 500 "internal error" (FK e CHECK do
// banco); agora são 404 e 400 com o motivo — melhoria declarada da extração.
func TestCampanhaHTTP_Create_ClienteInexistenteDa404EDatasInvertidasDa400(t *testing.T) {
	ctx, pool := poolHubCode(t)
	cliente := seedClienteLocal(t, ctx, pool)
	h := &CampaignsHandler{Repo: catalog.NewCampaigns(pool)}

	rec := postCampanha(t, h, campanhaNova(uuid.New(), ""))
	require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())

	corpo := campanhaNova(cliente, "")
	corpo["start_date"], corpo["end_date"] = "2027-02-28T00:00:00Z", "2026-12-01T00:00:00Z"
	rec = postCampanha(t, h, corpo)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "a data final não pode ser anterior à inicial")
	require.Zero(t, contaCampanhas(t, ctx, pool, cliente))
}
