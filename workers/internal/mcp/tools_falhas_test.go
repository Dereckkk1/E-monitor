package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/api/handlers"
	"radiocheck/internal/catalog"
	"radiocheck/internal/mcp/mcptest"
)

// cenarioFalha: campanha de UM dia (d = anteontem) com meta 2/dia (06–22h) em
// duas emissoras. Rádio Um caiu 1 h às 10h (fora da faixa 21h–24h de SP, onde
// o recorte UTC de station_failures e o de SP de failures_daily divergem) e
// só tocou 1 vez, fora da faixa: déficit 2 = 1 "fora do horário" + 1 "não
// tocou". Rádio Dois não tocou nada: déficit 2, todo "não tocou".
func cenarioFalha(t *testing.T) (ctx context.Context, pool *pgxpool.Pool, admin, camp, e1, e2 uuid.UUID, d string) {
	t.Helper()
	ctx, pool = mcptest.DBCatalogo(t)
	admin = mcptest.Usuario(t, ctx, pool, "admin", true)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	e1 = mcptest.Emissora(t, ctx, pool, "Rádio Um", 1000)
	e2 = mcptest.Emissora(t, ctx, pool, "Rádio Dois", 1000)
	tipo := mcptest.TipoMaterial(t, ctx, pool, "Spot")
	mat := mcptest.Material(t, ctx, pool, cli, tipo, "Spot A")
	d = time.Now().AddDate(0, 0, -2).Format("2006-01-02")
	camp = mcptest.Campanha(t, ctx, pool, cli, "Camp", d, d, e1, e2)
	mcptest.EstadoCampanha(t, ctx, pool, camp, "ativa")
	mcptest.Vincular(t, ctx, pool, camp, mat, e1, e2)
	mcptest.Regra(t, ctx, pool, camp, tipo, e1, d, d, 127, "06:00", "22:00", 2)
	mcptest.Regra(t, ctx, pool, camp, tipo, e2, d, d, 127, "06:00", "22:00", 2)
	mcptest.Veiculacao(t, ctx, pool, camp, mat, e1, "out_slot", mcptest.Quando(d, "23:00"))
	mcptest.Queda(t, ctx, pool, e1, mcptest.Quando(d, "10:00"), 3600)
	return
}

func TestFalhasEmissoras_ParidadeComATela(t *testing.T) {
	_, pool, admin, camp, e1, _, d := cenarioFalha(t)
	h := &handlers.StationFailuresHandler{Repo: catalog.NewStationFailures(pool)}
	var tela catalog.Result
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.Get, "/x?date="+d, nil, admin), &tela))
	require.Len(t, tela.Stations, 2, "cenário: as duas emissoras falharam")

	out := chamarTool(t, cfgTeste(pool), "falhas_emissoras", map[string]any{"data": d})
	r := out["resumo"].(map[string]any)
	require.EqualValues(t, tela.Summary.StationsWithFailure, r["emissoras_com_falha"])
	require.EqualValues(t, tela.Summary.TotalDownSeconds, r["segundos_fora_do_ar"])
	require.EqualValues(t, 3600, r["segundos_fora_do_ar"])
	require.EqualValues(t, tela.Summary.AffectedCampaigns, r["campanhas_afetadas"])
	require.EqualValues(t, len(tela.Stations), pag(t, out)["total"])
	xs := itens(t, out)
	for i, s := range tela.Stations {
		require.Equal(t, s.Station.ID.String(), xs[i]["emissora"].(map[string]any)["id"], "ordem da tela")
		require.EqualValues(t, s.TotalDownSeconds, xs[i]["segundos_fora_do_ar"])
		require.EqualValues(t, s.HasSilentGap, xs[i]["tem_lacuna_silenciosa"])
		require.EqualValues(t, len(s.Incidents), xs[i]["quedas_total"])
		for j, c := range s.Campaigns {
			cc := xs[i]["campanhas"].([]any)[j].(map[string]any)
			require.Equal(t, c.CampaignID.String(), cc["campanha"].(map[string]any)["id"])
			require.EqualValues(t, c.Expected, cc["esperado"])
			require.EqualValues(t, c.Delivered, cc["entregue"])
			require.EqualValues(t, c.Deficit, cc["deficit"])
		}
	}
	um := xs[0]
	require.Equal(t, e1.String(), um["emissora"].(map[string]any)["id"], "mais tempo fora do ar primeiro")
	require.EqualValues(t, 3600, um["quedas"].([]any)[0].(map[string]any)["duracao_seg"])
	c := um["campanhas"].([]any)[0].(map[string]any)
	require.Equal(t, camp.String(), c["campanha"].(map[string]any)["id"])
	require.EqualValues(t, 2, c["deficit"])
	require.Contains(t, c["motivos"], "stream-down")
	require.Contains(t, xs[1]["campanhas"].([]any)[0].(map[string]any)["motivos"], "silent-gap")
	require.Equal(t, tela.Stations[0].Station.Dial, um["dial"])
	require.Equal(t, d, out["data"])
	tela1 := out["links"].(map[string]any)["tela"].(string)
	require.Contains(t, tela1, "view=by_station")
	require.Contains(t, tela1, "date="+d)
	require.Contains(t, tela1, "min_down=60")
}

func TestFalhasEmissoras_Recusas(t *testing.T) {
	_, pool := mcptest.DBCatalogo(t)
	cfg := cfgTeste(pool)
	require.Contains(t, chamarToolErro(t, cfg, "falhas_emissoras", map[string]any{"data": time.Now().AddDate(0, 0, 3).Format("2006-01-02")}), "futuro")
	require.Contains(t, chamarToolErro(t, cfg, "falhas_emissoras", map[string]any{"data": time.Now().AddDate(0, 0, -95).Format("2006-01-02")}), "90 dias")
	require.Contains(t, chamarToolErro(t, cfg, "falhas_emissoras", map[string]any{"data": "ontem"}), "[entrada_invalida]")
	require.Contains(t, chamarToolErro(t, cfg, "falhas_emissoras", map[string]any{"min_queda_seg": 120}), "[entrada_invalida]")
	// Padrão: ontem em America/Sao_Paulo, ecoado.
	L := NovaLeitura(pool)
	fixo := time.Date(2026, 10, 28, 2, 0, 0, 0, time.UTC) // 27/10 23h em SP
	L.Agora = func() time.Time { return fixo }
	out := chamarTool(t, ConfigServidor{Versao: "teste", Leitura: L}, "falhas_emissoras", map[string]any{})
	require.Equal(t, "2026-10-26", out["data"])
	require.Equal(t, []any{}, out["items"])
}

// Pior caso de uma página: o teto de emissoras × (tetoPorItem quedas +
// tetoPorItem campanhas), com nomes longos. O JSON tem de caber no cliente MCP
// (~25k tokens; alvo < 60 KB).
func TestFalhasEmissoras_PiorCasoCabe(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	cli := mcptest.Cliente(t, ctx, pool, "Agência De Publicidade E Propaganda Do Interior Ltda")
	tipo := mcptest.TipoMaterial(t, ctx, pool, "Spot")
	d := time.Now().AddDate(0, 0, -2).Format("2006-01-02")
	var ests []uuid.UUID
	for i := range tetoPaginaFalhasEmissoras + 1 {
		e := mcptest.Emissora(t, ctx, pool, fmt.Sprintf("Rádio Comunitária E Educativa Do Interior Paulista %02d FM", i), 1000)
		_, err := pool.Exec(ctx, `UPDATE stations SET frequency_mhz = 102.7, city = 'São José Do Rio Preto Da Serra' WHERE id = $1`, e)
		require.NoError(t, err)
		for j := range tetoPorItem + 1 {
			mcptest.Queda(t, ctx, pool, e, mcptest.Quando(d, "06:00").Add(time.Duration(j)*20*time.Minute), 600)
		}
		ests = append(ests, e)
	}
	regras := catalog.NewDistributionRules(pool)
	for j := range tetoPorItem + 1 {
		camp := mcptest.Campanha(t, ctx, pool, cli, fmt.Sprintf("Campanha De Verão Institucional Com Nome Bem Comprido %02d", j), d, d, ests...)
		mcptest.EstadoCampanha(t, ctx, pool, camp, "ativa")
		_, err := regras.Create(ctx, catalog.CreateDistributionRuleInput{
			CampaignID: camp, TypeID: tipo, StationIDs: ests, MaterialIDs: []uuid.UUID{},
			Name: "Regra", StartDate: mcptest.Dia(d), EndDate: mcptest.Dia(d), WeekdayMask: 127,
			TimeStart: "06:00", TimeEnd: "22:00", PlaysPerDay: 12,
		})
		require.NoError(t, err)
	}

	out := chamarTool(t, cfgTeste(pool), "falhas_emissoras", map[string]any{"data": d, "limit": 100})
	require.Len(t, itens(t, out), tetoPaginaFalhasEmissoras)
	require.EqualValues(t, tetoPaginaFalhasEmissoras, pag(t, out)["limit"], "limit acima do teto é rebaixado e ecoado")
	require.EqualValues(t, tetoPaginaFalhasEmissoras+1, pag(t, out)["total"])
	x := itens(t, out)[0]
	require.Len(t, x["quedas"], tetoPorItem)
	require.EqualValues(t, tetoPorItem+1, x["quedas_total"])
	require.Len(t, x["campanhas"], tetoPorItem)
	require.EqualValues(t, tetoPorItem+1, x["campanhas_total"])
	require.Contains(t, out["ressalvas"].([]any)[0], fmt.Sprintf("as %d primeiras", tetoPorItem))
	n := tamanhoDoResultado(t, out)
	t.Logf("falhas_emissoras: %d emissoras × (%d quedas + %d campanhas) = %d bytes", tetoPaginaFalhasEmissoras, tetoPorItem, tetoPorItem, n)
	require.Less(t, n, 60_000)
}

func TestFalhasPorDia_ParidadeComATela(t *testing.T) {
	_, pool, admin, _, _, _, d := cenarioFalha(t)
	de := time.Now().AddDate(0, 0, -10).Format("2006-01-02")
	ate := time.Now().Format("2006-01-02")
	h := &handlers.FailuresDailyHandler{Repo: catalog.NewFailuresDaily(pool)}
	var tela catalog.FailuresDailyResult
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.Get, "/x?from="+de+"&to="+ate, nil, admin), &tela))

	out := chamarTool(t, cfgTeste(pool), "falhas_por_dia", map[string]any{"de": de, "ate": ate})
	r := out["resumo"].(map[string]any)
	require.EqualValues(t, tela.Summary.DaysTotal, r["dias"])
	require.EqualValues(t, tela.Summary.DaysWithFailure, r["dias_com_falha"])
	require.EqualValues(t, 1, r["dias_com_falha"])
	require.EqualValues(t, tela.Summary.PeakStations, r["pico_emissoras"])
	require.EqualValues(t, 2, r["pico_emissoras"])
	require.Equal(t, tela.Summary.PeakDate, r["dia_do_pico"])
	require.Equal(t, d, r["dia_do_pico"])
	require.EqualValues(t, tela.Summary.TotalDeficit, r["deficit_total"])
	require.EqualValues(t, 4, r["deficit_total"])
	require.EqualValues(t, tela.Summary.TotalDownSeconds, r["segundos_fora_do_ar"])
	require.EqualValues(t, 3600, r["segundos_fora_do_ar"])
	require.InDelta(t, tela.Summary.AvgStations, r["media_emissoras_por_dia"], 1e-9)
	xs := itens(t, out)
	require.Len(t, xs, len(tela.Days))
	for i, p := range tela.Days {
		require.Equal(t, p.Date, xs[i]["data"])
		require.EqualValues(t, p.Stations, xs[i]["emissoras"])
		require.EqualValues(t, p.Campaigns, xs[i]["campanhas"])
		require.EqualValues(t, p.Deficit, xs[i]["deficit"])
		require.EqualValues(t, p.DownSeconds, xs[i]["segundos_fora_do_ar"])
		require.EqualValues(t, p.DownEvents, xs[i]["quedas"])
	}
	require.Equal(t, de, out["de"])
	require.Equal(t, ate, out["ate"])
	require.Contains(t, out["links"].(map[string]any)["tela"], "view=by_day")
}

// O relógio da tool fica no meio-dia de SP de hoje: o "hoje" em SP (padrão,
// F2-5) e o do processo (limites do handler) são o mesmo dia em qualquer fuso
// a até 12 h de SP, e o teste não depende da hora em que roda.
func TestFalhasPorDia_PadraoERecusas(t *testing.T) {
	_, pool := mcptest.DBCatalogo(t)
	agora := hojeSP(time.Now())
	agora = time.Date(agora.Year(), agora.Month(), agora.Day(), 12, 0, 0, 0, fusoSP)
	L := NovaLeitura(pool)
	L.Agora = func() time.Time { return agora }
	cfg := ConfigServidor{Versao: "teste", Portal: "https://portal.teste", Leitura: L}
	hoje := dia(hojeSP(agora))
	em := func(dias int) string { return dia(hojeSP(agora).AddDate(0, 0, dias)) }

	out := chamarTool(t, cfg, "falhas_por_dia", map[string]any{})
	require.Equal(t, em(-29), out["de"])
	require.Equal(t, hoje, out["ate"])
	require.Len(t, itens(t, out), 30)
	require.Nil(t, out["resumo"].(map[string]any)["dia_do_pico"], "sem falha: pico null, não \"\"")
	require.EqualValues(t, 0, out["resumo"].(map[string]any)["dias_com_falha"])
	// Futuro é cortado em hoje, como o handler.
	out = chamarTool(t, cfg, "falhas_por_dia", map[string]any{"de": hoje, "ate": em(5)})
	require.Equal(t, hoje, out["ate"])
	require.Contains(t, chamarToolErro(t, cfg, "falhas_por_dia", map[string]any{"de": em(-91)}), "90 dias")
	require.Contains(t, chamarToolErro(t, cfg, "falhas_por_dia", map[string]any{"de": hoje, "ate": em(-1)}), "[entrada_invalida]")
	require.Contains(t, chamarToolErro(t, cfg, "falhas_por_dia", map[string]any{"de": em(2)}), "[entrada_invalida]")
	require.Contains(t, chamarToolErro(t, cfg, "falhas_por_dia", map[string]any{"min_queda_seg": 5}), "[entrada_invalida]")
}

// O handler do digest não aceita data: lê "ontem" no fuso do processo
// (yesterdayLocal), e a tool lê ontem em America/Sao_Paulo. Para a paridade
// valer em qualquer fuso e hora, o relógio da tool (L.Agora) é posto no meio-dia
// SP do dia seguinte ao "ontem" do handler: os dois leem o MESMO dia. A campanha
// cobre anteontem, ontem e hoje, então esse dia sempre tem falha.
func TestResumoDiarioFalhas_ParidadeComATela(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	e1 := mcptest.Emissora(t, ctx, pool, "Rádio Um", 1000)
	e2 := mcptest.Emissora(t, ctx, pool, "Rádio Dois", 1000)
	tipo := mcptest.TipoMaterial(t, ctx, pool, "Spot")
	ini, fim := time.Now().AddDate(0, 0, -2).Format("2006-01-02"), time.Now().Format("2006-01-02")
	camp := mcptest.Campanha(t, ctx, pool, cli, "Camp", ini, fim, e1, e2)
	mcptest.EstadoCampanha(t, ctx, pool, camp, "ativa")
	mcptest.Regra(t, ctx, pool, camp, tipo, e1, ini, fim, 127, "06:00", "22:00", 2)
	mcptest.Regra(t, ctx, pool, camp, tipo, e2, ini, fim, 127, "06:00", "22:00", 2)

	h := &handlers.DailyFailuresDigestHandler{Campaigns: catalog.NewCampaignFailures(pool), Seen: catalog.NewNotifications(pool)}
	var tela map[string]any
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.Get, "/x", nil, admin), &tela))

	ontemHandler := time.Now().AddDate(0, 0, -1) // o mesmo cálculo do yesterdayLocal
	L := NovaLeitura(pool)
	L.Agora = func() time.Time {
		return time.Date(ontemHandler.Year(), ontemHandler.Month(), ontemHandler.Day()+1, 12, 0, 0, 0, fusoSP)
	}
	out := chamarTool(t, ConfigServidor{Versao: "teste", Portal: "https://portal.teste", Leitura: L}, "resumo_diario_falhas", map[string]any{})
	require.Equal(t, ontemHandler.Format("2006-01-02"), tela["date"], "o handler leu o ontem do processo")
	require.Equal(t, tela["date"], out["data"])
	require.EqualValues(t, tela["summary"].(map[string]any)["campaigns"], out["campanhas_com_falha"])
	require.EqualValues(t, 1, out["campanhas_com_falha"])
	require.EqualValues(t, tela["summary"].(map[string]any)["stations"], out["emissoras_com_falha"])
	require.EqualValues(t, 2, out["emissoras_com_falha"])
	c := itens(t, out)[0]
	require.Equal(t, camp.String(), c["campanha"].(map[string]any)["id"])
	require.EqualValues(t, tela["campaigns"].([]any)[0].(map[string]any)["stations_failed"], c["emissoras_com_falha"])
	require.NotContains(t, out, "seen", "estado de UI por pessoa não sai")
	require.Contains(t, out["links"].(map[string]any)["tela"], "view=by_campaign")

	// Não marca visto: a chave do digest continua não lida para o admin.
	var lidas int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM notification_reads WHERE user_id = $1`, admin).Scan(&lidas))
	require.Zero(t, lidas)
}

// Empates (mesmo tempo fora do ar, mesmo nome) saem na ordem do id: o MCP
// pagina estas listas, e sem desempate a ordem podia trocar entre duas chamadas.
func TestFalhasEmissoras_EmpateDesempataPorID(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	tipo := mcptest.TipoMaterial(t, ctx, pool, "Spot")
	d := time.Now().AddDate(0, 0, -2).Format("2006-01-02")
	var ests []uuid.UUID
	for range 5 {
		ests = append(ests, mcptest.Emissora(t, ctx, pool, "Rádio Igual", 1000))
	}
	camp := mcptest.Campanha(t, ctx, pool, cli, "Camp", d, d, ests...)
	mcptest.EstadoCampanha(t, ctx, pool, camp, "ativa")
	for _, e := range ests {
		mcptest.Regra(t, ctx, pool, camp, tipo, e, d, d, 127, "06:00", "22:00", 2)
	}
	out := chamarTool(t, cfgTeste(pool), "falhas_emissoras", map[string]any{"data": d, "limit": 5})
	got := ids(t, itens(t, out), "emissora")
	require.True(t, slices.IsSortedFunc(got, strings.Compare), "empate desempata pelo id: %v", got)
}

func TestFalhasCampanhas_ParidadeComATela(t *testing.T) {
	_, pool, admin, camp, _, _, d := cenarioFalha(t)
	h := &handlers.CampaignFailuresHandler{Repo: catalog.NewCampaignFailures(pool)}
	var tela catalog.DailyResult
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.GetList, "/x?date="+d, nil, admin), &tela))

	out := chamarTool(t, cfgTeste(pool), "falhas_campanhas", map[string]any{"data": d})
	require.Equal(t, d, out["data"])
	r := out["resumo"].(map[string]any)
	require.EqualValues(t, tela.Summary.Campaigns, r["campanhas"])
	require.EqualValues(t, tela.Summary.Stations, r["emissoras"])
	require.EqualValues(t, tela.Summary.TotalDeficit, r["deficit"])
	require.EqualValues(t, tela.Summary.TotalDeficitAbsent, r["deficit_nao_tocou"])
	require.EqualValues(t, tela.Summary.TotalDeficitOffSlot, r["deficit_fora_do_horario"])
	require.EqualValues(t, []any{4.0, 3.0, 1.0}, []any{r["deficit"], r["deficit_nao_tocou"], r["deficit_fora_do_horario"]},
		"Rádio Um: 1 fora do horário + 1 não tocou; Rádio Dois: 2 não tocou")
	c := itens(t, out)[0]
	require.Equal(t, camp.String(), c["campanha"].(map[string]any)["id"])
	require.Equal(t, "ativa", c["estado"])
	require.EqualValues(t, len(tela.Campaigns[0].Stations), c["emissoras_total"])
	for i, s := range tela.Campaigns[0].Stations {
		e := c["emissoras"].([]any)[i].(map[string]any)
		require.Equal(t, s.Station.ID.String(), e["emissora"].(map[string]any)["id"], "ordem da tela")
		require.EqualValues(t, s.Programmed, e["programado"])
		require.EqualValues(t, s.Identified, e["identificado"])
		require.EqualValues(t, s.Deficit, e["deficit"])
		require.EqualValues(t, s.DeficitAbsent, e["deficit_nao_tocou"])
		require.EqualValues(t, s.DeficitOffSlot, e["deficit_fora_do_horario"])
		require.EqualValues(t, s.Extras, e["extras"])
		require.EqualValues(t, s.IsBonified, e["bonificada"])
		require.NotContains(t, e, "dias_de_falha", "o modo dia não traz os dias")
	}
	require.Contains(t, out["links"].(map[string]any)["tela"], "view=by_campaign")

	var hist catalog.HistoricalResult
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.GetList, "/x?mode=historical&page=1&page_size=50", nil, admin), &hist))
	outH := chamarTool(t, cfgTeste(pool), "falhas_campanhas", map[string]any{"modo": "historico"})
	require.EqualValues(t, hist.Total, pag(t, outH)["total"])
	require.EqualValues(t, 1, pag(t, outH)["total"])
	h0 := itens(t, outH)[0]
	require.Equal(t, hist.Campaigns[0].Campaign.ID.String(), h0["campanha"].(map[string]any)["id"])
	require.EqualValues(t, hist.Campaigns[0].StationsWithFailure, h0["emissoras_com_falha"])
	require.EqualValues(t, hist.Campaigns[0].TotalFailureDays, h0["dias_de_falha"])
	require.EqualValues(t, hist.Campaigns[0].TotalDeficit, h0["deficit"])
	require.EqualValues(t, hist.Campaigns[0].TotalDeficitAbsent, h0["deficit_nao_tocou"])
	require.EqualValues(t, 3, h0["deficit_nao_tocou"])
	require.EqualValues(t, hist.Campaigns[0].TotalDeficitOffSlot, h0["deficit_fora_do_horario"])
	require.EqualValues(t, hist.Campaigns[0].IsFullyBonified, h0["totalmente_bonificada"])
	require.Contains(t, outH["links"].(map[string]any)["tela"], "sub=historical")

	cfg := cfgTeste(pool)
	require.Contains(t, chamarToolErro(t, cfg, "falhas_campanhas", map[string]any{"modo": "historico", "data": d}), "[entrada_invalida]")
	require.Contains(t, chamarToolErro(t, cfg, "falhas_campanhas", map[string]any{"modo": "semana"}), "[entrada_invalida]")
	require.Contains(t, chamarToolErro(t, cfg, "falhas_campanhas", map[string]any{"data": time.Now().AddDate(0, 0, 1).Format("2006-01-02")}), "futuro")
}

func TestObterFalhasCampanha_ParidadeComATela(t *testing.T) {
	_, pool, admin, camp, e1, e2, d := cenarioFalha(t)
	h := &handlers.CampaignFailuresHandler{Repo: catalog.NewCampaignFailures(pool)}
	var tela catalog.DetailResult
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.GetByID, "/x", map[string]string{"id": camp.String()}, admin), &tela))

	out := chamarTool(t, cfgTeste(pool), "obter_falhas_campanha", map[string]any{"campanha_id": camp.String()})
	r := out["resumo"].(map[string]any)
	require.EqualValues(t, tela.Summary.StationsWithFailure, r["emissoras"])
	require.EqualValues(t, tela.Summary.TotalDeficit, r["deficit"])
	require.EqualValues(t, tela.Summary.TotalDeficitAbsent, r["deficit_nao_tocou"])
	require.EqualValues(t, tela.Summary.TotalDeficitOffSlot, r["deficit_fora_do_horario"])
	require.EqualValues(t, tela.Summary.TotalFailureDays, r["dias_de_falha"])
	require.EqualValues(t, 2, r["dias_de_falha"])
	xs := itens(t, out)
	require.Len(t, xs, len(tela.Stations))
	for i, s := range tela.Stations {
		require.Equal(t, s.Station.ID.String(), xs[i]["emissora"].(map[string]any)["id"], "ordem da tela")
		require.EqualValues(t, s.Deficit, xs[i]["deficit"])
		require.EqualValues(t, s.DeficitAbsent, xs[i]["deficit_nao_tocou"])
		require.EqualValues(t, s.DeficitOffSlot, xs[i]["deficit_fora_do_horario"])
		require.EqualValues(t, s.Extras, xs[i]["extras"])
		require.EqualValues(t, len(s.FailureDays), xs[i]["dias_de_falha_total"])
	}
	porID := map[string]map[string]any{}
	for _, x := range xs {
		porID[x["emissora"].(map[string]any)["id"].(string)] = x
	}
	require.EqualValues(t, 1, porID[e1.String()]["deficit_fora_do_horario"], "tocou 1 vez fora da faixa")
	require.EqualValues(t, 1, porID[e1.String()]["deficit_nao_tocou"])
	require.EqualValues(t, 1, porID[e1.String()]["extras"], "o out_slot conta como extra")
	require.EqualValues(t, 2, porID[e2.String()]["deficit_nao_tocou"])
	require.Equal(t, []any{d}, porID[e1.String()]["dias_de_falha"])
	require.Equal(t, "ativa", out["campanha"].(map[string]any)["estado"])
	require.Equal(t, d, out["campanha"].(map[string]any)["inicio"])
	require.Equal(t, "Cliente", out["cliente"])
	require.Equal(t, "https://portal.teste/admin/station-failures?campaign="+camp.String()+"&view=by_campaign", out["links"].(map[string]any)["tela"])
	require.Contains(t, out["links"].(map[string]any)["como_baixar"], "PDF de cobrança")

	cfg := cfgTeste(pool)
	require.Contains(t, chamarToolErro(t, cfg, "obter_falhas_campanha", map[string]any{"campanha_id": uuid.NewString()}), "[nao_encontrado]")
	require.Contains(t, chamarToolErro(t, cfg, "obter_falhas_campanha", map[string]any{"campanha_id": "x"}), "[entrada_invalida]")
	ctx := context.Background()
	mcptest.EstadoCampanha(t, ctx, pool, camp, "cancelada")
	require.Contains(t, chamarToolErro(t, cfg, "obter_falhas_campanha", map[string]any{"campanha_id": camp.String()}), "cancelada")
}

// Pior caso de uma página do modo dia: o teto de campanhas × tetoPorItem
// emissoras, com nomes longos (alvo < 60 KB).
func TestFalhasCampanhas_PiorCasoCabe(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	cli := mcptest.Cliente(t, ctx, pool, "Agência De Publicidade E Propaganda Do Interior Ltda")
	tipo := mcptest.TipoMaterial(t, ctx, pool, "Spot")
	d := time.Now().AddDate(0, 0, -2).Format("2006-01-02")
	ests := emissorasLongas(t, ctx, pool, tetoPorItem+1)
	regras := catalog.NewDistributionRules(pool)
	for j := range tetoPaginaFalhasDia + 1 {
		camp := mcptest.Campanha(t, ctx, pool, cli, fmt.Sprintf("Campanha De Verão Institucional Com Nome Bem Comprido %02d", j), d, d, ests...)
		mcptest.EstadoCampanha(t, ctx, pool, camp, "ativa")
		_, err := regras.Create(ctx, catalog.CreateDistributionRuleInput{
			CampaignID: camp, TypeID: tipo, StationIDs: ests, MaterialIDs: []uuid.UUID{},
			Name: "Regra", StartDate: mcptest.Dia(d), EndDate: mcptest.Dia(d), WeekdayMask: 127,
			TimeStart: "06:00", TimeEnd: "22:00", PlaysPerDay: 12,
		})
		require.NoError(t, err)
	}

	out := chamarTool(t, cfgTeste(pool), "falhas_campanhas", map[string]any{"data": d, "limit": 100})
	require.Len(t, itens(t, out), tetoPaginaFalhasDia)
	require.EqualValues(t, tetoPaginaFalhasDia, pag(t, out)["limit"])
	require.EqualValues(t, tetoPaginaFalhasDia+1, pag(t, out)["total"])
	x := itens(t, out)[0]
	require.Len(t, x["emissoras"], tetoPorItem)
	require.EqualValues(t, tetoPorItem+1, x["emissoras_total"])
	require.Contains(t, out["ressalvas"].([]any)[1], fmt.Sprintf("as %d primeiras", tetoPorItem))
	n := tamanhoDoResultado(t, out)
	t.Logf("falhas_campanhas (dia): %d campanhas × %d emissoras = %d bytes", tetoPaginaFalhasDia, tetoPorItem, n)
	require.Less(t, n, 60_000)

}

// Pior caso de uma página do modo histórico: a página máxima (100 campanhas)
// com nomes longos (alvo < 60 KB).
func TestFalhasCampanhas_HistoricoPiorCasoCabe(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	cli := mcptest.Cliente(t, ctx, pool, "Agência De Publicidade E Propaganda Do Interior Ltda")
	tipo := mcptest.TipoMaterial(t, ctx, pool, "Spot")
	d := time.Now().AddDate(0, 0, -2).Format("2006-01-02")
	est := emissorasLongas(t, ctx, pool, 1)[0]
	for j := range limiteTeto + 1 {
		camp := mcptest.Campanha(t, ctx, pool, cli, fmt.Sprintf("Campanha De Verão Institucional Com Nome Bem Comprido %03d", j), d, d, est)
		mcptest.EstadoCampanha(t, ctx, pool, camp, "ativa")
		mcptest.Regra(t, ctx, pool, camp, tipo, est, d, d, 127, "06:00", "22:00", 12)
	}
	out := chamarTool(t, cfgTeste(pool), "falhas_campanhas", map[string]any{"modo": "historico", "limit": 100})
	require.Len(t, itens(t, out), limiteTeto)
	require.EqualValues(t, limiteTeto+1, pag(t, out)["total"])
	n := tamanhoDoResultado(t, out)
	t.Logf("falhas_campanhas (historico): %d campanhas = %d bytes", limiteTeto, n)
	require.Less(t, n, 60_000)
}

// Pior caso de uma página do detalhe: o teto de emissoras × tetoPorItem dias
// de falha. Passa de 50 emissoras (o guard do próximo teste recusaria):
// o Cleanup as apaga.
func TestObterFalhasCampanha_PiorCasoCabe(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `TRUNCATE stations, clients RESTART IDENTITY CASCADE`)
	})
	cli := mcptest.Cliente(t, ctx, pool, "Agência De Publicidade E Propaganda Do Interior Ltda")
	tipo := mcptest.TipoMaterial(t, ctx, pool, "Spot")
	ini := time.Now().AddDate(0, 0, -(tetoPorItem + 3)).Format("2006-01-02")
	fim := time.Now().AddDate(0, 0, -2).Format("2006-01-02")
	ests := emissorasLongas(t, ctx, pool, tetoPaginaFalhasCampanha+1)
	camp := mcptest.Campanha(t, ctx, pool, cli, "Campanha De Verão Institucional Com Nome Bem Comprido", ini, fim, ests...)
	mcptest.EstadoCampanha(t, ctx, pool, camp, "ativa")
	_, err := catalog.NewDistributionRules(pool).Create(ctx, catalog.CreateDistributionRuleInput{
		CampaignID: camp, TypeID: tipo, StationIDs: ests, MaterialIDs: []uuid.UUID{},
		Name: "Regra", StartDate: mcptest.Dia(ini), EndDate: mcptest.Dia(fim), WeekdayMask: 127,
		TimeStart: "06:00", TimeEnd: "22:00", PlaysPerDay: 12,
	})
	require.NoError(t, err)

	out := chamarTool(t, cfgTeste(pool), "obter_falhas_campanha", map[string]any{"campanha_id": camp.String(), "limit": 100})
	require.Len(t, itens(t, out), tetoPaginaFalhasCampanha)
	require.EqualValues(t, tetoPaginaFalhasCampanha, pag(t, out)["limit"])
	require.EqualValues(t, tetoPaginaFalhasCampanha+1, pag(t, out)["total"])
	x := itens(t, out)[0]
	require.Len(t, x["dias_de_falha"], tetoPorItem)
	require.EqualValues(t, tetoPorItem+2, x["dias_de_falha_total"])
	n := tamanhoDoResultado(t, out)
	t.Logf("obter_falhas_campanha: %d emissoras × %d dias = %d bytes", tetoPaginaFalhasCampanha, tetoPorItem, n)
	require.Less(t, n, 60_000)
}

// emissorasLongas cria n emissoras com nome, dial e cidade longos (pior caso de bytes).
func emissorasLongas(t *testing.T, ctx context.Context, pool *pgxpool.Pool, n int) []uuid.UUID {
	t.Helper()
	var ests []uuid.UUID
	for i := range n {
		e := mcptest.Emissora(t, ctx, pool, fmt.Sprintf("Rádio Comunitária E Educativa Do Interior Paulista %02d FM", i), 1000)
		_, err := pool.Exec(ctx, `UPDATE stations SET frequency_mhz = 102.7, city = 'São José Do Rio Preto Da Serra' WHERE id = $1`, e)
		require.NoError(t, err)
		ests = append(ests, e)
	}
	return ests
}

// Empates no histórico (mesmas contagens, mesmo nome) e no detalhe (mesmos
// dias de falha, mesmo nome) saem na ordem do id: o MCP pagina as duas listas.
func TestFalhasCampanhas_EmpateDesempataPorID(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	tipo := mcptest.TipoMaterial(t, ctx, pool, "Spot")
	d := time.Now().AddDate(0, 0, -2).Format("2006-01-02")
	var ests []uuid.UUID
	for range 5 {
		ests = append(ests, mcptest.Emissora(t, ctx, pool, "Rádio Igual", 1000))
	}
	var camps []uuid.UUID
	for i := range 5 {
		es := ests[i : i+1]
		if i == 0 {
			es = ests // a primeira campanha tem as 5 emissoras empatadas no detalhe
		}
		c := mcptest.Campanha(t, ctx, pool, cli, "Camp Igual", d, d, es...)
		mcptest.EstadoCampanha(t, ctx, pool, c, "ativa")
		for _, e := range es {
			mcptest.Regra(t, ctx, pool, c, tipo, e, d, d, 127, "06:00", "22:00", 2)
		}
		camps = append(camps, c)
	}
	cfg := cfgTeste(pool)
	det := chamarTool(t, cfg, "obter_falhas_campanha", map[string]any{"campanha_id": camps[0].String()})
	got := ids(t, itens(t, det), "emissora")
	require.Len(t, got, 5)
	require.True(t, slices.IsSortedFunc(got, strings.Compare), "detalhe: empate desempata pelo id: %v", got)

	dia := chamarTool(t, cfg, "falhas_campanhas", map[string]any{"data": d})
	var noDia []string
	for _, e := range itens(t, dia)[0]["emissoras"].([]any) {
		noDia = append(noDia, e.(map[string]any)["emissora"].(map[string]any)["id"].(string))
	}
	require.Len(t, noDia, 5)
	require.True(t, slices.IsSortedFunc(noDia, strings.Compare), "modo dia: empate desempata pelo id: %v", noDia)

	// As 4 campanhas de 1 emissora empatam entre si no histórico.
	hist := itens(t, chamarTool(t, cfg, "falhas_campanhas", map[string]any{"modo": "historico"}))
	require.Len(t, hist, 5)
	got = ids(t, hist[1:], "campanha")
	require.True(t, slices.IsSortedFunc(got, strings.Compare), "histórico: empate desempata pelo id: %v", got)
	// E a paginação por offset não repete nem pula ninguém.
	var paginado []string
	for off := 0; off < 5; off += 2 {
		p := chamarTool(t, cfg, "falhas_campanhas", map[string]any{"modo": "historico", "limit": 2, "offset": off})
		paginado = append(paginado, ids(t, itens(t, p), "campanha")...)
	}
	require.Equal(t, ids(t, hist, "campanha"), paginado)
}
