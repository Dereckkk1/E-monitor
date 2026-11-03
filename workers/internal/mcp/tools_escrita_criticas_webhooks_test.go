package mcp

// Task 19 (Fatia 4): editar_webhook_cliente e testar_webhook. As duas são
// `critico`: a chamada NUNCA executa, grava um pedido pendente, e quem executa é a
// aprovação do dono em /pendentes (b.aprovar, sobre a lista REAL das escritas).
// Cada tool prova: (1) o pedido não escreve nem enfileira, (2) aprovar executa e
// devolve o formato do obter_webhook_cliente (ou {enfileirado:true,…}), (3) o
// efeito é o da rota HTTP, (4) cada recusa no pedido E na aprovação, (5) a
// auditoria com meta.mcp.pendente_id, (6) o segredo do webhook (e o caminho da URL
// antiga, que pode carregar credencial) não aparecem em lugar nenhum que o MCP
// devolve ou grava.
//
// As regras de URL rodam com RADIOCHECK_ENV vazio (produção: https obrigatório e
// guarda de SSRF ligada); um caso roda com `development` + loopback. As URLs de
// teste usam IP público literal (8.8.8.8) para a guarda de SSRF não depender de DNS.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"radiocheck/internal/acoes"
	"radiocheck/internal/api/handlers"
	"radiocheck/internal/catalog"
	"radiocheck/internal/mcp/mcptest"
	"radiocheck/internal/webhook"
)

const (
	toolEditarWebhookCliente = "editar_webhook_cliente"
	toolTestarWebhook        = "testar_webhook"

	// O que a fixture grava de um webhook que já existia. O caminho e a query da
	// URL antiga podem carregar a credencial do receptor (Slack, Discord, Zapier):
	// nada disso pode sair pelo MCP, nem o segredo do webhook.
	urlAntigaDoWebhook = "https://hooks.antigo.example/services/T0/B0/CAMINHOSECRETO?token=TOKENNAQUERY"
	urlNovaDoWebhook   = "https://8.8.8.8/hooks/destino-novo"
)

// ── fixtures e leitura ──────────────────────────────────────────────────

// semearWebhook cria um cliente e grava a configuração de webhook na linha dele.
// url "" = sem URL; segredo "" = sem segredo; eventos nil = o padrão do banco.
func semearWebhook(t *testing.T, b *bancadaTool, nome, url, segredo string, ativo bool, eventos []string) uuid.UUID {
	t.Helper()
	cli := mcptest.Cliente(t, b.ctx, b.pool, nome)
	var u, s *string
	if url != "" {
		u = &url
	}
	if segredo != "" {
		s = &segredo
	}
	_, err := b.pool.Exec(b.ctx, `UPDATE clients SET webhook_url = $2, webhook_secret = $3, webhook_enabled = $4 WHERE id = $1`, cli, u, s, ativo)
	require.NoError(t, err)
	if eventos != nil {
		_, err = b.pool.Exec(b.ctx, `UPDATE clients SET webhook_events = $2 WHERE id = $1`, cli, eventos)
		require.NoError(t, err)
	}
	return cli
}

// webhookComTudo é o cliente que o teste de edição altera: URL com credencial no
// caminho, segredo, ligado, só detection.confirmed e 2 entregas na fila.
func webhookComTudo(t *testing.T, b *bancadaTool, nome string) uuid.UUID {
	t.Helper()
	cli := semearWebhook(t, b, nome, urlAntigaDoWebhook, segredoDoWebhook, true, []string{"detection.confirmed"})
	_, err := b.pool.Exec(b.ctx, `INSERT INTO webhook_deliveries (client_id, event_type, payload) VALUES ($1, 'detection.confirmed', '{}'), ($1, 'detection.confirmed', '{}')`, cli)
	require.NoError(t, err)
	return cli
}

// linhaDoWebhook é a configuração gravada, com o segredo (para provar que ele não
// mudou): o que a rota e a tool precisam deixar IGUAL entre dois clientes gêmeos.
func linhaDoWebhook(t *testing.T, b *bancadaTool, cli uuid.UUID) string {
	t.Helper()
	var s string
	require.NoError(t, b.pool.QueryRow(b.ctx, `
		SELECT COALESCE((SELECT COALESCE(webhook_url, '-') || '|' || COALESCE(webhook_secret, '-') || '|' || webhook_enabled::text || '|' ||
		                        COALESCE(array_to_string(webhook_events, ','), '-')
		                   FROM clients WHERE id = $1), 'sumiu')`, cli).Scan(&s))
	return s
}

// entregasDo conta as linhas da outbox do cliente, por tipo ("" = todas).
func entregasDo(t *testing.T, b *bancadaTool, cli uuid.UUID, tipo string) int {
	t.Helper()
	var n int
	require.NoError(t, b.pool.QueryRow(b.ctx,
		`SELECT count(*) FROM webhook_deliveries WHERE client_id = $1 AND ($2 = '' OR event_type = $2)`, cli, tipo).Scan(&n))
	return n
}

// entregasDeTesteSemIdentidade: o que o webhook.test enfileirado tem de igual
// entre a rota e a tool (sem ids nem horários).
func entregasDeTesteSemIdentidade(t *testing.T, b *bancadaTool, cli uuid.UUID) []string {
	t.Helper()
	rows, err := b.pool.Query(b.ctx, `
		SELECT event_type || '|' || status || '|' || attempt_count || '|' || (payload->>'type') || '|' ||
		       COALESCE(payload->'data'->>'message', '-') || '|' || COALESCE(payload->'data'->>'environment', '-')
		  FROM webhook_deliveries WHERE client_id = $1 AND event_type = 'webhook.test' ORDER BY created_at, id`, cli)
	require.NoError(t, err)
	defer rows.Close()
	var out []string
	for rows.Next() {
		var l string
		require.NoError(t, rows.Scan(&l))
		out = append(out, l)
	}
	require.NoError(t, rows.Err())
	return out
}

// outboxGravadora é a outbox real (a linha em webhook_deliveries) que registra
// cada Enqueue; Err programa a falha da fila.
type outboxGravadora struct {
	real *webhook.Outbox
	Err  error

	mu      sync.Mutex
	eventos []webhook.Event
}

func (o *outboxGravadora) Enqueue(ctx context.Context, ev *webhook.Event) error {
	o.mu.Lock()
	o.eventos = append(o.eventos, *ev)
	o.mu.Unlock()
	if o.Err != nil {
		return o.Err
	}
	return o.real.Enqueue(ctx, ev)
}

func (o *outboxGravadora) chamadas() []webhook.Event {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]webhook.Event(nil), o.eventos...)
}

// ligarWebhooks põe a outbox no serviço da bancada (a bancada nasce sem ela, como
// a instalação sem fila de webhook).
func ligarWebhooks(b *bancadaTool) *outboxGravadora {
	o := &outboxGravadora{real: webhook.NewOutbox(b.pool, catalog.NewClients(b.pool), zap.NewNop())}
	b.E.Acoes.Webhooks = o
	return o
}

// emProducao: RADIOCHECK_ENV vazio, o comportamento de produção (https obrigatório,
// guarda de SSRF ligada).
func emProducao(t *testing.T) { t.Setenv("RADIOCHECK_ENV", "") }

// rotaDeWebhook monta as rotas HTTP reais de configuração e teste.
func rotaDeWebhook(b *bancadaTool) chi.Router {
	clients := catalog.NewClients(b.pool)
	h := handlers.NewWebhooksHandler(b.pool, clients, webhook.NewOutbox(b.pool, clients, zap.NewNop()))
	r := chi.NewRouter()
	r.Patch("/clients/{id}/webhook", h.PatchConfig)
	r.Post("/clients/{id}/webhook-test", h.SendTest)
	return r
}

func chamarRotaDeWebhook(r chi.Router, metodo, alvo, corpo string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(metodo, alvo, strings.NewReader(corpo)))
	return rec
}

func mapaDoJSON(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(s), &m), s)
	return m
}

func argsEditarWebhook(cli uuid.UUID, extra map[string]any) map[string]any {
	a := argsCliente(cli)
	for k, v := range extra {
		a[k] = v
	}
	return a
}

// exigeSemCredencialDaUrlAntiga: nada do que a URL antiga tinha de sensível sai
// pelo MCP (o preview só mostra esquema+host dela), junto do segredo do webhook.
func exigeSemCredencialDaUrlAntiga(t *testing.T, b *bancadaTool, saidas ...any) {
	t.Helper()
	exigeSemSegredo(t, b, saidas...)
	tudo := todoOAudit(t, b) + todasAsPendentes(t, b)
	for _, s := range saidas {
		tudo += mustJSON(t, s)
	}
	require.NotContains(t, tudo, "CAMINHOSECRETO", "o caminho da URL antiga pode carregar a credencial do receptor")
	require.NotContains(t, tudo, "TOKENNAQUERY", "a query da URL antiga pode carregar a credencial do receptor")
}

// ═══════════════════════════════════════════════════════════════════════
// editar_webhook_cliente
// ═══════════════════════════════════════════════════════════════════════

func TestEditarWebhook_PedidoNaoEscreveEAprovarEdita(t *testing.T) {
	emProducao(t)
	b := novaBancadaTool(t)
	outbox := ligarWebhooks(b)
	cli := webhookComTudo(t, b, "Cliente do Webhook")
	props := propriedades(esquemaDa(t, b.cfg, toolEditarWebhookCliente))
	require.NotContains(t, props, "confirmacao", "crítico nunca recebe token")
	require.ElementsMatch(t, []string{"cliente_id", "url", "ativo", "eventos"}, keysDe(props), "sem campo de segredo")
	antes := linhaCompletaDoCliente(t, b, cli)
	args := argsEditarWebhook(cli, map[string]any{
		"url": urlNovaDoWebhook, "ativo": false, "eventos": []string{"detection.confirmed", "webhook.test"}})

	out := b.pedePendente(t, toolEditarWebhookCliente, args)

	require.Contains(t, out["motivo"], "dado do cliente passa a sair para outro sistema")
	require.Equal(t, portalDosClientes, out["links"].(map[string]any)["tela"])
	require.Contains(t, out["aprovar_em"], "/pendentes?id="+out["pendente_id"].(string))
	require.Contains(t, out["aviso"], "Nada aconteceu ainda")
	require.Equal(t, antes, linhaCompletaDoCliente(t, b, cli), "o pedido não escreve")
	require.Empty(t, outbox.chamadas())
	pv := out["preview"].(map[string]any)
	require.Contains(t, pv["resumo"], "Cliente do Webhook")
	require.Equal(t, []any{
		map[string]any{"campo": "url", "de": "https://hooks.antigo.example/…", "para": urlNovaDoWebhook},
		map[string]any{"campo": "ativo", "de": true, "para": false},
		map[string]any{"campo": "eventos", "de": []any{"detection.confirmed"}, "para": []any{"detection.confirmed", "webhook.test"}},
	}, pv["mudancas"])
	ef := mustJSON(t, pv["efeitos"])
	require.Contains(t, ef, urlNovaDoWebhook, "o preview mostra a URL inteira: a pessoa aprova o destino")
	require.Contains(t, ef, "dado de veiculação")
	require.Contains(t, ef, "Há entregas na fila de envio", "o preview diz que há fila, não quantas (o número mudaria a cada minuto)")
	require.Contains(t, ef, "DESLIGADO")
	require.Contains(t, ef, "Passa a receber: webhook.test")
	rv := mustJSON(t, pv["ressalvas"])
	require.Contains(t, rv, "gravada por inteiro")
	require.Contains(t, rv, "testar_webhook")
	require.NotContains(t, rv, "não são assinadas", "o cliente tem segredo")
	pid := out["pendente_id"].(string)
	exigeSemEscritaNoPedido(t, b, toolEditarWebhookCliente, pid)

	ap := b.aprovar(t, pid)

	require.Equal(t, urlNovaDoWebhook+"|"+segredoDoWebhook+"|false|detection.confirmed,webhook.test", linhaDoWebhook(t, b, cli),
		"url, ativo e eventos mudam; o segredo fica como estava")
	res := ap.resultadoJSON(t)
	require.Equal(t, b.chamar(t, "obter_webhook_cliente", argsCliente(cli)), res, "mesmo formato (e mesmos valores) do obter_webhook_cliente")
	require.Equal(t, "https://8.8.8.8/…", res["url"], "o resultado mostra só esquema+host, como a leitura")
	require.Equal(t, false, res["ativo"])
	require.Equal(t, true, res["tem_segredo"])
	exigeAuditoriaDeRecurso(t, b, toolEditarWebhookCliente, "client", cli, pid)
	require.Equal(t, "executada", b.estadoDaPendente(t, pid))
	require.Empty(t, outbox.chamadas(), "editar não enfileira nada")
	require.Equal(t, 2, entregasDo(t, b, cli, ""), "as 2 entregas da fila seguem lá")
	exigeSemCredencialDaUrlAntiga(t, b, out, res)
}

// Só o que foi pedido muda; o resto, segredo inclusive, fica como estava.
func TestEditarWebhook_SoOQueFoiPedidoMuda(t *testing.T) {
	emProducao(t)
	for nome, caso := range map[string]struct {
		extra  map[string]any
		depois string
	}{
		"só desliga":    {map[string]any{"ativo": false}, urlAntigaDoWebhook + "|" + segredoDoWebhook + "|false|detection.confirmed"},
		"só os eventos": {map[string]any{"eventos": []string{"*"}}, urlAntigaDoWebhook + "|" + segredoDoWebhook + "|true|*"},
		"só a URL":      {map[string]any{"url": urlNovaDoWebhook}, urlNovaDoWebhook + "|" + segredoDoWebhook + "|true|detection.confirmed"},
		"eventos repetidos e com espaço viram a lista sem repetição": {
			map[string]any{"eventos": []string{" webhook.test", "detection.retracted", "webhook.test"}},
			urlAntigaDoWebhook + "|" + segredoDoWebhook + "|true|webhook.test,detection.retracted"},
	} {
		t.Run(nome, func(t *testing.T) {
			b := novaBancadaTool(t)
			cli := webhookComTudo(t, b, "Cliente")
			out := b.pedePendente(t, toolEditarWebhookCliente, argsEditarWebhook(cli, caso.extra))
			b.aprovar(t, out["pendente_id"].(string))
			require.Equal(t, caso.depois, linhaDoWebhook(t, b, cli))
		})
	}
}

// Cliente sem webhook: a URL é o que cria a configuração. O webhook nasce
// desligado e sem segredo, e o preview avisa das duas coisas.
func TestEditarWebhook_ClienteSemWebhookCriaAConfiguracao(t *testing.T) {
	emProducao(t)
	b := novaBancadaTool(t)
	cli := semearWebhook(t, b, "Cliente Novo", "", "", false, nil)

	out := b.pedePendente(t, toolEditarWebhookCliente, argsEditarWebhook(cli, map[string]any{"url": urlNovaDoWebhook}))

	pv := out["preview"].(map[string]any)
	require.Equal(t, []any{map[string]any{"campo": "url", "de": nil, "para": urlNovaDoWebhook}}, pv["mudancas"], "sem URL antes: de é vazio")
	require.Contains(t, mustJSON(t, pv["efeitos"]), "passa a ter webhook configurado")
	rv := mustJSON(t, pv["ressalvas"])
	require.Contains(t, rv, "não são assinadas", "sem segredo configurado as entregas não são assinadas")
	require.Contains(t, rv, "pela tela", "o segredo se configura pela tela")
	require.Contains(t, rv, "fica desligado", "o webhook nasce desligado")

	ap := b.aprovar(t, out["pendente_id"].(string))

	require.Equal(t, urlNovaDoWebhook+"|-|false|detection.confirmed", linhaDoWebhook(t, b, cli))
	res := ap.resultadoJSON(t)
	require.Equal(t, true, res["configurado"])
	require.Equal(t, false, res["tem_segredo"])
	require.Equal(t, b.chamar(t, "obter_webhook_cliente", argsCliente(cli)), res)
}

func TestEditarWebhook_RessalvaSemSegredoSoQuandoNaoHaSegredo(t *testing.T) {
	emProducao(t)
	b := novaBancadaTool(t)
	sem := semearWebhook(t, b, "Sem Segredo", urlAntigaDoWebhook, "", true, []string{"detection.confirmed"})
	com := semearWebhook(t, b, "Com Segredo", urlAntigaDoWebhook, segredoDoWebhook, true, []string{"detection.confirmed"})

	pSem := b.pedePendente(t, toolEditarWebhookCliente, argsEditarWebhook(sem, map[string]any{"ativo": false}))
	pCom := b.pedePendente(t, toolEditarWebhookCliente, argsEditarWebhook(com, map[string]any{"ativo": false}))

	rv := mustJSON(t, pSem["preview"].(map[string]any)["ressalvas"])
	require.Contains(t, rv, "sem segredo configurado")
	require.Contains(t, rv, "não são assinadas")
	require.Contains(t, rv, "configure pela tela")
	require.NotContains(t, mustJSON(t, pCom["preview"].(map[string]any)["ressalvas"]), "não são assinadas")
}

func TestEditarWebhook_DesligarAvisaDasEntregasQueJaEstaoNaFila(t *testing.T) {
	emProducao(t)
	b := novaBancadaTool(t)
	cli := webhookComTudo(t, b, "Cliente")
	vazio := semearWebhook(t, b, "Cliente Sem Fila", urlAntigaDoWebhook, segredoDoWebhook, true, []string{"detection.confirmed"})

	cheio := b.pedePendente(t, toolEditarWebhookCliente, argsEditarWebhook(cli, map[string]any{"ativo": false}))
	semFila := b.pedePendente(t, toolEditarWebhookCliente, argsEditarWebhook(vazio, map[string]any{"ativo": false}))

	ef := mustJSON(t, cheio["preview"].(map[string]any)["efeitos"])
	require.Contains(t, ef, "Há entregas na fila de envio")
	require.NotContains(t, ef, "2 entrega", "só o \"há\": o número mudaria a cada minuto")
	require.Contains(t, ef, "ainda são enviadas", "desligar impede só novos enfileiramentos")
	require.NotContains(t, mustJSON(t, semFila["preview"].(map[string]any)["efeitos"]), "na fila")
}

func TestEditarWebhook_ParidadeComARota(t *testing.T) {
	emProducao(t)
	b := novaBancadaTool(t)
	outbox := ligarWebhooks(b)
	viaRota := webhookComTudo(t, b, "Cliente Gêmeo")
	viaTool := webhookComTudo(t, b, "Cliente Gêmeo")
	passante := webhookComTudo(t, b, "Cliente Gêmeo")
	r := rotaDeWebhook(b)

	rec := chamarRotaDeWebhook(r, http.MethodPatch, "/clients/"+viaRota.String()+"/webhook",
		`{"webhook_url":"`+urlNovaDoWebhook+`","webhook_enabled":false,"webhook_events":["detection.confirmed","webhook.test"]}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	out := b.pedePendente(t, toolEditarWebhookCliente, argsEditarWebhook(viaTool, map[string]any{
		"url": urlNovaDoWebhook, "ativo": false, "eventos": []string{"detection.confirmed", "webhook.test"}}))
	ap := b.aprovar(t, out["pendente_id"].(string))

	require.Equal(t, urlNovaDoWebhook+"|"+segredoDoWebhook+"|false|detection.confirmed,webhook.test", linhaDoWebhook(t, b, viaRota))
	require.Equal(t, linhaDoWebhook(t, b, viaRota), linhaDoWebhook(t, b, viaTool), "a mesma configuração gravada pela rota e pela tool")
	require.Equal(t, entregasDo(t, b, viaRota, ""), entregasDo(t, b, viaTool, ""), "a fila de entregas fica como estava, pela rota e pela tool")
	require.Empty(t, outbox.chamadas(), "nenhuma das duas enfileira")
	require.Equal(t, urlAntigaDoWebhook+"|"+segredoDoWebhook+"|true|detection.confirmed", linhaDoWebhook(t, b, passante), "o outro cliente não é tocado")
	// O que a rota devolve na tela (DTO) e o que a tool devolve dizem o mesmo.
	dto := mapaDoJSON(t, rec.Body.String())
	res := ap.resultadoJSON(t)
	require.Equal(t, dto["webhook_enabled"], res["ativo"])
	require.Equal(t, dto["webhook_events"], res["eventos"])
	require.Equal(t, dto["has_secret"], res["tem_segredo"])
	require.Equal(t, soEsquemaEHost(dto["webhook_url"].(string)), res["url"])
	require.NotContains(t, rec.Body.String(), segredoDoWebhook, "a rota também só devolve o segredo mascarado")
}

// O segredo do webhook (e o caminho da URL antiga) não aparecem em lugar nenhum
// que o MCP devolve ou grava, e o pedido não tem por onde recebê-lo (F4-11).
func TestEditarWebhook_SegredoNuncaVaza(t *testing.T) {
	emProducao(t)
	b := novaBancadaTool(t)
	cli := webhookComTudo(t, b, "Cliente")
	args := argsEditarWebhook(cli, map[string]any{"url": urlNovaDoWebhook, "ativo": false})

	out := b.pedePendente(t, toolEditarWebhookCliente, args)
	pid := out["pendente_id"].(string)
	respostaDoPedido := mustJSON(t, out)
	pendenteAntes := todasAsPendentes(t, b)

	ap := b.aprovar(t, pid)

	lido := mustJSON(t, b.chamar(t, "obter_webhook_cliente", argsCliente(cli)))
	for nome, texto := range map[string]string{
		"resposta do pedido": respostaDoPedido, "pendente antes da aprovação": pendenteAntes,
		"pendente depois": todasAsPendentes(t, b), "resultado da aprovação": string(ap.ResultadoJSON),
		"obter_webhook_cliente": lido, "audit_log": todoOAudit(t, b), "estado da pendente aprovada": mustJSON(t, ap.Pendente),
	} {
		require.NotContains(t, texto, segredoDoWebhook, "o segredo do webhook vazou em: "+nome)
		require.NotContains(t, texto, "whsec_", "nenhum pedaço do segredo em: "+nome)
		require.NotContains(t, texto, "CAMINHOSECRETO", "o caminho da URL antiga vazou em: "+nome)
		require.NotContains(t, texto, "TOKENNAQUERY", "a query da URL antiga vazou em: "+nome)
	}
	// Os args gravados são os que a pessoa pediu, sem campo de segredo.
	var gravados map[string]any
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT args FROM mcp_acoes_pendentes WHERE id = $1`, pid).Scan(&gravados))
	require.ElementsMatch(t, []string{"cliente_id", "url", "ativo"}, keysDe(gravados))
	audit := b.auditoria(t, "mcp.escrita."+toolEditarWebhookCliente)[0].Meta["args"].(map[string]any)
	require.ElementsMatch(t, []string{"cliente_id", "url", "url_sha256", "ativo"}, keysDe(audit), "a URL vai resumida no audit_log: esquema+host e o hash")
	// O segredo continua exatamente o que era: a tool não o toca nem o relê.
	require.Equal(t, urlNovaDoWebhook+"|"+segredoDoWebhook+"|false|detection.confirmed", linhaDoWebhook(t, b, cli))
}

// O schema não aceita segredo: mandar um é erro de argumento, nada é gravado.
func TestEditarWebhook_NaoAceitaSegredoNosArgumentos(t *testing.T) {
	emProducao(t)
	b := novaBancadaTool(t)
	cli := webhookComTudo(t, b, "Cliente")
	for _, campo := range []string{"segredo", "secret", "webhook_secret", "senha", "token"} {
		t.Run(campo, func(t *testing.T) {
			texto, erro := chamarNoServidor(t, novoServidor(b.cfg, b.p), toolEditarWebhookCliente,
				argsEditarWebhook(cli, map[string]any{"ativo": false, campo: "segredo-com-16-chars!"}))
			require.True(t, erro, texto)
		})
	}
	require.Zero(t, b.totalAuditorias(t))
	require.Equal(t, urlAntigaDoWebhook+"|"+segredoDoWebhook+"|true|detection.confirmed", linhaDoWebhook(t, b, cli))
}

func TestEditarWebhook_RecusasNoPedido(t *testing.T) {
	emProducao(t)
	b := novaBancadaTool(t)
	cli := webhookComTudo(t, b, "Cliente")
	sem := semearWebhook(t, b, "Sem Webhook", "", "", false, nil)
	longa := "https://8.8.8.8/" + strings.Repeat("a", 2001)

	for nome, caso := range map[string]struct {
		args   map[string]any
		codigo string
		texto  string
	}{
		"nada a mudar": {argsCliente(cli), "[entrada_invalida]", "ao menos um"},

		"esquema que não é http":     {argsEditarWebhook(cli, map[string]any{"url": "ftp://8.8.8.8/h"}), "[entrada_invalida]", "url inválida"},
		"sem esquema":                {argsEditarWebhook(cli, map[string]any{"url": "hooks.cliente.com/x"}), "[entrada_invalida]", "url inválida"},
		"sem host":                   {argsEditarWebhook(cli, map[string]any{"url": "https://"}), "[entrada_invalida]", "url inválida"},
		"em branco":                  {argsEditarWebhook(cli, map[string]any{"url": "   "}), "[entrada_invalida]", "ativo=false"},
		"vazia (não há como apagar)": {argsEditarWebhook(cli, map[string]any{"url": ""}), "[entrada_invalida]", "ativo=false"},
		"longa demais":               {argsEditarWebhook(cli, map[string]any{"url": longa}), "[entrada_invalida]", "no máximo 2000"},
		"http em produção":           {argsEditarWebhook(cli, map[string]any{"url": "http://8.8.8.8/h"}), "[entrada_invalida]", "https"},
		"SSRF rede privada":          {argsEditarWebhook(cli, map[string]any{"url": "https://10.0.0.5/h"}), "[entrada_invalida]", "rede interna"},
		"SSRF metadados da nuvem":    {argsEditarWebhook(cli, map[string]any{"url": "https://169.254.169.254/latest/meta-data"}), "[entrada_invalida]", "rede interna"},
		"SSRF loopback":              {argsEditarWebhook(cli, map[string]any{"url": "https://127.0.0.1/h"}), "[entrada_invalida]", "rede interna"},
		"http em loopback (só dev)":  {argsEditarWebhook(cli, map[string]any{"url": "http://127.0.0.1:9000/h"}), "[entrada_invalida]", "https"},
		"URL com usuário e senha":    {argsEditarWebhook(cli, map[string]any{"url": "https://usuario:pw-SECRETO-123@8.8.8.8/h"}), "[entrada_invalida]", "usuário e senha"},
		"URL só com usuário":         {argsEditarWebhook(cli, map[string]any{"url": "https://token-solto@8.8.8.8/h"}), "[entrada_invalida]", "usuário e senha"},

		"eventos vazio":              {argsEditarWebhook(cli, map[string]any{"eventos": []string{}}), "[entrada_invalida]", "ao menos um evento"},
		"evento desconhecido":        {argsEditarWebhook(cli, map[string]any{"eventos": []string{"detection.confirmed", "coisa.rara"}}), "[entrada_invalida]", "coisa.rara"},
		"evento em branco":           {argsEditarWebhook(cli, map[string]any{"eventos": []string{"detection.confirmed", " "}}), "[entrada_invalida]", "evento desconhecido"},
		"evento em maiúsculas":       {argsEditarWebhook(cli, map[string]any{"eventos": []string{"DETECTION.CONFIRMED"}}), "[entrada_invalida]", "evento desconhecido"},
		"cliente inexistente":        {argsEditarWebhook(uuid.New(), map[string]any{"ativo": false}), "[nao_encontrado]", "cliente"},
		"id em texto":                {map[string]any{"cliente_id": "xyz", "ativo": false}, "[entrada_invalida]", "cliente_id"},
		"id em branco":               {map[string]any{"cliente_id": "", "ativo": false}, "[entrada_invalida]", "cliente_id"},
		"sem webhook: só ligar":      {argsEditarWebhook(sem, map[string]any{"ativo": true}), "[webhook_nao_configurado]", "url"},
		"sem webhook: só os eventos": {argsEditarWebhook(sem, map[string]any{"eventos": []string{"*"}}), "[webhook_nao_configurado]", "url"},

		"já está assim: tudo igual": {argsEditarWebhook(cli, map[string]any{
			"url": urlAntigaDoWebhook, "ativo": true, "eventos": []string{"detection.confirmed"}}), "[sem_mudanca]", "nada a fazer"},
		"já está assim: só a URL":   {argsEditarWebhook(cli, map[string]any{"url": urlAntigaDoWebhook}), "[sem_mudanca]", "nada a fazer"},
		"já está assim: só o ativo": {argsEditarWebhook(cli, map[string]any{"ativo": true}), "[sem_mudanca]", "nada a fazer"},
	} {
		texto := b.chamarErro(t, toolEditarWebhookCliente, caso.args)
		require.Contains(t, texto, caso.codigo, nome)
		require.Contains(t, texto, caso.texto, nome)
		require.NotContains(t, texto, "pw-SECRETO-123", nome+": a recusa não repete a senha da URL")
		require.NotContains(t, texto, "token-solto", nome+": a recusa não repete o usuário da URL")
		require.NotContains(t, texto, "CAMINHOSECRETO", nome+": nem a URL antiga")
	}
	require.Zero(t, b.totalAuditorias(t), "recusa não deixa pedido nem auditoria")
	require.Equal(t, urlAntigaDoWebhook+"|"+segredoDoWebhook+"|true|detection.confirmed", linhaDoWebhook(t, b, cli))
	var n int
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT count(*) FROM mcp_acoes_pendentes`).Scan(&n))
	require.Zero(t, n)
}

// Os eventos pedidos na mesma lista que já existe, em outra ordem, não mudam nada.
func TestEditarWebhook_EventosNaOutraOrdemSaoSemMudanca(t *testing.T) {
	emProducao(t)
	b := novaBancadaTool(t)
	cli := semearWebhook(t, b, "Cliente", urlAntigaDoWebhook, segredoDoWebhook, true, []string{"detection.confirmed", "webhook.test"})

	texto := b.chamarErro(t, toolEditarWebhookCliente, argsEditarWebhook(cli, map[string]any{"eventos": []string{"webhook.test", "detection.confirmed"}}))

	require.Contains(t, texto, "[sem_mudanca]")
}

// Parte do pedido já vale: só o que muda entra no preview e é gravado.
func TestEditarWebhook_ParteJaIgualNaoEntraNasMudancas(t *testing.T) {
	emProducao(t)
	b := novaBancadaTool(t)
	cli := webhookComTudo(t, b, "Cliente")

	out := b.pedePendente(t, toolEditarWebhookCliente, argsEditarWebhook(cli, map[string]any{
		"url": urlAntigaDoWebhook, "ativo": false}))

	require.Equal(t, []any{map[string]any{"campo": "ativo", "de": true, "para": false}}, out["preview"].(map[string]any)["mudancas"])
	b.aprovar(t, out["pendente_id"].(string))
	require.Equal(t, urlAntigaDoWebhook+"|"+segredoDoWebhook+"|false|detection.confirmed", linhaDoWebhook(t, b, cli))
}

func TestEditarWebhook_RecusaNaAprovacao(t *testing.T) {
	pedido := func(t *testing.T, extra map[string]any) (*bancadaTool, uuid.UUID, string) {
		t.Helper()
		emProducao(t)
		b := novaBancadaTool(t)
		cli := webhookComTudo(t, b, "Cliente")
		out := b.pedePendente(t, toolEditarWebhookCliente, argsEditarWebhook(cli, extra))
		return b, cli, out["pendente_id"].(string)
	}
	t.Run("alguém já aplicou a mesma mudança no meio", func(t *testing.T) {
		b, cli, pid := pedido(t, map[string]any{"url": urlNovaDoWebhook, "ativo": false})
		_, err := b.pool.Exec(b.ctx, `UPDATE clients SET webhook_url = $2, webhook_enabled = false WHERE id = $1`, cli, urlNovaDoWebhook)
		require.NoError(t, err)

		exigeRecusaNaAprovacao(t, b, toolEditarWebhookCliente, pid, "sem_mudanca")
	})
	t.Run("o webhook foi apagado da linha no meio", func(t *testing.T) {
		b, cli, pid := pedido(t, map[string]any{"ativo": false})
		_, err := b.pool.Exec(b.ctx, `UPDATE clients SET webhook_url = NULL WHERE id = $1`, cli)
		require.NoError(t, err)

		exigeRecusaNaAprovacao(t, b, toolEditarWebhookCliente, pid, "webhook_nao_configurado")
	})
	t.Run("o cliente foi apagado no meio", func(t *testing.T) {
		b, cli, pid := pedido(t, map[string]any{"ativo": false})
		_, err := b.pool.Exec(b.ctx, `DELETE FROM webhook_deliveries WHERE client_id = $1`, cli)
		require.NoError(t, err)
		_, err = b.pool.Exec(b.ctx, `DELETE FROM clients WHERE id = $1`, cli)
		require.NoError(t, err)

		exigeRecusaNaAprovacao(t, b, toolEditarWebhookCliente, pid, "nao_encontrado")
	})
	t.Run("a URL deixou de valer: o ambiente voltou a produção", func(t *testing.T) {
		t.Setenv("RADIOCHECK_ENV", "development")
		b := novaBancadaTool(t)
		cli := webhookComTudo(t, b, "Cliente")
		out := b.pedePendente(t, toolEditarWebhookCliente, argsEditarWebhook(cli, map[string]any{"url": "http://127.0.0.1:9000/hook"}))
		t.Setenv("RADIOCHECK_ENV", "")

		exigeRecusaNaAprovacao(t, b, toolEditarWebhookCliente, out["pendente_id"].(string), "entrada_invalida")
		require.Equal(t, urlAntigaDoWebhook+"|"+segredoDoWebhook+"|true|detection.confirmed", linhaDoWebhook(t, b, cli), "nada foi gravado")
	})
	t.Run("pedido gravado com URL de rede interna (a conferência da aprovação vale sozinha)", func(t *testing.T) {
		emProducao(t)
		b := novaBancadaTool(t)
		cli := webhookComTudo(t, b, "Cliente")
		pid := pendenteDireta(t, b, toolEditarWebhookCliente, argsEditarWebhook(cli, map[string]any{"url": "https://10.0.0.5/h"}))

		exigeRecusaNaAprovacao(t, b, toolEditarWebhookCliente, pid, "entrada_invalida")
	})
	t.Run("pedido gravado com eventos vazio", func(t *testing.T) {
		emProducao(t)
		b := novaBancadaTool(t)
		cli := webhookComTudo(t, b, "Cliente")
		pid := pendenteDireta(t, b, toolEditarWebhookCliente, argsEditarWebhook(cli, map[string]any{"eventos": []string{}}))

		exigeRecusaNaAprovacao(t, b, toolEditarWebhookCliente, pid, "entrada_invalida")
	})
}

// Em desenvolvimento o http vale, mas só em loopback; a guarda de SSRF não bloqueia
// rede privada (o receptor local de teste). Em produção o mesmo pedido é recusado
// (TestEditarWebhook_RecusasNoPedido).
func TestEditarWebhook_DesenvolvimentoAceitaHttpEmLoopback(t *testing.T) {
	t.Setenv("RADIOCHECK_ENV", "development")
	b := novaBancadaTool(t)
	cli := webhookComTudo(t, b, "Cliente")

	out := b.pedePendente(t, toolEditarWebhookCliente, argsEditarWebhook(cli, map[string]any{"url": "http://127.0.0.1:9000/hook"}))
	b.aprovar(t, out["pendente_id"].(string))

	require.Equal(t, "http://127.0.0.1:9000/hook|"+segredoDoWebhook+"|true|detection.confirmed", linhaDoWebhook(t, b, cli))
	// http continua proibido fora do loopback, e a credencial na URL também.
	require.Contains(t, b.chamarErro(t, toolEditarWebhookCliente, argsEditarWebhook(cli, map[string]any{"url": "http://8.8.8.8/h"})), "https")
	require.Contains(t, b.chamarErro(t, toolEditarWebhookCliente, argsEditarWebhook(cli, map[string]any{"url": "http://u:p@127.0.0.1:9000/h"})), "usuário e senha")
}

// O feito de uma escrita já gravada nunca vira erro: com a releitura funcionando o
// resultado é o do obter_webhook_cliente; se ela falha, traz o cliente e manda
// conferir (mesma regra de clienteFeito).
func TestEditarWebhook_FeitoNaoViraErroSeAReleituraFalha(t *testing.T) {
	emProducao(t)
	b := novaBancadaTool(t)
	cli := webhookComTudo(t, b, "Cliente")
	c := &catalog.Client{ID: cli, Name: "Cliente"}

	f := webhookFeito(b.ctx, b.cfg, b.E, c, "Uma ressalva.")
	require.Equal(t, "client", f.Recurso)
	require.Equal(t, cli, *f.RecursoID)
	require.Equal(t, []string{"Uma ressalva."}, f.Ressalvas)
	require.Equal(t, "https://hooks.antigo.example/…", mapaDoJSON(t, mustJSON(t, f.Resultado))["url"])

	cancelado, cancela := context.WithCancel(b.ctx)
	cancela()
	f = webhookFeito(cancelado, b.cfg, b.E, c, "Uma ressalva.")
	require.Equal(t, cli.String(), mapaDoJSON(t, mustJSON(t, f.Resultado))["cliente"].(map[string]any)["id"])
	require.Equal(t, "Uma ressalva.", f.Ressalvas[0])
	require.Contains(t, f.Ressalvas[1], "não consegui reler o webhook")
	require.Contains(t, f.Ressalvas[1], "obter_webhook_cliente")
}

// ═══════════════════════════════════════════════════════════════════════
// testar_webhook
// ═══════════════════════════════════════════════════════════════════════

// webhookQueRecebeOTeste: ligado, assina webhook.test.
func webhookQueRecebeOTeste(t *testing.T, b *bancadaTool, nome string) uuid.UUID {
	t.Helper()
	return semearWebhook(t, b, nome, "https://8.8.8.8/hooks/teste", segredoDoWebhook, true, []string{"detection.confirmed", "webhook.test"})
}

func TestTestarWebhook_PedidoNaoEnfileiraEAprovarEnfileira(t *testing.T) {
	emProducao(t)
	b := novaBancadaTool(t)
	outbox := ligarWebhooks(b)
	cli := webhookQueRecebeOTeste(t, b, "Cliente do Teste")
	require.NotContains(t, propriedades(esquemaDa(t, b.cfg, toolTestarWebhook)), "confirmacao", "crítico nunca recebe token")
	antes := linhaCompletaDoCliente(t, b, cli)

	out := b.pedePendente(t, toolTestarWebhook, argsCliente(cli))

	require.Contains(t, out["motivo"], "manda um evento de teste para o sistema do cliente")
	require.Equal(t, portalDosClientes, out["links"].(map[string]any)["tela"])
	require.Contains(t, out["aprovar_em"], "/pendentes?id="+out["pendente_id"].(string))
	require.Contains(t, out["aviso"], "Nada aconteceu ainda")
	require.Zero(t, entregasDo(t, b, cli, ""), "o pedido não enfileira nada")
	require.Empty(t, outbox.chamadas())
	require.Equal(t, antes, linhaCompletaDoCliente(t, b, cli))
	pv := out["preview"].(map[string]any)
	require.Contains(t, pv["resumo"], "Cliente do Teste")
	require.Contains(t, pv["resumo"], "webhook.test")
	require.Empty(t, pv["mudancas"], "testar não muda a configuração")
	ef := mustJSON(t, pv["efeitos"])
	require.Contains(t, ef, "https://8.8.8.8/…", "o destino aparece como esquema+host, como na leitura")
	require.NotContains(t, ef, "hooks/teste")
	require.Contains(t, ef, "não leva dado de veiculação")
	rv := mustJSON(t, pv["ressalvas"])
	require.Contains(t, rv, "assíncrono")
	require.Contains(t, rv, "listar_entregas_webhook")
	require.NotContains(t, rv, "não são assinadas", "o cliente tem segredo")
	pid := out["pendente_id"].(string)
	exigeSemEscritaNoPedido(t, b, toolTestarWebhook, pid)

	ap := b.aprovar(t, pid)

	require.Equal(t, 1, entregasDo(t, b, cli, "webhook.test"))
	require.Equal(t, 1, entregasDo(t, b, cli, ""))
	ev := outbox.chamadas()
	require.Len(t, ev, 1)
	require.Equal(t, webhook.EventWebhookTest, ev[0].Type)
	require.Equal(t, cli, ev[0].ClientID)
	res := ap.resultadoJSON(t)
	require.Equal(t, true, res["enfileirado"])
	require.Equal(t, cli.String(), res["cliente"].(map[string]any)["id"])
	require.Equal(t, "Cliente do Teste", res["cliente"].(map[string]any)["nome"])
	require.NotContains(t, res, "aviso", "o resultado é só {enfileirado, cliente}: o aviso mora nas ressalvas")
	require.Len(t, ap.Ressalvas, 1)
	require.Contains(t, ap.Ressalvas[0], "assíncrona", "a ressalva da execução é o que a aprovação devolve e grava")
	require.Contains(t, ap.Ressalvas[0], "listar_entregas_webhook")
	require.Equal(t, ap.Ressalvas, ap.Pendente.Ressalvas)
	exigeAuditoriaDeRecurso(t, b, toolTestarWebhook, "client", cli, pid)
	require.Equal(t, "executada", b.estadoDaPendente(t, pid))
	// A entrega enfileirada aparece na leitura que o aviso manda consultar.
	entregas := b.chamar(t, "listar_entregas_webhook", argsCliente(cli))
	require.Equal(t, "webhook.test", itens(t, entregas)[0]["evento"])
	require.Equal(t, "pending", itens(t, entregas)[0]["status"])
	exigeSemSegredo(t, b, out, res)
}

func TestTestarWebhook_ParidadeComARota(t *testing.T) {
	emProducao(t)
	b := novaBancadaTool(t)
	outbox := ligarWebhooks(b)
	viaRota := webhookQueRecebeOTeste(t, b, "Cliente Gêmeo")
	viaTool := webhookQueRecebeOTeste(t, b, "Cliente Gêmeo")
	passante := webhookQueRecebeOTeste(t, b, "Cliente Gêmeo")
	r := rotaDeWebhook(b)

	rec := chamarRotaDeWebhook(r, http.MethodPost, "/clients/"+viaRota.String()+"/webhook-test", "")
	require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
	require.JSONEq(t, `{"queued":true}`, rec.Body.String())
	out := b.pedePendente(t, toolTestarWebhook, argsCliente(viaTool))
	b.aprovar(t, out["pendente_id"].(string))

	rota := entregasDeTesteSemIdentidade(t, b, viaRota)
	require.Equal(t, []string{"webhook.test|pending|0|webhook.test|This is a test webhook from Radiocheck.|internal"}, rota)
	require.Equal(t, rota, entregasDeTesteSemIdentidade(t, b, viaTool), "a mesma linha na outbox pela rota e pela tool")
	require.Equal(t, entregasDo(t, b, viaRota, ""), entregasDo(t, b, viaTool, ""))
	require.Zero(t, entregasDo(t, b, passante, ""), "o outro cliente não é tocado")
	// A chamada à outbox: o mesmo evento, o mesmo corpo (a rota usa a outbox real; a
	// da tool é a gravadora, que repassa a mesma).
	ev := outbox.chamadas()
	require.Len(t, ev, 1)
	body := ev[0].Body.(map[string]any)
	require.Equal(t, "This is a test webhook from Radiocheck.", body["message"])
	require.Equal(t, "internal", body["environment"])
	require.ElementsMatch(t, []string{"message", "sent_at", "environment"}, keysDe(body), "o corpo do teste não leva dado de veiculação")
}

// A rota responde 202 {"queued":true} mesmo quando descarta o teste em silêncio (a
// tela nunca distinguiu); a tool recusa e diz a condição (F4-10).
func TestTestarWebhook_RotaDizSucessoQuandoDescartaEAToolRecusa(t *testing.T) {
	emProducao(t)
	b := novaBancadaTool(t)
	ligarWebhooks(b)
	cli := semearWebhook(t, b, "Desligado", "https://8.8.8.8/h", segredoDoWebhook, false, []string{"webhook.test"})

	rec := chamarRotaDeWebhook(rotaDeWebhook(b), http.MethodPost, "/clients/"+cli.String()+"/webhook-test", "")

	require.Equal(t, http.StatusAccepted, rec.Code)
	require.JSONEq(t, `{"queued":true}`, rec.Body.String())
	require.Zero(t, entregasDo(t, b, cli, ""), "a rota diz 202 e não enfileira nada")
	texto := b.chamarErro(t, toolTestarWebhook, argsCliente(cli))
	require.Contains(t, texto, "[webhook_teste_descartado]")
	require.Contains(t, texto, "desabilitado")
}

func TestTestarWebhook_RecusasNoPedido(t *testing.T) {
	emProducao(t)
	b := novaBancadaTool(t)
	ligarWebhooks(b)
	semUrl := semearWebhook(t, b, "Sem Webhook", "", "", false, nil)
	desligado := semearWebhook(t, b, "Desligado", "https://8.8.8.8/h", segredoDoWebhook, false, []string{"webhook.test"})
	semEvento := semearWebhook(t, b, "Sem o Evento", "https://8.8.8.8/h", segredoDoWebhook, true, []string{"detection.confirmed", "detection.retracted"})
	apenasPadrao := semearWebhook(t, b, "Padrão do Banco", "https://8.8.8.8/h", segredoDoWebhook, true, nil)

	for nome, caso := range map[string]struct {
		args   map[string]any
		codigo string
		texto  []string
	}{
		"sem URL": {argsCliente(semUrl), "[webhook_teste_descartado]",
			[]string{"descartado", "não tem URL de webhook", "editar_webhook_cliente"}},
		"desligado": {argsCliente(desligado), "[webhook_teste_descartado]",
			[]string{"descartado", "desabilitado", "ativo:true"}},
		"não assina webhook.test": {argsCliente(semEvento), "[webhook_teste_descartado]",
			[]string{"descartado", "não assinou o evento webhook.test", "webhook.test (ou *)"}},
		"o padrão do banco (só detection.confirmed) não assina webhook.test": {argsCliente(apenasPadrao), "[webhook_teste_descartado]",
			[]string{"não assinou o evento webhook.test"}},
		"cliente inexistente": {argsCliente(uuid.New()), "[nao_encontrado]", []string{"cliente"}},
		"id em texto":         {map[string]any{"cliente_id": "xyz"}, "[entrada_invalida]", []string{"cliente_id"}},
		"id em branco":        {map[string]any{"cliente_id": ""}, "[entrada_invalida]", []string{"cliente_id"}},
	} {
		texto := b.chamarErro(t, toolTestarWebhook, caso.args)
		require.Contains(t, texto, caso.codigo, nome)
		for _, trecho := range caso.texto {
			require.Contains(t, texto, trecho, nome)
		}
	}
	require.Zero(t, b.totalAuditorias(t), "recusa não deixa pedido nem auditoria")
	var n int
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT count(*) FROM mcp_acoes_pendentes`).Scan(&n))
	require.Zero(t, n)
}

// Filtro de eventos vazio assina tudo, e "*" também: o teste chega.
func TestTestarWebhook_FiltroVazioEAsteriscoAssinam(t *testing.T) {
	emProducao(t)
	b := novaBancadaTool(t)
	ligarWebhooks(b)
	vazio := semearWebhook(t, b, "Filtro Vazio", "https://8.8.8.8/h", segredoDoWebhook, true, []string{})
	todos := semearWebhook(t, b, "Todos", "https://8.8.8.8/h", segredoDoWebhook, true, []string{"*"})

	for _, cli := range []uuid.UUID{vazio, todos} {
		out := b.pedePendente(t, toolTestarWebhook, argsCliente(cli))
		b.aprovar(t, out["pendente_id"].(string))
		require.Equal(t, 1, entregasDo(t, b, cli, "webhook.test"))
	}
}

func TestTestarWebhook_SemSegredoAvisaQueNaoAssina(t *testing.T) {
	emProducao(t)
	b := novaBancadaTool(t)
	ligarWebhooks(b)
	cli := semearWebhook(t, b, "Sem Segredo", "https://8.8.8.8/h", "", true, []string{"webhook.test"})

	out := b.pedePendente(t, toolTestarWebhook, argsCliente(cli))

	rv := mustJSON(t, out["preview"].(map[string]any)["ressalvas"])
	require.Contains(t, rv, "não são assinadas")
	require.Contains(t, rv, "pela tela")
}

func TestTestarWebhook_SemFilaDeWebhookRecusaNoPedido(t *testing.T) {
	emProducao(t)
	b := novaBancadaTool(t) // a bancada nasce sem a outbox
	cli := webhookQueRecebeOTeste(t, b, "Cliente")

	texto := b.chamarErro(t, toolTestarWebhook, argsCliente(cli))

	require.Contains(t, texto, "[webhook_indisponivel]")
	require.Contains(t, texto, "fila de entregas")
	require.Zero(t, b.totalAuditorias(t))
}

func TestTestarWebhook_RecusaNaAprovacao(t *testing.T) {
	pedido := func(t *testing.T) (*bancadaTool, *outboxGravadora, uuid.UUID, string) {
		t.Helper()
		emProducao(t)
		b := novaBancadaTool(t)
		o := ligarWebhooks(b)
		cli := webhookQueRecebeOTeste(t, b, "Cliente")
		out := b.pedePendente(t, toolTestarWebhook, argsCliente(cli))
		return b, o, cli, out["pendente_id"].(string)
	}
	for nome, mexida := range map[string]string{
		"a URL foi tirada no meio":              `UPDATE clients SET webhook_url = NULL WHERE id = $1`,
		"alguém desligou o webhook no meio":     `UPDATE clients SET webhook_enabled = false WHERE id = $1`,
		"alguém tirou webhook.test dos eventos": `UPDATE clients SET webhook_events = ARRAY['detection.confirmed'] WHERE id = $1`,
	} {
		t.Run(nome, func(t *testing.T) {
			b, o, cli, pid := pedido(t)
			_, err := b.pool.Exec(b.ctx, mexida, cli)
			require.NoError(t, err)

			exigeRecusaNaAprovacao(t, b, toolTestarWebhook, pid, "webhook_teste_descartado")
			require.Empty(t, o.chamadas(), "nada foi enfileirado")
			require.Zero(t, entregasDo(t, b, cli, ""))
		})
	}
	t.Run("o cliente foi apagado no meio", func(t *testing.T) {
		b, o, cli, pid := pedido(t)
		_, err := b.pool.Exec(b.ctx, `DELETE FROM clients WHERE id = $1`, cli)
		require.NoError(t, err)

		exigeRecusaNaAprovacao(t, b, toolTestarWebhook, pid, "nao_encontrado")
		require.Empty(t, o.chamadas())
	})
	t.Run("a fila de webhook saiu do ar no meio", func(t *testing.T) {
		b, _, _, pid := pedido(t)
		b.E.Acoes.Webhooks = nil

		exigeRecusaNaAprovacao(t, b, toolTestarWebhook, pid, "webhook_indisponivel")
	})
}

// Falha da fila é erro interno: o pedido fica `falhou`, o texto do erro Go não sai.
func TestTestarWebhook_FalhaDaFilaNaoVazaOErro(t *testing.T) {
	emProducao(t)
	b := novaBancadaTool(t)
	o := ligarWebhooks(b)
	o.Err = errors.New("pq: fila-fora-do-ar-detalhe-interno")
	cli := webhookQueRecebeOTeste(t, b, "Cliente")
	out := b.pedePendente(t, toolTestarWebhook, argsCliente(cli))
	pid := out["pendente_id"].(string)

	_, err := b.tentarAprovar(t, pid)

	require.ErrorIs(t, err, ErrExecucaoInterrompida)
	require.NotContains(t, err.Error(), "fila-fora-do-ar-detalhe-interno")
	require.Equal(t, "falhou", b.estadoDaPendente(t, pid))
	require.Empty(t, b.auditoria(t, "mcp.escrita."+toolTestarWebhook), "não houve escrita")
	require.NotContains(t, todoOAudit(t, b)+todasAsPendentes(t, b), "fila-fora-do-ar-detalhe-interno")
}

// ── regras que valem para as duas ───────────────────────────────────────

func TestCriticasDeWebhook_DescricaoPedeAprovacaoENuncaInventarIds(t *testing.T) {
	vistas := 0
	for _, tl := range listarToolsDoServidor(t) {
		switch tl.Name {
		case toolEditarWebhookCliente, toolTestarWebhook:
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
	require.Equal(t, 2, vistas)
}

// A descrição diz o que a tool faz, quando usar e a armadilha — o que o modelo lê
// antes de pedir.
func TestCriticasDeWebhook_DescricaoDizAArmadilha(t *testing.T) {
	trechos := map[string][]string{
		toolEditarWebhookCliente: {"URL", "https", "eventos", "SUBSTITUI", "segredo", "pela tela", "sem_mudanca", "ativo", "obter_webhook_cliente", "testar_webhook"},
		toolTestarWebhook:        {"webhook.test", "webhook_teste_descartado", "assíncron", "listar_entregas_webhook", "editar_webhook_cliente"},
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
func TestCriticasDeWebhook_AprovarDuasVezesNaoExecutaDuasVezes(t *testing.T) {
	emProducao(t)
	b := novaBancadaTool(t)
	outbox := ligarWebhooks(b)
	cli := webhookQueRecebeOTeste(t, b, "Cliente")
	out := b.pedePendente(t, toolTestarWebhook, argsCliente(cli))
	repetido := b.pedePendente(t, toolTestarWebhook, argsCliente(cli))
	require.Equal(t, out["pendente_id"], repetido["pendente_id"])
	require.Equal(t, true, repetido["ja_existia"])
	require.Empty(t, outbox.chamadas(), "nenhuma chamada executa")

	b.aprovar(t, out["pendente_id"].(string))
	_, err := b.tentarAprovar(t, out["pendente_id"].(string))

	require.Error(t, err, "o pedido já foi executado")
	require.Len(t, outbox.chamadas(), 1)
	require.Len(t, b.auditoria(t, "mcp.escrita."+toolTestarWebhook), 1)
}

// A URL do webhook pode carregar a credencial do receptor (Slack, Discord e Zapier põem o token
// no caminho ou na query). Ela fica INTEIRA nos args do pedido — é o que o Executar grava, e só o
// dono a vê —, mas o audit_log, que todo admin lê por auditoria_mcp, guarda só esquema+host e o
// SHA-256 dela: em TODA linha que leva os args (pedido criado, aprovado, executado, falhou, recusado).
func TestEditarWebhook_UrlNovaNaoVaiInteiraParaOAuditLog(t *testing.T) {
	emProducao(t)
	b := novaBancadaTool(t)
	cli := webhookComTudo(t, b, "Cliente")
	const urlComSegredo = "https://8.8.8.8/services/T0/B0/NOVOCAMINHOSECRETO?token=NOVOTOKENNAQUERY"
	hash := sha256.Sum256([]byte(urlComSegredo))
	hashHex := hex.EncodeToString(hash[:])

	out := b.pedePendente(t, toolEditarWebhookCliente, argsEditarWebhook(cli, map[string]any{"url": urlComSegredo}))
	pid := out["pendente_id"].(string)
	// O pedido guarda a URL inteira: sem ela o Executar não teria o que gravar.
	var gravada string
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT args->>'url' FROM mcp_acoes_pendentes WHERE id = $1`, pid).Scan(&gravada))
	require.Equal(t, urlComSegredo, gravada)
	b.aprovar(t, pid)
	require.Equal(t, urlComSegredo+"|"+segredoDoWebhook+"|true|detection.confirmed", linhaDoWebhook(t, b, cli), "o Executar gravou a URL inteira")

	// recusado pelo dono
	out2 := b.pedePendente(t, toolEditarWebhookCliente, argsEditarWebhook(cli, map[string]any{"url": urlComSegredo + "&v=2"}))
	apr, err := NovasAprovacoes(b.cfg)
	require.NoError(t, err)
	_, err = apr.Recusar(b.ctx, uuid.MustParse(out2["pendente_id"].(string)), b.donoDoPortal())
	require.NoError(t, err)

	// falhou: a configuração já está como o pedido quando ele é aprovado (sem_mudanca)
	urlC := urlComSegredo + "&v=3"
	out3 := b.pedePendente(t, toolEditarWebhookCliente, argsEditarWebhook(cli, map[string]any{"url": urlC}))
	_, err = b.pool.Exec(b.ctx, `UPDATE clients SET webhook_url = $2 WHERE id = $1`, cli, urlC)
	require.NoError(t, err)
	_, err = b.tentarAprovar(t, out3["pendente_id"].(string))
	var rec *acoes.Recusa
	require.ErrorAs(t, err, &rec)

	for _, acao := range []string{"mcp.pendente.criada", "mcp.pendente.aprovada", "mcp.escrita." + toolEditarWebhookCliente,
		"mcp.pendente.executada", "mcp.pendente.recusada", "mcp.pendente.falhou"} {
		linhas := b.auditoria(t, acao)
		require.NotEmpty(t, linhas, acao)
		for _, l := range linhas {
			args := l.Meta["args"].(map[string]any)
			require.Equal(t, "https://8.8.8.8/…", args["url"], acao+": só esquema+host")
			require.Len(t, args["url_sha256"], 64, acao)
			require.Contains(t, args, "cliente_id", acao+": o resto dos args segue como veio")
		}
	}
	exec := b.auditoria(t, "mcp.escrita."+toolEditarWebhookCliente)[0].Meta["args"].(map[string]any)
	require.Equal(t, hashHex, exec["url_sha256"], "o hash é o do texto da URL gravada")

	tudo := todoOAudit(t, b)
	for _, s := range []string{"NOVOCAMINHOSECRETO", "NOVOTOKENNAQUERY", urlComSegredo, "/services/T0"} {
		require.NotContains(t, tudo, s, "a URL nova inteira não pode estar no audit_log")
	}
}

// O hook de auditoria cobre também o texto das ressalvas do pedido: a URL por inteiro mora no
// pedido (que só o dono lê e que NÃO é apagado depois de 24 h: o texto não pode prometer o contrário)
// e no preview que ele mesmo vê.
func TestEditarWebhook_PreviewNaoDizQueAAuditoriaGuardaAUrlInteira(t *testing.T) {
	emProducao(t)
	b := novaBancadaTool(t)
	cli := webhookComTudo(t, b, "Cliente")
	out := b.pedePendente(t, toolEditarWebhookCliente, argsEditarWebhook(cli, map[string]any{"url": urlNovaDoWebhook}))
	rv := mustJSON(t, out["preview"].(map[string]any)["ressalvas"])
	require.Contains(t, rv, "gravada por inteiro neste pedido, visível só para quem pediu")
	require.NotContains(t, rv, "e na auditoria", "a auditoria guarda só esquema, host e o hash")
	require.Contains(t, rv, "a auditoria guarda só esquema, host e um hash")
	require.NotContains(t, rv, "24 h", "a linha do pedido não é apagada: a URL não some em 24 h")
}
