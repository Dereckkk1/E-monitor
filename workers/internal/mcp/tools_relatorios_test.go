package mcp

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/url"
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

// periodoRFC: o recorte de [de 00:00, ate 23:59:59.999] em SP, como a tela manda.
func periodoRFC(de, ate string) string {
	ini, _ := limitesDoDiaSP(mcptest.Dia(de))
	_, fim := limitesDoDiaSP(mcptest.Dia(ate))
	return "from=" + url.QueryEscape(ini.Format(time.RFC3339Nano)) + "&to=" + url.QueryEscape(fim.Format(time.RFC3339Nano))
}

func TestRelatorioDataHora_ParidadeComATela(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	est := mcptest.Emissora(t, ctx, pool, "Rádio", 1000)
	semPreco := mcptest.Emissora(t, ctx, pool, "Rádio Sem Preço", 0)
	tipo := mcptest.TipoMaterial(t, ctx, pool, "Spot")
	mat := mcptest.Material(t, ctx, pool, cli, tipo, "Spot A")
	camp := mcptest.Campanha(t, ctx, pool, cli, "Camp", "2026-10-01", "2026-10-31", est, semPreco)
	mcptest.Preco(t, ctx, pool, camp, est, tipo, 150)
	mcptest.PMMTarget(t, ctx, pool, cli, est, 250)
	mcptest.Veiculacao(t, ctx, pool, camp, mat, est, "in_slot", mcptest.Quando("2026-10-10", "08:00"))
	mcptest.Veiculacao(t, ctx, pool, camp, mat, est, "out_slot", mcptest.Quando("2026-10-11", "23:00"))
	mcptest.Veiculacao(t, ctx, pool, camp, mat, semPreco, "bonus", mcptest.Quando("2026-10-12", "08:00"))
	mcptest.Veiculacao(t, ctx, pool, camp, mat, est, "in_slot", mcptest.Quando("2026-11-01", "08:00")) // fora do período

	h := &handlers.DetectionsHandler{Repo: catalog.NewDetections(pool)}
	var tela catalog.ListPagedResult
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.List, "/detections?page=1&page_size=100&sort=detected_at_asc&campaigns="+camp.String()+
		"&"+periodoRFC("2026-10-01", "2026-10-31"), nil, admin), &tela))
	require.Equal(t, 3, tela.Total)

	out := chamarTool(t, cfgTeste(pool), "relatorio_data_hora", map[string]any{
		"cliente_id": cli.String(), "campanha_ids": []string{camp.String()}, "de": "2026-10-01", "ate": "2026-10-31", "mais_antigas_primeiro": true})
	require.EqualValues(t, tela.Total, pag(t, out)["total"])
	xs := itens(t, out)
	require.Len(t, xs, len(tela.Data))
	for i, d := range tela.Data {
		require.Equal(t, d.ID.String(), xs[i]["id"])
		require.Equal(t, d.Category, xs[i]["categoria"])
	}
	require.EqualValues(t, *tela.Data[0].StationPMM, xs[0]["pmm"])
	require.EqualValues(t, *tela.Data[0].StationPMMTarget, xs[0]["pmm_target"])
	require.EqualValues(t, 150, xs[0]["valor_unitario_tabela"])
	require.Equal(t, "por_insercao", xs[0]["modo_preco"])
	require.EqualValues(t, 150, xs[1]["valor_unitario_tabela"], "a tela mostra o valor de tabela em qualquer categoria")
	require.Nil(t, xs[2]["valor_unitario_tabela"], "emissora sem linha de preço")
	require.Nil(t, xs[2]["modo_preco"], "sem preço = null (o 'none' da tela)")
	require.Nil(t, xs[2]["pmm_target"])
	tela0 := out["links"].(map[string]any)["tela"].(string)
	require.Contains(t, tela0, "/reports/airtime?")
	require.Contains(t, tela0, "competence=2026-10")
	require.Contains(t, tela0, "from=2026-10-01")
	require.Contains(t, tela0, "to=2026-10-31")
	require.Contains(t, tela0, "client_id="+cli.String())
}

func TestRelatorioDataHora_UmClienteEPeriodoCompleto(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	a := mcptest.Cliente(t, ctx, pool, "A")
	b := mcptest.Cliente(t, ctx, pool, "B")
	campA := mcptest.Campanha(t, ctx, pool, a, "Camp A", "2026-10-01", "2026-10-31")
	campB := mcptest.Campanha(t, ctx, pool, b, "Camp B", "2026-10-01", "2026-10-31")
	cfg := cfgTeste(pool)
	require.Contains(t, chamarToolErro(t, cfg, "relatorio_data_hora", map[string]any{
		"cliente_id": a.String(), "campanha_ids": []string{campA.String(), campB.String()}, "de": "2026-10-01", "ate": "2026-10-31"}), "Camp B")
	// de e ate são obrigatórios no esquema (o SDK recusa antes da tool) e, vazios, na tool.
	require.Contains(t, chamarToolErro(t, cfg, "relatorio_data_hora", map[string]any{
		"cliente_id": a.String(), "campanha_ids": []string{campA.String()}, "de": "2026-10-01"}), "ate")
	require.Contains(t, chamarToolErro(t, cfg, "relatorio_data_hora", map[string]any{
		"cliente_id": a.String(), "campanha_ids": []string{campA.String()}, "de": "2026-10-01", "ate": ""}), "[entrada_invalida]")
	require.Contains(t, chamarToolErro(t, cfg, "relatorio_data_hora", map[string]any{
		"cliente_id": a.String(), "campanha_ids": []string{campA.String()}, "de": "2026-10-31", "ate": "2026-10-01"}), "[entrada_invalida]")
}

func cenarioRelatorio(t *testing.T) (ctx context.Context, pool *pgxpool.Pool, admin, cli, camp uuid.UUID) {
	t.Helper()
	ctx, pool = mcptest.DBCatalogo(t)
	admin = mcptest.Usuario(t, ctx, pool, "admin", true)
	cli = mcptest.Cliente(t, ctx, pool, "Cliente")
	e1 := mcptest.Emissora(t, ctx, pool, "Rádio Um", 1000.4)
	e2 := mcptest.Emissora(t, ctx, pool, "Rádio Dois", 500)
	tipo := mcptest.TipoMaterial(t, ctx, pool, "Spot")
	m1 := mcptest.Material(t, ctx, pool, cli, tipo, "Spot A")
	m2 := mcptest.Material(t, ctx, pool, cli, tipo, "Spot B")
	camp = mcptest.Campanha(t, ctx, pool, cli, "Camp", "2026-10-01", "2026-10-31", e1, e2)
	mcptest.PMMTarget(t, ctx, pool, cli, e1, 300)
	mcptest.Preco(t, ctx, pool, camp, e1, tipo, 100)
	mcptest.Veiculacao(t, ctx, pool, camp, m1, e1, "in_slot", mcptest.Quando("2026-10-10", "08:00"))
	mcptest.Veiculacao(t, ctx, pool, camp, m1, e1, "bonus", mcptest.Quando("2026-10-10", "09:00"))
	mcptest.Veiculacao(t, ctx, pool, camp, m2, e1, "out_slot", mcptest.Quando("2026-10-10", "23:00"))
	mcptest.Veiculacao(t, ctx, pool, camp, m2, e2, "in_slot", mcptest.Quando("2026-10-11", "08:00"))
	// Fora da vigência (o período padrão das tools) e retratada: nenhuma entra.
	mcptest.Veiculacao(t, ctx, pool, camp, m1, e1, "out_date", mcptest.Quando("2026-11-02", "08:00"))
	ret := mcptest.Veiculacao(t, ctx, pool, camp, m1, e2, "in_slot", mcptest.Quando("2026-10-12", "08:00"))
	_, err := pool.Exec(ctx, `UPDATE detections SET retracted_at = now() WHERE id = $1`, ret)
	require.NoError(t, err)
	return
}

func TestResumoRelatorioCampanha_ParidadeComATela(t *testing.T) {
	_, pool, admin, cli, camp := cenarioRelatorio(t)
	h := &handlers.ReportsHandler{Detections: catalog.NewDetections(pool), CampaignRepo: catalog.NewCampaigns(pool), Pool: pool}
	var tela handlers.SummaryResponse
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.Summary, "/x?"+periodoRFC("2026-10-01", "2026-10-31"),
		map[string]string{"id": camp.String()}, admin), &tela))

	out := chamarTool(t, cfgTeste(pool), "resumo_relatorio_campanha", map[string]any{"campanha_id": camp.String()})
	tot := out["totais"].(map[string]any)
	require.EqualValues(t, tela.Totals.Detections, tot["veiculacoes"])
	require.EqualValues(t, 4, tot["veiculacoes"])
	require.EqualValues(t, tela.Totals.DistinctMaterials, tot["materiais"])
	require.EqualValues(t, tela.Totals.DistinctStations, tot["emissoras"])
	require.EqualValues(t, tela.Totals.Impactos, tot["impactos"])
	require.EqualValues(t, tela.Totals.ImpactosTarget, tot["impactos_target"])
	require.EqualValues(t, tela.Totals.StationsWithTarget, tot["emissoras_com_target"])
	require.EqualValues(t, 2501, tot["impactos"], "round(1000,4 × 2) + 500 × 1")
	require.EqualValues(t, 600, tot["impactos_target"], "300 × 2; a Rádio Dois não tem target")
	require.Len(t, out["por_material"], len(tela.ByMaterial))
	require.EqualValues(t, len(tela.ByMaterial), out["por_material_total"])
	require.EqualValues(t, len(tela.ByStation), pag(t, out)["total"])
	xs := itens(t, out)
	for i, s := range tela.ByStation {
		require.Equal(t, s.StationID.String(), xs[i]["emissora"].(map[string]any)["id"])
		require.EqualValues(t, s.Count, xs[i]["veiculacoes"])
		require.EqualValues(t, s.ImpactCount, xs[i]["base_de_impactos"])
	}
	require.EqualValues(t, 2001, xs[0]["impactos"], "round(1000,4 × 2)")
	require.Equal(t, map[string]any{"de": "2026-10-01", "ate": "2026-10-31"}, out["periodo"])
	require.Equal(t, cli.String(), out["cliente"].(map[string]any)["id"])
	l := out["links"].(map[string]any)
	require.Contains(t, l["tela"], "/reports/airtime?")
	require.Contains(t, l["como_baixar"], "PDF")
	_, temMatEst := out["by_material_station"]
	require.False(t, temMatEst, "o cruzamento material × emissora é o CSV consolidado, não sai aqui")
}

// linhasCSV lê um CSV do portal (BOM, separador ';'). Linhas em branco do
// rodapé somem na leitura (o csv.Reader as pula).
func linhasCSV(t *testing.T, corpo []byte) [][]string {
	t.Helper()
	r := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(corpo, []byte("\xef\xbb\xbf"))))
	r.Comma, r.FieldsPerRecord = ';', -1
	rs, err := r.ReadAll()
	require.NoError(t, err)
	return rs
}

func TestExportarConsolidado_LinhasBatemComOCSV(t *testing.T) {
	_, pool, admin, _, camp := cenarioRelatorio(t)
	h := &handlers.ReportsHandler{Detections: catalog.NewDetections(pool), CampaignRepo: catalog.NewCampaigns(pool), Pool: pool}
	rs := linhasCSV(t, chamarHandler(t, h.Consolidated, "/x?"+periodoRFC("2026-10-01", "2026-10-31"),
		map[string]string{"id": camp.String()}, admin))
	// O consolidado não tem rodapé: cabeçalho + uma linha por material × emissora.
	require.Equal(t, "ID Material", rs[0][0])
	for _, r := range rs[1:] {
		require.Len(t, r, len(rs[0]), "toda linha de dados tem as colunas do cabeçalho")
	}

	out := chamarTool(t, cfgTeste(pool), "exportar_consolidado", map[string]any{"campanha_id": camp.String()})
	require.EqualValues(t, len(rs)-1, out["linhas"])
	require.EqualValues(t, 3, out["linhas"], "A×Um, B×Um, B×Dois")
	require.Equal(t, map[string]any{"de": "2026-10-01", "ate": "2026-10-31"}, out["periodo"])
	l := out["links"].(map[string]any)
	require.Contains(t, l["tela"], "/reports/airtime?")
	require.Contains(t, l["como_baixar"], "CSV Consolidado")
	require.NotContains(t, l["tela"], ".csv", "nunca link direto de arquivo")

	// Período explícito encolhe o arquivo do mesmo jeito que a tela.
	rs10 := linhasCSV(t, chamarHandler(t, h.Consolidated, "/x?"+periodoRFC("2026-10-11", "2026-10-11"),
		map[string]string{"id": camp.String()}, admin))
	out10 := chamarTool(t, cfgTeste(pool), "exportar_consolidado", map[string]any{"campanha_id": camp.String(), "de": "2026-10-11", "ate": "2026-10-11"})
	require.EqualValues(t, len(rs10)-1, out10["linhas"])
	require.EqualValues(t, 1, out10["linhas"])
}

func TestExportarVeiculacoes_LinhasBatemComOCSV(t *testing.T) {
	_, pool, admin, _, camp := cenarioRelatorio(t)
	h := &handlers.DetectionsHandler{Repo: catalog.NewDetections(pool), CampaignRepo: catalog.NewCampaigns(pool)}
	rs := linhasCSV(t, chamarHandler(t, h.Export, "/x?campaign_id="+camp.String()+"&"+periodoRFC("2026-10-01", "2026-10-31"), nil, admin))
	// Layout (reportcsv.WriteDetailed): cabeçalho de 13 colunas, uma linha de 13
	// colunas por veiculação e o rodapé do fornecedor, cujas linhas têm no máximo
	// 2 campos (totais, resumo por UF e por comercial). Linhas de dados = as de
	// 13 campos depois do cabeçalho; o rodapé repete o total em "TOTAL DE VEICULAÇÕES".
	require.Equal(t, "Identificador", rs[0][0])
	dados, totalRodape := 0, ""
	for _, r := range rs[1:] {
		if len(r) == len(rs[0]) {
			dados++
		}
		if r[0] == "TOTAL DE VEICULAÇÕES" {
			totalRodape = r[1]
		}
	}

	out := chamarTool(t, cfgTeste(pool), "exportar_veiculacoes", map[string]any{"campanha_id": camp.String()})
	require.EqualValues(t, dados, out["linhas"])
	require.EqualValues(t, 4, out["linhas"], "sem a retratada e sem a de fora da vigência")
	require.Equal(t, "4", totalRodape)
	require.Equal(t, []any{}, out["ressalvas"], "ressalvas sempre presente, mesmo vazia")
	l := out["links"].(map[string]any)
	require.Contains(t, l["como_baixar"], "CSV Detalhado")
	require.NotContains(t, l["tela"], ".csv")
}

func TestRelatoriosCampanha_Recusas(t *testing.T) {
	_, pool, _, _, camp := cenarioRelatorio(t)
	cfg := cfgTeste(pool)
	for _, tool := range []string{"resumo_relatorio_campanha", "exportar_consolidado", "exportar_veiculacoes"} {
		require.Contains(t, chamarToolErro(t, cfg, tool, map[string]any{"campanha_id": "x"}), "[entrada_invalida]", tool)
		require.Contains(t, chamarToolErro(t, cfg, tool, map[string]any{"campanha_id": uuid.NewString()}), "[nao_encontrado]", tool)
		require.Contains(t, chamarToolErro(t, cfg, tool, map[string]any{"campanha_id": camp.String(), "de": "2026-10-20", "ate": "2026-10-10"}),
			"[entrada_invalida]", tool)
	}
}

func TestRelatorioDataHora_PaginasECancelada(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	est := mcptest.Emissora(t, ctx, pool, "Rádio", 1000)
	tipo := mcptest.TipoMaterial(t, ctx, pool, "Spot")
	mat := mcptest.Material(t, ctx, pool, cli, tipo, "Spot A")
	camp := mcptest.Campanha(t, ctx, pool, cli, "Camp", "2026-10-01", "2026-10-31", est)
	mcptest.EstadoCampanha(t, ctx, pool, camp, "cancelada")
	for h := 8; h < 13; h++ {
		mcptest.Veiculacao(t, ctx, pool, camp, mat, est, "in_slot", mcptest.Quando("2026-10-10", fmt.Sprintf("%02d:00", h)))
	}
	hd := &handlers.DetectionsHandler{Repo: catalog.NewDetections(pool)}
	var tela catalog.ListPagedResult
	require.NoError(t, json.Unmarshal(chamarHandler(t, hd.List, "/detections?page=1&page_size=100&campaigns="+camp.String()+
		"&"+periodoRFC("2026-10-01", "2026-10-31"), nil, admin), &tela))
	require.Equal(t, 5, tela.Total)

	cfg := cfgTeste(pool)
	var vistos []string
	for offset := 0; offset < 5; offset += 2 {
		out := chamarTool(t, cfg, "relatorio_data_hora", map[string]any{"cliente_id": cli.String(), "campanha_ids": []string{camp.String()},
			"de": "2026-10-01", "ate": "2026-10-31", "limit": 2, "offset": offset})
		p := pag(t, out)
		require.EqualValues(t, 5, p["total"])
		require.Equal(t, offset+2 < 5, p["has_more"])
		vistos = append(vistos, ids(t, itens(t, out), "id")...)
		require.Contains(t, out["ressalvas"], ressalvaCanceladaRelatorio, "cancelada: mantida e marcada")
	}
	esperados := make([]string, len(tela.Data))
	for i, d := range tela.Data {
		esperados[i] = d.ID.String()
	}
	require.Equal(t, esperados, vistos, "as páginas juntas são a lista da tela, sem repetir nem pular")
}

// tamanhoDoResultado: os bytes do JSON que o cliente MCP recebe.
func tamanhoDoResultado(t *testing.T, out map[string]any) int {
	t.Helper()
	b, err := json.Marshal(out)
	require.NoError(t, err)
	return len(b)
}

// Pior caso de uma página do relatorio_data_hora: limit=100 com nomes longos.
// O cliente MCP corta a saída em ~25k tokens; o alvo é ficar abaixo de ~75 KB.
func TestRelatorioDataHora_PiorCasoCabe(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente Com Um Nome Bem Comprido De Agência E Anunciante Ltda")
	tipo := mcptest.TipoMaterial(t, ctx, pool, "Spot Comercial De Trinta Segundos")
	mat := mcptest.Material(t, ctx, pool, cli, tipo, strings.Repeat("Material Com Título Longo ", 3))
	var ests []uuid.UUID
	for i := range 10 {
		e := mcptest.Emissora(t, ctx, pool, fmt.Sprintf("Rádio Comunitária E Educativa Do Interior Paulista %02d FM", i), 12345.67)
		_, err := pool.Exec(ctx, `UPDATE stations SET city = 'São José Do Rio Preto Da Serra', state = 'SP', frequency_mhz = 107.9 WHERE id = $1`, e)
		require.NoError(t, err)
		ests = append(ests, e)
	}
	camp := mcptest.Campanha(t, ctx, pool, cli, strings.Repeat("Campanha De Fim De Ano ", 3), "2026-10-01", "2026-10-31", ests...)
	for _, e := range ests {
		mcptest.Preco(t, ctx, pool, camp, e, tipo, 1234.56)
		mcptest.PMMTarget(t, ctx, pool, cli, e, 54321)
	}
	for i := range tetoPaginaDataHora + 5 {
		mcptest.Veiculacao(t, ctx, pool, camp, mat, ests[i%len(ests)], "in_slot",
			mcptest.Quando(fmt.Sprintf("2026-10-%02d", 1+i%28), fmt.Sprintf("%02d:%02d", 6+i%16, i%60)))
	}
	out := chamarTool(t, cfgTeste(pool), "relatorio_data_hora", map[string]any{"cliente_id": cli.String(), "campanha_ids": []string{camp.String()},
		"de": "2026-10-01", "ate": "2026-10-31", "limit": 100})
	require.Len(t, itens(t, out), tetoPaginaDataHora)
	require.EqualValues(t, tetoPaginaDataHora, pag(t, out)["limit"], "limit acima do teto é rebaixado e ecoado")
	require.Equal(t, true, pag(t, out)["has_more"])
	n := tamanhoDoResultado(t, out)
	t.Logf("relatorio_data_hora: %d linhas = %d bytes", tetoPaginaDataHora, n)
	require.Less(t, n, 75_000)
}

// por_material não é paginada: acima de 50 sai cortada, com o total real.
func TestResumoRelatorioCampanha_PorMaterialCortado(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	est := mcptest.Emissora(t, ctx, pool, "Rádio", 1000)
	tipo := mcptest.TipoMaterial(t, ctx, pool, "Spot")
	camp := mcptest.Campanha(t, ctx, pool, cli, "Camp", "2026-10-01", "2026-10-31", est)
	for i := range tetoMateriaisResumo + 1 {
		m := mcptest.Material(t, ctx, pool, cli, tipo, fmt.Sprintf("Spot %02d", i))
		mcptest.Veiculacao(t, ctx, pool, camp, m, est, "in_slot", mcptest.Quando("2026-10-10", fmt.Sprintf("%02d:%02d", 6+i/60, i%60)))
	}
	out := chamarTool(t, cfgTeste(pool), "resumo_relatorio_campanha", map[string]any{"campanha_id": camp.String()})
	require.Len(t, out["por_material"], tetoMateriaisResumo)
	require.EqualValues(t, tetoMateriaisResumo+1, out["por_material_total"])
	require.EqualValues(t, tetoMateriaisResumo+1, out["totais"].(map[string]any)["materiais"])
	require.Contains(t, out["ressalvas"], fmt.Sprintf("por_material cortado em %d de %d materiais (por_material_total traz o número real)",
		tetoMateriaisResumo, tetoMateriaisResumo+1))
}
