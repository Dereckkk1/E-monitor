package mcp

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"radiocheck/internal/api/handlers"
	"radiocheck/internal/assertiveness"
	"radiocheck/internal/catalog"
	"radiocheck/internal/mcp/mcptest"
	"radiocheck/internal/supervisor"
)

func TestVisaoGerencial_ParidadeComATela(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	e1 := mcptest.Emissora(t, ctx, pool, "Rádio Um", 1000)
	e2 := mcptest.Emissora(t, ctx, pool, "Rádio Dois", 1000)
	mcptest.Cobertura(t, ctx, pool, e1, "A1", 50, 75, mcptest.CidadeAlcance{IBGE: 3550308, Nome: "São Paulo", Km: 0})
	tipo := mcptest.TipoMaterial(t, ctx, pool, "Spot")
	mat := mcptest.Material(t, ctx, pool, cli, tipo, "Spot A")
	mcptest.Vincular(t, ctx, pool, mcptest.Campanha(t, ctx, pool, cli, "Outra", "2026-09-01", "2026-09-30", e1), mat)
	camp := mcptest.Campanha(t, ctx, pool, cli, "Camp", "2026-10-01", "2026-10-31", e1, e2)
	mcptest.EstadoCampanha(t, ctx, pool, camp, "ativa")
	mcptest.Vincular(t, ctx, pool, camp, mat)
	mcptest.Vincular(t, ctx, pool, camp, mcptest.Material(t, ctx, pool, cli, tipo, "Spot B"))
	mcptest.Veiculacao(t, ctx, pool, camp, mat, e1, "in_slot", mcptest.Quando("2026-10-10", "08:00"))
	// "Hoje" no relógio do BANCO: o SQL do catalog compara com now() do Postgres
	// (não há relógio injetável lá), e o relógio do Go pode divergir dele. Só
	// fica errado se o teste cruzar a meia-noite de SP entre o INSERT e o
	// SELECT. O período termina antes de hoje para esta não entrar em
	// veiculacoes_no_periodo.
	var agoraDoBanco time.Time
	require.NoError(t, pool.QueryRow(ctx, `SELECT now()`).Scan(&agoraDoBanco))
	mcptest.Veiculacao(t, ctx, pool, camp, mat, e1, "in_slot", agoraDoBanco)
	// O supervisor tem worker ativo em e1, inativo em e2 e ativo numa emissora fora do recorte.
	workers := &workersFalsos{st: []supervisor.WorkerStatus{
		{StationID: e1.String(), Active: true}, {StationID: e2.String(), Active: false}, {StationID: "fora", Active: true}}}

	h := &handlers.ManagementOverviewHandler{Repo: catalog.NewManagementOverview(pool), Workers: workers}
	var tela catalog.ManagementResult
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.Get, "/management-overview?client_id="+cli.String()+"&from=2026-01-01&to=2026-10-27", nil, admin), &tela))

	cfg := cfgTeste(pool)
	cfg.Leitura.Workers = workers
	out := chamarTool(t, cfg, "visao_gerencial", map[string]any{"cliente_id": cli.String(), "de": "2026-01-01", "ate": "2026-10-27"})
	k := out["kpis"].(map[string]any)
	require.EqualValues(t, tela.KPIs.StationsMonitored, k["emissoras_monitoradas"])
	require.EqualValues(t, tela.KPIs.StationsLive, k["emissoras_ao_vivo"])
	require.EqualValues(t, 1, k["emissoras_ao_vivo"])
	require.EqualValues(t, tela.KPIs.StatesCount, k["estados"])
	require.EqualValues(t, 1, k["estados"], "só a Rádio Um tem UF")
	require.EqualValues(t, tela.KPIs.MaterialsMonitored, k["materiais_monitorados"])
	require.EqualValues(t, 2, k["materiais_monitorados"], "Spot A e Spot B, sem repetir")
	require.EqualValues(t, tela.KPIs.CampaignsCount, k["campanhas"])
	require.EqualValues(t, 2, k["campanhas"])
	require.EqualValues(t, tela.KPIs.AiringsTotal, k["veiculacoes_no_periodo"])
	require.EqualValues(t, 1, k["veiculacoes_no_periodo"])
	require.EqualValues(t, tela.KPIs.AiringsToday, k["veiculacoes_hoje"])
	require.EqualValues(t, 1, k["veiculacoes_hoje"])
	require.EqualValues(t, len(tela.Stations), out["emissoras_no_mapa"])
	require.Equal(t, map[string]any{"de": "2026-01-01", "ate": "2026-10-27"}, out["periodo"])
	require.Equal(t, "https://portal.teste/management?client_id="+cli.String()+"&from=2026-01-01&to=2026-10-27",
		out["links"].(map[string]any)["tela"])
	_, temLista := out["stations"]
	require.False(t, temLista, "a lista do mapa e o feed não saem")

	// O período escolhe as campanhas: em outubro a "Outra" (setembro) sai, e
	// veiculacoes_hoje também só conta as campanhas escolhidas.
	out = chamarTool(t, cfg, "visao_gerencial", map[string]any{"cliente_id": cli.String(), "de": "2026-08-01", "ate": "2026-09-30"})
	k = out["kpis"].(map[string]any)
	require.EqualValues(t, 1, k["campanhas"], "só a Outra cruza a janela")
	require.EqualValues(t, 1, k["materiais_monitorados"])
	require.EqualValues(t, 0, k["veiculacoes_hoje"], "a tocada de hoje é da Camp, que não cruza a janela")

	// Filtro explícito de canceladas: os KPIs são o histórico delas.
	mcptest.EstadoCampanha(t, ctx, pool, camp, "cancelada")
	out = chamarTool(t, cfg, "visao_gerencial", map[string]any{"cliente_id": cli.String(), "estado": "cancelada", "de": "2026-01-01", "ate": "2026-10-27"})
	require.EqualValues(t, 1, out["kpis"].(map[string]any)["campanhas"])
	require.Contains(t, out["ressalvas"], ressalvaCanceladasFiltradas)
	require.NotContains(t, out["ressalvas"], ressalvaCanceladaRelatorio)
	mcptest.EstadoCampanha(t, ctx, pool, camp, "ativa")

	// Sem supervisor (CLI, testes): null, nunca 0.
	semW := chamarTool(t, cfgTeste(pool), "visao_gerencial", map[string]any{"cliente_id": cli.String()})
	ao, existe := semW["kpis"].(map[string]any)["emissoras_ao_vivo"]
	require.True(t, existe)
	require.Nil(t, ao)
}

func TestVisaoGerencial_PadraoDeDatasERecusas(t *testing.T) {
	_, pool := mcptest.DBCatalogo(t)
	cfg := cfgTeste(pool)
	cfg.Leitura.Agora = func() time.Time { return time.Date(2026, 10, 28, 2, 0, 0, 0, time.UTC) } // 27/10 23h em SP
	out := chamarTool(t, cfg, "visao_gerencial", map[string]any{})
	require.Equal(t, map[string]any{"de": "2026-01-01", "ate": "2026-10-27"}, out["periodo"])
	require.Contains(t, chamarToolErro(t, cfg, "visao_gerencial", map[string]any{"estado": "encerrada"}), "[entrada_invalida]")
	require.Contains(t, chamarToolErro(t, cfg, "visao_gerencial", map[string]any{"cliente_id": "x"}), "[entrada_invalida]")
	require.Contains(t, chamarToolErro(t, cfg, "visao_gerencial", map[string]any{"de": "2026-10-02", "ate": "2026-10-01"}), "[entrada_invalida]")
}

func TestAssertividade_ParidadeComATela(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	est := mcptest.Emissora(t, ctx, pool, "Rádio", 1000)
	tipo := mcptest.TipoMaterial(t, ctx, pool, "Spot")
	mat := mcptest.Material(t, ctx, pool, cli, tipo, "Spot A")
	camp := mcptest.Campanha(t, ctx, pool, cli, "Camp", "2026-08-01", "2026-09-30", est)
	agora := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC) // mês fechado = setembro; anterior = agosto
	mcptest.Assertividade(t, ctx, pool, mcptest.Dia("2026-09-10"), camp, est, mat, 195, 5, 3)
	mcptest.Assertividade(t, ctx, pool, mcptest.Dia("2026-08-10"), camp, est, mat, 90, 10, 0)

	h := &handlers.AssertivenessHandler{Repo: assertiveness.New(pool), NowFn: func() time.Time { return agora }}
	var tela assertiveness.Result
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.Get, "/assertiveness?client_id="+cli.String(), nil, admin), &tela))

	cfg := cfgTeste(pool)
	cfg.Leitura.Agora = func() time.Time { return agora }
	out := chamarTool(t, cfg, "assertividade", map[string]any{"cliente_id": cli.String()})
	m := out["mes_fechado"].(map[string]any)
	require.Equal(t, tela.Current.Month, m["mes"])
	require.EqualValues(t, *tela.Current.Pct, m["percentual"])
	require.EqualValues(t, tela.Current.Auto, m["detectadas_sozinhas"])
	require.EqualValues(t, tela.Current.Miss, m["nao_detectadas"])
	require.EqualValues(t, tela.Current.ManualTotal, m["manuais_total"])
	require.EqualValues(t, 97.5, m["percentual"], "195 / (195 + 5)")
	require.EqualValues(t, 3, m["manuais_desculpadas"].(map[string]any)["sem_fingerprint"])
	require.NotNil(t, tela.Previous)
	require.Equal(t, tela.Previous.Month, out["mes_anterior"].(map[string]any)["mes"])
	require.EqualValues(t, *tela.Previous.Pct, out["mes_anterior"].(map[string]any)["percentual"])
	require.EqualValues(t, *tela.DeltaPP, out["variacao_pp"])
	require.Contains(t, out["ressalvas"].([]any)[0], "volume")
	require.Equal(t, "https://portal.teste/management?client_id="+cli.String(), out["links"].(map[string]any)["tela"])

	// Outro cliente: sem veiculação no mês, percentual null (não é 0% nem 100%).
	outro := mcptest.Cliente(t, ctx, pool, "Outro")
	vazio := chamarTool(t, cfg, "assertividade", map[string]any{"cliente_id": outro.String()})
	p, existe := vazio["mes_fechado"].(map[string]any)["percentual"]
	require.True(t, existe)
	require.Nil(t, p)
}
