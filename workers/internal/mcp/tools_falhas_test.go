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

func TestFalhasPorDia_PadraoERecusas(t *testing.T) {
	_, pool := mcptest.DBCatalogo(t)
	cfg := cfgTeste(pool)
	hoje := time.Now().Format("2006-01-02")
	out := chamarTool(t, cfg, "falhas_por_dia", map[string]any{})
	require.Equal(t, time.Now().AddDate(0, 0, -29).Format("2006-01-02"), out["de"])
	require.Equal(t, hoje, out["ate"])
	require.Len(t, itens(t, out), 30)
	require.Nil(t, out["resumo"].(map[string]any)["dia_do_pico"], "sem falha: pico null, não \"\"")
	require.EqualValues(t, 0, out["resumo"].(map[string]any)["dias_com_falha"])
	// Futuro é cortado em hoje, como o handler.
	out = chamarTool(t, cfg, "falhas_por_dia", map[string]any{"de": hoje, "ate": time.Now().AddDate(0, 0, 5).Format("2006-01-02")})
	require.Equal(t, hoje, out["ate"])
	require.Contains(t, chamarToolErro(t, cfg, "falhas_por_dia", map[string]any{"de": time.Now().AddDate(0, 0, -91).Format("2006-01-02")}), "90 dias")
	require.Contains(t, chamarToolErro(t, cfg, "falhas_por_dia", map[string]any{"de": hoje, "ate": time.Now().AddDate(0, 0, -1).Format("2006-01-02")}), "[entrada_invalida]")
	require.Contains(t, chamarToolErro(t, cfg, "falhas_por_dia", map[string]any{"de": time.Now().AddDate(0, 0, 2).Format("2006-01-02")}), "[entrada_invalida]")
	require.Contains(t, chamarToolErro(t, cfg, "falhas_por_dia", map[string]any{"min_queda_seg": 5}), "[entrada_invalida]")
}

// O digest lê "ontem"; a campanha cobre anteontem, ontem e hoje para que o
// teste não dependa de qual "ontem" (processo × SP) prevalece — em qualquer um
// deles há 1 campanha com 2 emissoras falhando.
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

	out := chamarTool(t, cfgTeste(pool), "resumo_diario_falhas", map[string]any{})
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
