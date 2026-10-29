package mcp

// Task 14: criar_campanha, editar_campanha e editar_emissoras_campanha.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/api/handlers"
	"radiocheck/internal/catalog"
	"radiocheck/internal/hub"
	"radiocheck/internal/mcp/mcptest"
)

// ── fixtures e leitura ──────────────────────────────────────────────────

// cenarioCampEsc: um cliente com três emissoras e uma campanha (E1 e E2 como
// alvo) no estado e nas datas pedidos. E3 está fora da campanha.
type cenarioCampEsc struct {
	cli, e1, e2, e3, camp uuid.UUID
}

func semearCampEsc(t *testing.T, b *bancadaTool, inicio, fim, estado string) cenarioCampEsc {
	t.Helper()
	c := cenarioCampEsc{}
	c.cli = mcptest.Cliente(t, b.ctx, b.pool, "Cliente "+uuid.NewString()[:6])
	c.e1 = mcptest.Emissora(t, b.ctx, b.pool, "Rádio 1", 1000)
	c.e2 = mcptest.Emissora(t, b.ctx, b.pool, "Rádio 2", 1000)
	c.e3 = mcptest.Emissora(t, b.ctx, b.pool, "Rádio 3", 1000)
	c.camp = mcptest.Campanha(t, b.ctx, b.pool, c.cli, "Campanha Escrita", inicio, fim, c.e1, c.e2)
	if estado != "" {
		mcptest.EstadoCampanha(t, b.ctx, b.pool, c.camp, estado)
	}
	return c
}

type linhaCampanha struct {
	Nome, Estado, Inicio, Fim string
	Emissoras                 []uuid.UUID
	Codigo                    *string
}

func lerLinhaCampanha(t *testing.T, b *bancadaTool, id uuid.UUID) linhaCampanha {
	t.Helper()
	var l linhaCampanha
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT name, status, start_date::text, end_date::text, target_stations, hub_code
		FROM campaigns WHERE id = $1`, id).Scan(&l.Nome, &l.Estado, &l.Inicio, &l.Fim, &l.Emissoras, &l.Codigo))
	return l
}

func contaCampanhas(t *testing.T, b *bancadaTool) int {
	t.Helper()
	var n int
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT count(*) FROM campaigns`).Scan(&n))
	return n
}

// posicoes mapeia ids para a posição deles numa lista-base: a paridade entre duas
// fixtures compara POSIÇÕES, porque os ids de cada uma são diferentes.
func posicoes(t *testing.T, lista, base []uuid.UUID) []int {
	t.Helper()
	out := make([]int, len(lista))
	for i, id := range lista {
		out[i] = -1
		for j, b := range base {
			if b == id {
				out[i] = j
			}
		}
		require.GreaterOrEqual(t, out[i], 0, "id fora da lista-base")
	}
	return out
}

func tokensUsados(t *testing.T, b *bancadaTool) int {
	t.Helper()
	var n int
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT count(*) FROM mcp_confirmacoes_usadas`).Scan(&n))
	return n
}

// ids de uma lista de UUID como strings (o formato do JSON da tool).
func strsDe(ids ...uuid.UUID) []any {
	out := make([]any, len(ids))
	for i, id := range ids {
		out[i] = id.String()
	}
	return out
}

func textoDoPreview(t *testing.T, out map[string]any) string {
	t.Helper()
	return mustJSON(t, out["preview"])
}

// campanhaDoResultado é o bloco `campanha` do resultado (o formato do obter_campanha).
func campanhaDoResultado(out map[string]any) map[string]any {
	return out["resultado"].(map[string]any)["campanha"].(map[string]any)
}

// ── criar_campanha ──────────────────────────────────────────────────────

func argsCriarCampanha(cli uuid.UUID, extra map[string]any) map[string]any {
	a := map[string]any{"cliente_id": cli.String(), "nome": "Campanha Nova", "inicio": "2026-12-01", "fim": "2026-12-31"}
	for k, v := range extra {
		a[k] = v
	}
	return a
}

func TestCriarCampanha_PreviewNaoEscreveEConfirmarCria(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	c := semearCampEsc(t, b, "2026-12-01", "2026-12-31", "")
	antes := contaCampanhas(t, b)
	args := argsCriarCampanha(c.cli, map[string]any{"nome": "  Campanha Nova  ", "emissora_ids": strsDe(c.e1, c.e3)})
	require.Contains(t, propriedades(esquemaDa(t, b.cfg, "criar_campanha")), "confirmacao")

	pre := b.preview(t, "criar_campanha", args)

	require.Equal(t, antes, contaCampanhas(t, b), "o preview não escreve")
	require.Empty(t, b.hub.Eventos())
	require.Zero(t, b.totalAuditorias(t))
	texto := textoDoPreview(t, pre)
	require.Contains(t, texto, "Campanha Nova")
	require.Contains(t, texto, "Rádio 1")
	require.Contains(t, texto, "programada")

	out := b.confirmarEExecutar(t, "criar_campanha", args)

	require.Equal(t, antes+1, contaCampanhas(t, b))
	camp := campanhaDoResultado(out)
	id := uuid.MustParse(camp["id"].(string))
	require.Equal(t, "programada", camp["estado"], "nasce programada: o agendador a ativa na data de início")
	require.Equal(t, "Campanha Nova", camp["nome"], "o nome é aparado")
	require.Equal(t, "2026-12-01", camp["inicio"])
	require.EqualValues(t, 2, camp["emissoras_alvo"])
	require.Contains(t, mustJSON(t, out["ressalvas"]), "ativada automaticamente na data de início")
	l := lerLinhaCampanha(t, b, id)
	require.Equal(t, linhaCampanha{Nome: "Campanha Nova", Estado: "programada", Inicio: "2026-12-01", Fim: "2026-12-31",
		Emissoras: []uuid.UUID{c.e1, c.e3}}, l)
	ev := b.hub.EventosDoTipo("campanha.upsert")
	require.Len(t, ev, 1, "a criação avisa o hub")
	b.exigeAuditoria(t, "mcp.escrita.criar_campanha", "campaign", &id)
}

func TestCriarCampanha_ParidadeComARota(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	viaRota := semearCampEsc(t, b, "2026-12-01", "2026-12-31", "")
	viaTool := semearCampEsc(t, b, "2026-12-01", "2026-12-31", "")
	hubRota, emitiu := hubCapturando(t)
	supRota := &mcptest.SupervisorFalso{Campanhas: catalog.NewCampaigns(b.pool)}
	h := &handlers.CampaignsHandler{Repo: catalog.NewCampaigns(b.pool), Supervisor: supRota, Hub: hubRota}

	var criada catalog.Campaign
	require.NoError(t, json.Unmarshal(chamarHandlerCorpo(t, h.Create, http.MethodPost, "/campaigns", map[string]any{
		"client_id": viaRota.cli.String(), "name": "Campanha Paridade",
		"start_date": "2026-12-01T00:00:00Z", "end_date": "2026-12-31T00:00:00Z",
		"target_stations": []string{viaRota.e1.String(), viaRota.e2.String()},
	}, nil, b.admin), &criada))
	out := b.confirmarEExecutar(t, "criar_campanha", map[string]any{
		"cliente_id": viaTool.cli.String(), "nome": "Campanha Paridade", "inicio": "2026-12-01", "fim": "2026-12-31",
		"emissora_ids": strsDe(viaTool.e1, viaTool.e2),
	})
	idTool := uuid.MustParse(campanhaDoResultado(out)["id"].(string))

	esperado, obtido := lerLinhaCampanha(t, b, criada.ID), lerLinhaCampanha(t, b, idTool)
	require.Equal(t, posicoes(t, esperado.Emissoras, []uuid.UUID{viaRota.e1, viaRota.e2}), posicoes(t, obtido.Emissoras, []uuid.UUID{viaTool.e1, viaTool.e2}))
	esperado.Emissoras, obtido.Emissoras = nil, nil
	require.Equal(t, esperado, obtido, "a mesma linha gravada pela rota e pela tool")
	require.Empty(t, supRota.Chamadas(), "criar não mexe no supervisor (o agendador a ativa depois)")
	require.Empty(t, b.sup.Chamadas())

	var env map[string]any
	select {
	case env = <-emitiu:
	case <-time.After(5 * time.Second):
		t.Fatal("a rota não avisou o hub")
	}
	ev := b.hub.EventosDoTipo("campanha.upsert")
	require.Len(t, ev, 1)
	require.Equal(t, "campanha.upsert", env["tipo"])
	require.Equal(t, dadosDoEvento(t, env["dados"], "idNaPlataforma", "idClienteNaPlataforma"), dadosDoEvento(t, ev[0].Dados, "idNaPlataforma", "idClienteNaPlataforma"))
}

func TestCriarCampanha_CodigoDoHub(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	c := semearCampEsc(t, b, "2026-12-01", "2026-12-31", "")
	b.hub.Conferir = func(string) (*hub.CampanhaDoHub, error) {
		return mcptest.CampanhaDoClienteLigado("Camp hub", c.cli), nil
	}
	args := argsCriarCampanha(c.cli, map[string]any{"codigo_hub": "eh-7k4m2x"})

	pre := b.preview(t, "criar_campanha", args)
	require.Contains(t, textoDoPreview(t, pre), "EH-7K4M2X", "o preview mostra o código canônico")
	require.Equal(t, []string{"EH-7K4M2X"}, b.hub.Conferidos())

	out := b.confirmarEExecutar(t, "criar_campanha", args)
	require.Equal(t, "EH-7K4M2X", campanhaDoResultado(out)["codigo_hub"])

	// Código de OUTRO cliente: recusa no preview, antes do token.
	b.hub.Conferir = func(string) (*hub.CampanhaDoHub, error) {
		return mcptest.CampanhaDoClienteLigado("Camp de outro", uuid.New()), nil
	}
	texto := b.chamarErro(t, "criar_campanha", argsCriarCampanha(c.cli, map[string]any{"nome": "Outra", "codigo_hub": "EH-7K4M2X"}))
	require.Contains(t, texto, "[codigo_hub]")
	require.Contains(t, texto, "de outro cliente")
	// Código que nem é código, e código que o hub desconhece.
	b.hub.Conferir = nil
	require.Contains(t, b.chamarErro(t, "criar_campanha", argsCriarCampanha(c.cli, map[string]any{"nome": "Outra", "codigo_hub": "lixo"})), "[codigo_hub]")
	require.Contains(t, b.chamarErro(t, "criar_campanha", argsCriarCampanha(c.cli, map[string]any{"nome": "Outra", "codigo_hub": "EH-7K4M2X"})), "[codigo_hub]")
	require.Equal(t, 2, contaCampanhas(t, b), "só a semeada e a criada")
}

// A barreira do hub é refeita na confirmação: recusa de negócio libera o token.
func TestCriarCampanha_RecusaNaConfirmacaoLiberaOToken(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	c := semearCampEsc(t, b, "2026-12-01", "2026-12-31", "")
	b.hub.Conferir = func(string) (*hub.CampanhaDoHub, error) {
		return mcptest.CampanhaDoClienteLigado("Camp hub", c.cli), nil
	}
	args := argsCriarCampanha(c.cli, map[string]any{"codigo_hub": "EH-7K4M2X"})
	pre := b.preview(t, "criar_campanha", args)
	com := argsCriarCampanha(c.cli, map[string]any{"codigo_hub": "EH-7K4M2X", "confirmacao": pre["confirmacao"]})

	b.hub.Conferir = func(string) (*hub.CampanhaDoHub, error) {
		return mcptest.CampanhaDoClienteLigado("Camp de outro", uuid.New()), nil
	}
	require.Contains(t, b.chamarErro(t, "criar_campanha", com), "[codigo_hub]")
	require.Zero(t, tokensUsados(t, b), "a recusa de negócio devolve o token")
	require.Equal(t, 1, contaCampanhas(t, b))

	b.hub.Conferir = func(string) (*hub.CampanhaDoHub, error) {
		return mcptest.CampanhaDoClienteLigado("Camp hub", c.cli), nil
	}
	out := b.chamar(t, "criar_campanha", com)
	require.Equal(t, true, out["executou"], "o MESMO token volta a valer")
	require.Equal(t, 2, contaCampanhas(t, b))
}

func TestCriarCampanha_Escalada(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b) // hoje = 2026-10-29 (SP)
	c := semearCampEsc(t, b, "2026-12-01", "2026-12-31", "")
	antes := contaCampanhas(t, b)

	// Início hoje e início no passado: o agendador a ativa em ≤ 60 s — barra.
	for nome, inicio := range map[string]string{"hoje": "2026-10-29", "passado": "2026-10-01"} {
		out := b.barrado(t, "criar_campanha", argsCriarCampanha(c.cli, map[string]any{"inicio": inicio, "fim": "2026-12-31"}))
		require.Contains(t, out["motivo"], "ativa a campanha em até 60 s", nome)
		require.Equal(t, "https://portal.teste/campaigns/new", out["links"].(map[string]any)["tela"], nome)
		require.Contains(t, textoDoPreview(t, out), "ativa a campanha em até 60 s", "o preview do ramo barrado diz a verdade: "+nome)
	}
	require.Equal(t, antes, contaCampanhas(t, b), "nada foi criado")
	l := b.auditoria(t, "mcp.escrita.requer_aprovacao")
	require.Len(t, l, 2)
	require.Equal(t, "campaign", l[0].Recurso)
	require.Empty(t, b.auditoria(t, "mcp.escrita.criar_campanha"))
	require.Zero(t, tokensUsados(t, b))

	// Vizinho: início amanhã não escala (o agendador só ativa depois da virada do dia).
	pre := b.chamar(t, "criar_campanha", argsCriarCampanha(c.cli, map[string]any{"inicio": "2026-10-30", "fim": "2026-12-31"}))
	require.NotEmpty(t, pre["confirmacao"])
	require.NotContains(t, pre, "nivel")

	// O "hoje" é o de São Paulo, não o do relógio do processo: 02:00 UTC do dia 30 ainda é dia 29 em SP.
	b.E.Agora = func() time.Time { return time.Date(2026, 10, 30, 2, 0, 0, 0, time.UTC) }
	b.barrado(t, "criar_campanha", argsCriarCampanha(c.cli, map[string]any{"inicio": "2026-10-29", "fim": "2026-12-31"}))
	pre = b.chamar(t, "criar_campanha", argsCriarCampanha(c.cli, map[string]any{"inicio": "2026-10-30", "fim": "2026-12-31"}))
	require.NotEmpty(t, pre["confirmacao"], "em SP ainda é 29/10: iniciar em 30/10 não escala")
}

// F3-2: o tempo passa entre o preview e a confirmação e o início vira "hoje": a
// confirmação barra e o token não é consumido.
func TestCriarCampanha_EscaladaNaConfirmacaoBarraENaoConsomeOToken(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	c := semearCampEsc(t, b, "2026-12-01", "2026-12-31", "")
	args := argsCriarCampanha(c.cli, map[string]any{"inicio": "2026-10-30", "fim": "2026-12-31"})
	pre := b.preview(t, "criar_campanha", args)

	b.E.Agora = func() time.Time { return mcptest.Quando("2026-10-30", "08:00") } // amanhã chegou
	args["confirmacao"] = pre["confirmacao"]
	out := b.chamar(t, "criar_campanha", args)

	require.Equal(t, "critico", out["nivel"])
	require.Equal(t, 1, contaCampanhas(t, b))
	require.Zero(t, tokensUsados(t, b))
}

func TestCriarCampanha_Recusas(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	c := semearCampEsc(t, b, "2026-12-01", "2026-12-31", "")
	muitas := make([]any, 501)
	for i := range muitas {
		muitas[i] = uuid.NewString()
	}
	longo := strings.Repeat("ã", tetoNomeCampanha+1)

	for nome, caso := range map[string]struct {
		extra  map[string]any
		codigo string
	}{
		"cliente inválido":        {map[string]any{"cliente_id": "x"}, "[entrada_invalida]"},
		"cliente inexistente":     {map[string]any{"cliente_id": uuid.NewString()}, "[nao_encontrado]"},
		"nome vazio":              {map[string]any{"nome": ""}, "[entrada_invalida]"},
		"nome em branco":          {map[string]any{"nome": "   "}, "[entrada_invalida]"},
		"nome longo demais":       {map[string]any{"nome": longo}, "[entrada_invalida]"},
		"início inválido":         {map[string]any{"inicio": "01/12/2026"}, "[entrada_invalida]"},
		"início vazio":            {map[string]any{"inicio": ""}, "[entrada_invalida]"},
		"fim inválido":            {map[string]any{"fim": "amanhã"}, "[entrada_invalida]"},
		"fim antes do início":     {map[string]any{"inicio": "2026-12-31", "fim": "2026-12-01"}, "[entrada_invalida]"},
		"data zero":               {map[string]any{"inicio": "0001-01-01", "fim": "2026-12-31"}, "[entrada_invalida]"},
		"ano fora de 2000-2100":   {map[string]any{"fim": "2162-12-31"}, "[entrada_invalida]"},
		"emissora inválida":       {map[string]any{"emissora_ids": []any{"x"}}, "[entrada_invalida]"},
		"emissora inexistente":    {map[string]any{"emissora_ids": strsDe(uuid.New())}, "[nao_encontrado]"},
		"mais de 500 emissoras":   {map[string]any{"emissora_ids": muitas}, "[entrada_invalida]"},
		"código que não é código": {map[string]any{"codigo_hub": "lixo"}, "[codigo_hub]"},
	} {
		texto := b.chamarErro(t, "criar_campanha", argsCriarCampanha(c.cli, caso.extra))
		require.Contains(t, texto, caso.codigo, nome)
	}
	require.Equal(t, 1, contaCampanhas(t, b))
	require.Zero(t, b.totalAuditorias(t))
}

func TestCriarCampanha_RessalvasDoPreview(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	c := semearCampEsc(t, b, "2026-12-01", "2026-12-31", "")

	// Sem emissoras, sem código (hub configurado) e nome igual ao de outra campanha do cliente.
	pre := b.preview(t, "criar_campanha", argsCriarCampanha(c.cli, map[string]any{"nome": "campanha escrita"}))
	r := mustJSON(t, pre["preview"].(map[string]any)["ressalvas"])
	require.Contains(t, r, "Sem emissoras-alvo")
	require.Contains(t, r, "Sem codigo_hub")
	require.Contains(t, r, "já tem uma campanha com este nome")

	// Cliente desativado: a rota permite, o MCP avisa mas não recusa.
	_, err := b.pool.Exec(b.ctx, `UPDATE clients SET is_active = false WHERE id = $1`, c.cli)
	require.NoError(t, err)
	pre = b.preview(t, "criar_campanha", argsCriarCampanha(c.cli, nil))
	require.Contains(t, mustJSON(t, pre["preview"].(map[string]any)["ressalvas"]), "DESATIVADO")
	b.hub.SemConfiguracao = true
	pre = b.preview(t, "criar_campanha", argsCriarCampanha(c.cli, nil))
	require.NotContains(t, mustJSON(t, pre["preview"].(map[string]any)["ressalvas"]), "codigo_hub", "sem hub não há o que ressalvar")
}

// ── editar_campanha ─────────────────────────────────────────────────────

func argsEditarCampanha(camp uuid.UUID, extra map[string]any) map[string]any {
	a := map[string]any{"campanha_id": camp.String()}
	for k, v := range extra {
		a[k] = v
	}
	return a
}

func TestEditarCampanha_RenomearPreviewNaoEscreveEConfirmarGrava(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	c := semearCampEsc(t, b, "2026-10-01", "2026-11-30", "ativa")
	args := argsEditarCampanha(c.camp, map[string]any{"nome": "  Nome Novo  "})
	require.Contains(t, propriedades(esquemaDa(t, b.cfg, "editar_campanha")), "confirmacao")
	antes := lerLinhaCampanha(t, b, c.camp)

	pre := b.preview(t, "editar_campanha", args)

	require.Equal(t, antes, lerLinhaCampanha(t, b, c.camp), "o preview não escreve")
	require.Zero(t, b.totalAuditorias(t))
	mud := pre["preview"].(map[string]any)["mudancas"].([]any)
	require.Len(t, mud, 1, "só o que mudou")
	require.Equal(t, "Campanha Escrita", mud[0].(map[string]any)["de"])
	require.Equal(t, "Nome Novo", mud[0].(map[string]any)["para"])
	require.Contains(t, mustJSON(t, pre["preview"].(map[string]any)["ressalvas"]), "NÃO avisa o hub")

	out := b.confirmarEExecutar(t, "editar_campanha", args)

	depois := lerLinhaCampanha(t, b, c.camp)
	require.Equal(t, "Nome Novo", depois.Nome)
	require.Equal(t, antes.Inicio, depois.Inicio)
	require.Equal(t, antes.Fim, depois.Fim)
	require.Equal(t, "ativa", depois.Estado)
	require.Equal(t, "Nome Novo", campanhaDoResultado(out)["nome"])
	require.Empty(t, b.hub.Eventos(), "renomear não avisa o hub")
	require.Empty(t, b.sup.Chamadas())
	b.exigeAuditoria(t, "mcp.escrita.editar_campanha", "campaign", &c.camp)
}

func TestEditarCampanha_ParidadeComARota(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	viaRota := semearCampEsc(t, b, "2026-10-01", "2026-11-30", "ativa")
	viaTool := semearCampEsc(t, b, "2026-10-01", "2026-11-30", "ativa")
	h := &handlers.CampaignsHandler{Repo: catalog.NewCampaigns(b.pool), Supervisor: &mcptest.SupervisorFalso{}}

	// Renomeia e estende o fim (uma edição que não escala).
	chamarHandlerCorpo(t, h.Update, http.MethodPut, "/campaigns/x", map[string]any{
		"name": "Nome Paridade", "start_date": "2026-10-01T00:00:00Z", "end_date": "2026-12-15T00:00:00Z",
	}, map[string]string{"id": viaRota.camp.String()}, b.admin)
	b.confirmarEExecutar(t, "editar_campanha", argsEditarCampanha(viaTool.camp, map[string]any{"nome": "Nome Paridade", "fim": "2026-12-15"}))

	require.Equal(t, lerLinhaCampanha(t, b, viaRota.camp).Nome, "Nome Paridade")
	rota, tool := lerLinhaCampanha(t, b, viaRota.camp), lerLinhaCampanha(t, b, viaTool.camp)
	rota.Emissoras, tool.Emissoras = nil, nil
	require.Equal(t, rota, tool, "a mesma linha gravada pela rota e pela tool")
	require.Empty(t, b.hub.Eventos())
}

// A tabela das escaladas de F3-4 (a)/(b)/(c) e dos vizinhos que NÃO escalam.
// Hoje = 2026-10-29. Cada linha parte de uma campanha no estado e nas datas dados.
func TestEditarCampanha_Escalada(t *testing.T) {
	type caso struct {
		nome                string
		estado, inicio, fim string
		edicao              map[string]any
		escala              bool
		motivo              string // trecho do motivo, quando escala
	}
	casos := []caso{
		// (a) o estado que as datas dão muda
		{"ativa com início empurrado para o futuro (DEMOTE)", "ativa", "2026-10-01", "2026-11-30", map[string]any{"inicio": "2026-11-05"}, true, "ativa → programada"},
		{"ativa com fim no passado (conclui)", "ativa", "2026-10-01", "2026-11-30", map[string]any{"fim": "2026-10-20"}, true, "ativa → concluida"},
		{"concluida com fim estendido ao futuro (RECOVERY)", "concluida", "2026-09-01", "2026-10-20", map[string]any{"fim": "2026-11-30"}, true, "concluida → ativa"},
		{"programada com início no passado (vira ativa)", "programada", "2026-12-01", "2026-12-31", map[string]any{"inicio": "2026-10-20"}, true, "programada → ativa"},
		// (b) o conjunto de dias passados do período muda (sem mudar o estado)
		{"ativa com início adiantado (mais dias passados)", "ativa", "2026-10-10", "2026-11-30", map[string]any{"inicio": "2026-10-01"}, true, "dias já passados"},
		{"ativa com início atrasado (menos dias passados), ainda ativa", "ativa", "2026-10-01", "2026-11-30", map[string]any{"inicio": "2026-10-15"}, true, "dias já passados"},
		// (a)+(b)
		{"concluida de verdade com fim encurtado", "concluida", "2026-09-01", "2026-10-20", map[string]any{"fim": "2026-10-10"}, true, "dias já passados"},
		// vizinhos que NÃO escalam
		{"ativa só renomeada", "ativa", "2026-10-01", "2026-11-30", map[string]any{"nome": "Outro nome"}, false, ""},
		{"ativa com fim estendido (dias passados iguais)", "ativa", "2026-10-01", "2026-11-30", map[string]any{"fim": "2026-12-15"}, false, ""},
		{"ativa com fim encurtado, ainda no futuro", "ativa", "2026-10-01", "2026-11-30", map[string]any{"fim": "2026-11-10"}, false, ""},
		{"ativa que termina hoje", "ativa", "2026-10-01", "2026-11-30", map[string]any{"fim": "2026-10-29"}, false, ""},
		{"programada com as duas datas no futuro", "programada", "2026-12-01", "2026-12-31", map[string]any{"inicio": "2026-11-15", "fim": "2027-01-15"}, false, ""},
		{"programada que passa a começar hoje (vira ativa)", "programada", "2026-12-01", "2026-12-31", map[string]any{"inicio": "2026-10-29"}, true, "programada → ativa"},
		{"datas iguais às atuais + nome novo", "ativa", "2026-10-01", "2026-11-30", map[string]any{"nome": "Novo", "inicio": "2026-10-01", "fim": "2026-11-30"}, false, ""},
		{"concluida só renomeada", "concluida", "2026-09-01", "2026-10-20", map[string]any{"nome": "Novo"}, false, ""},
	}
	for _, k := range casos {
		t.Run(k.nome, func(t *testing.T) {
			b := novaBancadaTool(t)
			agoraFixa(b)
			c := semearCampEsc(t, b, k.inicio, k.fim, k.estado)
			antes := lerLinhaCampanha(t, b, c.camp)
			args := argsEditarCampanha(c.camp, k.edicao)
			if k.escala {
				out := b.barrado(t, "editar_campanha", args)
				require.Contains(t, out["motivo"], k.motivo)
				require.Equal(t, "https://portal.teste/campaigns/"+c.camp.String()+"/edit", out["links"].(map[string]any)["tela"])
				require.Equal(t, antes, lerLinhaCampanha(t, b, c.camp), "nada foi gravado")
				l := b.auditoria(t, "mcp.escrita.requer_aprovacao")
				require.Len(t, l, 1)
				require.Equal(t, "campaign", l[0].Recurso)
				require.Zero(t, tokensUsados(t, b))
				return
			}
			pre := b.chamar(t, "editar_campanha", args)
			require.NotEmpty(t, pre["confirmacao"], "não escala: pede confirmação")
			require.NotContains(t, pre, "nivel")
		})
	}
}

func TestEditarCampanha_CodigoDoHubSempreEscala(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	c := semearCampEsc(t, b, "2026-12-01", "2026-12-31", "") // programada futura: nenhuma outra condição vale
	b.hub.Conferir = func(string) (*hub.CampanhaDoHub, error) {
		return mcptest.CampanhaDoClienteLigado("Camp hub", c.cli), nil
	}

	out := b.barrado(t, "editar_campanha", argsEditarCampanha(c.camp, map[string]any{"codigo_hub": "eh-7k4m2x"}))
	require.Contains(t, out["motivo"], "código do hub")
	require.Contains(t, textoDoPreview(t, out), "EH-7K4M2X")
	require.Nil(t, lerLinhaCampanha(t, b, c.camp).Codigo, "nada foi gravado")

	// O ramo barrado também confere o código: de outro cliente é recusa, não barra.
	b.hub.Conferir = func(string) (*hub.CampanhaDoHub, error) {
		return mcptest.CampanhaDoClienteLigado("Camp de outro", uuid.New()), nil
	}
	require.Contains(t, b.chamarErro(t, "editar_campanha", argsEditarCampanha(c.camp, map[string]any{"codigo_hub": "EH-7K4M2X"})), "[codigo_hub]")
	require.Contains(t, b.chamarErro(t, "editar_campanha", argsEditarCampanha(c.camp, map[string]any{"codigo_hub": "lixo"})), "[codigo_hub]")

	// Vizinhos: o mesmo código já gravado, e apagar um código que não há, não mudam o código.
	_, err := b.pool.Exec(b.ctx, `UPDATE campaigns SET hub_code = 'EH-7K4M2X' WHERE id = $1`, c.camp)
	require.NoError(t, err)
	pre := b.chamar(t, "editar_campanha", argsEditarCampanha(c.camp, map[string]any{"nome": "Novo", "codigo_hub": "eh7k4m2x"}))
	require.NotEmpty(t, pre["confirmacao"], "o mesmo código, só em outra grafia, não é mudança")
	require.Len(t, pre["preview"].(map[string]any)["mudancas"], 1)

	// Apagar o código que existe escala; e o Executar (que a Fatia 4 vai chamar) avisa o hub.
	out = b.barrado(t, "editar_campanha", argsEditarCampanha(c.camp, map[string]any{"codigo_hub": ""}))
	require.Contains(t, textoDoPreview(t, out), "desliga a coleta")
	d := escritaEditarCampanha(b.cfg)
	apagar := ""
	f, err := d.Executar(b.ctx, b.E, b.p, entradaEditarCampanha{CampanhaID: c.camp.String(), CodigoHub: &apagar})
	require.NoError(t, err)
	require.Nil(t, lerLinhaCampanha(t, b, c.camp).Codigo)
	require.Equal(t, "campaign", f.Recurso)
	ev := b.hub.EventosDoTipo("campanha.upsert")
	require.Len(t, ev, 1, "apagar o código TEM de emitir (é o que desliga a coleta no hub)")
	require.Equal(t, "", dadosDoEvento(t, ev[0].Dados)["hubCode"])
}

// F3-2: o dia passa entre o preview e a confirmação e a edição passa a mexer em dias passados.
func TestEditarCampanha_EscaladaNaConfirmacaoBarraENaoConsomeOToken(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	c := semearCampEsc(t, b, "2026-10-01", "2026-11-30", "ativa")
	args := argsEditarCampanha(c.camp, map[string]any{"fim": "2026-12-15"})
	pre := b.preview(t, "editar_campanha", args) // hoje 29/10: os dias passados (01–28/10) não mudam

	// 05/12: o fim antigo (30/11) já passou; esticá-lo agora acrescenta dias passados (01–04/12).
	b.E.Agora = func() time.Time { return mcptest.Quando("2026-12-05", "08:00") }
	args["confirmacao"] = pre["confirmacao"]
	out := b.chamar(t, "editar_campanha", args)

	require.Equal(t, "critico", out["nivel"])
	require.Equal(t, "2026-11-30", lerLinhaCampanha(t, b, c.camp).Fim)
	require.Zero(t, tokensUsados(t, b))
}

func TestEditarCampanha_Recusas(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	c := semearCampEsc(t, b, "2026-10-01", "2026-11-30", "ativa")
	canc := semearCampEsc(t, b, "2026-10-01", "2026-11-30", "cancelada")

	for nome, caso := range map[string]struct {
		args   map[string]any
		codigo string
	}{
		"id inválido":             {map[string]any{"campanha_id": "x", "nome": "N"}, "[entrada_invalida]"},
		"id inexistente":          {map[string]any{"campanha_id": uuid.NewString(), "nome": "N"}, "[nao_encontrado]"},
		"nada enviado":            {argsEditarCampanha(c.camp, nil), "[entrada_invalida]"},
		"nome vazio":              {argsEditarCampanha(c.camp, map[string]any{"nome": ""}), "[entrada_invalida]"},
		"nome em branco":          {argsEditarCampanha(c.camp, map[string]any{"nome": "  "}), "[entrada_invalida]"},
		"nome longo demais":       {argsEditarCampanha(c.camp, map[string]any{"nome": strings.Repeat("x", 201)}), "[entrada_invalida]"},
		"início inválido":         {argsEditarCampanha(c.camp, map[string]any{"inicio": "2026-13-01"}), "[entrada_invalida]"},
		"fim zero":                {argsEditarCampanha(c.camp, map[string]any{"fim": "0001-01-01"}), "[entrada_invalida]"},
		"início depois do fim":    {argsEditarCampanha(c.camp, map[string]any{"inicio": "2026-12-15"}), "[entrada_invalida]"},
		"fim antes do início":     {argsEditarCampanha(c.camp, map[string]any{"fim": "2026-09-30"}), "[entrada_invalida]"},
		"tudo já como está":       {argsEditarCampanha(c.camp, map[string]any{"nome": "Campanha Escrita", "inicio": "2026-10-01", "fim": "2026-11-30"}), "[entrada_invalida]"},
		"código que não é código": {argsEditarCampanha(c.camp, map[string]any{"codigo_hub": "lixo"}), "[codigo_hub]"},
		"campanha cancelada":      {argsEditarCampanha(canc.camp, map[string]any{"nome": "N"}), "[campanha_cancelada]"},
	} {
		texto := b.chamarErro(t, "editar_campanha", caso.args)
		require.Contains(t, texto, caso.codigo, nome)
	}
	require.Equal(t, "Campanha Escrita", lerLinhaCampanha(t, b, c.camp).Nome)
	require.Zero(t, b.totalAuditorias(t))
}

// ── editar_emissoras_campanha ───────────────────────────────────────────

func argsEmissorasCampanha(camp uuid.UUID, adicionar, remover []uuid.UUID) map[string]any {
	a := map[string]any{"campanha_id": camp.String()}
	if len(adicionar) > 0 {
		a["adicionar"] = strsDe(adicionar...)
	}
	if len(remover) > 0 {
		a["remover"] = strsDe(remover...)
	}
	return a
}

func TestEditarEmissorasCampanha_PreviewNaoEscreveEConfirmarGrava(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearCampEsc(t, b, "2026-10-01", "2026-11-30", "ativa")
	args := argsEmissorasCampanha(c.camp, []uuid.UUID{c.e3}, []uuid.UUID{c.e1})
	require.Contains(t, propriedades(esquemaDa(t, b.cfg, "editar_emissoras_campanha")), "confirmacao")

	pre := b.preview(t, "editar_emissoras_campanha", args)

	require.Equal(t, []uuid.UUID{c.e1, c.e2}, lerLinhaCampanha(t, b, c.camp).Emissoras, "o preview não escreve")
	require.Empty(t, b.sup.Chamadas())
	require.Zero(t, b.totalAuditorias(t))
	texto := textoDoPreview(t, pre)
	require.Contains(t, texto, "Rádio 3")
	require.Contains(t, texto, "Rádio 1")
	require.Contains(t, texto, "os workers das emissoras acrescentadas sobem agora", "campanha ativa: efeito imediato")

	out := b.confirmarEExecutar(t, "editar_emissoras_campanha", args)

	require.Equal(t, []uuid.UUID{c.e2, c.e3}, lerLinhaCampanha(t, b, c.camp).Emissoras, "as demais ficam, a nova entra no fim")
	require.Equal(t, []mcptest.ChamadaSupervisor{{Op: "UpdateStations", Campanha: c.camp, Emissoras: []uuid.UUID{c.e2, c.e3}}}, b.sup.Chamadas())
	require.EqualValues(t, 2, campanhaDoResultado(out)["emissoras_alvo"])
	b.exigeAuditoria(t, "mcp.escrita.editar_emissoras_campanha", "campaign", &c.camp)
}

func TestEditarEmissorasCampanha_ParidadeComARota(t *testing.T) {
	b := novaBancadaTool(t)
	viaRota := semearCampEsc(t, b, "2026-10-01", "2026-11-30", "ativa")
	viaTool := semearCampEsc(t, b, "2026-10-01", "2026-11-30", "ativa")
	supRota := &mcptest.SupervisorFalso{Campanhas: catalog.NewCampaigns(b.pool)}
	h := &handlers.CampaignsHandler{Repo: catalog.NewCampaigns(b.pool), Supervisor: supRota}

	// A rota recebe a lista inteira; a tool, o que acrescenta e o que tira — o mesmo resultado.
	chamarHandlerCorpo(t, h.UpdateStations, http.MethodPut, "/campaigns/x/stations", map[string]any{
		"target_stations": []string{viaRota.e2.String(), viaRota.e3.String()},
	}, map[string]string{"id": viaRota.camp.String()}, b.admin)
	b.confirmarEExecutar(t, "editar_emissoras_campanha", argsEmissorasCampanha(viaTool.camp, []uuid.UUID{viaTool.e3}, []uuid.UUID{viaTool.e1}))

	baseRota, baseTool := []uuid.UUID{viaRota.e1, viaRota.e2, viaRota.e3}, []uuid.UUID{viaTool.e1, viaTool.e2, viaTool.e3}
	require.Equal(t, posicoes(t, lerLinhaCampanha(t, b, viaRota.camp).Emissoras, baseRota), posicoes(t, lerLinhaCampanha(t, b, viaTool.camp).Emissoras, baseTool))
	chRota, chTool := supRota.Chamadas(), b.sup.Chamadas()
	require.Len(t, chRota, 1)
	require.Len(t, chTool, 1)
	require.Equal(t, "UpdateStations", chTool[0].Op)
	require.Equal(t, chRota[0].Op, chTool[0].Op)
	require.Equal(t, posicoes(t, chRota[0].Emissoras, baseRota), posicoes(t, chTool[0].Emissoras, baseTool), "o supervisor recebeu a mesma lista")
}

// Decisão do dono (2026-10-29): tirar emissora COM tocadas passadas é confirma normal, sem escalada. O
// passado fica intacto (TestEditarEmissorasCampanha_ProvaDaRemocao).
func TestEditarEmissorasCampanha_RemoverEmissoraComTocadasPassadasEConfirmaNormal(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearCampEsc(t, b, "2026-10-01", "2026-11-30", "ativa")
	tipo := mcptest.TipoMaterial(t, b.ctx, b.pool, "Spot")
	mat := mcptest.Material(t, b.ctx, b.pool, c.cli, tipo, "Spot")
	mcptest.Vincular(t, b.ctx, b.pool, c.camp, mat, c.e1, c.e2)
	mcptest.Veiculacao(t, b.ctx, b.pool, c.camp, mat, c.e1, "in_slot", mcptest.Quando("2026-10-10", "10:00"))
	mcptest.Veiculacao(t, b.ctx, b.pool, c.camp, mat, c.e1, "bonus", mcptest.Quando("2026-10-10", "11:00"))
	args := argsEmissorasCampanha(c.camp, nil, []uuid.UUID{c.e1})

	// Preview normal: token, sem nível crítico, e o texto lista o que muda daqui para a frente.
	pre := b.preview(t, "editar_emissoras_campanha", args)
	require.NotContains(t, pre, "nivel", "não escala")
	texto := textoDoPreview(t, pre)
	require.Contains(t, texto, "NÃO são recalculados pela remoção")
	require.Contains(t, texto, "lista de PMM no target")
	require.Contains(t, texto, "mapa ao vivo")
	require.Contains(t, texto, "alertas de queda")
	require.Contains(t, texto, "workers das emissoras acrescentadas sobem agora e os das removidas param", "campanha ativa")
	require.NotContains(t, texto, "aprovação humana")
	require.NotContains(t, texto, "reclassificado")
	require.Equal(t, []uuid.UUID{c.e1, c.e2}, lerLinhaCampanha(t, b, c.camp).Emissoras, "o preview não escreve")
	require.Empty(t, b.sup.Chamadas())
	require.Zero(t, b.totalAuditorias(t))

	// Confirmação: executa, grava, mexe no supervisor e audita (sem pedido de aprovação).
	var projAntes string
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT string_agg(detection_id::text || category, ',' ORDER BY detection_id) FROM detection_campaigns`).Scan(&projAntes))
	out := b.confirmarEExecutar(t, "editar_emissoras_campanha", args)
	require.Equal(t, []uuid.UUID{c.e2}, lerLinhaCampanha(t, b, c.camp).Emissoras)
	require.Equal(t, []mcptest.ChamadaSupervisor{{Op: "UpdateStations", Campanha: c.camp, Emissoras: []uuid.UUID{c.e2}}}, b.sup.Chamadas())
	require.EqualValues(t, 1, campanhaDoResultado(out)["emissoras_alvo"])
	b.exigeAuditoria(t, "mcp.escrita.editar_emissoras_campanha", "campaign", &c.camp)
	require.Empty(t, b.auditoria(t, "mcp.escrita.requer_aprovacao"))
	var projDepois string
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT string_agg(detection_id::text || category, ',' ORDER BY detection_id) FROM detection_campaigns`).Scan(&projDepois))
	require.Equal(t, projAntes, projDepois, "as tocadas passadas seguem como estavam")

	// Misturar acrescentar e tirar também é confirma normal.
	pre = b.chamar(t, "editar_emissoras_campanha", argsEmissorasCampanha(c.camp, []uuid.UUID{c.e1, c.e3}, []uuid.UUID{c.e2}))
	require.NotEmpty(t, pre["confirmacao"])
	require.NotContains(t, pre, "nivel")
}

// Entra uma tocada na emissora entre o preview e a confirmação: nada muda, a confirmação executa (o token vale).
func TestEditarEmissorasCampanha_TocadaNovaEntrePreviewEConfirmacaoNaoBloqueia(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearCampEsc(t, b, "2026-10-01", "2026-11-30", "ativa")
	args := argsEmissorasCampanha(c.camp, nil, []uuid.UUID{c.e1})
	pre := b.preview(t, "editar_emissoras_campanha", args)

	tipo := mcptest.TipoMaterial(t, b.ctx, b.pool, "Spot")
	mat := mcptest.Material(t, b.ctx, b.pool, c.cli, tipo, "Spot")
	mcptest.Veiculacao(t, b.ctx, b.pool, c.camp, mat, c.e1, "in_slot", mcptest.Quando("2026-10-10", "10:00"))
	args["confirmacao"] = pre["confirmacao"]
	out := b.chamar(t, "editar_emissoras_campanha", args)

	require.Equal(t, true, out["executou"])
	require.NotContains(t, out, "nivel")
	require.Equal(t, []uuid.UUID{c.e2}, lerLinhaCampanha(t, b, c.camp).Emissoras)
	require.Equal(t, 1, tokensUsados(t, b), "o token foi consumido pela execução")
}

func TestEditarEmissorasCampanha_EfeitosPorEstadoERessalvaDosMateriais(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearCampEsc(t, b, "2026-12-01", "2026-12-31", "") // programada
	pre := b.preview(t, "editar_emissoras_campanha", argsEmissorasCampanha(c.camp, []uuid.UUID{c.e3}, nil))
	require.Contains(t, textoDoPreview(t, pre), "nenhum worker sobe nem para agora")
	require.NotContains(t, mustJSON(t, pre["preview"].(map[string]any)["ressalvas"]), "editar_emissoras_material", "sem material vinculado não há o que ressalvar")

	tipo := mcptest.TipoMaterial(t, b.ctx, b.pool, "Spot")
	mat := mcptest.Material(t, b.ctx, b.pool, c.cli, tipo, "Spot")
	mcptest.Vincular(t, b.ctx, b.pool, c.camp, mat, c.e1, c.e2)
	pre = b.preview(t, "editar_emissoras_campanha", argsEmissorasCampanha(c.camp, []uuid.UUID{c.e3}, nil))
	require.Contains(t, mustJSON(t, pre["preview"].(map[string]any)["ressalvas"]), "NÃO ganham as emissoras acrescentadas")
	out := b.confirmarEExecutar(t, "editar_emissoras_campanha", argsEmissorasCampanha(c.camp, []uuid.UUID{c.e3}, nil))
	require.Contains(t, mustJSON(t, out["ressalvas"]), "editar_emissoras_material")
	require.Zero(t, b.sup.Contagem("Reload", c.camp))

	// Remover tudo é permitido, com ressalva.
	pre = b.preview(t, "editar_emissoras_campanha", argsEmissorasCampanha(c.camp, nil, []uuid.UUID{c.e1, c.e2, c.e3}))
	require.Contains(t, mustJSON(t, pre["preview"].(map[string]any)["ressalvas"]), "SEM emissoras-alvo")
}

func TestEditarEmissorasCampanha_Recusas(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearCampEsc(t, b, "2026-10-01", "2026-11-30", "ativa")
	canc := semearCampEsc(t, b, "2026-10-01", "2026-11-30", "cancelada")
	muitas := make([]any, 501)
	for i := range muitas {
		muitas[i] = uuid.NewString()
	}

	for nome, caso := range map[string]struct {
		args   map[string]any
		codigo string
	}{
		"nenhuma lista":                {argsEmissorasCampanha(c.camp, nil, nil), "[entrada_invalida]"},
		"campanha inválida":            {map[string]any{"campanha_id": "x", "adicionar": strsDe(c.e3)}, "[entrada_invalida]"},
		"campanha inexistente":         {map[string]any{"campanha_id": uuid.NewString(), "adicionar": strsDe(c.e3)}, "[nao_encontrado]"},
		"campanha cancelada":           {argsEmissorasCampanha(canc.camp, []uuid.UUID{canc.e3}, nil), "[campanha_cancelada]"},
		"emissora inválida":            {map[string]any{"campanha_id": c.camp.String(), "adicionar": []any{"x"}}, "[entrada_invalida]"},
		"adicionar inexistente":        {argsEmissorasCampanha(c.camp, []uuid.UUID{uuid.New()}, nil), "[nao_encontrado]"},
		"adicionar já na campanha":     {argsEmissorasCampanha(c.camp, []uuid.UUID{c.e1}, nil), "[entrada_invalida]"},
		"remover fora da campanha":     {argsEmissorasCampanha(c.camp, nil, []uuid.UUID{c.e3}), "[entrada_invalida]"},
		"mesma em adicionar e remover": {argsEmissorasCampanha(c.camp, []uuid.UUID{c.e3}, []uuid.UUID{c.e3}), "[entrada_invalida]"},
		"mais de 500":                  {map[string]any{"campanha_id": c.camp.String(), "adicionar": muitas}, "[entrada_invalida]"},
	} {
		texto := b.chamarErro(t, "editar_emissoras_campanha", caso.args)
		require.Contains(t, texto, caso.codigo, nome)
	}
	require.Equal(t, []uuid.UUID{c.e1, c.e2}, lerLinhaCampanha(t, b, c.camp).Emissoras)
	require.Empty(t, b.sup.Chamadas())
	require.Zero(t, b.totalAuditorias(t))
}

// Campanha cancelada é recusada ANTES de qualquer escalada (a edição abaixo escalaria).
func TestCampanhaCancelada_RecusaAntesDaEscalada(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	c := semearCampEsc(t, b, "2026-10-01", "2026-11-30", "cancelada")
	for nome, args := range map[string]map[string]any{
		"editar_campanha":           argsEditarCampanha(c.camp, map[string]any{"fim": "2026-10-10"}), // escalaria, mas recusa vem antes
		"editar_emissoras_campanha": argsEmissorasCampanha(c.camp, nil, []uuid.UUID{c.e1}),
	} {
		require.Contains(t, b.chamarErro(t, nome, args), "[campanha_cancelada]", nome)
	}
}

// ── Prova da remoção de emissora da campanha ────────────────────────────

// Guarda que JUSTIFICA a decisão do dono (2026-10-29) de tratar editar_emissoras_campanha como confirma
// simples, sem escalada, mesmo ao tirar emissora COM tocadas passadas: tirar a emissora não reescreve o
// passado. Semeia tocadas passadas aprovadas na emissora removida (com regras, preço, PMM no target e um
// carve-out), lê pelas tools tudo o que o cliente vê do passado, tira a emissora PELA PRÓPRIA TOOL
// (preview + confirmação) e relê. Se este teste quebrar, a remoção passou a mexer no histórico e a
// escalada precisa voltar.
//
// RESULTADO (2026-10-29): grade, insights, financeiro, veiculações, planos do dia, falhas da campanha e as
// projeções (detection_campaigns) saem IDÊNTICOS — inclusive depois de editar uma regra (recat). O que
// muda é só a lista de emissoras-alvo (obter_campanha) e a lista de PMM no target do cliente
// (pmm_target_cliente); no código, o mapa ao vivo, a Visão Gerencial e os alertas de queda também leem
// campaigns.target_stations.
func TestEditarEmissorasCampanha_ProvaDaRemocao(t *testing.T) {
	b := novaBancadaTool(t)
	p := semearProva(t, b)
	p.renomearRegra(t, b, p.e2, "Regra do E2 v1") // normaliza as categorias pelo categorizador
	antes := p.capturar(t, b, true)
	projAntes := projecoes(t, b)
	require.Contains(t, antes["listar_veiculacoes"], "in_slot", "a prova não é vazia")
	require.Contains(t, antes["grade_campanha"], `"in_slot":4`)

	lerLista := func() (emissorasAlvo int, nomes string, pmmTotal int) {
		camp := b.chamar(t, "obter_campanha", map[string]any{"campanha_id": p.camp.String()})
		pmm := b.chamar(t, "pmm_target_cliente", map[string]any{"cliente_id": p.cli.String()})
		return int(campanhaDoResultadoDireto(camp)["emissoras_alvo"].(float64)), mustJSON(t, camp["emissoras"]), int(pmm["page"].(map[string]any)["total"].(float64))
	}
	alvoAntes, nomesAntes, pmmAntes := lerLista()
	require.Equal(t, 3, alvoAntes)
	require.Contains(t, nomesAntes, "Rádio Prova 1")
	require.Equal(t, 3, pmmAntes)

	// Remoção PELA TOOL (preview + confirmação): tira a emissora COM tocadas passadas.
	b.confirmarEExecutar(t, "editar_emissoras_campanha", argsEmissorasCampanha(p.camp, nil, []uuid.UUID{p.e1}))
	require.Equal(t, []uuid.UUID{p.e2, p.e3}, lerLinhaCampanha(t, b, p.camp).Emissoras)
	exigeIgual(t, "remover E1 da campanha", antes, p.capturar(t, b, true))
	require.Equal(t, projAntes, projecoes(t, b), "as projeções (detection_campaigns) não mudam")

	// Editar uma regra DEPOIS da remoção: o recat varre o tipo da campanha e não depende da lista de emissoras.
	p.renomearRegra(t, b, p.e2, "Regra do E2 v2")
	exigeIgual(t, "editar regra depois da remoção (recat)", antes, p.capturar(t, b, true))
	require.Equal(t, projAntes, projecoes(t, b), "o recat não mexeu nas categorias")

	// O que MUDA: as listas que leem campaigns.target_stations.
	alvoDepois, nomesDepois, pmmDepois := lerLista()
	require.Equal(t, 2, alvoDepois)
	require.NotContains(t, nomesDepois, "Rádio Prova 1")
	require.Equal(t, 2, pmmDepois, "a emissora sai da lista de PMM no target do cliente")
}

// campanhaDoResultadoDireto: o bloco `campanha` de uma leitura obter_campanha (sem o envelope `resultado`).
func campanhaDoResultadoDireto(out map[string]any) map[string]any {
	return out["campanha"].(map[string]any)
}

// ── criar_campanha: o upsert do hub e a descrição ───────────────────────

func TestCriarCampanha_PreviewMencionaOUpsertDoHubSempreQueOHubEstaConfigurado(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	c := semearCampEsc(t, b, "2026-12-01", "2026-12-31", "")
	efeitos := func(pre map[string]any) string { return mustJSON(t, pre["preview"].(map[string]any)["efeitos"]) }
	ressalvas := func(pre map[string]any) string { return mustJSON(t, pre["preview"].(map[string]any)["ressalvas"]) }

	// Sem código: acoes.CriarCampanha emite o campanha.upsert mesmo assim (o hub o ignora).
	pre := b.preview(t, "criar_campanha", argsCriarCampanha(c.cli, nil))
	require.Contains(t, efeitos(pre), "campanha.upsert")
	require.Contains(t, ressalvas(pre), "IGNORA o aviso")

	// Com código: o upsert segue, sem a ressalva.
	b.hub.Conferir = func(string) (*hub.CampanhaDoHub, error) {
		return mcptest.CampanhaDoClienteLigado("Camp hub", c.cli), nil
	}
	pre = b.preview(t, "criar_campanha", argsCriarCampanha(c.cli, map[string]any{"codigo_hub": "EH-7K4M2X"}))
	require.Contains(t, efeitos(pre), "campanha.upsert")
	require.NotContains(t, ressalvas(pre), "IGNORA")

	// E o que o preview promete é o que acontece: a confirmação sem código emitiu o evento.
	b.confirmarEExecutar(t, "criar_campanha", argsCriarCampanha(c.cli, map[string]any{"nome": "Sem código"}))
	require.Len(t, b.hub.EventosDoTipo("campanha.upsert"), 1)

	// Hub não configurado: nada de upsert.
	b.hub.SemConfiguracao = true
	pre = b.preview(t, "criar_campanha", argsCriarCampanha(c.cli, map[string]any{"nome": "Outra"}))
	require.NotContains(t, efeitos(pre), "campanha.upsert")

	d := escritaCriarCampanha(b.cfg).Descricao
	require.Contains(t, d, "MESMO cliente")
	require.Contains(t, d, "conferido contra o hub")
}

// ── editar_emissoras_campanha: acrescentar a uma campanha ativa ─────────

func TestEditarEmissorasCampanha_AcrescentarEmAtivaAvisaQueOClienteVeAEmissora(t *testing.T) {
	b := novaBancadaTool(t)
	ativa := semearCampEsc(t, b, "2026-10-01", "2026-11-30", "ativa")
	r := func(c cenarioCampEsc) string {
		return ressalvasDoPreview(t, b.preview(t, "editar_emissoras_campanha", argsEmissorasCampanha(c.camp, []uuid.UUID{c.e3}, nil)))
	}

	// Ativa, sem materiais: o cliente passa a ver a emissora; nada sobre materiais.
	texto := r(ativa)
	require.Contains(t, texto, "o cliente passa a ver a emissora acrescentada no mapa ao vivo e nas listas de emissoras-alvo")
	require.NotContains(t, texto, "NÃO ganham")

	// Ativa, com material vinculado: junto vem o aviso dos materiais.
	tipo := mcptest.TipoMaterial(t, b.ctx, b.pool, "Spot")
	mat := mcptest.Material(t, b.ctx, b.pool, ativa.cli, tipo, "Spot")
	mcptest.Vincular(t, b.ctx, b.pool, ativa.camp, mat, ativa.e1)
	texto = r(ativa)
	require.Contains(t, texto, "mapa ao vivo")
	require.Contains(t, texto, "NÃO ganham as emissoras acrescentadas")
	require.Contains(t, texto, "editar_emissoras_material")

	// Programada: o cliente ainda não vê nada (a campanha não começou); só o aviso dos materiais, se houver.
	prog := semearCampEsc(t, b, "2026-12-01", "2026-12-31", "")
	require.NotContains(t, r(prog), "mapa ao vivo")
	// Remover (em vez de acrescentar) numa ativa não fala de "passa a ver".
	pre := b.preview(t, "editar_emissoras_campanha", argsEmissorasCampanha(ativa.camp, nil, []uuid.UUID{ativa.e2}))
	if pre["preview"].(map[string]any)["ressalvas"] != nil {
		require.NotContains(t, ressalvasDoPreview(t, pre), "passa a ver")
	}
	require.Contains(t, mustJSON(t, pre["preview"].(map[string]any)["efeitos"]), "mapa ao vivo e de cobertura")
}
