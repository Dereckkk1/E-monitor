package mcp

// Task 18 (Fatia 4): desativar_cliente, ativar_cliente, apagar_cliente e
// apagar_material. As quatro são `critico`: a chamada NUNCA executa, grava um
// pedido pendente, e quem executa é a aprovação do dono em /pendentes (b.aprovar,
// sobre a lista REAL das escritas). Cada tool prova: (1) o pedido não escreve,
// (2) aprovar executa e devolve o formato do obter_* (ou {apagado:{id,nome}}),
// (3) o efeito é o da rota HTTP, (4) cada recusa no pedido E na aprovação,
// (5) a auditoria com meta.mcp.pendente_id, (6) nenhum segredo (segredo do
// webhook, hash de chave de API) no resultado, no pedido gravado nem no audit_log.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/api/handlers"
	"radiocheck/internal/auth"
	"radiocheck/internal/catalog"
	"radiocheck/internal/mcp/mcptest"
)

const (
	toolDesativarCliente = "desativar_cliente"
	toolAtivarCliente    = "ativar_cliente"
	toolApagarCliente    = "apagar_cliente"
	toolApagarMaterial   = "apagar_material"

	portalDosClientes  = "https://portal.teste/clients"
	portalDosMateriais = "https://portal.teste/materials"

	// O que a fixture grava onde não pode vazar.
	segredoDoWebhook = "whsec_ULTRASECRETO_do_cliente"
)

// ── fixtures e leitura ──────────────────────────────────────────────────

// rastroDoCliente: o cliente com tudo o que um DELETE levaria junto (CASCADE) —
// 2 chaves de API (1 revogada), 2 entregas e 1 falha de webhook, a configuração
// de webhook com segredo, 1 linha de PMM no target e 2 pós-vendas (1 enviado) —
// e SEM campanha, material ou usuário (o que o deixaria apagável).
type rastroDoCliente struct {
	cli       uuid.UUID
	estacao   uuid.UUID
	hashChave string
}

func semearClienteComRastro(t *testing.T, b *bancadaTool, nome string) rastroDoCliente {
	t.Helper()
	r := rastroDoCliente{cli: mcptest.Cliente(t, b.ctx, b.pool, nome)}
	r.estacao = mcptest.Emissora(t, b.ctx, b.pool, "Rádio do rastro "+uuid.NewString()[:6], 1000)
	hash := func() string { s := sha256.Sum256([]byte(uuid.NewString())); return hex.EncodeToString(s[:]) }
	r.hashChave = hash()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO api_keys (client_id, key_hash) VALUES ($1, $2)`, []any{r.cli, r.hashChave}},
		{`INSERT INTO api_keys (client_id, key_hash, revoked_at) VALUES ($1, $2, now())`, []any{r.cli, hash()}},
		{`INSERT INTO webhook_deliveries (client_id, event_type, payload) VALUES ($1, 'detection.confirmed', '{}'), ($1, 'detection.confirmed', '{}')`, []any{r.cli}},
		{`INSERT INTO webhook_failures (client_id, detection_id, payload, next_retry_at) VALUES ($1, $2, '{}', now())`, []any{r.cli, uuid.New()}},
		{`UPDATE clients SET webhook_url = 'https://cliente.teste/hook', webhook_secret = $2, webhook_enabled = true WHERE id = $1`, []any{r.cli, segredoDoWebhook}},
		{`INSERT INTO client_station_pmm (client_id, station_id, pmm_target) VALUES ($1, $2, 1500)`, []any{r.cli, r.estacao}},
		{`INSERT INTO post_sale_reports (client_id, title, status) VALUES ($1, 'Rascunho', 'draft')`, []any{r.cli}},
		{`INSERT INTO post_sale_reports (client_id, title, status, payload_json, sent_at) VALUES ($1, 'Enviado', 'sent', '{}', now())`, []any{r.cli}},
	} {
		_, err := b.pool.Exec(b.ctx, q.sql, q.args...)
		require.NoError(t, err, q.sql)
	}
	return r
}

// contagemCli são as linhas que dependem de um cliente (as que o CASCADE leva).
type contagemCli struct{ Cliente, Chaves, Entregas, Falhas, PMM, PosVenda int }

func contarDoCliente(t *testing.T, b *bancadaTool, cli uuid.UUID) contagemCli {
	t.Helper()
	var c contagemCli
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT
		(SELECT count(*) FROM clients WHERE id = $1),
		(SELECT count(*) FROM api_keys WHERE client_id = $1),
		(SELECT count(*) FROM webhook_deliveries WHERE client_id = $1),
		(SELECT count(*) FROM webhook_failures WHERE client_id = $1),
		(SELECT count(*) FROM client_station_pmm WHERE client_id = $1),
		(SELECT count(*) FROM post_sale_reports WHERE client_id = $1)`, cli).
		Scan(&c.Cliente, &c.Chaves, &c.Entregas, &c.Falhas, &c.PMM, &c.PosVenda))
	return c
}

var contagemComRastro = contagemCli{Cliente: 1, Chaves: 2, Entregas: 2, Falhas: 1, PMM: 1, PosVenda: 2}

// linhaCompletaDoCliente é a linha inteira (id e timestamps inclusive): "o pedido
// não escreve" é igualdade byte a byte.
func linhaCompletaDoCliente(t *testing.T, b *bancadaTool, cli uuid.UUID) string {
	t.Helper()
	var s string
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT COALESCE((SELECT to_jsonb(c)::text FROM clients c WHERE id = $1), 'sumiu')`, cli).Scan(&s))
	return s
}

// linhaDoClienteSemIdentidade: o que a rota e a tool precisam deixar IGUAL entre
// dois clientes gêmeos (sem id e sem timestamps).
func linhaDoClienteSemIdentidade(t *testing.T, b *bancadaTool, cli uuid.UUID) string {
	t.Helper()
	var s string
	require.NoError(t, b.pool.QueryRow(b.ctx, `
		SELECT COALESCE((SELECT name || '|ativo=' || is_active::text || '|cnpj=' || COALESCE(cnpj,'-') ||
		                        '|hub=' || COALESCE(hub_id,'-') || '|hook=' || COALESCE(webhook_url,'-') || '|' || COALESCE(webhook_secret,'-')
		                   FROM clients WHERE id = $1), 'sumiu')`, cli).Scan(&s))
	return s
}

func clienteAtivo(t *testing.T, b *bancadaTool, cli uuid.UUID) bool {
	t.Helper()
	var ativo bool
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT is_active FROM clients WHERE id = $1`, cli).Scan(&ativo))
	return ativo
}

func desativarClienteNoBanco(t *testing.T, b *bancadaTool, cli uuid.UUID) {
	t.Helper()
	_, err := b.pool.Exec(b.ctx, `UPDATE clients SET is_active = false WHERE id = $1`, cli)
	require.NoError(t, err)
}

func exigeAuditoriaDeRecurso(t *testing.T, b *bancadaTool, tool, recurso string, id uuid.UUID, pendenteID string) {
	t.Helper()
	l := b.exigeAuditoria(t, "mcp.escrita."+tool, recurso, &id)
	require.Equal(t, pendenteID, l.Meta["mcp"].(map[string]any)["pendente_id"])
	require.Equal(t, "critico", l.Meta["nivel"])
	require.Equal(t, tool, l.Meta["tool"])
}

func argsCliente(cli uuid.UUID) map[string]any { return map[string]any{"cliente_id": cli.String()} }

// exigeSemSegredo varre o que o MCP devolveu e o que ficou gravado.
func exigeSemSegredo(t *testing.T, b *bancadaTool, saidas ...any) {
	t.Helper()
	tudo := todoOAudit(t, b) + todasAsPendentes(t, b)
	for _, s := range saidas {
		tudo += mustJSON(t, s)
	}
	require.NotContains(t, tudo, segredoDoWebhook, "o segredo do webhook não pode aparecer em lugar nenhum")
	require.NotContains(t, strings.ToLower(tudo), "key_hash")
}

// carteiraDeClientes cria usuários viewer para os cenários de login:
//   - soAlvo: só o cliente alvo; comOutroAtivo: alvo + um cliente ativo;
//   - comOutroInativo: alvo + um cliente já desativado; desativado: usuário inativo.
type carteiraDeClientes struct {
	alvo, outroAtivo, outroInativo                     uuid.UUID
	soAlvo, comOutroAtivo, comOutroInativo, desativado string
}

func semearCarteiraDeClientes(t *testing.T, b *bancadaTool, alvo uuid.UUID) carteiraDeClientes {
	t.Helper()
	c := carteiraDeClientes{alvo: alvo}
	c.outroAtivo = mcptest.Cliente(t, b.ctx, b.pool, "Outro ativo")
	c.outroInativo = mcptest.Cliente(t, b.ctx, b.pool, "Outro inativo")
	desativarClienteNoBanco(t, b, c.outroInativo)
	novo := func(quem string, ativo bool, carteira ...uuid.UUID) string {
		u := novoUsuarioDe(t, b, "viewer", ativo, carteira...)
		nome := "Pessoa " + quem
		_, err := b.pool.Exec(b.ctx, `UPDATE users SET name = $2 WHERE id = $1`, u.ID, nome)
		require.NoError(t, err)
		return nome
	}
	c.soAlvo = novo("Ana", true, alvo)
	c.comOutroAtivo = novo("Bia", true, alvo, c.outroAtivo)
	c.comOutroInativo = novo("Caio", true, alvo, c.outroInativo)
	c.desativado = novo("Davi", false, alvo)
	return c
}

// ═══════════════════════════════════════════════════════════════════════
// desativar_cliente
// ═══════════════════════════════════════════════════════════════════════

func TestDesativarCliente_PedidoNaoEscreveEAprovarDesativa(t *testing.T) {
	b := novaBancadaTool(t)
	r := semearClienteComRastro(t, b, "Cliente Alvo")
	cart := semearCarteiraDeClientes(t, b, r.cli)
	require.NotContains(t, propriedades(esquemaDa(t, b.cfg, toolDesativarCliente)), "confirmacao", "crítico nunca recebe token")
	antes := linhaCompletaDoCliente(t, b, r.cli)

	out := b.pedePendente(t, toolDesativarCliente, argsCliente(r.cli))

	require.Contains(t, out["motivo"], "bloqueia o login dos usuários do cliente")
	require.Equal(t, portalDosClientes, out["links"].(map[string]any)["tela"])
	require.Equal(t, antes, linhaCompletaDoCliente(t, b, r.cli), "o pedido não escreve")
	require.True(t, clienteAtivo(t, b, r.cli))
	pv := out["preview"].(map[string]any)
	require.Contains(t, pv["resumo"], "Cliente Alvo")
	require.Equal(t, []any{map[string]any{"campo": "situação", "de": "ativo", "para": "inativo"}}, pv["mudancas"])
	ef := mustJSON(t, pv["efeitos"])
	require.Contains(t, ef, "2 usuário(s) ativo(s) deixam de entrar")
	require.Contains(t, ef, cart.soAlvo)
	require.Contains(t, ef, cart.comOutroInativo, "o outro cliente também está desativado: ele perde o acesso")
	require.Contains(t, ef, "1 usuário(s) ativo(s) segue(m) entrando por outro cliente ativo")
	require.Contains(t, ef, cart.comOutroAtivo)
	require.NotContains(t, ef, cart.desativado, "usuário já desativado não entra na conta")
	require.Contains(t, ef, "reversível")
	rv := mustJSON(t, pv["ressalvas"])
	require.Contains(t, rv, "8 h")
	require.Contains(t, rv, "chaves de API")
	require.Contains(t, rv, "webhooks")
	require.Contains(t, rv, "monitoramento das campanhas")
	pid := out["pendente_id"].(string)
	exigeSemEscritaNoPedido(t, b, toolDesativarCliente, pid)

	ap := b.aprovar(t, pid)

	require.False(t, clienteAtivo(t, b, r.cli))
	require.Equal(t, contagemComRastro, contarDoCliente(t, b, r.cli), "desativar não apaga nada")
	res := ap.resultadoJSON(t)
	lido := b.chamar(t, "obter_cliente", argsCliente(r.cli))
	require.Equal(t, lido, res, "mesmo formato (e mesmos valores) do obter_cliente")
	require.Equal(t, false, res["cliente"].(map[string]any)["ativo"])
	exigeAuditoriaDeRecurso(t, b, toolDesativarCliente, "client", r.cli, pid)
	require.Equal(t, "executada", b.estadoDaPendente(t, pid))
	exigeSemSegredo(t, b, out, res)
}

func TestDesativarCliente_SemUsuarioAtivoNinguemPerdeAcesso(t *testing.T) {
	b := novaBancadaTool(t)
	cli := mcptest.Cliente(t, b.ctx, b.pool, "Cliente Sem Gente")

	out := b.pedePendente(t, toolDesativarCliente, argsCliente(cli))

	require.Contains(t, mustJSON(t, out["preview"].(map[string]any)["efeitos"]), "não tem usuário ativo")
}

func TestDesativarCliente_AvisaDoHubQuandoTemPonte(t *testing.T) {
	b := novaBancadaTool(t)
	cli := mcptest.Cliente(t, b.ctx, b.pool, "Cliente do Hub")
	sem := mcptest.Cliente(t, b.ctx, b.pool, "Cliente Sem Hub")
	_, err := b.pool.Exec(b.ctx, `UPDATE clients SET hub_id = 'hub-abc123' WHERE id = $1`, cli)
	require.NoError(t, err)

	com := b.pedePendente(t, toolDesativarCliente, argsCliente(cli))
	sem2 := b.pedePendente(t, toolDesativarCliente, argsCliente(sem))

	rv := mustJSON(t, com["preview"].(map[string]any)["ressalvas"])
	require.Contains(t, rv, "hub-abc123")
	require.Contains(t, rv, "client.upsert", "o hub regrava o ativo na próxima edição do cliente lá")
	require.NotContains(t, mustJSON(t, sem2["preview"].(map[string]any)["ressalvas"]), "hub")
}

func TestDesativarCliente_ParidadeComARota(t *testing.T) {
	b := novaBancadaTool(t)
	viaRota := semearClienteComRastro(t, b, "Cliente Gêmeo")
	viaTool := semearClienteComRastro(t, b, "Cliente Gêmeo")
	passante := semearClienteComRastro(t, b, "Cliente Gêmeo")
	h := &handlers.ClientsHandler{Repo: catalog.NewClients(b.pool)}

	chamarHandlerCorpo(t, h.Deactivate, http.MethodPost, "/clients/x/deactivate", nil, map[string]string{"id": viaRota.cli.String()}, b.admin)
	out := b.pedePendente(t, toolDesativarCliente, argsCliente(viaTool.cli))
	b.aprovar(t, out["pendente_id"].(string))

	require.False(t, clienteAtivo(t, b, viaRota.cli))
	require.Equal(t, linhaDoClienteSemIdentidade(t, b, viaRota.cli), linhaDoClienteSemIdentidade(t, b, viaTool.cli), "a mesma linha pela rota e pela tool")
	require.Equal(t, contarDoCliente(t, b, viaRota.cli), contarDoCliente(t, b, viaTool.cli), "desativar não apaga nada, pela rota e pela tool")
	require.True(t, clienteAtivo(t, b, passante.cli), "o outro cliente não é tocado")
}

func TestDesativarCliente_Recusas(t *testing.T) {
	b := novaBancadaTool(t)
	inativo := mcptest.Cliente(t, b.ctx, b.pool, "Já Inativo")
	desativarClienteNoBanco(t, b, inativo)

	for nome, caso := range map[string]struct {
		args   map[string]any
		codigo string
		texto  string
	}{
		"já inativo":   {argsCliente(inativo), "[sem_mudanca]", "já está inativo"},
		"inexistente":  {argsCliente(uuid.New()), "[nao_encontrado]", "cliente"},
		"id em texto":  {map[string]any{"cliente_id": "xyz"}, "[entrada_invalida]", "cliente_id"},
		"id em branco": {map[string]any{"cliente_id": ""}, "[entrada_invalida]", "cliente_id"},
	} {
		texto := b.chamarErro(t, toolDesativarCliente, caso.args)
		require.Contains(t, texto, caso.codigo, nome)
		require.Contains(t, texto, caso.texto, nome)
	}
	require.Zero(t, b.totalAuditorias(t), "recusa não deixa pedido nem auditoria")
	var n int
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT count(*) FROM mcp_acoes_pendentes`).Scan(&n))
	require.Zero(t, n)
}

func TestDesativarCliente_RecusaNaAprovacao(t *testing.T) {
	t.Run("alguém já desativou no meio", func(t *testing.T) {
		b := novaBancadaTool(t)
		cli := mcptest.Cliente(t, b.ctx, b.pool, "Cliente")
		out := b.pedePendente(t, toolDesativarCliente, argsCliente(cli))
		desativarClienteNoBanco(t, b, cli)

		exigeRecusaNaAprovacao(t, b, toolDesativarCliente, out["pendente_id"].(string), "sem_mudanca")
	})
	t.Run("o cliente foi apagado no meio", func(t *testing.T) {
		b := novaBancadaTool(t)
		cli := mcptest.Cliente(t, b.ctx, b.pool, "Cliente")
		out := b.pedePendente(t, toolDesativarCliente, argsCliente(cli))
		_, err := b.pool.Exec(b.ctx, `DELETE FROM clients WHERE id = $1`, cli)
		require.NoError(t, err)

		exigeRecusaNaAprovacao(t, b, toolDesativarCliente, out["pendente_id"].(string), "nao_encontrado")
	})
}

// ═══════════════════════════════════════════════════════════════════════
// ativar_cliente
// ═══════════════════════════════════════════════════════════════════════

func TestAtivarCliente_PedidoNaoEscreveEAprovarAtiva(t *testing.T) {
	b := novaBancadaTool(t)
	r := semearClienteComRastro(t, b, "Cliente Suspenso")
	cart := semearCarteiraDeClientes(t, b, r.cli)
	desativarClienteNoBanco(t, b, r.cli)
	require.NotContains(t, propriedades(esquemaDa(t, b.cfg, toolAtivarCliente)), "confirmacao", "crítico nunca recebe token")
	antes := linhaCompletaDoCliente(t, b, r.cli)

	out := b.pedePendente(t, toolAtivarCliente, argsCliente(r.cli))

	require.Contains(t, out["motivo"], "o cliente volta a entrar")
	require.Equal(t, portalDosClientes, out["links"].(map[string]any)["tela"])
	require.Equal(t, antes, linhaCompletaDoCliente(t, b, r.cli), "o pedido não escreve")
	pv := out["preview"].(map[string]any)
	require.Contains(t, pv["resumo"], "Cliente Suspenso")
	require.Equal(t, []any{map[string]any{"campo": "situação", "de": "inativo", "para": "ativo"}}, pv["mudancas"])
	ef := mustJSON(t, pv["efeitos"])
	require.Contains(t, ef, "2 usuário(s) ativo(s) voltam a poder entrar")
	require.Contains(t, ef, cart.soAlvo)
	pid := out["pendente_id"].(string)
	exigeSemEscritaNoPedido(t, b, toolAtivarCliente, pid)

	ap := b.aprovar(t, pid)

	require.True(t, clienteAtivo(t, b, r.cli))
	res := ap.resultadoJSON(t)
	require.Equal(t, b.chamar(t, "obter_cliente", argsCliente(r.cli)), res, "mesmo formato do obter_cliente")
	require.Equal(t, true, res["cliente"].(map[string]any)["ativo"])
	exigeAuditoriaDeRecurso(t, b, toolAtivarCliente, "client", r.cli, pid)
	require.Equal(t, "executada", b.estadoDaPendente(t, pid))
	exigeSemSegredo(t, b, out, res)
}

// Quem já entrava por outro cliente ativo não "volta": o preview separa as duas
// contas em vez de somar tudo.
func TestAtivarCliente_SeparaQuemVoltaDeQuemJaEntrava(t *testing.T) {
	b := novaBancadaTool(t)
	cli := mcptest.Cliente(t, b.ctx, b.pool, "Cliente Suspenso")
	cart := semearCarteiraDeClientes(t, b, cli)
	desativarClienteNoBanco(t, b, cli)

	out := b.pedePendente(t, toolAtivarCliente, argsCliente(cli))

	ef := mustJSON(t, out["preview"].(map[string]any)["efeitos"])
	require.Contains(t, ef, "2 usuário(s) ativo(s) voltam a poder entrar")
	require.Contains(t, ef, cart.soAlvo)
	require.Contains(t, ef, cart.comOutroInativo)
	require.Contains(t, ef, "1 usuário(s) ativo(s) já entrava(m) por outro cliente ativo")
	require.Contains(t, ef, cart.comOutroAtivo)
}

func TestAtivarCliente_ParidadeComARota(t *testing.T) {
	b := novaBancadaTool(t)
	viaRota := semearClienteComRastro(t, b, "Cliente Gêmeo")
	viaTool := semearClienteComRastro(t, b, "Cliente Gêmeo")
	passante := semearClienteComRastro(t, b, "Cliente Gêmeo")
	for _, c := range []uuid.UUID{viaRota.cli, viaTool.cli, passante.cli} {
		desativarClienteNoBanco(t, b, c)
	}
	h := &handlers.ClientsHandler{Repo: catalog.NewClients(b.pool)}

	chamarHandlerCorpo(t, h.Activate, http.MethodPost, "/clients/x/activate", nil, map[string]string{"id": viaRota.cli.String()}, b.admin)
	out := b.pedePendente(t, toolAtivarCliente, argsCliente(viaTool.cli))
	b.aprovar(t, out["pendente_id"].(string))

	require.True(t, clienteAtivo(t, b, viaRota.cli))
	require.Equal(t, linhaDoClienteSemIdentidade(t, b, viaRota.cli), linhaDoClienteSemIdentidade(t, b, viaTool.cli))
	require.False(t, clienteAtivo(t, b, passante.cli), "o outro cliente não é tocado")
}

func TestAtivarCliente_Recusas(t *testing.T) {
	b := novaBancadaTool(t)
	ativo := mcptest.Cliente(t, b.ctx, b.pool, "Já Ativo")

	for nome, caso := range map[string]struct {
		args   map[string]any
		codigo string
		texto  string
	}{
		"já ativo":     {argsCliente(ativo), "[sem_mudanca]", "já está ativo"},
		"inexistente":  {argsCliente(uuid.New()), "[nao_encontrado]", "cliente"},
		"id em texto":  {map[string]any{"cliente_id": "xyz"}, "[entrada_invalida]", "cliente_id"},
		"id em branco": {map[string]any{"cliente_id": ""}, "[entrada_invalida]", "cliente_id"},
	} {
		texto := b.chamarErro(t, toolAtivarCliente, caso.args)
		require.Contains(t, texto, caso.codigo, nome)
		require.Contains(t, texto, caso.texto, nome)
	}
	require.Zero(t, b.totalAuditorias(t))
}

func TestAtivarCliente_RecusaNaAprovacao(t *testing.T) {
	t.Run("alguém já ativou no meio", func(t *testing.T) {
		b := novaBancadaTool(t)
		cli := mcptest.Cliente(t, b.ctx, b.pool, "Cliente")
		desativarClienteNoBanco(t, b, cli)
		out := b.pedePendente(t, toolAtivarCliente, argsCliente(cli))
		_, err := b.pool.Exec(b.ctx, `UPDATE clients SET is_active = true WHERE id = $1`, cli)
		require.NoError(t, err)

		exigeRecusaNaAprovacao(t, b, toolAtivarCliente, out["pendente_id"].(string), "sem_mudanca")
	})
	t.Run("o cliente foi apagado no meio", func(t *testing.T) {
		b := novaBancadaTool(t)
		cli := mcptest.Cliente(t, b.ctx, b.pool, "Cliente")
		desativarClienteNoBanco(t, b, cli)
		out := b.pedePendente(t, toolAtivarCliente, argsCliente(cli))
		_, err := b.pool.Exec(b.ctx, `DELETE FROM clients WHERE id = $1`, cli)
		require.NoError(t, err)

		exigeRecusaNaAprovacao(t, b, toolAtivarCliente, out["pendente_id"].(string), "nao_encontrado")
	})
}

// ═══════════════════════════════════════════════════════════════════════
// apagar_cliente
// ═══════════════════════════════════════════════════════════════════════

func TestApagarCliente_PedidoNaoEscreveEAprovarApaga(t *testing.T) {
	b := novaBancadaTool(t)
	r := semearClienteComRastro(t, b, "Cliente Para Apagar")
	outro := semearClienteComRastro(t, b, "Cliente Que Fica")
	require.NotContains(t, propriedades(esquemaDa(t, b.cfg, toolApagarCliente)), "confirmacao", "crítico nunca recebe token")
	antes := linhaCompletaDoCliente(t, b, r.cli)
	require.Equal(t, contagemComRastro, contarDoCliente(t, b, r.cli))

	out := b.pedePendente(t, toolApagarCliente, argsCliente(r.cli))

	require.Contains(t, out["motivo"], "irreversível")
	require.Equal(t, portalDosClientes, out["links"].(map[string]any)["tela"])
	require.Equal(t, antes, linhaCompletaDoCliente(t, b, r.cli), "o pedido não escreve")
	require.Equal(t, contagemComRastro, contarDoCliente(t, b, r.cli), "o pedido não apaga nada")
	pv := out["preview"].(map[string]any)
	require.Contains(t, pv["resumo"], "Cliente Para Apagar")
	require.Equal(t, []any{map[string]any{"campo": "cliente", "de": "Cliente Para Apagar", "para": "apagado"}}, pv["mudancas"])
	ef := mustJSON(t, pv["efeitos"])
	require.Contains(t, ef, "sem volta")
	require.Contains(t, ef, "chaves de API: 2 (1 ativa)")
	require.Contains(t, ef, "entregas de webhook: 2")
	require.Contains(t, ef, "falhas de webhook: 1")
	require.Contains(t, ef, "PMM no target: 1 emissora")
	require.Contains(t, ef, "pós-vendas: 2 (1 enviado)")
	require.Contains(t, ef, "configuração de webhook", "a URL e o segredo moram na linha do cliente")
	require.Contains(t, ef, "deixam de funcionar", "a chave ativa para de autenticar")
	require.Contains(t, ef, "/pos-venda", "o link do pós-venda enviado deixa de abrir")
	pid := out["pendente_id"].(string)
	exigeSemEscritaNoPedido(t, b, toolApagarCliente, pid)

	ap := b.aprovar(t, pid)

	require.Equal(t, contagemCli{}, contarDoCliente(t, b, r.cli), "o cliente e tudo que é só dele somem")
	require.Equal(t, contagemComRastro, contarDoCliente(t, b, outro.cli), "o outro cliente não é tocado")
	res := ap.resultadoJSON(t)
	require.Equal(t, map[string]any{"apagado": map[string]any{"id": r.cli.String(), "nome": "Cliente Para Apagar"}}, res)
	exigeAuditoriaDeRecurso(t, b, toolApagarCliente, "client", r.cli, pid)
	require.Equal(t, "executada", b.estadoDaPendente(t, pid))
	require.Contains(t, b.chamarErro(t, "obter_cliente", argsCliente(r.cli)), "nao_encontrado", "apagado de verdade")
	exigeSemSegredo(t, b, out, res)
}

func TestApagarCliente_SemRastroDizQueNaoVaiNada(t *testing.T) {
	b := novaBancadaTool(t)
	cli := mcptest.Cliente(t, b.ctx, b.pool, "Cliente Vazio")

	out := b.pedePendente(t, toolApagarCliente, argsCliente(cli))

	ef := mustJSON(t, out["preview"].(map[string]any)["efeitos"])
	require.Contains(t, ef, "chaves de API: 0")
	require.Contains(t, ef, "pós-vendas: 0")
	require.NotContains(t, ef, "deixam de funcionar", "sem chave ativa não há o que avisar")
	require.NotContains(t, ef, "configuração de webhook")
}

func TestApagarCliente_AvisaDoHubQuandoTemPonte(t *testing.T) {
	b := novaBancadaTool(t)
	cli := mcptest.Cliente(t, b.ctx, b.pool, "Cliente do Hub")
	_, err := b.pool.Exec(b.ctx, `UPDATE clients SET hub_id = 'hub-zzz999' WHERE id = $1`, cli)
	require.NoError(t, err)

	out := b.pedePendente(t, toolApagarCliente, argsCliente(cli))

	rv := mustJSON(t, out["preview"].(map[string]any)["ressalvas"])
	require.Contains(t, rv, "hub-zzz999")
	require.Contains(t, rv, "não é avisado")
	require.Contains(t, rv, "client.upsert", "o hub recria o cliente na próxima edição dele lá")
}

func TestApagarCliente_ParidadeComARota(t *testing.T) {
	b := novaBancadaTool(t)
	viaRota := semearClienteComRastro(t, b, "Cliente Gêmeo")
	viaTool := semearClienteComRastro(t, b, "Cliente Gêmeo")
	passante := semearClienteComRastro(t, b, "Cliente Gêmeo")
	h := &handlers.ClientsHandler{Repo: catalog.NewClients(b.pool)}

	chamarHandlerCorpo(t, h.Delete, http.MethodDelete, "/clients/x", nil, map[string]string{"id": viaRota.cli.String()}, b.admin)
	out := b.pedePendente(t, toolApagarCliente, argsCliente(viaTool.cli))
	b.aprovar(t, out["pendente_id"].(string))

	require.Equal(t, contagemCli{}, contarDoCliente(t, b, viaRota.cli))
	require.Equal(t, contarDoCliente(t, b, viaRota.cli), contarDoCliente(t, b, viaTool.cli), "as mesmas linhas somem pela rota e pela tool")
	require.Equal(t, contagemComRastro, contarDoCliente(t, b, passante.cli))
}

// vinculosDoCliente cria o vínculo pedido e devolve uma descrição para o nome do caso.
func vinculoDoCliente(t *testing.T, b *bancadaTool, cli uuid.UUID, tipo string) {
	t.Helper()
	switch tipo {
	case "campanha":
		mcptest.Campanha(t, b.ctx, b.pool, cli, "Campanha do vínculo", "2026-10-01", "2026-12-31")
	case "material":
		mcptest.Material(t, b.ctx, b.pool, cli, mcptest.TipoMaterial(t, b.ctx, b.pool, "Spot"), "Spot do vínculo")
	case "usuário":
		novoUsuarioDe(t, b, "viewer", true, cli)
	case "usuário desativado":
		novoUsuarioDe(t, b, "viewer", false, cli)
	case "usuário apagado":
		u := novoUsuarioDe(t, b, "viewer", true, cli)
		_, err := b.pool.Exec(b.ctx, `UPDATE users SET deleted_at = now(), is_active = false WHERE id = $1`, u.ID)
		require.NoError(t, err)
	default:
		t.Fatalf("vínculo desconhecido: %s", tipo)
	}
}

var vinculosQueBloqueiam = []string{"campanha", "material", "usuário", "usuário desativado", "usuário apagado"}

func TestApagarCliente_RecusaNoPedidoQuemTemVinculos(t *testing.T) {
	for _, tipo := range vinculosQueBloqueiam {
		t.Run(tipo, func(t *testing.T) {
			b := novaBancadaTool(t)
			r := semearClienteComRastro(t, b, "Cliente Com Vínculo")
			vinculoDoCliente(t, b, r.cli, tipo)
			antes := contarDoCliente(t, b, r.cli)

			texto := b.chamarErro(t, toolApagarCliente, argsCliente(r.cli))

			require.Contains(t, texto, "[cliente_com_vinculos]")
			require.Contains(t, texto, "Cliente Com Vínculo")
			require.Contains(t, texto, "desativar_cliente", "o caminho reversível")
			require.Equal(t, antes, contarDoCliente(t, b, r.cli), "nada foi apagado")
			require.Zero(t, b.totalAuditorias(t))
		})
	}
}

func TestApagarCliente_RecusaTrazAsContagens(t *testing.T) {
	b := novaBancadaTool(t)
	cli := mcptest.Cliente(t, b.ctx, b.pool, "Cliente Cheio")
	vinculoDoCliente(t, b, cli, "campanha")
	vinculoDoCliente(t, b, cli, "campanha")
	vinculoDoCliente(t, b, cli, "material")
	vinculoDoCliente(t, b, cli, "usuário")
	vinculoDoCliente(t, b, cli, "usuário apagado")

	texto := b.chamarErro(t, toolApagarCliente, argsCliente(cli))

	require.Contains(t, texto, "2 campanhas")
	require.Contains(t, texto, "1 material")
	require.Contains(t, texto, "2 usuários")
	require.Contains(t, texto, "inativos e apagados", "o usuário apagado ainda segura o vínculo")

	// No singular, sem o "s".
	um := mcptest.Cliente(t, b.ctx, b.pool, "Cliente Com Um")
	vinculoDoCliente(t, b, um, "campanha")
	textoUm := b.chamarErro(t, toolApagarCliente, argsCliente(um))
	require.Contains(t, textoUm, "1 campanha")
	require.NotContains(t, textoUm, "1 campanhas")
}

// Entra vínculo entre o pedido e a aprovação: o Preview rerodado recusa.
func TestApagarCliente_RecusaNaAprovacao(t *testing.T) {
	for _, tipo := range vinculosQueBloqueiam {
		t.Run(tipo, func(t *testing.T) {
			b := novaBancadaTool(t)
			r := semearClienteComRastro(t, b, "Cliente Com Vínculo")
			out := b.pedePendente(t, toolApagarCliente, argsCliente(r.cli))
			vinculoDoCliente(t, b, r.cli, tipo)
			antes := contarDoCliente(t, b, r.cli)

			exigeRecusaNaAprovacao(t, b, toolApagarCliente, out["pendente_id"].(string), "cliente_com_vinculos")

			require.Equal(t, antes, contarDoCliente(t, b, r.cli))
		})
	}
	t.Run("o cliente sumiu no meio", func(t *testing.T) {
		b := novaBancadaTool(t)
		cli := mcptest.Cliente(t, b.ctx, b.pool, "Cliente")
		out := b.pedePendente(t, toolApagarCliente, argsCliente(cli))
		_, err := b.pool.Exec(b.ctx, `DELETE FROM clients WHERE id = $1`, cli)
		require.NoError(t, err)

		exigeRecusaNaAprovacao(t, b, toolApagarCliente, out["pendente_id"].(string), "nao_encontrado")
	})
}

func TestApagarCliente_RecusasDeEntrada(t *testing.T) {
	b := novaBancadaTool(t)
	for nome, caso := range map[string]struct {
		args   map[string]any
		codigo string
	}{
		"inexistente":  {argsCliente(uuid.New()), "[nao_encontrado]"},
		"id inválido":  {map[string]any{"cliente_id": "xyz"}, "[entrada_invalida]"},
		"id em branco": {map[string]any{"cliente_id": ""}, "[entrada_invalida]"},
	} {
		require.Contains(t, b.chamarErro(t, toolApagarCliente, caso.args), caso.codigo, nome)
	}
	require.Zero(t, b.totalAuditorias(t))
}

// ═══════════════════════════════════════════════════════════════════════
// apagar_material
// ═══════════════════════════════════════════════════════════════════════

// cenarioApagarMat: um cliente, duas emissoras, uma campanha e um material SOLTO
// (sem vínculo e sem veiculação) com hashes de fingerprint, um vizinho que o
// aponta como "mais parecido" e outro material de controle.
type cenarioApagarMat struct {
	cli, tipo, e1, camp uuid.UUID
	mat, vizinho        uuid.UUID
}

func semearMaterialSolto(t *testing.T, b *bancadaTool, titulo string) cenarioApagarMat {
	t.Helper()
	c := cenarioApagarMat{cli: mcptest.Cliente(t, b.ctx, b.pool, "Cliente "+uuid.NewString()[:6])}
	c.tipo = mcptest.TipoMaterial(t, b.ctx, b.pool, "Spot")
	c.e1 = mcptest.Emissora(t, b.ctx, b.pool, "Rádio "+uuid.NewString()[:6], 1000)
	c.camp = mcptest.Campanha(t, b.ctx, b.pool, c.cli, "Campanha do material", "2026-10-01", "2026-12-31", c.e1)
	c.mat = mcptest.Material(t, b.ctx, b.pool, c.cli, c.tipo, titulo)
	c.vizinho = mcptest.Material(t, b.ctx, b.pool, c.cli, c.tipo, "Vizinho "+uuid.NewString()[:6])
	_, err := b.pool.Exec(b.ctx, `UPDATE materials SET most_similar_material_id = $1, similarity_score = 0.8 WHERE id = $2`, c.mat, c.vizinho)
	require.NoError(t, err)
	_, err = b.pool.Exec(b.ctx, `INSERT INTO fingerprint_hashes (commercial_id, variant_id, rate_id, hash_value, time_frame) VALUES ($1,0,0,111,1), ($1,0,0,222,2)`, c.mat)
	require.NoError(t, err)
	return c
}

type contagemMat struct{ Material, Hashes, VizinhoApontaPraEle int }

func contarDoMaterial(t *testing.T, b *bancadaTool, c cenarioApagarMat) contagemMat {
	t.Helper()
	var m contagemMat
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT
		(SELECT count(*) FROM materials WHERE id = $1),
		(SELECT count(*) FROM fingerprint_hashes WHERE commercial_id = $1),
		(SELECT count(*) FROM materials WHERE id = $2 AND most_similar_material_id = $1)`, c.mat, c.vizinho).
		Scan(&m.Material, &m.Hashes, &m.VizinhoApontaPraEle))
	return m
}

func linhaCompletaDoMaterial(t *testing.T, b *bancadaTool, id uuid.UUID) string {
	t.Helper()
	var s string
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT COALESCE((SELECT to_jsonb(m)::text FROM materials m WHERE id = $1), 'sumiu')`, id).Scan(&s))
	return s
}

func argsMaterial(id uuid.UUID) map[string]any { return map[string]any{"material_id": id.String()} }

// historicoDoMaterial pendura no material o tipo de histórico pedido.
func historicoDoMaterial(t *testing.T, b *bancadaTool, c cenarioApagarMat, tipo string) {
	t.Helper()
	quando := mcptest.Quando("2026-10-20", "09:00")
	switch tipo {
	case "veiculação":
		mcptest.Veiculacao(t, b.ctx, b.pool, c.camp, c.mat, c.e1, "in_slot", quando)
	case "veiculação retratada":
		v := mcptest.Veiculacao(t, b.ctx, b.pool, c.camp, c.mat, c.e1, "in_slot", quando)
		_, err := b.pool.Exec(b.ctx, `UPDATE detections SET retracted_at = now() WHERE id = $1`, v)
		require.NoError(t, err)
	case "veiculação ignorada":
		v := mcptest.Veiculacao(t, b.ctx, b.pool, c.camp, c.mat, c.e1, "in_slot", quando)
		_, err := b.pool.Exec(b.ctx, `UPDATE detections SET ignored_at = now() WHERE id = $1`, v)
		require.NoError(t, err)
	case "só projeção":
		// A tocada base é de OUTRO material; só a projeção desta campanha cita o nosso.
		v := mcptest.Veiculacao(t, b.ctx, b.pool, c.camp, c.vizinho, c.e1, "in_slot", quando)
		outraCamp := mcptest.Campanha(t, b.ctx, b.pool, c.cli, "Outra campanha", "2026-10-01", "2026-12-31", c.e1)
		mcptest.Projecao(t, b.ctx, b.pool, v, quando, outraCamp, c.mat, "bonus")
	default:
		t.Fatalf("histórico desconhecido: %s", tipo)
	}
}

var historicosDeMaterial = []string{"veiculação", "veiculação retratada", "veiculação ignorada", "só projeção"}

func TestApagarMaterial_PedidoNaoEscreveEAprovarApaga(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearMaterialSolto(t, b, "Spot Para Apagar")
	outro := semearMaterialSolto(t, b, "Spot Que Fica")
	require.NotContains(t, propriedades(esquemaDa(t, b.cfg, toolApagarMaterial)), "confirmacao", "crítico nunca recebe token")
	antes := linhaCompletaDoMaterial(t, b, c.mat)
	require.Equal(t, contagemMat{Material: 1, Hashes: 2, VizinhoApontaPraEle: 1}, contarDoMaterial(t, b, c))

	out := b.pedePendente(t, toolApagarMaterial, argsMaterial(c.mat))

	require.Contains(t, out["motivo"], "irreversível")
	require.Equal(t, portalDosMateriais, out["links"].(map[string]any)["tela"])
	require.Equal(t, antes, linhaCompletaDoMaterial(t, b, c.mat), "o pedido não escreve")
	require.Equal(t, contagemMat{Material: 1, Hashes: 2, VizinhoApontaPraEle: 1}, contarDoMaterial(t, b, c))
	pv := out["preview"].(map[string]any)
	require.Contains(t, pv["resumo"], "Spot Para Apagar")
	require.Equal(t, []any{map[string]any{"campo": "material", "de": "Spot Para Apagar", "para": "apagado"}}, pv["mudancas"])
	ef := mustJSON(t, pv["efeitos"])
	require.Contains(t, ef, "sem volta")
	require.Contains(t, ef, "1 outro(s) material(is) do cliente apontava(m) para este como o mais parecido")
	rv := mustJSON(t, pv["ressalvas"])
	require.Contains(t, rv, "arquivo master")
	require.Contains(t, rv, "hashes")
	require.Contains(t, rv, "ficam no servidor")
	pid := out["pendente_id"].(string)
	exigeSemEscritaNoPedido(t, b, toolApagarMaterial, pid)

	ap := b.aprovar(t, pid)

	require.Equal(t, contagemMat{Material: 0, Hashes: 2, VizinhoApontaPraEle: 0}, contarDoMaterial(t, b, c),
		"o material some, os hashes FICAM e o ponteiro do vizinho é zerado (SET NULL)")
	require.Equal(t, 1, contarDoMaterial(t, b, outro).Material, "o outro material não é tocado")
	res := ap.resultadoJSON(t)
	require.Equal(t, map[string]any{"apagado": map[string]any{"id": c.mat.String(), "nome": "Spot Para Apagar"}}, res)
	exigeAuditoriaDeRecurso(t, b, toolApagarMaterial, "material", c.mat, pid)
	require.Equal(t, "executada", b.estadoDaPendente(t, pid))
	require.Contains(t, b.chamarErro(t, "obter_material", argsMaterial(c.mat)), "nao_encontrado", "apagado de verdade")
	exigeSemSegredo(t, b, out, res)
}

// Fingerprint ainda em andamento: o worker pode gravar hashes depois do DELETE.
func TestApagarMaterial_AvisaDoFingerprintEmAndamento(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearMaterialSolto(t, b, "Spot Recém-subido") // o default da coluna é pending
	pronto := semearMaterialSolto(t, b, "Spot Pronto")
	_, err := b.pool.Exec(b.ctx, `UPDATE materials SET fingerprint_status = 'ready' WHERE id = $1`, pronto.mat)
	require.NoError(t, err)

	pendente := b.pedePendente(t, toolApagarMaterial, argsMaterial(c.mat))
	feito := b.pedePendente(t, toolApagarMaterial, argsMaterial(pronto.mat))

	require.Contains(t, mustJSON(t, pendente["preview"].(map[string]any)["ressalvas"]), "fingerprint do material ainda não terminou (pending)")
	require.NotContains(t, mustJSON(t, feito["preview"].(map[string]any)["ressalvas"]), "ainda não terminou")
}

func TestApagarMaterial_ParidadeComARota(t *testing.T) {
	b := novaBancadaTool(t)
	viaRota := semearMaterialSolto(t, b, "Spot Gêmeo")
	viaTool := semearMaterialSolto(t, b, "Spot Gêmeo")
	passante := semearMaterialSolto(t, b, "Spot Gêmeo")
	h := &handlers.MaterialsHandler{Repo: catalog.NewMaterials(b.pool)}

	chamarHandlerCorpo(t, h.Delete, http.MethodDelete, "/materials/x", nil, map[string]string{"id": viaRota.mat.String()}, b.admin)
	out := b.pedePendente(t, toolApagarMaterial, argsMaterial(viaTool.mat))
	b.aprovar(t, out["pendente_id"].(string))

	require.Equal(t, contagemMat{Material: 0, Hashes: 2}, contarDoMaterial(t, b, viaRota))
	require.Equal(t, contarDoMaterial(t, b, viaRota), contarDoMaterial(t, b, viaTool), "as mesmas linhas somem (e as mesmas ficam) pela rota e pela tool")
	require.Equal(t, contagemMat{Material: 1, Hashes: 2, VizinhoApontaPraEle: 1}, contarDoMaterial(t, b, passante))
}

func TestApagarMaterial_RecusaNoPedidoQuemEstaVinculado(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearMaterialSolto(t, b, "Spot Vinculado")
	segunda := mcptest.Campanha(t, b.ctx, b.pool, c.cli, "Segunda Campanha", "2026-11-01", "2026-12-31", c.e1)
	mcptest.Vincular(t, b.ctx, b.pool, c.camp, c.mat, c.e1)
	mcptest.Vincular(t, b.ctx, b.pool, segunda, c.mat, c.e1)
	mcptest.EstadoCampanha(t, b.ctx, b.pool, segunda, "ativa")
	antes := contarDoMaterial(t, b, c)

	texto := b.chamarErro(t, toolApagarMaterial, argsMaterial(c.mat))

	require.Contains(t, texto, "[material_vinculado]")
	require.Contains(t, texto, "2 campanhas")
	require.Contains(t, texto, "Campanha do material", "lista as campanhas pelo nome")
	require.Contains(t, texto, "Segunda Campanha")
	require.Contains(t, texto, "desvincular_material", "o caminho: desvincular primeiro")
	require.Equal(t, antes, contarDoMaterial(t, b, c), "nada foi apagado")
	require.Zero(t, b.totalAuditorias(t))
}

func TestApagarMaterial_RecusaNoPedidoQuemTemHistorico(t *testing.T) {
	for _, tipo := range historicosDeMaterial {
		t.Run(tipo, func(t *testing.T) {
			b := novaBancadaTool(t)
			c := semearMaterialSolto(t, b, "Spot Com Histórico")
			historicoDoMaterial(t, b, c, tipo)
			antes := contarDoMaterial(t, b, c)

			texto := b.chamarErro(t, toolApagarMaterial, argsMaterial(c.mat))

			require.Contains(t, texto, "[material_com_historico]")
			require.Contains(t, texto, "Spot Com Histórico")
			require.Equal(t, antes, contarDoMaterial(t, b, c), "nada foi apagado")
			require.Zero(t, b.totalAuditorias(t))
		})
	}
}

// Vinculado E com histórico (o caso comum de um material que já foi ao ar): a
// recusa que vale é a do histórico, que não se resolve desvinculando.
func TestApagarMaterial_VinculadoComHistoricoRecusaPeloHistorico(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearMaterialSolto(t, b, "Spot No Ar")
	mcptest.Vincular(t, b.ctx, b.pool, c.camp, c.mat, c.e1)
	historicoDoMaterial(t, b, c, "veiculação")

	texto := b.chamarErro(t, toolApagarMaterial, argsMaterial(c.mat))

	require.Contains(t, texto, "[material_com_historico]")
	require.Contains(t, texto, "vinculado", "avisa que também está vinculado")
}

// O que a rota faz hoje com um material que tem histórico: apaga em silêncio
// (detections.commercial_id não tem FK) e deixa as tocadas apontando para um id
// que não existe mais. É por isso que o MCP é mais estrito que a rota.
func TestApagarMaterial_RotaApagaMaterialComHistoricoEmSilencio(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearMaterialSolto(t, b, "Spot Com Histórico")
	historicoDoMaterial(t, b, c, "veiculação") // sem vínculo: só o histórico
	h := &handlers.MaterialsHandler{Repo: catalog.NewMaterials(b.pool)}

	chamarHandlerCorpo(t, h.Delete, http.MethodDelete, "/materials/x", nil, map[string]string{"id": c.mat.String()}, b.admin)

	require.Zero(t, contarDoMaterial(t, b, c).Material, "a rota apaga")
	var orfas int
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT count(*) FROM detections WHERE commercial_id = $1`, c.mat).Scan(&orfas))
	require.Equal(t, 1, orfas, "e a veiculação fica apontando para um material que não existe mais")
}

// Material vinculado: a rota recusa (409), como o MCP.
func TestApagarMaterial_RotaTambemRecusaVinculado(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearMaterialSolto(t, b, "Spot Vinculado")
	mcptest.Vincular(t, b.ctx, b.pool, c.camp, c.mat, c.e1)
	h := &handlers.MaterialsHandler{Repo: catalog.NewMaterials(b.pool)}

	req := httptest.NewRequest(http.MethodDelete, "/materials/x", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", c.mat.String())
	ctx := auth.WithClaims(req.Context(), &auth.Claims{UserID: b.admin, Role: "admin"})
	req = req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	h.Delete(rec, req)

	require.Equal(t, http.StatusConflict, rec.Code)
	require.Equal(t, 1, contarDoMaterial(t, b, c).Material)
}

// Entra vínculo ou histórico entre o pedido e a aprovação: o Preview rerodado recusa.
func TestApagarMaterial_RecusaNaAprovacao(t *testing.T) {
	t.Run("foi vinculado a uma campanha no meio", func(t *testing.T) {
		b := novaBancadaTool(t)
		c := semearMaterialSolto(t, b, "Spot")
		out := b.pedePendente(t, toolApagarMaterial, argsMaterial(c.mat))
		mcptest.Vincular(t, b.ctx, b.pool, c.camp, c.mat, c.e1)
		antes := contarDoMaterial(t, b, c)

		exigeRecusaNaAprovacao(t, b, toolApagarMaterial, out["pendente_id"].(string), "material_vinculado")

		require.Equal(t, antes, contarDoMaterial(t, b, c))
	})
	for _, tipo := range historicosDeMaterial {
		t.Run("ganhou histórico no meio: "+tipo, func(t *testing.T) {
			b := novaBancadaTool(t)
			c := semearMaterialSolto(t, b, "Spot")
			out := b.pedePendente(t, toolApagarMaterial, argsMaterial(c.mat))
			historicoDoMaterial(t, b, c, tipo)
			antes := contarDoMaterial(t, b, c)

			exigeRecusaNaAprovacao(t, b, toolApagarMaterial, out["pendente_id"].(string), "material_com_historico")

			require.Equal(t, antes, contarDoMaterial(t, b, c))
		})
	}
	t.Run("o material sumiu no meio", func(t *testing.T) {
		b := novaBancadaTool(t)
		c := semearMaterialSolto(t, b, "Spot")
		out := b.pedePendente(t, toolApagarMaterial, argsMaterial(c.mat))
		_, err := b.pool.Exec(b.ctx, `DELETE FROM materials WHERE id = $1`, c.mat)
		require.NoError(t, err)

		exigeRecusaNaAprovacao(t, b, toolApagarMaterial, out["pendente_id"].(string), "nao_encontrado")
	})
}

func TestApagarMaterial_RecusasDeEntrada(t *testing.T) {
	b := novaBancadaTool(t)
	for nome, caso := range map[string]struct {
		args   map[string]any
		codigo string
	}{
		"inexistente":  {argsMaterial(uuid.New()), "[nao_encontrado]"},
		"id inválido":  {map[string]any{"material_id": "xyz"}, "[entrada_invalida]"},
		"id em branco": {map[string]any{"material_id": ""}, "[entrada_invalida]"},
	} {
		require.Contains(t, b.chamarErro(t, toolApagarMaterial, caso.args), caso.codigo, nome)
	}
	require.Zero(t, b.totalAuditorias(t))
}

// ── regras que valem para as quatro ─────────────────────────────────────

func TestCriticasDeClienteEMaterial_DescricaoPedeAprovacaoENuncaInventarIds(t *testing.T) {
	vistas := 0
	for _, tl := range listarToolsDoServidor(t) {
		switch tl.Name {
		case toolDesativarCliente, toolAtivarCliente, toolApagarCliente, toolApagarMaterial:
		default:
			continue
		}
		vistas++
		require.Contains(t, tl.Description, "/pendentes", tl.Name)
		require.Contains(t, tl.Description, "aprovar_em", tl.Name)
		require.Contains(t, tl.Description, "nada aconteceu ainda", tl.Name)
		require.Contains(t, tl.Description, "Nunca invente ids", tl.Name)
		require.True(t, strings.HasSuffix(tl.Description, " Nível: critico."), tl.Name)
	}
	require.Equal(t, 4, vistas)
}

// A descrição diz o que a tool faz, quando usar e a armadilha — o que o modelo lê
// antes de pedir.
func TestCriticasDeClienteEMaterial_DescricaoDizAArmadilha(t *testing.T) {
	trechos := map[string][]string{
		toolDesativarCliente: {"login", "8 h", "chaves de API", "webhooks", "sem_mudanca", "ativar_cliente", "obter_cliente"},
		toolAtivarCliente:    {"volta a entrar", "sem_mudanca", "desativar_cliente", "obter_cliente"},
		toolApagarCliente:    {"irreversível", "cliente_com_vinculos", "desativar_cliente", "chaves de API", "webhook", "PMM", "pós-venda"},
		toolApagarMaterial:   {"irreversível", "material_vinculado", "material_com_historico", "desvincular_material", "arquivo master", "hashes"},
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
	require.Equal(t, 4, vistas)
}

// Repetir o pedido não o duplica, e aprovar duas vezes executa uma só.
func TestCriticasDeClienteEMaterial_AprovarDuasVezesNaoExecutaDuasVezes(t *testing.T) {
	b := novaBancadaTool(t)
	cli := mcptest.Cliente(t, b.ctx, b.pool, "Cliente")
	out := b.pedePendente(t, toolDesativarCliente, argsCliente(cli))
	repetido := b.pedePendente(t, toolDesativarCliente, argsCliente(cli))
	require.Equal(t, out["pendente_id"], repetido["pendente_id"])
	require.Equal(t, true, repetido["ja_existia"])
	require.True(t, clienteAtivo(t, b, cli), "nenhuma chamada executa")

	b.aprovar(t, out["pendente_id"].(string))
	_, err := b.tentarAprovar(t, out["pendente_id"].(string))

	require.Error(t, err, "o pedido já foi executado")
	require.Len(t, b.auditoria(t, "mcp.escrita."+toolDesativarCliente), 1)
}
