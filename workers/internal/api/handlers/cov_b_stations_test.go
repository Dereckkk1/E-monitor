package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/catalog"
	"radiocheck/internal/mcp/mcptest"
	"radiocheck/internal/supervisor"
)

type covBStationEnv struct {
	ctx  context.Context
	pool *pgxpool.Pool
	h    *StationsHandler
	sup  *covBSup
}

// covBStationsSetup devolve o handler e 3 emissoras criadas PELO handler:
// Alfa (FM/SP), Beta (AM/RJ) e Gama (FM/SP).
func covBStationsSetup(t *testing.T) (*covBStationEnv, map[string]uuid.UUID) {
	t.Helper()
	ctx, pool := covBDB(t)
	sup := &covBSup{}
	env := &covBStationEnv{ctx: ctx, pool: pool, h: &StationsHandler{Repo: catalog.NewStations(pool), Workers: sup}, sup: sup}
	ids := map[string]uuid.UUID{}
	seed := []struct{ name, band, city, state string }{
		{"Rádio Alfa FM", "FM", "São Paulo", "SP"},
		{"Rádio Beta AM", "AM", "Rio de Janeiro", "RJ"},
		{"Rádio Gama FM", "FM", "Campinas", "SP"},
	}
	for _, s := range seed {
		body := `{"name":"` + s.name + `","band":"` + s.band + `","stream_url":"http://exemplo.test/` + strings.Fields(s.name)[1] +
			`","city":"` + s.city + `","state":"` + s.state + `"}`
		rec := covBServe(env.h.Create, covBReq("POST", "/stations", body, nil, nil))
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		id, err := uuid.Parse(covBJSON(t, rec)["id"].(string))
		require.NoError(t, err)
		ids[strings.Fields(s.name)[1]] = id
	}
	return env, ids
}

func covBListIDs(t *testing.T, rec *httptest.ResponseRecorder) (names []string, total int) {
	t.Helper()
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var out struct {
		Data  []catalog.Station `json:"data"`
		Total int               `json:"total"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	for _, s := range out.Data {
		names = append(names, s.Name)
	}
	return names, out.Total
}

func TestCovB_Stations_CreateGetUpdate(t *testing.T) {
	env, ids := covBStationsSetup(t)

	t.Run("create: sucesso devolve a emissora completa e já nasce em calibração", func(t *testing.T) {
		rec := covBServe(env.h.Get, covBReq("GET", "/x", "", map[string]string{"id": ids["Alfa"].String()}, nil))
		require.Equal(t, http.StatusOK, rec.Code)
		m := covBJSON(t, rec)
		assert.Equal(t, "Rádio Alfa FM", m["name"])
		assert.Equal(t, "FM", m["band"])
		assert.Equal(t, "SP", m["state"])
		assert.Equal(t, "paused", m["monitoring_status"], "emissora nova não monitora até uma campanha ativa")
		assert.Greater(t, m["short_id"].(float64), float64(0))
	})
	t.Run("get: inexistente 404", func(t *testing.T) {
		rec := covBServe(env.h.Get, covBReq("GET", "/x", "", map[string]string{"id": uuid.NewString()}, nil))
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})
	t.Run("threshold: emissora nova segue o padrão de calibração", func(t *testing.T) {
		rec := covBServe(env.h.GetThreshold, covBReq("GET", "/x", "", map[string]string{"id": ids["Alfa"].String()}, nil))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
		m := covBJSON(t, rec)
		assert.Equal(t, true, m["calibration_mode"])
		assert.EqualValues(t, 5, m["min_hashes"])
		assert.EqualValues(t, 0, m["days_elapsed"])
	})
	t.Run("threshold: sem linha usa o padrão; id inválido 400", func(t *testing.T) {
		rec := covBServe(env.h.GetThreshold, covBReq("GET", "/x", "", map[string]string{"id": uuid.NewString()}, nil))
		require.Equal(t, http.StatusOK, rec.Code)
		assert.EqualValues(t, 5, covBJSON(t, rec)["min_hashes"])
		rec = covBServe(env.h.GetThreshold, covBReq("GET", "/x", "", map[string]string{"id": "x"}, nil))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})
	valid := `{"name":"Rádio Alfa Nova FM","band":"AM","stream_url":"https://novo.test/live","city":"Santos","state":"SP","pmm":2500.5}`
	params := map[string]string{"id": ids["Alfa"].String()}

	t.Run("update: validações", func(t *testing.T) {
		tests := []struct {
			name   string
			params map[string]string
			body   string
			code   int
		}{
			{"id inválido", map[string]string{"id": "x"}, valid, http.StatusBadRequest},
			{"json inválido", params, `{"name":`, http.StatusBadRequest},
			{"sem nome", params, `{"band":"FM","stream_url":"http://x"}`, http.StatusBadRequest},
			{"sem banda", params, `{"name":"X","stream_url":"http://x"}`, http.StatusBadRequest},
			{"sem stream_url", params, `{"name":"X","band":"FM"}`, http.StatusBadRequest},
			{"banda inválida", params, `{"name":"X","band":"XX","stream_url":"http://x"}`, http.StatusBadRequest},
			{"inexistente", map[string]string{"id": uuid.NewString()}, valid, http.StatusNotFound},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				rec := covBServe(env.h.Update, covBReq("PUT", "/x", tc.body, tc.params, nil))
				assert.Equal(t, tc.code, rec.Code, rec.Body.String())
			})
		}
	})
	t.Run("update: sucesso grava os campos", func(t *testing.T) {
		rec := covBServe(env.h.Update, covBReq("PUT", "/x", valid, params, nil))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		m := covBJSON(t, rec)
		assert.Equal(t, "Rádio Alfa Nova FM", m["name"])
		assert.Equal(t, "AM", m["band"])
		assert.Equal(t, "https://novo.test/live", m["stream_url"])
		assert.Equal(t, "Santos", m["city"])
		assert.EqualValues(t, 2500.5, m["pmm"])
	})

	t.Run("stream url: troca só a URL e preserva o resto", func(t *testing.T) {
		rec := covBServe(env.h.UpdateStreamURL, covBReq("PATCH", "/x", `{"url":"https://stream.test/novo.mp3"}`,
			map[string]string{"id": ids["Beta"].String()}, nil))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		m := covBJSON(t, rec)
		assert.Equal(t, "https://stream.test/novo.mp3", m["stream_url"])
		assert.Equal(t, "Rádio Beta AM", m["name"])
	})
	t.Run("stream url: inexistente 404", func(t *testing.T) {
		rec := covBServe(env.h.UpdateStreamURL, covBReq("PATCH", "/x", `{"url":"http://x.test/a"}`,
			map[string]string{"id": uuid.NewString()}, nil))
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})
	t.Run("stream url: scheme perigoso e id inválido", func(t *testing.T) {
		for _, u := range []string{"file:///etc/passwd", "ftp://x.test", "gopher://x", "http:/x"} {
			rec := covBServe(env.h.UpdateStreamURL, covBReq("PATCH", "/x", `{"url":"`+u+`"}`, params, nil))
			assert.Equal(t, http.StatusBadRequest, rec.Code, u)
		}
		rec := covBServe(env.h.UpdateStreamURL, covBReq("PATCH", "/x", `{"url":"http://ok.test"}`, map[string]string{"id": "x"}, nil))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})
}

func TestCovB_Stations_ThresholdReflectsCalibration(t *testing.T) {
	env, ids := covBStationsSetup(t)
	id := ids["Gama"]
	_, err := env.pool.Exec(env.ctx,
		`UPDATE station_thresholds SET calibration_mode = FALSE, min_hashes = 9,
		        calibration_started_at = now() - interval '10 days' WHERE station_id = $1`, id)
	require.NoError(t, err)
	rec := covBServe(env.h.GetThreshold, covBReq("GET", "/x", "", map[string]string{"id": id.String()}, nil))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	m := covBJSON(t, rec)
	assert.Equal(t, false, m["calibration_mode"])
	assert.EqualValues(t, 9, m["min_hashes"])
	assert.EqualValues(t, 10, m["days_elapsed"])
}

func TestCovB_Stations_ListAndSuggest(t *testing.T) {
	env, ids := covBStationsSetup(t)
	list := func(query string) *httptest.ResponseRecorder {
		return covBServe(env.h.List, covBReq("GET", "/stations"+query, "", nil, nil))
	}

	tests := []struct {
		name  string
		query string
		want  []string
	}{
		{"sem filtro ordena e traz todas", "", []string{"Rádio Alfa FM", "Rádio Beta AM", "Rádio Gama FM"}},
		{"busca por nome", "?q=beta", []string{"Rádio Beta AM"}},
		{"banda AM", "?band=AM", []string{"Rádio Beta AM"}},
		{"banda FM", "?band=FM", []string{"Rádio Alfa FM", "Rádio Gama FM"}},
		{"estado RJ", "?state=RJ", []string{"Rádio Beta AM"}},
		{"cidade exata", "?city=Campinas", []string{"Rádio Gama FM"}},
		{"cidade sem acento casa com acento", "?city=Sao%20Paulo", []string{"Rádio Alfa FM"}},
		{"emissora específica", "?station_id=" + ids["Gama"].String(), []string{"Rádio Gama FM"}},
		{"ids fecha o conjunto", "?ids=" + ids["Alfa"].String() + "," + ids["Beta"].String(), []string{"Rádio Alfa FM", "Rádio Beta AM"}},
		{"ids ignora entradas em branco", "?ids=" + ids["Gama"].String() + ",,%20", []string{"Rádio Gama FM"}},
		{"sem resultado", "?q=naoexiste", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			names, total := covBListIDs(t, list(tc.query))
			assert.ElementsMatch(t, tc.want, names)
			assert.Equal(t, len(tc.want), total)
		})
	}

	t.Run("paginação respeita limit e page", func(t *testing.T) {
		rec := list("?limit=2&page=2")
		require.Equal(t, http.StatusOK, rec.Code)
		m := covBJSON(t, rec)
		assert.EqualValues(t, 3, m["total"])
		assert.EqualValues(t, 2, m["limit"])
		assert.EqualValues(t, 2, m["page"])
		assert.EqualValues(t, 2, m["pages"])
		assert.Len(t, m["data"], 1)
	})
	t.Run("limit acima do teto é ignorado", func(t *testing.T) {
		rec := list("?limit=10001")
		require.Equal(t, http.StatusOK, rec.Code)
		assert.NotEqualValues(t, 10001, covBJSON(t, rec)["limit"])
	})
	t.Run("entradas inválidas 400", func(t *testing.T) {
		parts := make([]string, 501)
		for i := range parts {
			parts[i] = uuid.NewString()
		}
		for name, q := range map[string]string{
			"station_id":    "?station_id=xx",
			"ids":           "?ids=" + ids["Alfa"].String() + ",xx",
			"ids acima 500": "?ids=" + strings.Join(parts, ","),
			"contracted_by": "?contracted_by=xx",
		} {
			assert.Equal(t, http.StatusBadRequest, list(q).Code, name)
		}
	})

	t.Run("suggest: menos de 2 caracteres devolve os três grupos vazios com 200", func(t *testing.T) {
		rec := covBServe(env.h.Suggest, covBReq("GET", "/stations/suggest?q=a", "", nil, nil))
		require.Equal(t, http.StatusOK, rec.Code)
		assert.JSONEq(t, `{"stations":[],"cities":[],"states":[]}`, rec.Body.String())
	})
	t.Run("suggest: acha emissora, cidade e UF", func(t *testing.T) {
		rec := covBServe(env.h.Suggest, covBReq("GET", "/stations/suggest?q=Campinas", "", nil, nil))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var out catalog.SuggestOutput
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
		require.NotEmpty(t, out.Cities)
		assert.Equal(t, "Campinas", out.Cities[0].City)
		assert.EqualValues(t, 1, out.Cities[0].Count)

		rec = covBServe(env.h.Suggest, covBReq("GET", "/stations/suggest?q=Alfa&band=FM", "", nil, nil))
		require.Equal(t, http.StatusOK, rec.Code)
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
		require.Len(t, out.Stations, 1)
		assert.Equal(t, "Rádio Alfa FM", out.Stations[0].Name)

		rec = covBServe(env.h.Suggest, covBReq("GET", "/stations/suggest?q=Alfa&band=AM", "", nil, nil))
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
		assert.Empty(t, out.Stations, "o filtro de banda também vale na sugestão")
	})
}

func TestCovB_Stations_ContractedBy(t *testing.T) {
	env, ids := covBStationsSetup(t)
	pool2, ctx := env.pool, env.ctx
	cli := mcptest.Cliente(t, ctx, pool2, "Cliente Teste")
	outro := mcptest.Cliente(t, ctx, pool2, "Outro Cliente Teste")
	camp := mcptest.Campanha(t, ctx, pool2, cli, "Campanha Teste", "2026-01-01", "2026-12-31", ids["Alfa"], ids["Gama"])
	mcptest.EstadoCampanha(t, ctx, pool2, camp, "ativa")

	req := func(claims string, id uuid.UUID) *httptest.ResponseRecorder {
		var c = covBClaims(claims, uuid.New())
		if claims == "viewer" {
			c = covBClaims("viewer", uuid.New(), cli)
		}
		return covBServe(env.h.List, covBReq("GET", "/stations?contracted_by="+id.String(), "", nil, c))
	}

	t.Run("admin lista só as emissoras do cliente, com o vínculo", func(t *testing.T) {
		rec := req("admin", cli)
		names, total := covBListIDs(t, rec)
		assert.ElementsMatch(t, []string{"Rádio Alfa FM", "Rádio Gama FM"}, names)
		assert.Equal(t, 2, total)
		var out struct {
			Data []catalog.Station `json:"data"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
		for _, s := range out.Data {
			require.NotNil(t, s.Contract)
			assert.Equal(t, 1, s.Contract.Campaigns)
			assert.True(t, s.Contract.OnAir)
		}
	})
	t.Run("cliente sem campanha vigente recebe lista vazia", func(t *testing.T) {
		names, total := covBListIDs(t, req("admin", outro))
		assert.Empty(t, names)
		assert.Equal(t, 0, total)
	})
	t.Run("viewer enxerga a própria carteira", func(t *testing.T) {
		names, _ := covBListIDs(t, req("viewer", cli))
		assert.Len(t, names, 2)
	})
	t.Run("viewer pedindo carteira alheia recebe 404 (anti-oráculo)", func(t *testing.T) {
		assert.Equal(t, http.StatusNotFound, req("viewer", outro).Code)
	})
	t.Run("campanha cancelada deixa de contar como contratada", func(t *testing.T) {
		mcptest.EstadoCampanha(t, ctx, pool2, camp, "cancelada")
		names, _ := covBListIDs(t, req("admin", cli))
		assert.Empty(t, names)
	})
}

func TestCovB_Stations_ConnectionTest(t *testing.T) {
	env, ids := covBStationsSetup(t)
	live := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer live.Close()
	// aponta a Alfa para o servidor local (a URL salva é a "testada" por padrão).
	rec := covBServe(env.h.UpdateStreamURL, covBReq("PATCH", "/x", `{"url":"`+live.URL+`/stream"}`,
		map[string]string{"id": ids["Alfa"].String()}, nil))
	require.Equal(t, http.StatusOK, rec.Code)

	call := func(id uuid.UUID, body string) *httptest.ResponseRecorder {
		return covBServe(env.h.ConnectionTest, covBReq("POST", "/x", body, map[string]string{"id": id.String()}, nil))
	}
	results := func(t *testing.T, rec *httptest.ResponseRecorder) (map[string]any, map[string]any) {
		t.Helper()
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		m := covBJSON(t, rec)
		return m, m["results"].(map[string]any)
	}
	status := func(r map[string]any, name string) string { return r[name].(map[string]any)["status"].(string) }

	t.Run("inexistente 404", func(t *testing.T) {
		assert.Equal(t, http.StatusNotFound, call(uuid.New(), `{"tests":["ping"]}`).Code)
	})
	t.Run("ping na URL salva: ok e os outros testes ficam skipped", func(t *testing.T) {
		m, r := results(t, call(ids["Alfa"], `{"tests":["ping"]}`))
		assert.Equal(t, live.URL+"/stream", m["tested_url"])
		assert.Equal(t, ids["Alfa"].String(), m["station_id"])
		assert.Equal(t, "ok", status(r, "ping"))
		assert.Equal(t, "skipped", status(r, "stream"))
		assert.Equal(t, "skipped", status(r, "worker"))
	})
	t.Run("ping com override para porta morta falha com detalhe", func(t *testing.T) {
		m, r := results(t, call(ids["Alfa"], `{"tests":["ping"],"url":"http://`+covBDeadAddr(t)+`/x"}`))
		assert.Contains(t, m["tested_url"], "/x")
		assert.Equal(t, "fail", status(r, "ping"))
		assert.Equal(t, "host inalcançável", r["ping"].(map[string]any)["detail"])
	})
	t.Run("override com scheme não http é recusado em ping e stream (sem spawnar ffprobe)", func(t *testing.T) {
		_, r := results(t, call(ids["Alfa"], `{"tests":["ping","stream"],"url":"file:///etc/passwd"}`))
		assert.Equal(t, "fail", status(r, "ping"))
		assert.Equal(t, "fail", status(r, "stream"))
		assert.Contains(t, r["stream"].(map[string]any)["detail"], "scheme")
	})
	t.Run("worker vivo com PCM fresco responde 'live' sem probe", func(t *testing.T) {
		env.sup.ws = []supervisor.WorkerStatus{{StationID: ids["Alfa"].String(), Active: true, LastPCMAt: time.Now()}}
		_, r := results(t, call(ids["Alfa"], `{"tests":["worker"]}`))
		w := r["worker"].(map[string]any)
		assert.Equal(t, "ok", w["status"])
		assert.Equal(t, "live", w["source"])
		_, err := time.Parse(time.RFC3339, w["last_pcm_at"].(string))
		assert.NoError(t, err)
	})
	t.Run("worker com override de URL ignora o worker vivo e usa o caminho efêmero", func(t *testing.T) {
		env.sup.ws = []supervisor.WorkerStatus{{StationID: ids["Alfa"].String(), Active: true, LastPCMAt: time.Now()}}
		_, r := results(t, call(ids["Alfa"], `{"tests":["worker"],"url":"gopher://x"}`))
		w := r["worker"].(map[string]any)
		assert.Equal(t, "fail", w["status"], "URL com scheme inválido falha antes de qualquer ffmpeg")
		assert.NotEqual(t, "live", w["source"])
	})
}

func TestCovB_StreamHealth(t *testing.T) {
	ctx, pool := covBDB(t)
	sup := &covBSup{}
	h := &StreamHealthHandler{
		HealthEvents: catalog.NewHealthEvents(pool),
		Stations:     catalog.NewStations(pool),
		Sup:          sup,
	}
	mk := func(name string) uuid.UUID {
		id := mcptest.Emissora(t, ctx, pool, name, 0)
		_, err := pool.Exec(ctx, `UPDATE stations SET monitoring_status = 'active' WHERE id = $1`, id)
		require.NoError(t, err)
		return id
	}
	running, stalled := mk("Rádio A FM"), mk("Rádio B FM")
	_ = mk("Rádio C FM") // ativa, sem worker no supervisor
	// uma emissora pausada nunca aparece.
	_ = mcptest.Emissora(t, ctx, pool, "Rádio Pausada FM", 0)
	now := time.Now()
	sup.ws = []supervisor.WorkerStatus{
		{StationID: running.String(), Active: true, LastPCMAt: now},
		{StationID: stalled.String(), Active: true, LastPCMAt: now.Add(-time.Hour), StallRisk: true},
		{StationID: "nao-e-uuid"},
	}
	mcptest.Queda(t, ctx, pool, stalled, now.Add(-2*time.Hour), 600)

	t.Run("list: só emissoras ativas, com estado do worker e uptime", func(t *testing.T) {
		rec := covBServe(h.List, covBReq("GET", "/stream-health", "", nil, nil))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var rows []map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &rows))
		require.Len(t, rows, 3)
		by := map[string]map[string]any{}
		for _, r := range rows {
			by[r["name"].(string)] = r
		}
		assert.Equal(t, "running", by["Rádio A FM"]["worker_status"])
		assert.NotEmpty(t, by["Rádio A FM"]["worker_last_pcm_at"])
		assert.Equal(t, "stalled", by["Rádio B FM"]["worker_status"])
		assert.Equal(t, "missing", by["Rádio C FM"]["worker_status"])
		assert.Nil(t, by["Rádio C FM"]["worker_last_pcm_at"])
		assert.EqualValues(t, 100, by["Rádio A FM"]["uptime_pct"], "sem quedas = 100%")
		assert.Less(t, by["Rádio B FM"]["uptime_pct"].(float64), float64(100), "a queda de 10min reduz o uptime")
		assert.EqualValues(t, 1, by["Rádio B FM"]["incident_count"])
		assert.NotNil(t, by["Rádio B FM"]["last_incident_at"])
		assert.IsType(t, []any{}, by["Rádio A FM"]["daily_summary"])
	})
	t.Run("list sem supervisor: todos missing", func(t *testing.T) {
		h2 := &StreamHealthHandler{HealthEvents: h.HealthEvents, Stations: h.Stations}
		rec := covBServe(h2.List, covBReq("GET", "/stream-health?days=3", "", nil, nil))
		require.Equal(t, http.StatusOK, rec.Code)
		var rows []map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &rows))
		for _, r := range rows {
			assert.Equal(t, "missing", r["worker_status"])
		}
	})
	t.Run("detail: eventos da emissora e janela do período", func(t *testing.T) {
		rec := covBServe(h.Detail, covBReq("GET", "/stream-health/x?days=3", "", map[string]string{"stationId": stalled.String()}, nil))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		m := covBJSON(t, rec)
		assert.Equal(t, stalled.String(), m["station_id"])
		evs, ok := m["events"].([]any)
		require.True(t, ok)
		require.Len(t, evs, 1)
		assert.Equal(t, "down", evs[0].(map[string]any)["event_type"])
		from, err1 := time.Parse(time.RFC3339, m["period_start"].(string))
		to, err2 := time.Parse(time.RFC3339, m["period_end"].(string))
		require.NoError(t, err1)
		require.NoError(t, err2)
		assert.InDelta(t, 72, to.Sub(from).Hours(), 0.1, "days=3 → janela de 72h")
	})
	t.Run("detail: emissora sem eventos devolve array vazio", func(t *testing.T) {
		rec := covBServe(h.Detail, covBReq("GET", "/stream-health/x", "", map[string]string{"stationId": running.String()}, nil))
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, []any{}, covBJSON(t, rec)["events"])
	})
	t.Run("detail: id inválido 400", func(t *testing.T) {
		rec := covBServe(h.Detail, covBReq("GET", "/stream-health/x", "", map[string]string{"stationId": "x"}, nil))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})
}

func TestCovB_ParseDays(t *testing.T) {
	tests := map[string]int{
		"":          7,
		"?days=1":   1,
		"?days=90":  90,
		"?days=30":  30,
		"?days=0":   7,
		"?days=91":  7,
		"?days=-3":  7,
		"?days=abc": 7,
	}
	for q, want := range tests {
		req := httptest.NewRequest("GET", "/x"+q, nil)
		assert.Equal(t, want, parseDays(req, 7), "query %q", q)
	}
	assert.Equal(t, 14, parseDays(httptest.NewRequest("GET", "/x?days=oops", nil), 14), "o default informado é respeitado")
}

func TestCovB_Health(t *testing.T) {
	_, pool := covBDB(t)

	t.Run("check: banco ok e NATS ausente = degraded", func(t *testing.T) {
		h := &HealthHandler{DB: pool}
		rec := covBServe(h.Check, covBReq("GET", "/health", "", nil, nil))
		require.Equal(t, http.StatusOK, rec.Code)
		m := covBJSON(t, rec)
		assert.Equal(t, "degraded", m["status"])
		deps := m["deps"].(map[string]any)
		assert.Equal(t, "ok", deps["postgres"])
		assert.Equal(t, "down", deps["nats"])
	})
	t.Run("check: tudo ok", func(t *testing.T) {
		h := &HealthHandler{DB: pool, NATS: covBNATS(t)}
		m := covBJSON(t, covBServe(h.Check, covBReq("GET", "/health", "", nil, nil)))
		assert.Equal(t, "ok", m["status"])
		assert.Equal(t, "ok", m["deps"].(map[string]any)["nats"])
	})
	t.Run("check: banco fora marca down com a causa", func(t *testing.T) {
		h := &HealthHandler{DB: covBClosedPool(t), NATS: covBNATS(t)}
		m := covBJSON(t, covBServe(h.Check, covBReq("GET", "/health", "", nil, nil)))
		assert.Equal(t, "degraded", m["status"])
		assert.True(t, strings.HasPrefix(m["deps"].(map[string]any)["postgres"].(string), "down: "))
	})

	t.Run("workers: sem supervisor devolve lista vazia e CLAP desligado", func(t *testing.T) {
		t.Setenv("CLAP_VERIFIER_URL", "")
		h := &HealthHandler{}
		rec := covBServe(h.WorkerStatus, covBReq("GET", "/workers", "", nil, nil))
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
		assert.JSONEq(t, `{"workers":[],"clap_verifier":false}`, strings.TrimSpace(rec.Body.String()))
	})
	t.Run("workers: lista o supervisor e sonda o CLAP", func(t *testing.T) {
		clap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/health" {
				w.WriteHeader(200)
				return
			}
			w.WriteHeader(404)
		}))
		defer clap.Close()
		t.Setenv("CLAP_VERIFIER_URL", clap.URL)
		id := uuid.New()
		h := &HealthHandler{Sup: &covBSup{ws: []supervisor.WorkerStatus{{StationID: id.String(), Active: true, Reconnects: 4}}}}
		var out struct {
			Workers []supervisor.WorkerStatus `json:"workers"`
			Clap    bool                      `json:"clap_verifier"`
		}
		require.NoError(t, json.Unmarshal(covBServe(h.WorkerStatus, covBReq("GET", "/workers", "", nil, nil)).Body.Bytes(), &out))
		assert.True(t, out.Clap)
		require.Len(t, out.Workers, 1)
		assert.Equal(t, id.String(), out.Workers[0].StationID)
		assert.EqualValues(t, 4, out.Workers[0].Reconnects)
	})
	t.Run("workers: CLAP com erro 500 ou inalcançável = false", func(t *testing.T) {
		bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
		defer bad.Close()
		for _, url := range []string{bad.URL, "http://" + covBDeadAddr(t)} {
			t.Setenv("CLAP_VERIFIER_URL", url)
			h := &HealthHandler{}
			m := covBJSON(t, covBServe(h.WorkerStatus, covBReq("GET", "/workers", "", nil, nil)))
			assert.Equal(t, false, m["clap_verifier"], url)
		}
	})
}
