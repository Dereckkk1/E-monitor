package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

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
		require.Equal(t, r.ID.String(), ds[i]["id"], "o id do destinatário (não é segredo) é o que reenviar/revogar recebem")
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

// bytesJSON: quantos bytes o texto ocupa como string JSON (sem as aspas).
func bytesJSON(t *testing.T, s string) int {
	t.Helper()
	b, err := json.Marshal(s)
	require.NoError(t, err)
	return len(b) - 2
}

// Pior caso de obter_pos_venda: o teto de campanhas × tetoPorItem linhas de
// checking, com nota de ~10 KB e mensagem de ~20 KB. O orçamento é em bytes:
// "<" vira < (6 bytes) no JSON e um emoji ocupa 4, então os três
// alfabetos são medidos (alvo < 60 KB).
func TestObterPosVenda_PiorCasoCabe(t *testing.T) {
	for _, c := range []struct{ nome, nota, mensagem string }{
		{"texto", strings.Repeat("Compensou com inserções extras no fim de semana. ", 210), strings.Repeat("Olá! Segue o fechamento. ", 820)},
		{"menor-que", strings.Repeat("<", 10_000), strings.Repeat("<", 20_000)},
		{"emoji", strings.Repeat("📻", 2_500), strings.Repeat("📻", 5_000)},
	} {
		t.Run(c.nome, func(t *testing.T) {
			ctx, pool := mcptest.DBCatalogo(t)
			cli := mcptest.Cliente(t, ctx, pool, "Agência De Publicidade E Propaganda Do Interior Ltda")
			repo := postsale.NewRepo(pool)
			rep, err := repo.CreateDraft(ctx, postsale.CreateDraftInput{ClientID: cli, Title: "Fechamento De Setembro Com Título Bem Comprido"})
			require.NoError(t, err)
			var blocos []postsale.BlockRow
			var cbs []postsale.CampaignBlock
			for i := range tetoBlocosPosVenda + 1 {
				camp := mcptest.Campanha(t, ctx, pool, cli, fmt.Sprintf("Campanha De Verão Institucional Com Nome Bem Comprido %02d", i), "2026-09-01", "2026-09-30")
				blocos = append(blocos, postsale.BlockRow{CampaignID: camp, From: mcptest.Dia("2026-09-01"), To: mcptest.Dia("2026-09-30"), Position: i})
				var rows []postsale.StationRow
				for j := range tetoPorItem + 1 {
					rows = append(rows, postsale.StationRow{StationID: uuid.New(), Name: fmt.Sprintf("Rádio Comunitária E Educativa Do Interior Paulista %02d FM", j),
						Programmed: 1234, Identified: 1200, Deficit: 34, Extras: 12, Kind: postsale.KindCompensation, Note: c.nota})
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
			_, err = pool.Exec(ctx, `UPDATE post_sale_reports SET intro_message = $2 WHERE id = $1`, rep.ID, c.mensagem)
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
			nota := b0["checking"].([]any)[0].(map[string]any)["nota"].(string)
			require.True(t, strings.HasSuffix(nota, "…"))
			require.LessOrEqual(t, utf8.RuneCountInString(nota), tetoNota)
			require.LessOrEqual(t, bytesJSON(t, nota), tetoNota, "o teto também é em bytes do JSON")
			require.LessOrEqual(t, bytesJSON(t, out["mensagem"].(string)), tetoMensagem)
			require.Contains(t, out["ressalvas"], ressalvaTextoCortado)
			n := tamanhoDoResultado(t, out)
			t.Logf("obter_pos_venda (%s): %d campanhas × %d linhas de checking (nota de ~10 KB, mensagem de ~20 KB) = %d bytes",
				c.nome, tetoBlocosPosVenda, tetoPorItem, n)
			require.Less(t, n, 60_000)
		})
	}
}

// Pior caso de listar_pos_vendas: a página máxima com títulos acima do teto
// (saem cortados em tetoTitulo) e nome de cliente longo (alvo < 60 KB).
func TestListarPosVendas_PiorCasoCabe(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	cli := mcptest.Cliente(t, ctx, pool, strings.Repeat("Agência De Publicidade E Propaganda ", 4)+"Do Interior Ltda")
	camp := mcptest.Campanha(t, ctx, pool, cli, "Camp", "2026-09-01", "2026-09-30")
	for i := range tetoPaginaPosVendas + 1 {
		mcptest.PosVenda(t, ctx, pool, cli, camp, fmt.Sprintf("%03d ", i)+strings.Repeat("Fechamento De Setembro Com Título ", 10),
			"2026-09-01", "2026-09-30", false, nil)
	}
	out := chamarTool(t, cfgTeste(pool), "listar_pos_vendas", map[string]any{"limit": 100})
	require.Len(t, itens(t, out), tetoPaginaPosVendas)
	require.EqualValues(t, tetoPaginaPosVendas, pag(t, out)["limit"], "limit acima do teto é rebaixado e ecoado")
	require.Equal(t, true, pag(t, out)["has_more"])
	titulo := itens(t, out)[0]["titulo"].(string)
	require.True(t, strings.HasSuffix(titulo, "…"))
	require.LessOrEqual(t, bytesJSON(t, titulo), tetoTitulo)
	require.Contains(t, out["ressalvas"], ressalvaTextoCortado)
	n := tamanhoDoResultado(t, out)
	t.Logf("listar_pos_vendas: %d itens com título no teto = %d bytes", tetoPaginaPosVendas, n)
	require.Less(t, n, 60_000)
}

// Texto livre (título, mensagem, nota, erro de email) não vaza URL nem token:
// o admin pode colar o link do Drive na mensagem, e o erro de SMTP pode trazer
// o link público do cliente. Nenhuma das 3 tools deixa o segredo passar.
func TestPosVenda_TextoLivreSemSegredo(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	est := mcptest.Emissora(t, ctx, pool, "Rádio X", 1000)
	camp := mcptest.Campanha(t, ctx, pool, cli, "Camp", "2026-09-01", "2026-09-30", est)
	const anexo = "https://drive.google.com/d/SEGREDO-ANEXO/view"
	const colado = "https://drive.google.com/d/SEGREDO2/view"
	nota := "veja " + colado + " e também " + anexo
	id := mcptest.PosVenda(t, ctx, pool, cli, camp, "Fechamento "+colado, "2026-09-01", "2026-09-30", true,
		&postsale.Payload{Version: postsale.PayloadVersion, Campaigns: []postsale.CampaignBlock{{CampaignID: camp,
			CheckingRows: []postsale.StationRow{{StationID: est, Name: "Rádio X", Deficit: 1, Kind: postsale.KindCompensation, Note: nota},
				{StationID: uuid.New(), Name: "Rádio Y", Kind: postsale.KindAbove}}}}},
		postsale.RecipientInput{Email: "a@cliente.com", Name: "Ana"})
	_, err := pool.Exec(ctx, `UPDATE post_sale_reports SET attachments_url = $2, intro_message = $3 WHERE id = $1`,
		id, anexo, "Segue o link "+colado+" e o anexo "+anexo)
	require.NoError(t, err)
	var token string
	require.NoError(t, pool.QueryRow(ctx, `UPDATE post_sale_report_recipients
		SET email_status = 'failed', email_error = '550 rejected HTTPS://portal.teste/pos-venda/' || token || ' (token ' || token || ')'
		WHERE report_id = $1 RETURNING token`, id).Scan(&token))

	cfg := cfgTeste(pool)
	textos := map[string]string{
		"obter_pos_venda":         textoTool(t, cfg, "obter_pos_venda", map[string]any{"pos_venda_id": id.String()}),
		"listar_pos_vendas":       textoTool(t, cfg, "listar_pos_vendas", map[string]any{}),
		"destinatarios_pos_venda": textoTool(t, cfg, "destinatarios_pos_venda", map[string]any{"pos_venda_id": id.String()}),
	}
	for nome, texto := range textos {
		require.NotContains(t, texto, "SEGREDO2", nome)
		require.NotContains(t, texto, "SEGREDO-ANEXO", nome)
		require.NotContains(t, texto, token, nome)
		require.NotContains(t, texto, "pos-venda/"+token[:8], nome)
		require.NotContains(t, strings.ToLower(texto), "portal.teste/pos-venda", nome)
	}
	var o map[string]any
	require.NoError(t, json.Unmarshal([]byte(textos["obter_pos_venda"]), &o))
	require.Equal(t, "Segue o link https://drive.google.com/… e o anexo https://drive.google.com/…", o["mensagem"])
	require.Equal(t, "Fechamento https://drive.google.com/…", o["titulo"])
	ck := itens(t, o)[0]["checking"].([]any)
	require.Equal(t, "veja https://drive.google.com/… e também https://drive.google.com/…", ck[0].(map[string]any)["nota"])
	require.Nil(t, ck[1].(map[string]any)["nota"], "sem nota: null, não \"\"")
	var d map[string]any
	require.NoError(t, json.Unmarshal([]byte(textos["destinatarios_pos_venda"]), &d))
	require.Equal(t, "550 rejected https://portal.teste/… (token [omitido])", itens(t, d)[0]["erro_email"])
}

// Rascunho: destinatarios_pos_venda explica a lista vazia; obter_pos_venda não
// fala de checking cortado (não há checking).
func TestPosVenda_RascunhoExplica(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	camp := mcptest.Campanha(t, ctx, pool, cli, "Camp", "2026-09-01", "2026-09-30")
	id := mcptest.PosVenda(t, ctx, pool, cli, camp, "Rascunho", "2026-09-01", "2026-09-30", false, nil)
	cfg := cfgTeste(pool)
	d := chamarTool(t, cfg, "destinatarios_pos_venda", map[string]any{"pos_venda_id": id.String()})
	require.Empty(t, itens(t, d))
	require.Len(t, d["ressalvas"], 1)
	require.Contains(t, d["ressalvas"].([]any)[0], "cliente_id")
	require.Contains(t, d["ressalvas"].([]any)[0], cli.String())
	o := chamarTool(t, cfg, "obter_pos_venda", map[string]any{"pos_venda_id": id.String()})
	for _, r := range o["ressalvas"].([]any) {
		require.NotContains(t, r, "checking de cada campanha", "rascunho não tem checking para cortar")
	}
	require.Contains(t, o["ressalvas"], ressalvaRascunho)
	// A busca chega aparada no repositório, como no handler.
	require.EqualValues(t, 1, pag(t, chamarTool(t, cfg, "listar_pos_vendas", map[string]any{"busca": "  Rascunho  "}))["total"])
}

// "sent" legado com payload_json NULL: números null + ressalva, não erro interno.
func TestObterPosVenda_EnviadoSemPayload(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	camp := mcptest.Campanha(t, ctx, pool, cli, "Camp", "2026-09-01", "2026-09-30")
	id := mcptest.PosVenda(t, ctx, pool, cli, camp, "F", "2026-09-01", "2026-09-30", true,
		&postsale.Payload{Version: postsale.PayloadVersion, Campaigns: []postsale.CampaignBlock{{CampaignID: camp}}})
	_, err := pool.Exec(ctx, `UPDATE post_sale_reports SET payload_json = NULL WHERE id = $1`, id)
	require.NoError(t, err)
	o := chamarTool(t, cfgTeste(pool), "obter_pos_venda", map[string]any{"pos_venda_id": id.String()})
	require.Equal(t, "enviado", o["estado"])
	require.Nil(t, itens(t, o)[0]["numeros"])
	require.Nil(t, itens(t, o)[0]["checking"])
	require.Contains(t, o["ressalvas"], ressalvaEnviadoSemPayload)
}

// Pior caso de destinatarios_pos_venda: a página máxima com erro de email
// longo (o teto o corta) e nome/email longos (alvo < 60 KB).
func TestDestinatariosPosVenda_PiorCasoCabe(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	camp := mcptest.Campanha(t, ctx, pool, cli, "Camp", "2026-09-01", "2026-09-30")
	var pessoas []postsale.RecipientInput
	for i := range limiteTeto + 1 {
		pessoas = append(pessoas, postsale.RecipientInput{Email: fmt.Sprintf("%s%03d@agencia-de-publicidade-do-interior.com.br", strings.Repeat("contato.comercial", 2), i),
			Name: "Maria Aparecida Dos Santos Oliveira Ferreira Da Silva"})
	}
	id := mcptest.PosVenda(t, ctx, pool, cli, camp, "F", "2026-09-01", "2026-09-30", true,
		&postsale.Payload{Version: postsale.PayloadVersion}, pessoas...)
	_, err := pool.Exec(ctx, `UPDATE post_sale_report_recipients SET email_status = 'failed', email_error = $2,
		opened_at = now(), revoked_at = now() WHERE report_id = $1`, id, strings.Repeat("550 5.7.1 mensagem recusada pelo servidor ", 300))
	require.NoError(t, err)
	out := chamarTool(t, cfgTeste(pool), "destinatarios_pos_venda", map[string]any{"pos_venda_id": id.String(), "limit": 100})
	require.Len(t, itens(t, out), tetoPaginaDestinatarios)
	require.EqualValues(t, tetoPaginaDestinatarios, pag(t, out)["limit"], "limit acima do teto é rebaixado e ecoado")
	erro := itens(t, out)[0]["erro_email"].(string)
	require.True(t, strings.HasSuffix(erro, "…"))
	require.LessOrEqual(t, bytesJSON(t, erro), tetoErroEmail)
	require.Contains(t, out["ressalvas"], ressalvaTextoCortado)
	n := tamanhoDoResultado(t, out)
	t.Logf("destinatarios_pos_venda: %d destinatários com erro de email longo = %d bytes", tetoPaginaDestinatarios, n)
	require.Less(t, n, 60_000)
}

// O anexo citado SEM esquema (host+caminho, com www., com //) e o link público
// de OUTRO pós-venda também não passam; texto que só parece URL fica intacto.
func TestPosVenda_URLSemEsquemaETokenAlheio(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	est := mcptest.Emissora(t, ctx, pool, "Rádio X", 1000)
	camp := mcptest.Campanha(t, ctx, pool, cli, "Camp", "2026-09-01", "2026-09-30", est)
	alheio, err := postsale.NewToken() // 43 caracteres base64url, de outro pós-venda
	require.NoError(t, err)
	require.Len(t, alheio, 43)
	nota := "link antigo e-monitor.online/pos-venda/" + alheio + " e solto pos-venda/" + alheio
	id := mcptest.PosVenda(t, ctx, pool, cli, camp, "Fechamento pos-venda/"+alheio, "2026-09-01", "2026-09-30", true,
		&postsale.Payload{Version: postsale.PayloadVersion, Campaigns: []postsale.CampaignBlock{{CampaignID: camp,
			CheckingRows: []postsale.StationRow{{StationID: est, Name: "Rádio X", Deficit: 1, Kind: postsale.KindCompensation, Note: nota}}}}},
		postsale.RecipientInput{Email: "a@cliente.com", Name: "Ana"})
	mensagem := "acesse drive.google.com/drive/folders/1AbCSEGREDO, www.drive.google.com/drive/folders/1AbCSEGREDO, " +
		"//drive.google.com/drive/folders/1AbCSEGREDO ou Drive.Google.com/drive/folders/1AbCSEGREDO. " +
		"Valor R$ 1.500/mês desde 01/10, e.g./S.A./Ltda./SP."
	_, err = pool.Exec(ctx, `UPDATE post_sale_reports SET attachments_url = 'https://drive.google.com/drive/folders/1AbCSEGREDO',
		intro_message = $2 WHERE id = $1`, id, mensagem)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE post_sale_report_recipients SET email_status = 'failed',
		email_error = 'falhou: e-monitor.online/pos-venda/' || $2 WHERE report_id = $1`, id, alheio)
	require.NoError(t, err)

	cfg := cfgTeste(pool)
	for _, nome := range []string{"obter_pos_venda", "listar_pos_vendas", "destinatarios_pos_venda"} {
		args := map[string]any{"pos_venda_id": id.String()}
		if nome == "listar_pos_vendas" {
			args = map[string]any{}
		}
		texto := textoTool(t, cfg, nome, args)
		require.NotContains(t, texto, "1AbCSEGREDO", nome)
		require.NotContains(t, texto, alheio, nome)
	}
	o := chamarTool(t, cfg, "obter_pos_venda", map[string]any{"pos_venda_id": id.String()})
	require.Equal(t, "acesse drive.google.com/…, www.drive.google.com/…, //drive.google.com/… ou Drive.Google.com/…. "+
		"Valor R$ 1.500/mês desde 01/10, e.g./S.A./Ltda./SP.", o["mensagem"], "o que só parece URL fica intacto")
	require.Equal(t, "Fechamento pos-venda/[omitido]", o["titulo"])
	require.Equal(t, "link antigo e-monitor.online/… e solto pos-venda/[omitido]",
		itens(t, o)[0]["checking"].([]any)[0].(map[string]any)["nota"])
	l := chamarTool(t, cfg, "listar_pos_vendas", map[string]any{})
	require.Equal(t, "Fechamento pos-venda/[omitido]", itens(t, l)[0]["titulo"], "a lista também apaga token alheio")
}

// attachments_url que não é URL http(s) não entra no Replacer: "Drive" no
// texto continua "Drive".
func TestPosVenda_AnexoQueNaoEURL(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	camp := mcptest.Campanha(t, ctx, pool, cli, "Camp", "2026-09-01", "2026-09-30")
	id := mcptest.PosVenda(t, ctx, pool, cli, camp, "Fechamento no Drive", "2026-09-01", "2026-09-30", false, nil)
	_, err := pool.Exec(ctx, `UPDATE post_sale_reports SET attachments_url = 'Drive', intro_message = 'Os arquivos estão no Drive.' WHERE id = $1`, id)
	require.NoError(t, err)
	o := chamarTool(t, cfgTeste(pool), "obter_pos_venda", map[string]any{"pos_venda_id": id.String()})
	require.Equal(t, "Os arquivos estão no Drive.", o["mensagem"])
	require.Equal(t, "Fechamento no Drive", o["titulo"])
	require.Equal(t, "…", o["anexo"], "o anexo que não parseia sai como …, nunca o valor")
}

func TestCortarTexto(t *testing.T) {
	for _, teto := range []int{0, -1} {
		s, cortou := cortarTexto("abc", teto)
		require.Equal(t, "", s, "teto %d não dá panic", teto)
		require.True(t, cortou)
	}
	s, cortou := cortarTexto("", 0)
	require.Equal(t, "", s)
	require.False(t, cortou)
	s, cortou = cortarTexto("abc", 3)
	require.Equal(t, "abc", s)
	require.False(t, cortou)
	s, cortou = cortarTexto("abcdef", 5)
	require.Equal(t, "ab…", s, "5 bytes: 2 letras + … (3 bytes)")
	require.True(t, cortou)
	s, _ = cortarTexto(strings.Repeat("<", 100), 20)
	require.Equal(t, "<<…", s, "cada < custa 6 bytes no JSON")
	require.LessOrEqual(t, bytesJSON(t, s), 20)
	s, _ = cortarTexto(strings.Repeat("📻", 100), 20)
	require.Equal(t, "📻📻📻📻…", s)
	require.LessOrEqual(t, bytesJSON(t, s), 20)
	s, _ = cortarTexto("<<<", 2)
	require.Equal(t, "", s, "nem o … cabe")
}

// Números do pós-venda seguem a regra de exibição do /insights (que os gerou):
// bloco 100% consolidado sem parcela por inserção → bonificação null (a página
// do cliente esconde o card); sem impactos → CPM null; target sem impactos →
// cpm_target null. Bloco consolidado COM parcela por inserção mostra o bônus.
func TestObterPosVenda_RegraDeExibicaoDosNumeros(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	c1 := mcptest.Campanha(t, ctx, pool, cli, "Consolidada", "2026-09-01", "2026-09-30")
	c2 := mcptest.Campanha(t, ctx, pool, cli, "Mista", "2026-09-01", "2026-09-30")
	repo := postsale.NewRepo(pool)
	rep, err := repo.CreateDraft(ctx, postsale.CreateDraftInput{ClientID: cli, Title: "F"})
	require.NoError(t, err)
	require.NoError(t, repo.ReplaceBlocks(ctx, rep.ID, []postsale.BlockRow{
		{CampaignID: c1, From: mcptest.Dia("2026-09-01"), To: mcptest.Dia("2026-09-30"), Position: 0},
		{CampaignID: c2, From: mcptest.Dia("2026-09-01"), To: mcptest.Dia("2026-09-30"), Position: 1},
	}))
	b, err := json.Marshal(postsale.Payload{Version: postsale.PayloadVersion, Campaigns: []postsale.CampaignBlock{
		{CampaignID: c1, KPIs: postsale.BlockKPIs{ValorEntregue: 5000, Consolidated: true, StationsWithTarget: 1, StationsCount: 2}},
		{CampaignID: c2, KPIs: postsale.BlockKPIs{ValorEntregue: 9000, Impactos: 1000, CPM: 9.5, Consolidated: true,
			Bonificacao: 0, BonificacaoCount: 3, StationsWithTarget: 1, ImpactosTarget: 400, CPMTarget: 22.5}},
	}})
	require.NoError(t, err)
	require.NoError(t, repo.MarkSent(ctx, rep.ID, b))

	o := chamarTool(t, cfgTeste(pool), "obter_pos_venda", map[string]any{"pos_venda_id": rep.ID.String()})
	xs := itens(t, o)
	consolidada := xs[0]["numeros"].(map[string]any)
	require.Nil(t, consolidada["bonificacao"], "100% consolidado: a tela esconde o card")
	require.Nil(t, consolidada["bonificacao_insercoes"])
	require.Nil(t, consolidada["cpm"], "sem impactos o CPM não se aplica")
	require.EqualValues(t, 0, consolidada["impactos_target"], "há emissora com target: o zero é zero")
	require.Nil(t, consolidada["cpm_target"], "sem impactos no target o CPM no target não se aplica")
	mista := xs[1]["numeros"].(map[string]any)
	require.EqualValues(t, 0, mista["bonificacao"], "consolidada com parcela por inserção: o card aparece (zero em R$, 3 inserções)")
	require.EqualValues(t, 3, mista["bonificacao_insercoes"])
	require.EqualValues(t, 9.5, mista["cpm"])
	require.EqualValues(t, 22.5, mista["cpm_target"])
	require.Contains(t, o["ressalvas"], ressalvaBonificacaoOculta)
	require.Contains(t, o["ressalvas"], ressalvaCPMSemImpactos)
}
