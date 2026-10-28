package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"radiocheck/internal/api/handlers"
	"radiocheck/internal/mcp/mcptest"
	"radiocheck/internal/postsale"
)

// textoTool chama a tool e devolve o texto cru (o JSON serializado que o
// modelo recebe), para provar o que NÃO pode sair nele.
func textoTool(t *testing.T, cfg ConfigServidor, nome string, args any) string {
	t.Helper()
	texto, erro := chamarNoServidor(t, novoServidor(cfg, principalTeste), nome, args)
	require.False(t, erro, texto)
	return texto
}

func handlerPosVenda(pool *pgxpool.Pool) *handlers.PostSaleHandler {
	return handlers.NewPostSaleHandler(postsale.New(postsale.Config{Repo: postsale.NewRepo(pool)}), zap.NewNop())
}

func TestPosVenda_ParidadeESemSegredos(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	est := mcptest.Emissora(t, ctx, pool, "Rádio X", 1000)
	camp := mcptest.Campanha(t, ctx, pool, cli, "Camp", "2026-09-01", "2026-09-30", est)
	payload := &postsale.Payload{Version: postsale.PayloadVersion, Title: "Fechamento", Campaigns: []postsale.CampaignBlock{{
		CampaignID: camp, Name: "Camp", PeriodFrom: "2026-09-01", PeriodTo: "2026-09-30",
		KPIs: postsale.BlockKPIs{ValorEntregue: 12000, Impactos: 300000, CPM: 40, Bonificacao: 500, BonificacaoCount: 4, StationsCount: 3},
		CheckingRows: []postsale.StationRow{{StationID: est, Name: "Rádio X", Programmed: 10, Identified: 8, Deficit: 2, Extras: 3,
			Compensated: true, Kind: postsale.KindCompensation, Note: "compensou no sábado"}},
		ConformingCount: 2,
	}}}
	enviado := mcptest.PosVenda(t, ctx, pool, cli, camp, "Fechamento", "2026-09-01", "2026-09-30", true, payload,
		postsale.RecipientInput{Email: "a@cliente.com", Name: "Ana"}, postsale.RecipientInput{Email: "b@cliente.com", Name: "Bia"})
	_ = mcptest.PosVenda(t, ctx, pool, cli, camp, "Rascunho", "2026-09-01", "2026-09-30", false, nil)
	_, err := pool.Exec(ctx, `UPDATE post_sale_reports SET attachments_url = 'https://drive.google.com/d/SEGREDO/view' WHERE id = $1`, enviado)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE post_sale_report_campaigns SET assets = '{"bundle_zip":"postsale/CHAVE-S3/a.zip","map_png":"postsale/CHAVE-S3/m.png"}' WHERE report_id = $1`, enviado)
	require.NoError(t, err)

	h := handlerPosVenda(pool)
	var tela postsale.ListPage
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.List, "/x?per_page=100", nil, admin), &tela))
	cfg := cfgTeste(pool)
	l := chamarTool(t, cfg, "listar_pos_vendas", map[string]any{})
	require.EqualValues(t, tela.Total, pag(t, l)["total"])
	require.EqualValues(t, 2, pag(t, l)["total"])
	cont := l["contagens"].(map[string]any)
	require.EqualValues(t, tela.Counts.All, cont["todos"])
	require.EqualValues(t, tela.Counts.Sent, cont["enviados"])
	require.EqualValues(t, tela.Counts.Draft, cont["rascunhos"])
	for i, it := range tela.Items {
		x := itens(t, l)[i]
		require.Equal(t, it.ID.String(), x["id"], "ordem da tela")
		require.EqualValues(t, it.Recipients, x["destinatarios"])
		require.EqualValues(t, it.Opened, x["abriram"])
		require.EqualValues(t, it.Campaigns, x["campanhas"])
	}
	for _, x := range itens(t, l) {
		require.Equal(t, "https://portal.teste/admin/pos-venda/"+x["id"].(string), x["tela"])
	}

	var rep postsale.Report
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.Get, "/x", map[string]string{"id": enviado.String()}, admin), &rep))
	require.Len(t, rep.Recipients, 2)
	textos := []string{
		textoTool(t, cfg, "obter_pos_venda", map[string]any{"pos_venda_id": enviado.String()}),
		textoTool(t, cfg, "listar_pos_vendas", map[string]any{}),
		textoTool(t, cfg, "destinatarios_pos_venda", map[string]any{"pos_venda_id": enviado.String()}),
	}
	for _, texto := range textos {
		for _, r := range rep.Recipients {
			require.NotEmpty(t, r.Token)
			require.NotContains(t, texto, r.Token, "o token é a credencial do link público")
		}
		require.NotContains(t, texto, `"token"`, "nenhum campo token")
		require.NotContains(t, texto, "portal.teste/pos-venda/", "o link público do cliente nunca sai")
		require.NotContains(t, texto, "SEGREDO", "o anexo sai só como domínio")
		require.NotContains(t, texto, "CHAVE-S3", "chave S3 nunca sai")
	}

	var o map[string]any
	require.NoError(t, json.Unmarshal([]byte(textos[0]), &o))
	require.Equal(t, "enviado", o["estado"])
	require.Equal(t, "https://drive.google.com/…", o["anexo"])
	require.EqualValues(t, 1, pag(t, o)["total"])
	bloco := itens(t, o)[0]
	require.Equal(t, camp.String(), bloco["campanha"].(map[string]any)["id"])
	num := bloco["numeros"].(map[string]any)
	require.EqualValues(t, 12000, num["valor_entregue"])
	require.EqualValues(t, 300000, num["impactos"])
	require.EqualValues(t, 40, num["cpm"])
	require.EqualValues(t, 500, num["bonificacao"])
	require.EqualValues(t, 4, num["bonificacao_insercoes"])
	require.Nil(t, num["impactos_target"], "sem emissora com target: null, não 0")
	require.EqualValues(t, 2, bloco["emissoras_conformes"])
	require.EqualValues(t, 1, bloco["checking_total"])
	ck := bloco["checking"].([]any)[0].(map[string]any)
	require.Equal(t, est.String(), ck["emissora"].(map[string]any)["id"])
	require.Equal(t, "compensation", ck["situacao"])
	require.Equal(t, true, ck["compensada"])
	require.Equal(t, "compensou no sábado", ck["nota"])
	dest := o["destinatarios"].(map[string]any)
	require.EqualValues(t, 2, dest["total"])
	require.EqualValues(t, 1, dest["abriram"])
	require.EqualValues(t, 0, dest["revogados"])
	require.Equal(t, "https://portal.teste/admin/pos-venda/"+enviado.String(), o["links"].(map[string]any)["tela"])

	rasc := itens(t, chamarTool(t, cfg, "listar_pos_vendas", map[string]any{"estado": "rascunho"}))
	require.Len(t, rasc, 1)
	or := chamarTool(t, cfg, "obter_pos_venda", map[string]any{"pos_venda_id": rasc[0]["id"]})
	b0 := itens(t, or)[0]
	require.Nil(t, b0["numeros"], "rascunho não tem números congelados")
	require.Nil(t, b0["checking"])
	require.Nil(t, b0["emissoras_conformes"])
	require.Equal(t, "rascunho", or["estado"])
	require.Contains(t, strings.Join(func() []string {
		var ss []string
		for _, r := range or["ressalvas"].([]any) {
			ss = append(ss, r.(string))
		}
		return ss
	}(), " "), "Rascunho")

	d := chamarTool(t, cfg, "destinatarios_pos_venda", map[string]any{"pos_venda_id": enviado.String()})
	ds := itens(t, d)
	require.Len(t, ds, len(rep.Recipients))
	for i, r := range rep.Recipients {
		require.Equal(t, r.Email, ds[i]["email"], "ordem da tela")
		require.Equal(t, r.EmailStatus, ds[i]["status_email"])
		require.EqualValues(t, r.OpenCount, ds[i]["aberturas"])
		require.NotContains(t, ds[i], "token")
	}
	require.NotNil(t, ds[0]["aberto_em"])
	require.Nil(t, ds[1]["aberto_em"])

	require.Contains(t, chamarToolErro(t, cfg, "destinatarios_pos_venda", map[string]any{}), "[entrada_invalida]")
	require.Contains(t, chamarToolErro(t, cfg, "destinatarios_pos_venda", map[string]any{"pos_venda_id": enviado.String(), "cliente_id": cli.String()}), "[entrada_invalida]")
	require.Contains(t, chamarToolErro(t, cfg, "destinatarios_pos_venda", map[string]any{"pos_venda_id": uuid.NewString()}), "[nao_encontrado]")
	require.Contains(t, chamarToolErro(t, cfg, "obter_pos_venda", map[string]any{"pos_venda_id": uuid.NewString()}), "[nao_encontrado]")
	require.Contains(t, chamarToolErro(t, cfg, "obter_pos_venda", map[string]any{"pos_venda_id": "x"}), "[entrada_invalida]")
	require.Contains(t, chamarToolErro(t, cfg, "listar_pos_vendas", map[string]any{"estado": "sent"}), "[entrada_invalida]")
	require.Contains(t, chamarToolErro(t, cfg, "listar_pos_vendas", map[string]any{"competencia": "2026-13"}), "[entrada_invalida]")

	// Filtros: competência e cliente, com paridade.
	var telaMes postsale.ListPage
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.List, "/x?per_page=100&month=2026-09&client_id="+cli.String(), nil, admin), &telaMes))
	lm := chamarTool(t, cfg, "listar_pos_vendas", map[string]any{"competencia": "2026-09", "cliente_id": cli.String()})
	require.EqualValues(t, telaMes.Total, pag(t, lm)["total"])
	require.EqualValues(t, 0, pag(t, chamarTool(t, cfg, "listar_pos_vendas", map[string]any{"competencia": "2026-11"}))["total"])
}

// Campanha cancelada depois do envio: o documento continua congelado, mas a
// ressalva de relatório diz que ela saiu da cobrança.
func TestObterPosVenda_CanceladaRessalva(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	camp := mcptest.Campanha(t, ctx, pool, cli, "Camp", "2026-09-01", "2026-09-30")
	id := mcptest.PosVenda(t, ctx, pool, cli, camp, "F", "2026-09-01", "2026-09-30", true,
		&postsale.Payload{Version: postsale.PayloadVersion, Campaigns: []postsale.CampaignBlock{{CampaignID: camp,
			KPIs: postsale.BlockKPIs{ValorEntregue: 10, StationsWithTarget: 2, ImpactosTarget: 900, CPMTarget: 11.1}}}})
	mcptest.EstadoCampanha(t, ctx, pool, camp, "cancelada")
	o := chamarTool(t, cfgTeste(pool), "obter_pos_venda", map[string]any{"pos_venda_id": id.String()})
	require.Contains(t, o["ressalvas"], ressalvaCanceladaRelatorio)
	b := itens(t, o)[0]
	require.Equal(t, "cancelada", b["estado"])
	require.EqualValues(t, 900, b["numeros"].(map[string]any)["impactos_target"], "com emissora no target o número sai")
	require.Equal(t, []any{}, b["checking"], "enviado sem linha fora do plano: lista vazia, não null")
	require.Nil(t, o["anexo"], "sem anexo: null")
}

func TestDestinatariosPosVenda_PreviaParidadeComATela(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	outro := mcptest.Cliente(t, ctx, pool, "Outro")
	for i, c := range []uuid.UUID{cli, cli, outro} {
		var u uuid.UUID
		require.NoError(t, pool.QueryRow(ctx, `INSERT INTO users (email, password_hash, role, client_id, name, is_active)
			VALUES ($1, 'h', 'viewer', $2, $3, true) RETURNING id`, fmt.Sprintf("v%d@cliente.teste", i), c, fmt.Sprintf("Viewer %d", i)).Scan(&u))
		_, err := pool.Exec(ctx, `INSERT INTO user_clients (user_id, client_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, u, c)
		require.NoError(t, err)
	}
	_, err := pool.Exec(ctx, `UPDATE users SET receive_post_sale_emails = TRUE WHERE id = $1`, admin)
	require.NoError(t, err)

	var tela struct {
		Client   []map[string]string `json:"client"`
		Internal []map[string]string `json:"internal"`
	}
	require.NoError(t, json.Unmarshal(chamarHandler(t, handlerPosVenda(pool).RecipientsPreview, "/x?client_id="+cli.String(), nil, admin), &tela))
	out := chamarTool(t, cfgTeste(pool), "destinatarios_pos_venda", map[string]any{"cliente_id": cli.String()})
	require.Len(t, out["cliente"], len(tela.Client))
	require.Len(t, out["cliente"], 2)
	require.EqualValues(t, 2, out["cliente_total"])
	for i, p := range tela.Client {
		require.Equal(t, p["email"], out["cliente"].([]any)[i].(map[string]any)["email"])
	}
	require.Len(t, out["internos"], len(tela.Internal))
	require.Len(t, out["internos"], 1)
	require.Contains(t, chamarToolErro(t, cfgTeste(pool), "destinatarios_pos_venda", map[string]any{"cliente_id": uuid.NewString()}), "[nao_encontrado]")
}

// Pior caso de obter_pos_venda: o teto de campanhas × tetoPorItem linhas de
// checking com nota longa (alvo < 60 KB).
func TestObterPosVenda_PiorCasoCabe(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	cli := mcptest.Cliente(t, ctx, pool, "Agência De Publicidade E Propaganda Do Interior Ltda")
	repo := postsale.NewRepo(pool)
	rep, err := repo.CreateDraft(ctx, postsale.CreateDraftInput{ClientID: cli, Title: "Fechamento De Setembro Com Título Bem Comprido"})
	require.NoError(t, err)
	nota := strings.Repeat("Compensou com inserções extras no fim de semana. ", 4)
	var blocos []postsale.BlockRow
	var cbs []postsale.CampaignBlock
	for i := range tetoBlocosPosVenda + 1 {
		camp := mcptest.Campanha(t, ctx, pool, cli, fmt.Sprintf("Campanha De Verão Institucional Com Nome Bem Comprido %02d", i), "2026-09-01", "2026-09-30")
		blocos = append(blocos, postsale.BlockRow{CampaignID: camp, From: mcptest.Dia("2026-09-01"), To: mcptest.Dia("2026-09-30"), Position: i})
		var rows []postsale.StationRow
		for j := range tetoPorItem + 1 {
			rows = append(rows, postsale.StationRow{StationID: uuid.New(), Name: fmt.Sprintf("Rádio Comunitária E Educativa Do Interior Paulista %02d FM", j),
				Programmed: 1234, Identified: 1200, Deficit: 34, Extras: 12, Kind: postsale.KindCompensation, Note: nota})
		}
		lbl := "Classes AB 25-49"
		cbs = append(cbs, postsale.CampaignBlock{CampaignID: camp, CheckingRows: rows, ConformingCount: 123,
			KPIs: postsale.BlockKPIs{ValorEntregue: 123456.78, Impactos: 12345678, CPM: 12.34, Bonificacao: 2345.67, BonificacaoCount: 123,
				StationsCount: 45, ImpactosTarget: 1234567, CPMTarget: 123.45, StationsWithTarget: 40, TargetLabel: &lbl}})
	}
	require.NoError(t, repo.ReplaceBlocks(ctx, rep.ID, blocos))
	b, err := json.Marshal(postsale.Payload{Version: postsale.PayloadVersion, Campaigns: cbs})
	require.NoError(t, err)
	require.NoError(t, repo.MarkSent(ctx, rep.ID, b))
	_, err = pool.Exec(ctx, `UPDATE post_sale_reports SET intro_message = $2 WHERE id = $1`, rep.ID, strings.Repeat("Olá! Segue o fechamento. ", 20))
	require.NoError(t, err)

	out := chamarTool(t, cfgTeste(pool), "obter_pos_venda", map[string]any{"pos_venda_id": rep.ID.String(), "limit": 100})
	require.Len(t, itens(t, out), tetoBlocosPosVenda)
	require.EqualValues(t, tetoBlocosPosVenda, pag(t, out)["limit"], "limit acima do teto é rebaixado e ecoado")
	require.EqualValues(t, tetoBlocosPosVenda+1, pag(t, out)["total"])
	require.Equal(t, true, pag(t, out)["has_more"])
	b0 := itens(t, out)[0]
	require.Len(t, b0["checking"], tetoPorItem)
	require.EqualValues(t, tetoPorItem+1, b0["checking_total"])
	require.Contains(t, out["ressalvas"], fmt.Sprintf("checking de cada campanha: as %d primeiras linhas (checking_total traz o real).", tetoPorItem))
	// A página seguinte traz a campanha que sobrou, na ordem da tela (position).
	p2 := chamarTool(t, cfgTeste(pool), "obter_pos_venda", map[string]any{"pos_venda_id": rep.ID.String(), "offset": tetoBlocosPosVenda})
	require.Equal(t, blocos[tetoBlocosPosVenda].CampaignID.String(), itens(t, p2)[0]["campanha"].(map[string]any)["id"])
	n := tamanhoDoResultado(t, out)
	t.Logf("obter_pos_venda: %d campanhas × %d linhas de checking = %d bytes", tetoBlocosPosVenda, tetoPorItem, n)
	require.Less(t, n, 60_000)
}

// Pior caso de listar_pos_vendas: uma página de 100 com nomes longos.
func TestListarPosVendas_PiorCasoCabe(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	cli := mcptest.Cliente(t, ctx, pool, "Agência De Publicidade E Propaganda Do Interior Ltda")
	camp := mcptest.Campanha(t, ctx, pool, cli, "Camp", "2026-09-01", "2026-09-30")
	for i := range limiteTeto + 1 {
		mcptest.PosVenda(t, ctx, pool, cli, camp, fmt.Sprintf("Fechamento De Setembro Com Título Bem Comprido %03d", i), "2026-09-01", "2026-09-30", false, nil)
	}
	out := chamarTool(t, cfgTeste(pool), "listar_pos_vendas", map[string]any{"limit": 100})
	require.Len(t, itens(t, out), limiteTeto)
	require.Equal(t, true, pag(t, out)["has_more"])
	n := tamanhoDoResultado(t, out)
	t.Logf("listar_pos_vendas: %d itens = %d bytes", limiteTeto, n)
	require.Less(t, n, 60_000)
}
