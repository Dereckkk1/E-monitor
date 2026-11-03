package mcp

// Task 20 (Fatia 4): reenviar_pos_venda e revogar_destinatario_pos_venda. As duas são
// `critico`: a chamada NUNCA executa, grava um pedido pendente, e quem executa é a
// aprovação do dono em /pendentes (b.aprovar, sobre a lista REAL das escritas). Cada
// tool prova: (1) o pedido não escreve nem manda email, (2) aprovar executa e devolve o
// destinatário no formato de destinatarios_pos_venda, (3) o efeito é o da rota HTTP,
// (4) cada recusa no pedido E na aprovação, (5) a auditoria com meta.mcp.pendente_id,
// (6) o token do destinatário (a credencial do link público /pos-venda/<token>) não
// aparece em lugar nenhum que o MCP devolve ou grava.
//
// O serviço de pós-venda das tools é o REAL (postsale.Service sobre o banco de teste)
// com o correio falso de criar_usuario (correioFalso): o mesmo que registra o email e
// pode ser programado para falhar.

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"radiocheck/internal/api/handlers"
	"radiocheck/internal/mcp/mcptest"
	"radiocheck/internal/postsale"
)

const (
	toolReenviarPosVenda            = "reenviar_pos_venda"
	toolRevogarDestinatarioPosVenda = "revogar_destinatario_pos_venda"

	// O link público de um pós-venda é <portal>/pos-venda/<token>; o link da tela
	// do admin (…/admin/pos-venda/<id>) não casa com este prefixo.
	prefixoDoLinkPublico = "portal.teste/pos-venda/"
)

// ── fixtures e leitura ──────────────────────────────────────────────────

// posVendaSemeado é um pós-venda ENVIADO com duas pessoas (Ana e Bia); Ana abriu
// duas vezes (mcptest.PosVenda), e o token de cada uma é um token de verdade
// (postsale.NewToken, 43 caracteres).
type posVendaSemeado struct {
	rep, cli, camp     uuid.UUID
	ana, bia           uuid.UUID
	tokenAna, tokenBia string
}

func (p posVendaSemeado) tokens() []string { return []string{p.tokenAna, p.tokenBia} }

func semearPosVendaEnviado(t *testing.T, b *bancadaTool, titulo string) posVendaSemeado {
	t.Helper()
	cli := mcptest.Cliente(t, b.ctx, b.pool, "Cliente do "+titulo)
	camp := mcptest.Campanha(t, b.ctx, b.pool, cli, "Campanha "+titulo, "2026-09-01", "2026-09-30")
	tkAna, err := postsale.NewToken()
	require.NoError(t, err)
	tkBia, err := postsale.NewToken()
	require.NoError(t, err)
	payload := &postsale.Payload{Version: postsale.PayloadVersion, Title: titulo, Client: postsale.ClientBrief{Name: "Cliente do " + titulo},
		Campaigns: []postsale.CampaignBlock{{CampaignID: camp, Name: "Campanha " + titulo, PeriodFrom: "2026-09-01", PeriodTo: "2026-09-30", PeriodLabel: "01/09 a 30/09/2026"}}}
	rep := mcptest.PosVenda(t, b.ctx, b.pool, cli, camp, titulo, "2026-09-01", "2026-09-30", true, payload,
		postsale.RecipientInput{Email: "ana@cliente.teste", Name: "Ana Souza", Token: tkAna},
		postsale.RecipientInput{Email: "bia@cliente.teste", Name: "Bia Lima", Token: tkBia})
	recs, err := postsale.NewRepo(b.pool).Recipients(b.ctx, rep) // ORDER BY email: ana, bia
	require.NoError(t, err)
	require.Len(t, recs, 2)
	return posVendaSemeado{rep: rep, cli: cli, camp: camp, ana: recs[0].ID, bia: recs[1].ID, tokenAna: tkAna, tokenBia: tkBia}
}

// semearPosVendaRascunho é um rascunho com um destinatário gravado à força (o publish
// é quem os cria, mas o id existe no banco): prova que o rascunho é recusado por ser
// rascunho, e não só por não ter destinatário.
func semearPosVendaRascunho(t *testing.T, b *bancadaTool, titulo string) (rep, dest uuid.UUID) {
	t.Helper()
	cli := mcptest.Cliente(t, b.ctx, b.pool, "Cliente do "+titulo)
	camp := mcptest.Campanha(t, b.ctx, b.pool, cli, "Campanha "+titulo, "2026-09-01", "2026-09-30")
	rep = mcptest.PosVenda(t, b.ctx, b.pool, cli, camp, titulo, "2026-09-01", "2026-09-30", false, nil)
	tk, err := postsale.NewToken()
	require.NoError(t, err)
	recs, err := postsale.NewRepo(b.pool).CreateRecipients(b.ctx, rep, []postsale.RecipientInput{{Email: "rascunho@cliente.teste", Name: "Pessoa", Token: tk}})
	require.NoError(t, err)
	return rep, recs[0].ID
}

// ligarPosVenda põe o serviço REAL de pós-venda (repo do banco de teste, correio
// falso) na bancada. correioLigado=false é "sem SMTP" (o servidor sem credencial).
func ligarPosVenda(t *testing.T, b *bancadaTool, correioLigado bool) (*postsale.Service, *correioFalso) {
	t.Helper()
	correio := &correioFalso{}
	svc := postsale.New(postsale.Config{
		Repo: postsale.NewRepo(b.pool), Mailer: correio, MailEnabled: correioLigado,
		BaseURL: "https://portal.teste", Log: zap.NewNop(),
	})
	b.E.PosVenda = svc
	return svc, correio
}

func argsPosVenda(rep, dest uuid.UUID) map[string]any {
	return map[string]any{"pos_venda_id": rep.String(), "destinatario_id": dest.String()}
}

// linhasDoPosVenda é o pós-venda e todos os destinatários como estão gravados: o que o
// pedido não pode mexer.
func linhasDoPosVenda(t *testing.T, b *bancadaTool, rep uuid.UUID) string {
	t.Helper()
	var s string
	require.NoError(t, b.pool.QueryRow(b.ctx, `
		SELECT COALESCE((SELECT to_jsonb(p)::text FROM post_sale_reports p WHERE p.id = $1), 'sumiu') || E'\n' ||
		       COALESCE((SELECT string_agg(to_jsonb(r)::text, E'\n' ORDER BY r.email) FROM post_sale_report_recipients r WHERE r.report_id = $1), '')`,
		rep).Scan(&s))
	return s
}

// estadoDoDestinatario é o que a rota e a tool precisam deixar IGUAL entre dois
// pós-vendas gêmeos (sem ids nem horários). O token é trocado por <token>.
func estadoDoDestinatario(t *testing.T, b *bancadaTool, dest uuid.UUID, token string) string {
	t.Helper()
	var s string
	require.NoError(t, b.pool.QueryRow(b.ctx, `
		SELECT COALESCE((SELECT email_status || '|' || COALESCE(email_error, '-') || '|abertos=' || open_count || '|abriu=' || (opened_at IS NOT NULL)::text ||
		                        '|revogado=' || (revoked_at IS NOT NULL)::text || '|por=' || COALESCE(revoked_by::text, '-') || '|' || email || '|' || name
		                   FROM post_sale_report_recipients WHERE id = $1), 'sumiu')`, dest).Scan(&s))
	return strings.ReplaceAll(s, token, "<token>")
}

func itemDoDestinatario(t *testing.T, b *bancadaTool, rep, dest uuid.UUID) map[string]any {
	t.Helper()
	lista := b.chamar(t, "destinatarios_pos_venda", map[string]any{"pos_venda_id": rep.String()})
	for _, it := range itens(t, lista) {
		if it["id"] == dest.String() {
			return it
		}
	}
	t.Fatalf("destinatário %s não está em destinatarios_pos_venda: %v", dest, lista)
	return nil
}

// linkAbre diz se o link público do token ainda abre o documento.
func linkAbre(t *testing.T, b *bancadaTool, token string) bool {
	t.Helper()
	_, err := postsale.NewRepo(b.pool).ResolveToken(b.ctx, token)
	if errors.Is(err, postsale.ErrNotFound) {
		return false
	}
	require.NoError(t, err)
	return true
}

// sqlDoCaso troca 'DEST' e 'REP' pelos ids do pós-venda semeado (o pgx não aceita
// parâmetro que o SQL não usa).
func sqlDoCaso(sql string, pv posVendaSemeado) string {
	return strings.NewReplacer("'DEST'", "'"+pv.ana.String()+"'", "'REP'", "'"+pv.rep.String()+"'").Replace(sql)
}

func revogarNoBanco(t *testing.T, b *bancadaTool, dest uuid.UUID) {
	t.Helper()
	_, err := b.pool.Exec(b.ctx, `UPDATE post_sale_report_recipients SET revoked_at = now() WHERE id = $1`, dest)
	require.NoError(t, err)
}

func exigeAuditoriaDePosVenda(t *testing.T, b *bancadaTool, tool string, rep uuid.UUID, pendenteID string) {
	t.Helper()
	l := b.exigeAuditoria(t, "mcp.escrita."+tool, "post_sale_report", &rep)
	require.Equal(t, pendenteID, l.Meta["mcp"].(map[string]any)["pendente_id"])
	require.Equal(t, "critico", l.Meta["nivel"])
	require.Equal(t, tool, l.Meta["tool"])
}

// exigeSemTokenDePosVenda varre o que o MCP devolveu e o que ficou gravado (pedidos e
// auditoria): nem o token de ninguém nem o link público, em nenhuma forma.
func exigeSemTokenDePosVenda(t *testing.T, b *bancadaTool, tokens []string, saidas ...any) {
	t.Helper()
	tudo := todoOAudit(t, b) + todasAsPendentes(t, b)
	for _, s := range saidas {
		tudo += mustJSON(t, s)
	}
	for _, tk := range tokens {
		require.NotContains(t, tudo, tk, "o token do destinatário é a credencial do link público e não pode aparecer em lugar nenhum")
		require.NotContains(t, tudo, tk[:20], "nem um pedaço dele")
	}
	require.NotContains(t, tudo, prefixoDoLinkPublico, "o link público do cliente nunca sai")
}

// rotaDePosVenda é o handler REAL das rotas de reenvio e revogação, sobre o MESMO
// serviço da bancada.
func rotaDePosVenda(b *bancadaTool) *handlers.PostSaleHandler {
	return handlers.NewPostSaleHandler(b.E.PosVenda, zap.NewNop())
}

func reenviarPelaRota(t *testing.T, b *bancadaTool, rep, dest uuid.UUID) {
	t.Helper()
	chamarHandlerCorpo(t, rotaDePosVenda(b).Resend, http.MethodPost, "/post-sale/reports/"+rep.String()+"/resend",
		map[string]any{"recipient_id": dest.String()}, map[string]string{"id": rep.String()}, b.admin)
}

func revogarPelaRota(t *testing.T, b *bancadaTool, dest uuid.UUID) {
	t.Helper()
	chamarHandlerCorpo(t, rotaDePosVenda(b).RevokeRecipient, http.MethodPost, "/post-sale/recipients/"+dest.String()+"/revoke",
		nil, map[string]string{"rid": dest.String()}, b.admin)
}

// emailSemIdentidade normaliza um email para comparar o da rota com o da tool: o
// token (o link) muda de uma fixture para a outra.
func emailSemIdentidade(e envio, tokens ...string) string {
	s := e.Assunto + "\n" + strings.Join(e.Para, ",") + "\n" + e.Texto + "\n" + e.HTML
	for _, tk := range tokens {
		s = strings.ReplaceAll(s, tk, "<token>")
	}
	return s
}

// ═══════════════════════════════════════════════════════════════════════
// reenviar_pos_venda
// ═══════════════════════════════════════════════════════════════════════

func TestReenviarPosVenda_PedidoNaoEnviaEAprovarReenvia(t *testing.T) {
	b := novaBancadaTool(t)
	_, correio := ligarPosVenda(t, b, true)
	pv := semearPosVendaEnviado(t, b, "Fechamento de Setembro")
	// A Ana teve o email recusado antes: o reenvio regrava o status e limpa o erro.
	_, err := b.pool.Exec(b.ctx, `UPDATE post_sale_report_recipients SET email_status = 'failed', email_error = '550 caixa cheia' WHERE id = $1`, pv.ana)
	require.NoError(t, err)
	props := propriedades(esquemaDa(t, b.cfg, toolReenviarPosVenda))
	require.NotContains(t, props, "confirmacao", "crítico nunca recebe token")
	require.ElementsMatch(t, []string{"pos_venda_id", "destinatario_id"}, keysDe(props))
	antes := linhasDoPosVenda(t, b, pv.rep)

	out := b.pedePendente(t, toolReenviarPosVenda, argsPosVenda(pv.rep, pv.ana))

	require.Contains(t, out["motivo"], "manda email para fora")
	require.Equal(t, "https://portal.teste/admin/pos-venda/"+pv.rep.String(), out["links"].(map[string]any)["tela"])
	require.Contains(t, out["aprovar_em"], "/pendentes?id="+out["pendente_id"].(string))
	require.Contains(t, out["aviso"], "Nada aconteceu ainda")
	require.Zero(t, correio.total(), "o pedido não manda email")
	require.Equal(t, antes, linhasDoPosVenda(t, b, pv.rep), "o pedido não escreve")
	pre := out["preview"].(map[string]any)
	require.Contains(t, pre["resumo"], "Fechamento de Setembro")
	require.Contains(t, pre["resumo"], "Ana Souza")
	require.Contains(t, pre["resumo"], "ana@cliente.teste")
	require.Equal(t, []any{map[string]any{"campo": "status_email", "de": "failed", "para": "o desfecho do novo envio (sent ou failed)"}}, pre["mudancas"])
	ef := mustJSON(t, pre["efeitos"])
	require.Contains(t, ef, "MESMO link", "reenviar não emite link novo")
	require.Contains(t, ef, "ana@cliente.teste")
	require.Contains(t, ef, "AGORA")
	rv := mustJSON(t, pre["ressalvas"])
	require.Contains(t, rv, "já abriu o documento 2 vez(es)", "a Ana abriu: o reenvio só repete o email")
	require.Contains(t, rv, "550 caixa cheia", "o último erro de email aparece no preview (já higienizado)")
	require.Contains(t, rv, "revogar_destinatario_pos_venda", "cortar um link é outra tool")
	pid := out["pendente_id"].(string)
	exigeSemEscritaNoPedido(t, b, toolReenviarPosVenda, pid)

	ap := b.aprovar(t, pid)

	require.Equal(t, 1, correio.total())
	mail := correio.enviados[0]
	require.Equal(t, []string{"ana@cliente.teste"}, mail.Para, "só a Ana")
	require.Contains(t, mail.Texto, "https://portal.teste/pos-venda/"+pv.tokenAna, "o MESMO token vai no email — e só nele")
	require.Equal(t, "sent|-|abertos=2|abriu=true|revogado=false|por=-|ana@cliente.teste|Ana Souza", estadoDoDestinatario(t, b, pv.ana, pv.tokenAna))
	require.Contains(t, estadoDoDestinatario(t, b, pv.bia, pv.tokenBia), "sent|-|abertos=0", "a Bia não é tocada")
	require.True(t, linkAbre(t, b, pv.tokenAna) && linkAbre(t, b, pv.tokenBia), "o link segue abrindo: mesmo token")
	res := ap.resultadoJSON(t)
	require.ElementsMatch(t, []string{"pos_venda", "destinatario"}, keysDe(res), "sem aviso quando o email saiu")
	require.Equal(t, map[string]any{"id": pv.rep.String(), "nome": "Fechamento de Setembro"}, res["pos_venda"])
	lido := itemDoDestinatario(t, b, pv.rep, pv.ana)
	require.Equal(t, lido, res["destinatario"], "o destinatário no formato (e com os valores) de destinatarios_pos_venda")
	require.Equal(t, "sent", lido["status_email"])
	require.Nil(t, lido["erro_email"], "o reenvio limpa o erro anterior")
	exigeAuditoriaDePosVenda(t, b, toolReenviarPosVenda, pv.rep, pid)
	require.Equal(t, "executada", b.estadoDaPendente(t, pid))
	exigeSemTokenDePosVenda(t, b, pv.tokens(), out, res, lido)
}

func TestReenviarPosVenda_ParidadeComARota(t *testing.T) {
	b := novaBancadaTool(t)
	_, correio := ligarPosVenda(t, b, true)
	viaRota := semearPosVendaEnviado(t, b, "Fechamento Gêmeo")
	viaTool := semearPosVendaEnviado(t, b, "Fechamento Gêmeo")

	reenviarPelaRota(t, b, viaRota.rep, viaRota.ana)
	out := b.pedePendente(t, toolReenviarPosVenda, argsPosVenda(viaTool.rep, viaTool.ana))
	b.aprovar(t, out["pendente_id"].(string))

	require.Equal(t, 2, correio.total(), "um email pela rota, um pela tool")
	rota, tool := correio.enviados[0], correio.enviados[1]
	require.Equal(t, emailSemIdentidade(rota, viaRota.tokenAna), emailSemIdentidade(tool, viaTool.tokenAna), "o mesmo email (assunto, destino, corpo)")
	require.Equal(t, estadoDoDestinatario(t, b, viaRota.ana, viaRota.tokenAna), estadoDoDestinatario(t, b, viaTool.ana, viaTool.tokenAna),
		"a mesma linha do destinatário pela rota e pela tool")
	require.Equal(t, estadoDoDestinatario(t, b, viaRota.bia, viaRota.tokenBia), estadoDoDestinatario(t, b, viaTool.bia, viaTool.tokenBia), "a Bia fica igual nos dois")
}

// O SMTP falha: o Resend GRAVA email_status=failed (e o erro) antes de voltar, então a
// escrita aconteceu — não é recusa (recusa é "nada foi gravado"): o pedido executa, o
// resultado traz o aviso e a auditoria registra. O erro passa por semSegredoNoTexto.
func TestReenviarPosVenda_FalhaDoEmailGravaFailedEViraAviso(t *testing.T) {
	b := novaBancadaTool(t)
	_, correio := ligarPosVenda(t, b, true)
	pv := semearPosVendaEnviado(t, b, "Fechamento")
	correio.erro = errors.New("smtp: 550 5.7.1 rejeitado para ana@cliente.teste ao entregar https://portal.teste/pos-venda/" + pv.tokenAna + " (token " + pv.tokenAna + ")")
	out := b.pedePendente(t, toolReenviarPosVenda, argsPosVenda(pv.rep, pv.ana))
	pid := out["pendente_id"].(string)

	ap := b.aprovar(t, pid) // exige "executada": a falha do email NÃO é erro

	require.Zero(t, correio.total(), "o correio recusou: nada saiu")
	var status string
	var erroBruto *string
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT email_status, email_error FROM post_sale_report_recipients WHERE id = $1`, pv.ana).Scan(&status, &erroBruto))
	require.Equal(t, "failed", status, "o estado foi gravado: por isso não é Recusa")
	require.NotNil(t, erroBruto)
	require.Contains(t, *erroBruto, "rejeitado", "o banco guarda o erro como a rota o guarda")
	res := ap.resultadoJSON(t)
	dest := res["destinatario"].(map[string]any)
	require.Equal(t, "failed", dest["status_email"])
	require.Contains(t, dest["erro_email"], "rejeitado para ana@cliente.teste")
	require.Contains(t, res["aviso"], "NÃO saiu")
	require.Contains(t, res["aviso"], "rejeitado para ana@cliente.teste", "o aviso traz o erro, já sem o link")
	require.Contains(t, res["aviso"], "failed")
	require.Equal(t, itemDoDestinatario(t, b, pv.rep, pv.ana), dest)
	exigeAuditoriaDePosVenda(t, b, toolReenviarPosVenda, pv.rep, pid)
	require.Equal(t, "executada", b.estadoDaPendente(t, pid))
	// O texto do erro de email (que carregava o link e o token) passou por semSegredoNoTexto.
	exigeSemTokenDePosVenda(t, b, pv.tokens(), out, res, itemDoDestinatario(t, b, pv.rep, pv.ana))
	require.NotContains(t, mustJSON(t, res), "https://portal.teste/pos-venda", "nem o link sem o token")
}

// O erro do SMTP é texto livre e pode ser enorme: o resultado (que fica guardado no pedido) e
// o aviso o trazem cortados no teto, com a ressalva de que o texto completo está na tela.
func TestReenviarPosVenda_ErroDeEmailLongoVemCortado(t *testing.T) {
	b := novaBancadaTool(t)
	_, correio := ligarPosVenda(t, b, true)
	pv := semearPosVendaEnviado(t, b, "Fechamento")
	correio.erro = errors.New("550 5.7.1 " + strings.Repeat("mensagem recusada pelo servidor ", 80))
	out := b.pedePendente(t, toolReenviarPosVenda, argsPosVenda(pv.rep, pv.ana))

	ap := b.aprovar(t, out["pendente_id"].(string))

	res := ap.resultadoJSON(t)
	erro := res["destinatario"].(map[string]any)["erro_email"].(string)
	require.True(t, strings.HasSuffix(erro, "…"), "cortado no teto")
	require.LessOrEqual(t, bytesJSON(t, erro), tetoErroEmail)
	require.Contains(t, res["aviso"], erro, "o aviso traz o erro já cortado")
	require.Less(t, len(ap.ResultadoJSON), 2000, "o resultado guardado no pedido segue pequeno")
}

// O feito de uma escrita já gravada nunca vira erro: com a releitura funcionando o resultado
// é o destinatário; se ela falha, traz quem é e manda conferir (mesma regra de webhookFeito).
func TestDestinatarioFeito_NaoViraErroSeAReleituraFalha(t *testing.T) {
	b := novaBancadaTool(t)
	pv := semearPosVendaEnviado(t, b, "Fechamento")
	pl, err := planejarDestinatario(b.ctx, b.E, entradaDestinatarioPosVenda{PosVendaID: pv.rep.String(), DestinatarioID: pv.ana.String()})
	require.NoError(t, err)
	semAviso := func(destinatarioDTO, *postsale.Report) string { return "" }

	f := destinatarioFeito(b.ctx, b.cfg, b.E, pl, semAviso)
	require.Equal(t, "post_sale_report", f.Recurso)
	require.Equal(t, pv.rep, *f.RecursoID)
	require.Empty(t, f.Ressalvas)
	require.Equal(t, pv.ana.String(), mapaDoJSON(t, mustJSON(t, f.Resultado))["destinatario"].(map[string]any)["id"])

	cancelado, cancela := context.WithCancel(b.ctx)
	cancela()
	f = destinatarioFeito(cancelado, b.cfg, b.E, pl, semAviso)
	res := mapaDoJSON(t, mustJSON(t, f.Resultado))
	require.Equal(t, pv.ana.String(), res["destinatario_id"])
	require.Equal(t, pv.rep.String(), res["pos_venda"].(map[string]any)["id"])
	require.Contains(t, res["aviso"], "não consegui reler o destinatário")
	require.Contains(t, res["aviso"], "destinatarios_pos_venda")
	require.Equal(t, []string{res["aviso"].(string)}, f.Ressalvas)
}

// A rota faz o mesmo com o email que falha: grava failed e responde 204. Tool e rota
// deixam o destinatário no mesmo estado.
func TestReenviarPosVenda_FalhaDoEmailParidadeComARota(t *testing.T) {
	b := novaBancadaTool(t)
	_, correio := ligarPosVenda(t, b, true)
	viaRota := semearPosVendaEnviado(t, b, "Fechamento Gêmeo")
	viaTool := semearPosVendaEnviado(t, b, "Fechamento Gêmeo")
	correio.erro = errors.New("smtp: 451 tente depois")

	reenviarPelaRota(t, b, viaRota.rep, viaRota.ana) // 204
	out := b.pedePendente(t, toolReenviarPosVenda, argsPosVenda(viaTool.rep, viaTool.ana))
	b.aprovar(t, out["pendente_id"].(string))

	require.Equal(t, "failed|smtp: 451 tente depois|abertos=2|abriu=true|revogado=false|por=-|ana@cliente.teste|Ana Souza", estadoDoDestinatario(t, b, viaRota.ana, viaRota.tokenAna))
	require.Equal(t, estadoDoDestinatario(t, b, viaRota.ana, viaRota.tokenAna), estadoDoDestinatario(t, b, viaTool.ana, viaTool.tokenAna))
}

// Sem SMTP no servidor a rota regrava o status como 'disabled' (e sobrescreve o 'sent'
// de um envio que saiu de verdade) sem mandar nada. A tool é mais estrita (F4-10): recusa
// no pedido, sem escrever, e diz a condição.
func TestReenviarPosVenda_SemSMTPRecusaEmVezDeRegravarDisabled(t *testing.T) {
	b := novaBancadaTool(t)
	_, correio := ligarPosVenda(t, b, false)
	pv := semearPosVendaEnviado(t, b, "Fechamento")
	semRota := semearPosVendaEnviado(t, b, "Fechamento Rota")

	reenviarPelaRota(t, b, semRota.rep, semRota.ana)
	texto := b.chamarErro(t, toolReenviarPosVenda, argsPosVenda(pv.rep, pv.ana))

	require.Contains(t, estadoDoDestinatario(t, b, semRota.ana, semRota.tokenAna), "disabled|", "a rota regrava o status e nada sai")
	require.Contains(t, texto, "[email_desligado]")
	require.Contains(t, texto, "sem SMTP")
	require.Contains(t, texto, "nenhum email sairia")
	require.Contains(t, estadoDoDestinatario(t, b, pv.ana, pv.tokenAna), "sent|", "a tool não regravou o status")
	require.Zero(t, correio.total())
	require.Zero(t, b.totalAuditorias(t), "recusa não deixa pedido nem auditoria")
}

func TestReenviarPosVenda_SemOServicoRecusaNoPedido(t *testing.T) {
	b := novaBancadaTool(t) // a bancada nasce sem o serviço de pós-venda
	pv := semearPosVendaEnviado(t, b, "Fechamento")

	texto := b.chamarErro(t, toolReenviarPosVenda, argsPosVenda(pv.rep, pv.ana))

	require.Contains(t, texto, "[pos_venda_indisponivel]")
	require.Contains(t, texto, "serviço de pós-venda")
	require.Zero(t, b.totalAuditorias(t))
}

func TestReenviarPosVenda_RecusasNoPedido(t *testing.T) {
	b := novaBancadaTool(t)
	_, correio := ligarPosVenda(t, b, true)
	pv := semearPosVendaEnviado(t, b, "Fechamento")
	revogarNoBanco(t, b, pv.bia)
	outro := semearPosVendaEnviado(t, b, "Outro Fechamento")
	repRasc, destRasc := semearPosVendaRascunho(t, b, "Rascunho")
	semDoc := semearPosVendaEnviado(t, b, "Sem Documento")
	_, err := b.pool.Exec(b.ctx, `UPDATE post_sale_reports SET payload_json = NULL WHERE id = $1`, semDoc.rep)
	require.NoError(t, err)
	antes := linhasDoPosVenda(t, b, pv.rep)

	for nome, caso := range map[string]struct {
		args   map[string]any
		codigo string
		texto  string
	}{
		"rascunho (ainda não foi enviado)": {argsPosVenda(repRasc, destRasc), "[nao_encontrado]", "rascunho"},
		"link revogado (não ressuscita)":   {argsPosVenda(pv.rep, pv.bia), "[nao_encontrado]", "revogado"},
		"destinatário de outro pós-venda":  {argsPosVenda(pv.rep, outro.ana), "[nao_encontrado]", "não existe neste pós-venda"},
		"destinatário inexistente":         {argsPosVenda(pv.rep, uuid.New()), "[nao_encontrado]", "não existe neste pós-venda"},
		"pós-venda inexistente":            {argsPosVenda(uuid.New(), pv.ana), "[nao_encontrado]", "pós-venda"},
		"enviado sem o documento gravado":  {argsPosVenda(semDoc.rep, semDoc.ana), "[pos_venda_sem_documento]", "congelamento"},
		"pos_venda_id em texto":            {map[string]any{"pos_venda_id": "xyz", "destinatario_id": pv.ana.String()}, "[entrada_invalida]", "pos_venda_id"},
		"pos_venda_id em branco":           {map[string]any{"pos_venda_id": "", "destinatario_id": pv.ana.String()}, "[entrada_invalida]", "pos_venda_id"},
		"destinatario_id em texto":         {map[string]any{"pos_venda_id": pv.rep.String(), "destinatario_id": "xyz"}, "[entrada_invalida]", "destinatario_id"},
		"destinatario_id em branco":        {map[string]any{"pos_venda_id": pv.rep.String(), "destinatario_id": ""}, "[entrada_invalida]", "destinatario_id"},
	} {
		texto := b.chamarErro(t, toolReenviarPosVenda, caso.args)
		require.Contains(t, texto, caso.codigo, nome)
		require.Contains(t, texto, caso.texto, nome)
		for _, tk := range append(pv.tokens(), outro.tokens()...) {
			require.NotContains(t, texto, tk, nome+": a recusa não repete token")
		}
	}
	// Não vaza se o destinatário existe em outro pós-venda: o texto é o mesmo do que não existe.
	require.Equal(t,
		b.chamarErro(t, toolReenviarPosVenda, argsPosVenda(pv.rep, outro.ana)),
		b.chamarErro(t, toolReenviarPosVenda, argsPosVenda(pv.rep, uuid.New())))
	require.Zero(t, b.totalAuditorias(t), "recusa não deixa pedido nem auditoria")
	require.Zero(t, correio.total())
	require.Equal(t, antes, linhasDoPosVenda(t, b, pv.rep))
	var n int
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT count(*) FROM mcp_acoes_pendentes`).Scan(&n))
	require.Zero(t, n)
}

func TestReenviarPosVenda_RecusaNaAprovacao(t *testing.T) {
	pedido := func(t *testing.T) (*bancadaTool, *correioFalso, posVendaSemeado, string) {
		t.Helper()
		b := novaBancadaTool(t)
		_, c := ligarPosVenda(t, b, true)
		pv := semearPosVendaEnviado(t, b, "Fechamento")
		out := b.pedePendente(t, toolReenviarPosVenda, argsPosVenda(pv.rep, pv.ana))
		return b, c, pv, out["pendente_id"].(string)
	}
	for nome, caso := range map[string]struct {
		mexida string // SQL com 'DEST' = id do destinatário e 'REP' = id do pós-venda
		codigo string
	}{
		"alguém revogou o link no meio":            {`UPDATE post_sale_report_recipients SET revoked_at = now() WHERE id = 'DEST'`, "nao_encontrado"},
		"o pós-venda voltou a rascunho no meio":    {`UPDATE post_sale_reports SET status = 'draft' WHERE id = 'REP'`, "nao_encontrado"},
		"o destinatário foi apagado no meio":       {`DELETE FROM post_sale_report_recipients WHERE id = 'DEST'`, "nao_encontrado"},
		"o pós-venda foi apagado no meio":          {`DELETE FROM post_sale_reports WHERE id = 'REP'`, "nao_encontrado"},
		"o documento congelado sumiu no meio":      {`UPDATE post_sale_reports SET payload_json = NULL WHERE id = 'REP'`, "pos_venda_sem_documento"},
		"o destinatário foi movido para outro pós": {`UPDATE post_sale_report_recipients SET report_id = (SELECT id FROM post_sale_reports WHERE id <> 'REP' LIMIT 1) WHERE id = 'DEST'`, "nao_encontrado"},
	} {
		t.Run(nome, func(t *testing.T) {
			b, correio, pv, pid := pedido(t)
			semearPosVendaEnviado(t, b, "Outro Fechamento") // para existir outro pós-venda para onde mover
			_, err := b.pool.Exec(b.ctx, sqlDoCaso(caso.mexida, pv))
			require.NoError(t, err)

			exigeRecusaNaAprovacao(t, b, toolReenviarPosVenda, pid, caso.codigo)
			require.Zero(t, correio.total(), "nada foi enviado")
		})
	}
	t.Run("o envio de email foi desligado no meio", func(t *testing.T) {
		b, correio, _, pid := pedido(t)
		ligarPosVenda(t, b, false)

		exigeRecusaNaAprovacao(t, b, toolReenviarPosVenda, pid, "email_desligado")
		require.Zero(t, correio.total())
	})
	t.Run("o serviço de pós-venda saiu no meio", func(t *testing.T) {
		b, _, _, pid := pedido(t)
		b.E.PosVenda = nil

		exigeRecusaNaAprovacao(t, b, toolReenviarPosVenda, pid, "pos_venda_indisponivel")
	})
}

// Falha de execução (aqui o documento congelado ilegível) é erro interno: o pedido fica
// `falhou`, o texto do erro Go (que cita o tipo e a biblioteca) não sai por lugar nenhum.
func TestReenviarPosVenda_FalhaInternaNaoVazaOErro(t *testing.T) {
	b := novaBancadaTool(t)
	_, correio := ligarPosVenda(t, b, true)
	pv := semearPosVendaEnviado(t, b, "Fechamento")
	out := b.pedePendente(t, toolReenviarPosVenda, argsPosVenda(pv.rep, pv.ana))
	pid := out["pendente_id"].(string)
	_, err := b.pool.Exec(b.ctx, `UPDATE post_sale_reports SET payload_json = '[]'::jsonb WHERE id = $1`, pv.rep)
	require.NoError(t, err)

	_, err = b.tentarAprovar(t, pid)

	require.ErrorIs(t, err, ErrExecucaoInterrompida)
	require.Equal(t, "falhou", b.estadoDaPendente(t, pid))
	for _, detalhe := range []string{"cannot unmarshal", "ilegível", "postsale"} {
		require.NotContains(t, err.Error(), detalhe)
		require.NotContains(t, todoOAudit(t, b)+todasAsPendentes(t, b), detalhe, "o erro Go não vai para o audit_log nem para o pedido")
	}
	require.Empty(t, b.auditoria(t, "mcp.escrita."+toolReenviarPosVenda), "não houve escrita")
	require.Zero(t, correio.total())
}

// O token do destinatário e o link público não aparecem em lugar nenhum que o MCP
// devolve ou grava, nem com o email dando certo nem com o erro do SMTP citando o link.
func TestReenviarPosVenda_TokenNuncaVaza(t *testing.T) {
	for nome, erroDoSMTP := range map[string]error{
		"email que sai":                          nil,
		"erro do SMTP que cita o link e o token": errors.New("550 recusado: link https://portal.teste/pos-venda/TOKEN e pos-venda/TOKEN (TOKEN)"),
	} {
		t.Run(nome, func(t *testing.T) {
			b := novaBancadaTool(t)
			_, correio := ligarPosVenda(t, b, true)
			pv := semearPosVendaEnviado(t, b, "Fechamento")
			if erroDoSMTP != nil {
				correio.erro = errors.New(strings.ReplaceAll(erroDoSMTP.Error(), "TOKEN", pv.tokenAna))
			}
			out := b.pedePendente(t, toolReenviarPosVenda, argsPosVenda(pv.rep, pv.ana))
			pid := out["pendente_id"].(string)
			respostaDoPedido := mustJSON(t, out)
			pendenteAntes := todasAsPendentes(t, b)

			ap := b.aprovar(t, pid)

			lista := mustJSON(t, b.chamar(t, "destinatarios_pos_venda", map[string]any{"pos_venda_id": pv.rep.String()}))
			obter := mustJSON(t, b.chamar(t, "obter_pos_venda", map[string]any{"pos_venda_id": pv.rep.String()}))
			for nomeDaSaida, texto := range map[string]string{
				"resposta do pedido": respostaDoPedido, "pendente antes da aprovação": pendenteAntes,
				"pendente depois": todasAsPendentes(t, b), "resultado da aprovação": string(ap.ResultadoJSON),
				"estado da pendente aprovada": mustJSON(t, ap.Pendente), "audit_log": todoOAudit(t, b),
				"destinatarios_pos_venda": lista, "obter_pos_venda": obter,
			} {
				for _, tk := range pv.tokens() {
					require.NotContains(t, texto, tk, "o token vazou em: "+nomeDaSaida)
					require.NotContains(t, texto, tk[:20], "um pedaço do token vazou em: "+nomeDaSaida)
				}
				require.NotContains(t, texto, prefixoDoLinkPublico, "o link público vazou em: "+nomeDaSaida)
			}
			// Os args gravados são os ids que a pessoa pediu, sem campo de segredo.
			var gravados map[string]any
			require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT args FROM mcp_acoes_pendentes WHERE id = $1`, pid).Scan(&gravados))
			require.ElementsMatch(t, []string{"pos_venda_id", "destinatario_id"}, keysDe(gravados))
			audit := b.auditoria(t, "mcp.escrita."+toolReenviarPosVenda)[0].Meta["args"].(map[string]any)
			require.ElementsMatch(t, []string{"pos_venda_id", "destinatario_id"}, keysDe(audit))
			// O token existe e vai SÓ no email (quando ele sai): a prova não é vazia.
			if erroDoSMTP == nil {
				require.Contains(t, correio.enviados[0].Texto, pv.tokenAna)
				require.Contains(t, correio.enviados[0].HTML, pv.tokenAna)
			}
		})
	}
}

// ═══════════════════════════════════════════════════════════════════════
// revogar_destinatario_pos_venda
// ═══════════════════════════════════════════════════════════════════════

func TestRevogarDestinatario_PedidoNaoRevogaEAprovarRevoga(t *testing.T) {
	b := novaBancadaTool(t)
	_, correio := ligarPosVenda(t, b, true)
	pv := semearPosVendaEnviado(t, b, "Fechamento de Setembro")
	props := propriedades(esquemaDa(t, b.cfg, toolRevogarDestinatarioPosVenda))
	require.NotContains(t, props, "confirmacao", "crítico nunca recebe token")
	require.ElementsMatch(t, []string{"pos_venda_id", "destinatario_id"}, keysDe(props))
	antes := linhasDoPosVenda(t, b, pv.rep)

	out := b.pedePendente(t, toolRevogarDestinatarioPosVenda, argsPosVenda(pv.rep, pv.ana))

	require.Contains(t, out["motivo"], "irreversível: o link morre e não há como emitir outro para esta pessoa")
	require.Contains(t, out["motivo"], "o documento não se republica")
	require.Equal(t, "https://portal.teste/admin/pos-venda/"+pv.rep.String(), out["links"].(map[string]any)["tela"])
	require.Contains(t, out["aprovar_em"], "/pendentes?id="+out["pendente_id"].(string))
	require.Contains(t, out["aviso"], "Nada aconteceu ainda")
	require.Equal(t, antes, linhasDoPosVenda(t, b, pv.rep), "o pedido não escreve")
	require.True(t, linkAbre(t, b, pv.tokenAna), "o link da Ana segue abrindo depois do pedido")
	pre := out["preview"].(map[string]any)
	require.Contains(t, pre["resumo"], "Fechamento de Setembro")
	require.Contains(t, pre["resumo"], "Ana Souza")
	require.Contains(t, pre["resumo"], "ana@cliente.teste")
	require.Equal(t, []any{map[string]any{"campo": "link", "de": "ativo", "para": "revogado"}}, pre["mudancas"])
	ef := mustJSON(t, pre["efeitos"])
	require.Contains(t, ef, "para de abrir na hora")
	require.Contains(t, ef, "IRREVERSÍVEL")
	require.Contains(t, ef, "não se republica", "sem como emitir outro link")
	require.Contains(t, ef, "continua na caixa", "o email que ela já recebeu fica; o link dentro dele morre")
	require.Contains(t, ef, "Restam 1 link(s) ativo(s)", "os links dos outros não mudam")
	require.Contains(t, ef, "abriu o documento 2 vez(es)")
	pid := out["pendente_id"].(string)
	exigeSemEscritaNoPedido(t, b, toolRevogarDestinatarioPosVenda, pid)

	ap := b.aprovar(t, pid)

	require.False(t, linkAbre(t, b, pv.tokenAna), "o link da Ana morre")
	require.True(t, linkAbre(t, b, pv.tokenBia), "o da Bia segue abrindo")
	var por *uuid.UUID
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT revoked_by FROM post_sale_report_recipients WHERE id = $1`, pv.ana).Scan(&por))
	require.NotNil(t, por)
	require.Equal(t, b.admin, *por, "revoked_by é quem aprova: o dono do pedido, logado no portal")
	require.Equal(t, 0, correio.total(), "revogar não manda email")
	res := ap.resultadoJSON(t)
	require.ElementsMatch(t, []string{"pos_venda", "destinatario"}, keysDe(res))
	require.Equal(t, map[string]any{"id": pv.rep.String(), "nome": "Fechamento de Setembro"}, res["pos_venda"])
	lido := itemDoDestinatario(t, b, pv.rep, pv.ana)
	require.Equal(t, lido, res["destinatario"], "o destinatário no formato (e com os valores) de destinatarios_pos_venda")
	require.NotNil(t, lido["revogado_em"])
	exigeAuditoriaDePosVenda(t, b, toolRevogarDestinatarioPosVenda, pv.rep, pid)
	require.Equal(t, "executada", b.estadoDaPendente(t, pid))
	exigeSemTokenDePosVenda(t, b, pv.tokens(), out, res, lido)
}

func TestRevogarDestinatario_ParidadeComARota(t *testing.T) {
	b := novaBancadaTool(t)
	ligarPosVenda(t, b, true)
	viaRota := semearPosVendaEnviado(t, b, "Fechamento Gêmeo")
	viaTool := semearPosVendaEnviado(t, b, "Fechamento Gêmeo")

	revogarPelaRota(t, b, viaRota.ana)
	out := b.pedePendente(t, toolRevogarDestinatarioPosVenda, argsPosVenda(viaTool.rep, viaTool.ana))
	b.aprovar(t, out["pendente_id"].(string))

	require.Equal(t, "sent|-|abertos=2|abriu=true|revogado=true|por="+b.admin.String()+"|ana@cliente.teste|Ana Souza", estadoDoDestinatario(t, b, viaRota.ana, viaRota.tokenAna),
		"a rota grava revoked_by = o admin logado")
	require.Equal(t, estadoDoDestinatario(t, b, viaRota.ana, viaRota.tokenAna), estadoDoDestinatario(t, b, viaTool.ana, viaTool.tokenAna),
		"a mesma linha do destinatário pela rota e pela tool")
	require.Equal(t, estadoDoDestinatario(t, b, viaRota.bia, viaRota.tokenBia), estadoDoDestinatario(t, b, viaTool.bia, viaTool.tokenBia), "a Bia fica igual nos dois")
	require.False(t, linkAbre(t, b, viaRota.tokenAna) || linkAbre(t, b, viaTool.tokenAna))
	require.True(t, linkAbre(t, b, viaRota.tokenBia) && linkAbre(t, b, viaTool.tokenBia))
}

// O `by` do RevokeRecipient é o usuário que revoga (revoked_by, FK para users), não o
// "dono" do relatório: não existe checagem de posse ali. O MCP passa quem executa (o
// dono do pedido, logado na aprovação) — e, sem principal, o UUID zero vira NULL (a
// revogação de segurança nunca falha por causa da auditoria).
func TestRevogarDestinatario_ByEQuemExecuta(t *testing.T) {
	b := novaBancadaTool(t)
	pv := semearPosVendaEnviado(t, b, "Fechamento")
	d := escritaRevogarDestinatarioPosVenda(b.cfg)
	in := entradaDestinatarioPosVenda{PosVendaID: pv.rep.String(), DestinatarioID: pv.ana.String()}

	f, err := d.Executar(b.ctx, b.E, nil, in)

	require.NoError(t, err)
	require.Equal(t, "post_sale_report", f.Recurso)
	require.False(t, linkAbre(t, b, pv.tokenAna))
	var por *uuid.UUID
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT revoked_by FROM post_sale_report_recipients WHERE id = $1`, pv.ana).Scan(&por))
	require.Nil(t, por, "sem principal o UUID zero vira NULL")

	_, err = d.Executar(b.ctx, b.E, b.p, entradaDestinatarioPosVenda{PosVendaID: pv.rep.String(), DestinatarioID: pv.bia.String()})
	require.NoError(t, err)
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT revoked_by FROM post_sale_report_recipients WHERE id = $1`, pv.bia).Scan(&por))
	require.NotNil(t, por)
	require.Equal(t, b.p.UserID, *por)
}

// Revogar é segurança e não manda email: não depende do serviço de pós-venda.
func TestRevogarDestinatario_NaoDependeDoServicoDeEmail(t *testing.T) {
	b := novaBancadaTool(t) // sem o serviço (PosVenda nil)
	pv := semearPosVendaEnviado(t, b, "Fechamento")

	out := b.pedePendente(t, toolRevogarDestinatarioPosVenda, argsPosVenda(pv.rep, pv.ana))
	b.aprovar(t, out["pendente_id"].(string))

	require.False(t, linkAbre(t, b, pv.tokenAna))
}

func TestRevogarDestinatario_UltimoLinkAtivoAvisa(t *testing.T) {
	b := novaBancadaTool(t)
	pv := semearPosVendaEnviado(t, b, "Fechamento")
	revogarNoBanco(t, b, pv.bia)

	out := b.pedePendente(t, toolRevogarDestinatarioPosVenda, argsPosVenda(pv.rep, pv.ana))
	ap := b.aprovar(t, out["pendente_id"].(string))

	pre := out["preview"].(map[string]any)
	require.Contains(t, mustJSON(t, pre["ressalvas"]), "ÚLTIMO link ativo")
	require.NotContains(t, mustJSON(t, pre["efeitos"]), "Restam")
	require.Contains(t, ap.resultadoJSON(t)["aviso"], "último link ativo", "a ressalva também mora no resultado: é o que sobra depois da aprovação")
}

func TestRevogarDestinatario_RecusasNoPedido(t *testing.T) {
	b := novaBancadaTool(t)
	pv := semearPosVendaEnviado(t, b, "Fechamento")
	revogarNoBanco(t, b, pv.bia)
	outro := semearPosVendaEnviado(t, b, "Outro Fechamento")
	repRasc, destRasc := semearPosVendaRascunho(t, b, "Rascunho")
	antes := linhasDoPosVenda(t, b, pv.rep)

	for nome, caso := range map[string]struct {
		args   map[string]any
		codigo string
		texto  string
	}{
		"já revogado":                      {argsPosVenda(pv.rep, pv.bia), "[sem_mudanca]", "já está revogado"},
		"rascunho (ainda não foi enviado)": {argsPosVenda(repRasc, destRasc), "[nao_encontrado]", "rascunho"},
		"destinatário de outro pós-venda":  {argsPosVenda(pv.rep, outro.ana), "[nao_encontrado]", "não existe neste pós-venda"},
		"destinatário inexistente":         {argsPosVenda(pv.rep, uuid.New()), "[nao_encontrado]", "não existe neste pós-venda"},
		"pós-venda inexistente":            {argsPosVenda(uuid.New(), pv.ana), "[nao_encontrado]", "pós-venda"},
		"pos_venda_id em texto":            {map[string]any{"pos_venda_id": "xyz", "destinatario_id": pv.ana.String()}, "[entrada_invalida]", "pos_venda_id"},
		"pos_venda_id em branco":           {map[string]any{"pos_venda_id": "", "destinatario_id": pv.ana.String()}, "[entrada_invalida]", "pos_venda_id"},
		"destinatario_id em texto":         {map[string]any{"pos_venda_id": pv.rep.String(), "destinatario_id": "xyz"}, "[entrada_invalida]", "destinatario_id"},
		"destinatario_id em branco":        {map[string]any{"pos_venda_id": pv.rep.String(), "destinatario_id": ""}, "[entrada_invalida]", "destinatario_id"},
	} {
		texto := b.chamarErro(t, toolRevogarDestinatarioPosVenda, caso.args)
		require.Contains(t, texto, caso.codigo, nome)
		require.Contains(t, texto, caso.texto, nome)
		for _, tk := range append(pv.tokens(), outro.tokens()...) {
			require.NotContains(t, texto, tk, nome+": a recusa não repete token")
		}
	}
	require.Equal(t,
		b.chamarErro(t, toolRevogarDestinatarioPosVenda, argsPosVenda(pv.rep, outro.ana)),
		b.chamarErro(t, toolRevogarDestinatarioPosVenda, argsPosVenda(pv.rep, uuid.New())),
		"não vaza se o destinatário existe em outro pós-venda")
	require.Zero(t, b.totalAuditorias(t), "recusa não deixa pedido nem auditoria")
	require.Equal(t, antes, linhasDoPosVenda(t, b, pv.rep))
	require.True(t, linkAbre(t, b, pv.tokenAna), "nenhuma recusa revogou nada")
	require.True(t, linkAbre(t, b, outro.tokenAna), "o destinatário do outro pós-venda segue intacto")
	var n int
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT count(*) FROM mcp_acoes_pendentes`).Scan(&n))
	require.Zero(t, n)
}

// A rota revoga qualquer destinatário pelo id, sem conferir de qual pós-venda é (o id do
// pós-venda nem faz parte dela); a tool confere, e a Ana de outro pós-venda não morre.
func TestRevogarDestinatario_RotaNaoConfereORelatorioEAToolConfere(t *testing.T) {
	b := novaBancadaTool(t)
	ligarPosVenda(t, b, true)
	pv := semearPosVendaEnviado(t, b, "Fechamento")
	outro := semearPosVendaEnviado(t, b, "Outro Fechamento")

	texto := b.chamarErro(t, toolRevogarDestinatarioPosVenda, argsPosVenda(pv.rep, outro.ana))
	require.Contains(t, texto, "[nao_encontrado]")
	require.True(t, linkAbre(t, b, outro.tokenAna))

	revogarPelaRota(t, b, outro.ana) // a rota não tem o id do pós-venda
	require.False(t, linkAbre(t, b, outro.tokenAna))
}

func TestRevogarDestinatario_RecusaNaAprovacao(t *testing.T) {
	pedido := func(t *testing.T) (*bancadaTool, posVendaSemeado, string) {
		t.Helper()
		b := novaBancadaTool(t)
		pv := semearPosVendaEnviado(t, b, "Fechamento")
		out := b.pedePendente(t, toolRevogarDestinatarioPosVenda, argsPosVenda(pv.rep, pv.ana))
		return b, pv, out["pendente_id"].(string)
	}
	for nome, caso := range map[string]struct {
		mexida string // SQL com 'DEST' = id do destinatário e 'REP' = id do pós-venda
		codigo string
	}{
		"alguém revogou o mesmo link no meio":   {`UPDATE post_sale_report_recipients SET revoked_at = now() WHERE id = 'DEST'`, "sem_mudanca"},
		"o pós-venda voltou a rascunho no meio": {`UPDATE post_sale_reports SET status = 'draft' WHERE id = 'REP'`, "nao_encontrado"},
		"o destinatário foi apagado no meio":    {`DELETE FROM post_sale_report_recipients WHERE id = 'DEST'`, "nao_encontrado"},
		"o pós-venda foi apagado no meio":       {`DELETE FROM post_sale_reports WHERE id = 'REP'`, "nao_encontrado"},
		"o destinatário foi movido para outro pós-venda": {
			`UPDATE post_sale_report_recipients SET report_id = (SELECT id FROM post_sale_reports WHERE id <> 'REP' LIMIT 1) WHERE id = 'DEST'`, "nao_encontrado"},
	} {
		t.Run(nome, func(t *testing.T) {
			b, pv, pid := pedido(t)
			semearPosVendaEnviado(t, b, "Outro Fechamento") // para existir outro pós-venda para onde mover
			_, err := b.pool.Exec(b.ctx, sqlDoCaso(caso.mexida, pv))
			require.NoError(t, err)

			exigeRecusaNaAprovacao(t, b, toolRevogarDestinatarioPosVenda, pid, caso.codigo)
			var porAlguem int
			require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT count(*) FROM post_sale_report_recipients WHERE id IN ($1, $2) AND revoked_by IS NOT NULL`,
				pv.ana, pv.bia).Scan(&porAlguem))
			require.Zero(t, porAlguem, "a recusa não gravou revoked_by em ninguém")
		})
	}
}

func TestRevogarDestinatario_TokenNuncaVaza(t *testing.T) {
	b := novaBancadaTool(t)
	pv := semearPosVendaEnviado(t, b, "Fechamento")
	out := b.pedePendente(t, toolRevogarDestinatarioPosVenda, argsPosVenda(pv.rep, pv.ana))
	pid := out["pendente_id"].(string)
	respostaDoPedido := mustJSON(t, out)
	pendenteAntes := todasAsPendentes(t, b)

	ap := b.aprovar(t, pid)

	lista := mustJSON(t, b.chamar(t, "destinatarios_pos_venda", map[string]any{"pos_venda_id": pv.rep.String()}))
	for nomeDaSaida, texto := range map[string]string{
		"resposta do pedido": respostaDoPedido, "pendente antes da aprovação": pendenteAntes,
		"pendente depois": todasAsPendentes(t, b), "resultado da aprovação": string(ap.ResultadoJSON),
		"estado da pendente aprovada": mustJSON(t, ap.Pendente), "audit_log": todoOAudit(t, b),
		"destinatarios_pos_venda": lista,
	} {
		for _, tk := range pv.tokens() {
			require.NotContains(t, texto, tk, "o token vazou em: "+nomeDaSaida)
			require.NotContains(t, texto, tk[:20], "um pedaço do token vazou em: "+nomeDaSaida)
		}
		require.NotContains(t, texto, prefixoDoLinkPublico, "o link público vazou em: "+nomeDaSaida)
	}
	var gravados map[string]any
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT args FROM mcp_acoes_pendentes WHERE id = $1`, pid).Scan(&gravados))
	require.ElementsMatch(t, []string{"pos_venda_id", "destinatario_id"}, keysDe(gravados))
}

// ── regras que valem para as duas ───────────────────────────────────────

func TestCriticasDePosVenda_DescricaoPedeAprovacaoENuncaInventarIds(t *testing.T) {
	vistas := 0
	for _, tl := range listarToolsDoServidor(t) {
		switch tl.Name {
		case toolReenviarPosVenda, toolRevogarDestinatarioPosVenda:
		default:
			continue
		}
		vistas++
		require.Contains(t, tl.Description, "/pendentes", tl.Name)
		require.Contains(t, tl.Description, "aprovar_em", tl.Name)
		require.Contains(t, tl.Description, "nada aconteceu ainda", tl.Name)
		require.Contains(t, tl.Description, "Nunca invente ids", tl.Name)
		require.Contains(t, tl.Description, "destinatarios_pos_venda", tl.Name, "o id do destinatário vem dali")
		require.Contains(t, tl.Description, "listar_pos_vendas", tl.Name)
		require.True(t, strings.HasSuffix(tl.Description, " Nível: critico."), tl.Name)
	}
	require.Equal(t, 2, vistas)
}

// A descrição diz o que a tool faz, quando usar e a armadilha — o que o modelo lê antes
// de pedir.
func TestCriticasDePosVenda_DescricaoDizAArmadilha(t *testing.T) {
	trechos := map[string][]string{
		toolReenviarPosVenda:            {"MESMO link", "email", "failed", "aviso", "revogar_destinatario_pos_venda", "nao_encontrado", "email_desligado", "rascunho"},
		toolRevogarDestinatarioPosVenda: {"IRREVERSÍVEL", "não se republica", "sem_mudanca", "nao_encontrado", "reenviar_pos_venda", "link vazado"},
	}
	vistas := 0
	for _, tl := range listarToolsDoServidor(t) {
		want, ok := trechos[tl.Name]
		if !ok {
			continue
		}
		vistas++
		for _, trecho := range want {
			require.Contains(t, tl.Description, trecho, tl.Name)
		}
	}
	require.Equal(t, 2, vistas)
}

// Repetir o pedido não o duplica, e aprovar duas vezes executa uma só.
func TestCriticasDePosVenda_AprovarDuasVezesNaoExecutaDuasVezes(t *testing.T) {
	b := novaBancadaTool(t)
	_, correio := ligarPosVenda(t, b, true)
	pv := semearPosVendaEnviado(t, b, "Fechamento")
	out := b.pedePendente(t, toolReenviarPosVenda, argsPosVenda(pv.rep, pv.ana))
	repetido := b.pedePendente(t, toolReenviarPosVenda, argsPosVenda(pv.rep, pv.ana))
	require.Equal(t, out["pendente_id"], repetido["pendente_id"])
	require.Equal(t, true, repetido["ja_existia"])
	require.Zero(t, correio.total(), "nenhuma chamada manda email")

	b.aprovar(t, out["pendente_id"].(string))
	_, err := b.tentarAprovar(t, out["pendente_id"].(string))

	require.Error(t, err, "o pedido já foi executado")
	require.Equal(t, 1, correio.total(), "um só email")
	require.Len(t, b.auditoria(t, "mcp.escrita."+toolReenviarPosVenda), 1)
}
