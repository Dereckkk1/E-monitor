package mcp

// Task 15 (Fatia 4): cancelar_campanha, apagar_campanha, editar_cpm_fixo e
// editar_precos_campanha. As quatro são `critico`: a chamada NUNCA executa, grava
// um pedido pendente, e quem executa é a aprovação do dono em /pendentes
// (b.aprovar, sobre a lista REAL das escritas). Cada tool prova: (1) o pedido não
// escreve, (2) aprovar executa e devolve o formato do obter_*, (3) o efeito é o
// mesmo da rota HTTP, (4) cada recusa no pedido E na aprovação, (5) a auditoria
// com meta.mcp.pendente_id.

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/api/handlers"
	"radiocheck/internal/catalog"
	"radiocheck/internal/mcp/mcptest"
)

// ── fixtures e leitura ──────────────────────────────────────────────────

// cenarioCritCamp: a campanha de semearCampEsc (E1 e E2 como alvo) com um tipo,
// um material vinculado, uma regra, um ajuste do dia e um preço por inserção —
// o que um DELETE levaria junto.
type cenarioCritCamp struct {
	cenarioCampEsc
	tipo, mat uuid.UUID
}

// semearCritCamp cria o cenário; tipo != uuid.Nil reaproveita um tipo existente
// (as fixtures gêmeas das provas de paridade precisam do MESMO tipo para as
// linhas casarem).
func semearCritCamp(t *testing.T, b *bancadaTool, estado string, tipo uuid.UUID) cenarioCritCamp {
	t.Helper()
	c := cenarioCritCamp{cenarioCampEsc: semearCampEsc(t, b, "2026-10-01", "2026-12-31", estado)}
	c.tipo = tipo
	if c.tipo == uuid.Nil {
		c.tipo = mcptest.TipoMaterial(t, b.ctx, b.pool, "Spot")
	}
	c.mat = mcptest.Material(t, b.ctx, b.pool, c.cli, c.tipo, "Spot "+uuid.NewString()[:6])
	mcptest.Vincular(t, b.ctx, b.pool, c.camp, c.mat, c.e1, c.e2)
	mcptest.Regra(t, b.ctx, b.pool, c.camp, c.tipo, c.e1, "2026-10-01", "2026-12-31", 127, "08:00", "10:00", 1)
	mcptest.Ajuste(t, b.ctx, b.pool, c.camp, c.tipo, c.e1, "2026-11-10", 2, "08:00", "10:00")
	mcptest.Preco(t, b.ctx, b.pool, c.camp, c.e1, c.tipo, 10)
	return c
}

// contagemCamp são as linhas que dependem de uma campanha.
type contagemCamp struct{ Campanha, Vinculos, Regras, Ajustes, Precos, PrecosPorTipo int }

func contarDaCampanha(t *testing.T, b *bancadaTool, camp uuid.UUID) contagemCamp {
	t.Helper()
	var c contagemCamp
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT
		(SELECT count(*) FROM campaigns WHERE id = $1),
		(SELECT count(*) FROM campaign_materials WHERE campaign_id = $1),
		(SELECT count(*) FROM distribution_rules WHERE campaign_id = $1),
		(SELECT count(*) FROM distribution_overrides WHERE campaign_id = $1),
		(SELECT count(*) FROM campaign_station_pricing WHERE campaign_id = $1),
		(SELECT count(*) FROM campaign_station_type_pricing WHERE campaign_id = $1)`, camp).
		Scan(&c.Campanha, &c.Vinculos, &c.Regras, &c.Ajustes, &c.Precos, &c.PrecosPorTipo))
	return c
}

// estadoCancelamento: o estado da campanha e se o cancelled_at foi gravado.
func estadoCancelamento(t *testing.T, b *bancadaTool, camp uuid.UUID) (string, bool) {
	t.Helper()
	var estado string
	var canceladaEm bool
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT status, cancelled_at IS NOT NULL FROM campaigns WHERE id = $1`, camp).Scan(&estado, &canceladaEm))
	return estado, canceladaEm
}

func lerCPMFixo(t *testing.T, b *bancadaTool, camp uuid.UUID) *float64 {
	t.Helper()
	var v *float64
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT fixed_cpm FROM campaigns WHERE id = $1`, camp).Scan(&v))
	return v
}

// linhasDePreco: uma linha por emissora (ordenada pelo nome) com modo, valor
// consolidado e os valores por tipo — comparável entre fixtures que dividem os
// tipos.
func linhasDePreco(t *testing.T, b *bancadaTool, camp uuid.UUID) []string {
	t.Helper()
	rows, err := b.pool.Query(b.ctx, `
		SELECT s.name || '|' || p.mode || '|' || COALESCE(p.consolidated_value::text, '-') || '|' ||
		       COALESCE((SELECT string_agg(tp.type_id::text || ':' || tp.unit_value::text, ',' ORDER BY tp.type_id)
		                   FROM campaign_station_type_pricing tp
		                  WHERE tp.campaign_id = p.campaign_id AND tp.station_id = p.station_id), '')
		  FROM campaign_station_pricing p JOIN stations s ON s.id = p.station_id
		 WHERE p.campaign_id = $1 ORDER BY s.name`, camp)
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

// ultimaPendente devolve o estado e o erro do pedido no banco.
func erroDaPendente(t *testing.T, b *bancadaTool, pendenteID string) *string {
	t.Helper()
	var erro *string
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT erro FROM mcp_acoes_pendentes WHERE id = $1`, pendenteID).Scan(&erro))
	return erro
}

// exigeRecusaNaAprovacao aprova o pedido e exige a recusa de negócio com o
// código dado: nada executa, o pedido volta a `pendente` com o porquê gravado e
// nenhuma linha mcp.escrita.<tool> é escrita.
func exigeRecusaNaAprovacao(t *testing.T, b *bancadaTool, tool, pendenteID, codigo string) {
	t.Helper()
	_, err := b.tentarAprovar(t, pendenteID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "["+codigo+"]", "a recusa na aprovação")
	require.Equal(t, "pendente", b.estadoDaPendente(t, pendenteID), "recusa de negócio devolve o pedido a pendente")
	require.NotNil(t, erroDaPendente(t, b, pendenteID), "o porquê fica gravado para a pessoa ler")
	require.Empty(t, b.auditoria(t, "mcp.escrita."+tool), "nada executou")
}

// exigeAuditoriaDaAprovacao: a execução da aprovação grava mcp.escrita.<tool> com
// o alvo, o token do pedido e o id do pedido em meta.mcp.pendente_id.
func exigeAuditoriaDaAprovacao(t *testing.T, b *bancadaTool, tool string, camp uuid.UUID, pendenteID string) {
	t.Helper()
	l := b.exigeAuditoria(t, "mcp.escrita."+tool, "campaign", &camp)
	require.Equal(t, pendenteID, l.Meta["mcp"].(map[string]any)["pendente_id"])
	require.Equal(t, "critico", l.Meta["nivel"])
	require.Equal(t, tool, l.Meta["tool"])
}

// exigeSemEscritaNoPedido: o pedido gravou um `pendente` e nada mais — nenhum
// token consumido, nenhuma execução auditada.
func exigeSemEscritaNoPedido(t *testing.T, b *bancadaTool, tool, pendenteID string) {
	t.Helper()
	require.Equal(t, "pendente", b.estadoDaPendente(t, pendenteID))
	require.Zero(t, tokensUsados(t, b))
	require.Empty(t, b.auditoria(t, "mcp.escrita."+tool))
	require.Len(t, b.auditoria(t, "mcp.pendente.criada"), 1)
}

func linkDaCampanhaNoPortal(camp uuid.UUID) string {
	return "https://portal.teste/campaigns?campaign=" + camp.String()
}

// ── cancelar_campanha ───────────────────────────────────────────────────

func argsCancelar(camp uuid.UUID) map[string]any { return map[string]any{"campanha_id": camp.String()} }

func TestCancelarCampanha_PedidoNaoEscreveEAprovarCancela(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearCritCamp(t, b, "ativa", uuid.Nil)
	require.NotContains(t, propriedades(esquemaDa(t, b.cfg, "cancelar_campanha")), "confirmacao", "crítico nunca recebe token")

	out := b.pedePendente(t, "cancelar_campanha", argsCancelar(c.camp))

	require.Contains(t, out["motivo"], "congela o déficit e tira a campanha das telas operacionais e da cobrança")
	require.Equal(t, linkDaCampanhaNoPortal(c.camp), out["links"].(map[string]any)["tela"])
	require.Contains(t, out["aprovar_em"], "/pendentes?id="+out["pendente_id"].(string))
	require.Contains(t, out["aviso"], "Nada aconteceu ainda")
	estado, canceladaEm := estadoCancelamento(t, b, c.camp)
	require.Equal(t, "ativa", estado, "o pedido não escreve")
	require.False(t, canceladaEm)
	require.Empty(t, b.sup.Chamadas(), "o pedido não mexe nos workers")
	pid := out["pendente_id"].(string)
	exigeSemEscritaNoPedido(t, b, "cancelar_campanha", pid)

	ap := b.aprovar(t, pid)

	estado, canceladaEm = estadoCancelamento(t, b, c.camp)
	require.Equal(t, "cancelada", estado)
	require.True(t, canceladaEm)
	require.Equal(t, 1, b.sup.Contagem("StopWorkers", c.camp))
	res := ap.resultadoJSON(t)
	camp := res["campanha"].(map[string]any)
	require.Equal(t, "cancelada", camp["estado"])
	require.NotNil(t, camp["cancelada_em"])
	require.Len(t, res["emissoras"], 2)
	require.Equal(t, linkDeEdicaoNoPortal(c.camp), res["links"].(map[string]any)["tela"])
	// O resultado é o do obter_campanha, lido depois.
	lido := b.chamar(t, "obter_campanha", map[string]any{"campanha_id": c.camp.String()})
	require.Equal(t, lido["campanha"], camp, "mesmo formato (e mesmos valores) do obter_campanha")
	exigeAuditoriaDaAprovacao(t, b, "cancelar_campanha", c.camp, pid)
	require.Equal(t, "executada", b.estadoDaPendente(t, pid))
}

// linkDeEdicaoNoPortal: o link do obter_campanha é o da tela de edição.
func linkDeEdicaoNoPortal(camp uuid.UUID) string {
	return "https://portal.teste/campaigns/" + camp.String() + "/edit"
}

func TestCancelarCampanha_PreviewNomeiaAsEmissorasQueParam(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearCritCamp(t, b, "ativa", uuid.Nil)
	// Outra campanha ativa usa a Rádio 2 (a do mesmo cliente): o worker dela fica.
	outra := mcptest.Campanha(t, b.ctx, b.pool, c.cli, "Outra ativa", "2026-10-01", "2026-12-31", c.e2, c.e3)
	mcptest.EstadoCampanha(t, b.ctx, b.pool, outra, "ativa")

	out := b.pedePendente(t, "cancelar_campanha", argsCancelar(c.camp))

	pv := out["preview"].(map[string]any)
	efeitos := mustJSON(t, pv["efeitos"])
	require.Contains(t, efeitos, "Os workers de 1 emissora(s) param agora")
	require.Contains(t, efeitos, "Rádio 1")
	require.Contains(t, efeitos, "Rádio 2", "a Rádio 2 aparece como a que continua")
	require.Contains(t, efeitos, "seguem monitoradas por outra campanha ativa")
	require.Contains(t, efeitos, "sai de seletores, telas ao vivo e KPIs")
	require.Contains(t, efeitos, "fica marcada como cancelada no histórico e nos relatórios")
	require.Contains(t, pv["resumo"], "Campanha Escrita")
	mud := pv["mudancas"].([]any)
	require.Len(t, mud, 1)
	require.Equal(t, "ativa", mud[0].(map[string]any)["de"])
	require.Equal(t, "cancelada", mud[0].(map[string]any)["para"])
}

func TestCancelarCampanha_ProgramadaNaoDerrubaWorker(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearCritCamp(t, b, "programada", uuid.Nil)

	out := b.pedePendente(t, "cancelar_campanha", argsCancelar(c.camp))

	require.Contains(t, mustJSON(t, out["preview"].(map[string]any)["efeitos"]), "programada: ainda não monitora nada")
	ap := b.aprovar(t, out["pendente_id"].(string))
	require.Equal(t, "cancelada", ap.resultadoJSON(t)["campanha"].(map[string]any)["estado"])
}

func TestCancelarCampanha_ParidadeComARota(t *testing.T) {
	b := novaBancadaTool(t)
	viaRota := semearCritCamp(t, b, "ativa", uuid.Nil)
	viaTool := semearCritCamp(t, b, "ativa", uuid.Nil)
	supRota := &mcptest.SupervisorFalso{Campanhas: catalog.NewCampaigns(b.pool)}
	h := &handlers.CampaignsHandler{Repo: catalog.NewCampaigns(b.pool), Supervisor: supRota}

	chamarHandlerCorpo(t, h.Cancel, http.MethodPost, "/campaigns/x/cancel", nil, map[string]string{"id": viaRota.camp.String()}, b.admin)
	out := b.pedePendente(t, "cancelar_campanha", argsCancelar(viaTool.camp))
	b.aprovar(t, out["pendente_id"].(string))

	estRota, canRota := estadoCancelamento(t, b, viaRota.camp)
	estTool, canTool := estadoCancelamento(t, b, viaTool.camp)
	require.Equal(t, estRota, estTool)
	require.Equal(t, canRota, canTool)
	require.Equal(t, "cancelada", estTool)
	require.True(t, canTool)
	rota, tool := lerLinhaCampanha(t, b, viaRota.camp), lerLinhaCampanha(t, b, viaTool.camp)
	rota.Emissoras, tool.Emissoras = nil, nil
	require.Equal(t, rota, tool, "a mesma linha gravada pela rota e pela tool")
	// O que sobra da campanha (vínculos, regras…) é igual: cancelar não apaga nada.
	require.Equal(t, contarDaCampanha(t, b, viaRota.camp), contarDaCampanha(t, b, viaTool.camp))
	// E o supervisor recebeu a mesma chamada, uma vez, pela campanha certa.
	require.Equal(t, []string{"StopWorkers"}, opsDe(supRota.Chamadas()))
	require.Equal(t, []string{"StopWorkers"}, opsDe(b.sup.Chamadas()))
	require.Equal(t, viaRota.camp, supRota.Chamadas()[0].Campanha)
	require.Equal(t, viaTool.camp, b.sup.Chamadas()[0].Campanha)
}

func opsDe(cs []mcptest.ChamadaSupervisor) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Op
	}
	return out
}

func TestCancelarCampanha_Recusas(t *testing.T) {
	b := novaBancadaTool(t)
	cancelada := semearCritCamp(t, b, "cancelada", uuid.Nil)
	concluida := semearCritCamp(t, b, "concluida", uuid.Nil)

	for nome, caso := range map[string]struct {
		args   map[string]any
		codigo string
	}{
		"já cancelada":  {argsCancelar(cancelada.camp), "[estado_terminal]"},
		"já concluída":  {argsCancelar(concluida.camp), "[estado_terminal]"},
		"inexistente":   {argsCancelar(uuid.New()), "[nao_encontrado]"},
		"id inválido":   {map[string]any{"campanha_id": "xyz"}, "[entrada_invalida]"},
		"id em branco":  {map[string]any{"campanha_id": ""}, "[entrada_invalida]"},
		"id com espaço": {map[string]any{"campanha_id": " " + cancelada.camp.String() + "x"}, "[entrada_invalida]"},
	} {
		texto := b.chamarErro(t, "cancelar_campanha", caso.args)
		require.Contains(t, texto, caso.codigo, nome)
	}
	require.Contains(t, b.chamarErro(t, "cancelar_campanha", argsCancelar(cancelada.camp)), "cancelada")
	require.Contains(t, b.chamarErro(t, "cancelar_campanha", argsCancelar(concluida.camp)), "concluida")
	require.Zero(t, b.totalAuditorias(t), "recusa não deixa pedido nem auditoria")
	require.Empty(t, b.sup.Chamadas())
	var n int
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT count(*) FROM mcp_acoes_pendentes`).Scan(&n))
	require.Zero(t, n)
}

// O estado muda entre o pedido e a aprovação: a recusa vem do Preview rerodado.
func TestCancelarCampanha_RecusaNaAprovacao(t *testing.T) {
	t.Run("a campanha foi cancelada no meio", func(t *testing.T) {
		b := novaBancadaTool(t)
		c := semearCritCamp(t, b, "ativa", uuid.Nil)
		out := b.pedePendente(t, "cancelar_campanha", argsCancelar(c.camp))
		mcptest.EstadoCampanha(t, b.ctx, b.pool, c.camp, "cancelada")

		exigeRecusaNaAprovacao(t, b, "cancelar_campanha", out["pendente_id"].(string), "estado_terminal")
		require.Empty(t, b.sup.Chamadas(), "a recusa não derruba worker")
	})
	t.Run("a campanha terminou no meio", func(t *testing.T) {
		b := novaBancadaTool(t)
		c := semearCritCamp(t, b, "ativa", uuid.Nil)
		out := b.pedePendente(t, "cancelar_campanha", argsCancelar(c.camp))
		mcptest.EstadoCampanha(t, b.ctx, b.pool, c.camp, "concluida")

		exigeRecusaNaAprovacao(t, b, "cancelar_campanha", out["pendente_id"].(string), "estado_terminal")
	})
	t.Run("a campanha foi apagada no meio", func(t *testing.T) {
		b := novaBancadaTool(t)
		c := semearCritCamp(t, b, "programada", uuid.Nil)
		out := b.pedePendente(t, "cancelar_campanha", argsCancelar(c.camp))
		_, err := b.pool.Exec(b.ctx, `DELETE FROM campaigns WHERE id = $1`, c.camp)
		require.NoError(t, err)

		exigeRecusaNaAprovacao(t, b, "cancelar_campanha", out["pendente_id"].(string), "nao_encontrado")
	})
}

// ── apagar_campanha ─────────────────────────────────────────────────────

func argsApagar(camp uuid.UUID) map[string]any { return map[string]any{"campanha_id": camp.String()} }

func TestApagarCampanha_PedidoNaoEscreveEAprovarApaga(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearCritCamp(t, b, "ativa", uuid.Nil)
	outra := semearCritCamp(t, b, "ativa", uuid.Nil)
	require.NotContains(t, propriedades(esquemaDa(t, b.cfg, "apagar_campanha")), "confirmacao")
	antes := contarDaCampanha(t, b, c.camp)
	require.Equal(t, contagemCamp{Campanha: 1, Vinculos: 1, Regras: 1, Ajustes: 1, Precos: 1, PrecosPorTipo: 1}, antes)

	out := b.pedePendente(t, "apagar_campanha", argsApagar(c.camp))

	require.Contains(t, out["motivo"], "irreversível")
	require.Equal(t, linkDaCampanhaNoPortal(c.camp), out["links"].(map[string]any)["tela"])
	require.Equal(t, antes, contarDaCampanha(t, b, c.camp), "o pedido não apaga nada")
	require.Empty(t, b.sup.Chamadas())
	pv := out["preview"].(map[string]any)
	efeitos := mustJSON(t, pv["efeitos"])
	require.Contains(t, efeitos, "vínculos de material: 1")
	require.Contains(t, efeitos, "regras de distribuição: 1")
	require.Contains(t, efeitos, "ajustes do dia: 1")
	require.Contains(t, efeitos, "preços por emissora: 1")
	require.Contains(t, efeitos, "sem volta")
	require.Contains(t, pv["resumo"], "Campanha Escrita")
	pid := out["pendente_id"].(string)
	exigeSemEscritaNoPedido(t, b, "apagar_campanha", pid)

	ap := b.aprovar(t, pid)

	require.Equal(t, contagemCamp{}, contarDaCampanha(t, b, c.camp), "a campanha e tudo que é dela somem")
	require.Equal(t, antes, contarDaCampanha(t, b, outra.camp), "a outra campanha não é tocada")
	require.Equal(t, 1, b.sup.Contagem("StopWorkers", c.camp))
	res := ap.resultadoJSON(t)
	require.Equal(t, map[string]any{"apagada": map[string]any{"id": c.camp.String(), "nome": "Campanha Escrita"}}, res)
	exigeAuditoriaDaAprovacao(t, b, "apagar_campanha", c.camp, pid)
	// Apagada de verdade: nenhuma tool acha mais.
	require.Contains(t, b.chamarErro(t, "obter_campanha", map[string]any{"campanha_id": c.camp.String()}), "nao_encontrado")
}

func TestApagarCampanha_ParidadeComARota(t *testing.T) {
	b := novaBancadaTool(t)
	tipo := mcptest.TipoMaterial(t, b.ctx, b.pool, "Spot")
	viaRota := semearCritCamp(t, b, "ativa", tipo)
	viaTool := semearCritCamp(t, b, "ativa", tipo)
	passante := semearCritCamp(t, b, "ativa", tipo)
	supRota := &mcptest.SupervisorFalso{Campanhas: catalog.NewCampaigns(b.pool)}
	h := &handlers.CampaignsHandler{Repo: catalog.NewCampaigns(b.pool), Supervisor: supRota}

	chamarHandlerCorpo(t, h.Delete, http.MethodDelete, "/campaigns/x", nil, map[string]string{"id": viaRota.camp.String()}, b.admin)
	out := b.pedePendente(t, "apagar_campanha", argsApagar(viaTool.camp))
	b.aprovar(t, out["pendente_id"].(string))

	require.Equal(t, contagemCamp{}, contarDaCampanha(t, b, viaRota.camp))
	require.Equal(t, contarDaCampanha(t, b, viaRota.camp), contarDaCampanha(t, b, viaTool.camp), "as mesmas linhas somem pela rota e pela tool")
	require.Equal(t, contagemCamp{Campanha: 1, Vinculos: 1, Regras: 1, Ajustes: 1, Precos: 1, PrecosPorTipo: 1}, contarDaCampanha(t, b, passante.camp))
	// Os materiais da biblioteca ficam (só o vínculo vai embora), nos dois caminhos.
	var materiais int
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT count(*) FROM materials WHERE id = ANY($1)`, []uuid.UUID{viaRota.mat, viaTool.mat}).Scan(&materiais))
	require.Equal(t, 2, materiais)
	require.Equal(t, []string{"StopWorkers"}, opsDe(supRota.Chamadas()))
	require.Equal(t, []string{"StopWorkers"}, opsDe(b.sup.Chamadas()))
}

func TestApagarCampanha_CanceladaSemHistoricoPodeSerApagada(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearCritCamp(t, b, "cancelada", uuid.Nil)

	out := b.pedePendente(t, "apagar_campanha", argsApagar(c.camp))
	b.aprovar(t, out["pendente_id"].(string))

	require.Equal(t, contagemCamp{}, contarDaCampanha(t, b, c.camp))
}

// insereHistorico cria, em campanha c, o tipo de histórico pedido.
func insereHistorico(t *testing.T, b *bancadaTool, c cenarioCritCamp, tipo string) {
	t.Helper()
	switch tipo {
	case "veiculação":
		mcptest.Veiculacao(t, b.ctx, b.pool, c.camp, c.mat, c.e1, "in_slot", mcptest.Quando("2026-10-20", "09:00"))
	case "veiculação retratada":
		v := mcptest.Veiculacao(t, b.ctx, b.pool, c.camp, c.mat, c.e1, "in_slot", mcptest.Quando("2026-10-20", "09:00"))
		_, err := b.pool.Exec(b.ctx, `UPDATE detections SET retracted_at = now() WHERE id = $1`, v)
		require.NoError(t, err)
	case "projeção de tocada alheia":
		// A tocada é de OUTRA campanha e carrega a projeção desta.
		dono := mcptest.Campanha(t, b.ctx, b.pool, c.cli, "Dona da tocada", "2026-10-01", "2026-12-31", c.e1)
		matDono := mcptest.Material(t, b.ctx, b.pool, c.cli, c.tipo, "Spot dono "+uuid.NewString()[:6])
		quando := mcptest.Quando("2026-10-20", "09:00")
		v := mcptest.Veiculacao(t, b.ctx, b.pool, dono, matDono, c.e1, "in_slot", quando)
		mcptest.Projecao(t, b.ctx, b.pool, v, quando, c.camp, c.mat, "bonus")
	case "comprovante manual":
		_, err := b.pool.Exec(b.ctx, `INSERT INTO manual_proof_batches (campaign_id, station_id, proof_pdf_key, proof_pdf_size) VALUES ($1,$2,'k',1)`, c.camp, c.e1)
		require.NoError(t, err)
	case "pós-venda":
		var rep uuid.UUID
		require.NoError(t, b.pool.QueryRow(b.ctx, `INSERT INTO post_sale_reports (client_id, title) VALUES ($1, 'Fechamento') RETURNING id`, c.cli).Scan(&rep))
		_, err := b.pool.Exec(b.ctx, `INSERT INTO post_sale_report_campaigns (report_id, campaign_id, period_from, period_to) VALUES ($1,$2,'2026-10-01','2026-10-31')`, rep, c.camp)
		require.NoError(t, err)
	default:
		t.Fatalf("histórico desconhecido: %s", tipo)
	}
}

var tiposDeHistorico = []string{"veiculação", "veiculação retratada", "projeção de tocada alheia", "comprovante manual", "pós-venda"}

func TestApagarCampanha_RecusaNoPedidoQuemTemHistorico(t *testing.T) {
	for _, tipo := range tiposDeHistorico {
		t.Run(tipo, func(t *testing.T) {
			b := novaBancadaTool(t)
			c := semearCritCamp(t, b, "ativa", uuid.Nil)
			insereHistorico(t, b, c, tipo)
			antes := contarDaCampanha(t, b, c.camp)

			texto := b.chamarErro(t, "apagar_campanha", argsApagar(c.camp))

			require.Contains(t, texto, "[campanha_com_historico]")
			require.Contains(t, texto, "cancele em vez de apagar")
			require.Equal(t, antes, contarDaCampanha(t, b, c.camp), "nada foi apagado")
			require.Empty(t, b.sup.Chamadas(), "a recusa não cancela nem derruba worker")
			estado, _ := estadoCancelamento(t, b, c.camp)
			require.Equal(t, "ativa", estado)
			require.Zero(t, b.totalAuditorias(t))
		})
	}
}

// Entra histórico entre o pedido e a aprovação: o Preview rerodado recusa.
func TestApagarCampanha_RecusaNaAprovacao(t *testing.T) {
	for _, tipo := range tiposDeHistorico {
		t.Run(tipo, func(t *testing.T) {
			b := novaBancadaTool(t)
			c := semearCritCamp(t, b, "ativa", uuid.Nil)
			out := b.pedePendente(t, "apagar_campanha", argsApagar(c.camp))
			insereHistorico(t, b, c, tipo)
			antes := contarDaCampanha(t, b, c.camp)

			exigeRecusaNaAprovacao(t, b, "apagar_campanha", out["pendente_id"].(string), "campanha_com_historico")

			require.Equal(t, antes, contarDaCampanha(t, b, c.camp))
			require.Empty(t, b.sup.Chamadas(), "a recusa não cancela nem derruba worker")
			estado, _ := estadoCancelamento(t, b, c.camp)
			require.Equal(t, "ativa", estado)
		})
	}
	t.Run("a campanha sumiu no meio", func(t *testing.T) {
		b := novaBancadaTool(t)
		c := semearCritCamp(t, b, "ativa", uuid.Nil)
		out := b.pedePendente(t, "apagar_campanha", argsApagar(c.camp))
		_, err := b.pool.Exec(b.ctx, `DELETE FROM campaigns WHERE id = $1`, c.camp)
		require.NoError(t, err)

		exigeRecusaNaAprovacao(t, b, "apagar_campanha", out["pendente_id"].(string), "nao_encontrado")
	})
}

func TestApagarCampanha_RecusasDeEntrada(t *testing.T) {
	b := novaBancadaTool(t)
	for nome, caso := range map[string]struct {
		args   map[string]any
		codigo string
	}{
		"inexistente":  {argsApagar(uuid.New()), "[nao_encontrado]"},
		"id inválido":  {map[string]any{"campanha_id": "xyz"}, "[entrada_invalida]"},
		"id em branco": {map[string]any{"campanha_id": ""}, "[entrada_invalida]"},
	} {
		require.Contains(t, b.chamarErro(t, "apagar_campanha", caso.args), caso.codigo, nome)
	}
	require.Zero(t, b.totalAuditorias(t))
}

// ── editar_cpm_fixo ─────────────────────────────────────────────────────

func argsCPM(camp uuid.UUID, cpm any) map[string]any {
	return map[string]any{"campanha_id": camp.String(), "cpm_fixo": cpm}
}

func TestEditarCPMFixo_PedidoNaoEscreveEAprovarGrava(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearCritCamp(t, b, "ativa", uuid.Nil)
	require.NotContains(t, propriedades(esquemaDa(t, b.cfg, "editar_cpm_fixo")), "confirmacao")

	out := b.pedePendente(t, "editar_cpm_fixo", argsCPM(c.camp, 12.5))

	require.Contains(t, out["motivo"], "muda o CPM que o cliente vê")
	require.Equal(t, "https://portal.teste/campaigns/"+c.camp.String()+"/edit", out["links"].(map[string]any)["tela"])
	require.Nil(t, lerCPMFixo(t, b, c.camp), "o pedido não escreve")
	pv := out["preview"].(map[string]any)
	mud := pv["mudancas"].([]any)
	require.Len(t, mud, 1)
	require.Equal(t, "cpm_fixo", mud[0].(map[string]any)["campo"])
	require.Contains(t, mud[0].(map[string]any)["de"], "dinâmico")
	require.Equal(t, "R$ 12,50 (fixo)", mud[0].(map[string]any)["para"])
	require.Contains(t, mustJSON(t, pv["efeitos"]), "/campaigns, /insights e dashboard")
	require.Contains(t, mustJSON(t, pv["efeitos"]), "pós-venda já enviado não muda")
	require.Contains(t, mustJSON(t, pv["ressalvas"]), "CPM no target é SEMPRE dinâmico")
	pid := out["pendente_id"].(string)
	exigeSemEscritaNoPedido(t, b, "editar_cpm_fixo", pid)

	ap := b.aprovar(t, pid)

	got := lerCPMFixo(t, b, c.camp)
	require.NotNil(t, got)
	require.Equal(t, 12.5, *got)
	res := ap.resultadoJSON(t)
	require.Equal(t, 12.5, res["campanha"].(map[string]any)["cpm_fixo"])
	lido := b.chamar(t, "obter_campanha", map[string]any{"campanha_id": c.camp.String()})
	require.Equal(t, lido["campanha"], res["campanha"], "mesmo formato do obter_campanha")
	exigeAuditoriaDaAprovacao(t, b, "editar_cpm_fixo", c.camp, pid)
	require.Empty(t, b.sup.Chamadas())
}

func TestEditarCPMFixo_NullVoltaAoDinamico(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearCritCamp(t, b, "ativa", uuid.Nil)
	_, err := b.pool.Exec(b.ctx, `UPDATE campaigns SET fixed_cpm = 9.9 WHERE id = $1`, c.camp)
	require.NoError(t, err)

	out := b.pedePendente(t, "editar_cpm_fixo", argsCPM(c.camp, nil))

	pv := out["preview"].(map[string]any)
	mud := pv["mudancas"].([]any)[0].(map[string]any)
	require.Equal(t, "R$ 9,90 (fixo)", mud["de"])
	require.Contains(t, mud["para"], "dinâmico")
	require.Contains(t, mustJSON(t, pv["efeitos"]), "volta a valer o CPM dinâmico")
	require.NotNil(t, lerCPMFixo(t, b, c.camp), "o pedido não escreve")

	ap := b.aprovar(t, out["pendente_id"].(string))

	require.Nil(t, lerCPMFixo(t, b, c.camp))
	camp := ap.resultadoJSON(t)["campanha"].(map[string]any)
	cpm, existe := camp["cpm_fixo"]
	require.True(t, existe, "o formato do obter_campanha sempre traz cpm_fixo")
	require.Nil(t, cpm)
}

// cpm_fixo é OBRIGATÓRIO mas aceita null (ausente ≠ null): esquecer o campo
// nunca limpa o CPM fixo da campanha.
func TestEditarCPMFixo_CampoObrigatorioMasAceitaNull(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearCritCamp(t, b, "ativa", uuid.Nil)
	_, err := b.pool.Exec(b.ctx, `UPDATE campaigns SET fixed_cpm = 9.9 WHERE id = $1`, c.camp)
	require.NoError(t, err)
	esq := esquemaDa(t, b.cfg, "editar_cpm_fixo")

	obrigatorios, _ := esq["required"].([]any)
	require.Contains(t, obrigatorios, "cpm_fixo")
	require.Contains(t, obrigatorios, "campanha_id")
	tipos, _ := propriedades(esq)["cpm_fixo"].(map[string]any)["type"].([]any)
	require.ElementsMatch(t, []any{"null", "number"}, tipos)

	texto := b.chamarErro(t, "editar_cpm_fixo", map[string]any{"campanha_id": c.camp.String()})
	require.Contains(t, texto, "cpm_fixo")
	require.Equal(t, 9.9, *lerCPMFixo(t, b, c.camp))
	require.Zero(t, b.totalAuditorias(t))
	var n int
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT count(*) FROM mcp_acoes_pendentes`).Scan(&n))
	require.Zero(t, n, "sem o campo não há nem pedido")

	// O null explícito sobrevive à ida e volta pelo JSONB do pedido (pedePendente valida).
	b.pedePendente(t, "editar_cpm_fixo", argsCPM(c.camp, nil))
}

func TestEditarCPMFixo_ParidadeComARota(t *testing.T) {
	b := novaBancadaTool(t)
	viaRota := semearCritCamp(t, b, "ativa", uuid.Nil)
	viaTool := semearCritCamp(t, b, "ativa", uuid.Nil)
	h := &handlers.CampaignsHandler{Repo: catalog.NewCampaigns(b.pool), Supervisor: &mcptest.SupervisorFalso{}}

	for _, valor := range []any{47.9, nil, 0.0} {
		chamarHandlerCorpo(t, h.UpdateFixedCPM, http.MethodPut, "/campaigns/x/fixed-cpm", map[string]any{"fixed_cpm": valor},
			map[string]string{"id": viaRota.camp.String()}, b.admin)
		out := b.pedePendente(t, "editar_cpm_fixo", argsCPM(viaTool.camp, valor))
		b.aprovar(t, out["pendente_id"].(string))

		rota, tool := lerCPMFixo(t, b, viaRota.camp), lerCPMFixo(t, b, viaTool.camp)
		require.Equal(t, rota, tool, "o mesmo CPM gravado pela rota e pela tool: %v", valor)
		rl, tl := lerLinhaCampanha(t, b, viaRota.camp), lerLinhaCampanha(t, b, viaTool.camp)
		rl.Emissoras, tl.Emissoras = nil, nil
		require.Equal(t, rl, tl)
	}
	// 0 é um CPM fixo válido (e diferente de "sem CPM fixo").
	require.NotNil(t, lerCPMFixo(t, b, viaTool.camp))
	require.Zero(t, *lerCPMFixo(t, b, viaTool.camp))
	require.Empty(t, b.sup.Chamadas())
}

func TestEditarCPMFixo_Recusas(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearCritCamp(t, b, "ativa", uuid.Nil)
	cancelada := semearCritCamp(t, b, "cancelada", uuid.Nil)
	_, err := b.pool.Exec(b.ctx, `UPDATE campaigns SET fixed_cpm = 9.9 WHERE id = $1`, c.camp)
	require.NoError(t, err)
	semFixo := semearCritCamp(t, b, "ativa", uuid.Nil)
	a, soma := 0.1, 0.2
	soma += a // 0.30000000000000004 em float64 (a constante 0.1+0.2 seria exata)

	for nome, caso := range map[string]struct {
		args   map[string]any
		codigo string
	}{
		"cancelada":                {argsCPM(cancelada.camp, 10.0), "[campanha_cancelada]"},
		"inexistente":              {argsCPM(uuid.New(), 10.0), "[nao_encontrado]"},
		"id inválido":              {map[string]any{"campanha_id": "xyz", "cpm_fixo": 10.0}, "[entrada_invalida]"},
		"mesmo valor":              {argsCPM(c.camp, 9.9), "[sem_mudanca]"},
		"mesmo valor, 2 casas":     {argsCPM(c.camp, 9.90), "[sem_mudanca]"},
		"null sem CPM fixo":        {argsCPM(semFixo.camp, nil), "[sem_mudanca]"},
		"negativo":                 {argsCPM(c.camp, -1.0), "[entrada_invalida]"},
		"mais de 2 casas":          {argsCPM(c.camp, 12.345), "[entrada_invalida]"},
		"muitas casas":             {argsCPM(c.camp, soma), "[entrada_invalida]"},
		"grande demais":            {argsCPM(c.camp, 1e10), "[entrada_invalida]"},
		"enorme":                   {argsCPM(c.camp, 1e300), "[entrada_invalida]"},
		"texto no lugar do número": {argsCPM(c.camp, "12,5"), ""},
	} {
		texto := b.chamarErro(t, "editar_cpm_fixo", caso.args)
		if caso.codigo != "" {
			require.Contains(t, texto, caso.codigo, nome)
		}
	}
	require.Contains(t, b.chamarErro(t, "editar_cpm_fixo", argsCPM(c.camp, 12.345)), "2 casas decimais")
	require.Equal(t, 9.9, *lerCPMFixo(t, b, c.camp), "nenhuma recusa gravou")
	require.Nil(t, lerCPMFixo(t, b, semFixo.camp))
	require.Zero(t, b.totalAuditorias(t))
	// O maior valor que a coluna comporta passa (NUMERIC(12,2)).
	b.pedePendente(t, "editar_cpm_fixo", argsCPM(c.camp, 9999999999.99))
}

func TestEditarCPMFixo_RecusaNaAprovacao(t *testing.T) {
	t.Run("a campanha foi cancelada no meio", func(t *testing.T) {
		b := novaBancadaTool(t)
		c := semearCritCamp(t, b, "ativa", uuid.Nil)
		out := b.pedePendente(t, "editar_cpm_fixo", argsCPM(c.camp, 12.5))
		mcptest.EstadoCampanha(t, b.ctx, b.pool, c.camp, "cancelada")

		exigeRecusaNaAprovacao(t, b, "editar_cpm_fixo", out["pendente_id"].(string), "campanha_cancelada")
		require.Nil(t, lerCPMFixo(t, b, c.camp))
	})
	t.Run("alguém já pôs o mesmo valor", func(t *testing.T) {
		b := novaBancadaTool(t)
		c := semearCritCamp(t, b, "ativa", uuid.Nil)
		out := b.pedePendente(t, "editar_cpm_fixo", argsCPM(c.camp, 12.5))
		_, err := b.pool.Exec(b.ctx, `UPDATE campaigns SET fixed_cpm = 12.5 WHERE id = $1`, c.camp)
		require.NoError(t, err)

		exigeRecusaNaAprovacao(t, b, "editar_cpm_fixo", out["pendente_id"].(string), "sem_mudanca")
	})
	t.Run("a campanha foi apagada no meio", func(t *testing.T) {
		b := novaBancadaTool(t)
		c := semearCritCamp(t, b, "ativa", uuid.Nil)
		out := b.pedePendente(t, "editar_cpm_fixo", argsCPM(c.camp, 12.5))
		_, err := b.pool.Exec(b.ctx, `DELETE FROM campaigns WHERE id = $1`, c.camp)
		require.NoError(t, err)

		exigeRecusaNaAprovacao(t, b, "editar_cpm_fixo", out["pendente_id"].(string), "nao_encontrado")
	})
}

// ── editar_precos_campanha ──────────────────────────────────────────────

func precoConsolidado(est uuid.UUID, valor any) map[string]any {
	return map[string]any{"emissora_id": est.String(), "modo": "consolidado", "valor_consolidado": valor}
}

func precoPorInsercao(est uuid.UUID, tipos ...any) map[string]any {
	return map[string]any{"emissora_id": est.String(), "modo": "por_insercao", "por_tipo": tipos}
}

func valorDoTipo(tipo uuid.UUID, valor any) map[string]any {
	return map[string]any{"tipo_id": tipo.String(), "valor_unitario": valor}
}

func argsPrecos(camp uuid.UUID, precos ...any) map[string]any {
	if precos == nil {
		precos = []any{} // lista vazia é [], não null
	}
	return map[string]any{"campanha_id": camp.String(), "precos": precos}
}

func TestEditarPrecosCampanha_PedidoNaoEscreveEAprovarGrava(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearCritCamp(t, b, "ativa", uuid.Nil)
	outroTipo := mcptest.TipoMaterial(t, b.ctx, b.pool, "Vinheta")
	require.NotContains(t, propriedades(esquemaDa(t, b.cfg, "editar_precos_campanha")), "confirmacao")
	antes := linhasDePreco(t, b, c.camp)
	require.Len(t, antes, 1, "só a Rádio 1 tem preço (por inserção)")
	args := argsPrecos(c.camp,
		precoPorInsercao(c.e1, valorDoTipo(c.tipo, 12.5), valorDoTipo(outroTipo, 4)),
		precoConsolidado(c.e2, 1500.75))

	out := b.pedePendente(t, "editar_precos_campanha", args)

	require.Contains(t, out["motivo"], "dinheiro")
	require.Equal(t, "https://portal.teste/campaigns/"+c.camp.String()+"/edit", out["links"].(map[string]any)["tela"])
	require.Equal(t, antes, linhasDePreco(t, b, c.camp), "o pedido não escreve")
	pv := out["preview"].(map[string]any)
	mud := pv["mudancas"].([]any)
	require.Len(t, mud, 2)
	m1 := mud[0].(map[string]any)
	require.Contains(t, m1["campo"], "Rádio 1")
	require.Contains(t, m1["de"], "por inserção")
	require.Contains(t, m1["de"], "R$ 10,00")
	require.Contains(t, m1["para"], "R$ 12,50")
	require.Contains(t, m1["para"], "Vinheta")
	m2 := mud[1].(map[string]any)
	require.Contains(t, m2["campo"], "Rádio 2")
	require.Equal(t, "sem preço", m2["de"])
	require.Equal(t, "consolidado: R$ 1.500,75", m2["para"])
	require.Contains(t, mustJSON(t, pv["ressalvas"]), "pós-venda já enviado não muda")
	require.Contains(t, mustJSON(t, pv["efeitos"]), "SUBSTITUÍDO por inteiro")
	pid := out["pendente_id"].(string)
	exigeSemEscritaNoPedido(t, b, "editar_precos_campanha", pid)

	ap := b.aprovar(t, pid)

	require.Equal(t, []string{
		"Rádio 1|per_insertion|-|" + ordenarPares(fmt.Sprintf("%s:12.50", c.tipo), fmt.Sprintf("%s:4.00", outroTipo)),
		"Rádio 2|consolidated|1500.75|",
	}, linhasDePreco(t, b, c.camp))
	res := ap.resultadoJSON(t)
	require.Equal(t, c.camp.String(), res["campanha"].(map[string]any)["id"])
	itens := res["precos"].([]any)
	require.Len(t, itens, 2)
	// O formato é o de listar_precos_campanha, linha por linha.
	lista := itens
	listados := b.chamar(t, "listar_precos_campanha", map[string]any{"campanha_id": c.camp.String()})["items"].([]any)
	require.Len(t, listados, 2)
	porEmissora := map[string]any{}
	for _, l := range listados {
		porEmissora[l.(map[string]any)["emissora"].(map[string]any)["id"].(string)] = l
	}
	for _, l := range lista {
		id := l.(map[string]any)["emissora"].(map[string]any)["id"].(string)
		require.Equal(t, porEmissora[id], l, "mesma linha do listar_precos_campanha")
	}
	exigeAuditoriaDaAprovacao(t, b, "editar_precos_campanha", c.camp, pid)
}

// ordenarPares ordena "tipo:valor" pelo tipo, como o string_agg do linhasDePreco.
func ordenarPares(pares ...string) string {
	for i := range pares {
		for j := i + 1; j < len(pares); j++ {
			if pares[j] < pares[i] {
				pares[i], pares[j] = pares[j], pares[i]
			}
		}
	}
	return strings.Join(pares, ",")
}

func TestEditarPrecosCampanha_ParidadeComARota(t *testing.T) {
	b := novaBancadaTool(t)
	tipo := mcptest.TipoMaterial(t, b.ctx, b.pool, "Spot")
	outroTipo := mcptest.TipoMaterial(t, b.ctx, b.pool, "Vinheta")
	viaRota := semearCritCamp(t, b, "ativa", tipo)
	viaTool := semearCritCamp(t, b, "ativa", tipo)
	h := &handlers.PricingHandler{Repo: catalog.NewPricing(b.pool)}

	// A rota grava uma emissora por PUT; a tool, o lote numa transação. O estado final é o mesmo.
	chamarHandlerCorpo(t, h.Upsert, http.MethodPut, "/campaigns/x/pricing/y", map[string]any{
		"mode": "per_insertion", "per_type": []any{map[string]any{"type_id": tipo.String(), "unit_value": 12.5}, map[string]any{"type_id": outroTipo.String(), "unit_value": 4}},
	}, map[string]string{"campaignID": viaRota.camp.String(), "stationID": viaRota.e1.String()}, b.admin)
	chamarHandlerCorpo(t, h.Upsert, http.MethodPut, "/campaigns/x/pricing/y", map[string]any{
		"mode": "consolidated", "consolidated_value": 1500.75,
	}, map[string]string{"campaignID": viaRota.camp.String(), "stationID": viaRota.e2.String()}, b.admin)
	out := b.pedePendente(t, "editar_precos_campanha", argsPrecos(viaTool.camp,
		precoPorInsercao(viaTool.e1, valorDoTipo(tipo, 12.5), valorDoTipo(outroTipo, 4)),
		precoConsolidado(viaTool.e2, 1500.75)))
	b.aprovar(t, out["pendente_id"].(string))

	rota, tool := linhasDePreco(t, b, viaRota.camp), linhasDePreco(t, b, viaTool.camp)
	require.Len(t, rota, 2)
	require.Equal(t, rota, tool, "as mesmas linhas de preço gravadas pela rota e pela tool")
	// Trocar de modo na mesma emissora também limpa o detalhe por tipo, nos dois caminhos.
	chamarHandlerCorpo(t, h.Upsert, http.MethodPut, "/campaigns/x/pricing/y", map[string]any{
		"mode": "consolidated", "consolidated_value": 800,
	}, map[string]string{"campaignID": viaRota.camp.String(), "stationID": viaRota.e1.String()}, b.admin)
	out = b.pedePendente(t, "editar_precos_campanha", argsPrecos(viaTool.camp, precoConsolidado(viaTool.e1, 800)))
	b.aprovar(t, out["pendente_id"].(string))
	require.Equal(t, linhasDePreco(t, b, viaRota.camp), linhasDePreco(t, b, viaTool.camp))
	require.Equal(t, contarDaCampanha(t, b, viaRota.camp), contarDaCampanha(t, b, viaTool.camp))
	require.Zero(t, contarDaCampanha(t, b, viaTool.camp).PrecosPorTipo)
}

func TestEditarPrecosCampanha_SoMudaOQueFoiPedido(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearCritCamp(t, b, "ativa", uuid.Nil)
	mcptest.Preco(t, b.ctx, b.pool, c.camp, c.e2, c.tipo, 7)
	antes := linhasDePreco(t, b, c.camp)
	require.Len(t, antes, 2)

	// Só a Rádio 1 muda; a Rádio 2 (mesmo valor que já tem) entra no pedido e fica de fora.
	out := b.pedePendente(t, "editar_precos_campanha", argsPrecos(c.camp,
		precoPorInsercao(c.e1, valorDoTipo(c.tipo, 11)),
		precoPorInsercao(c.e2, valorDoTipo(c.tipo, 7))))

	pv := out["preview"].(map[string]any)
	require.Len(t, pv["mudancas"], 1)
	require.Contains(t, mustJSON(t, pv["ressalvas"]), "1 emissora(s) já estavam como pedido")
	b.aprovar(t, out["pendente_id"].(string))
	depois := linhasDePreco(t, b, c.camp)
	require.Equal(t, antes[1], depois[1], "a Rádio 2 não foi tocada")
	require.NotEqual(t, antes[0], depois[0])
}

func TestEditarPrecosCampanha_RessalvaDasEmissorasSemPreco(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearCritCamp(t, b, "ativa", uuid.Nil) // só a Rádio 1 tem preço

	out := b.pedePendente(t, "editar_precos_campanha", argsPrecos(c.camp, precoConsolidado(c.e1, 100)))

	require.Contains(t, mustJSON(t, out["preview"].(map[string]any)["ressalvas"]), "ficam sem preço (e fora do investido): Rádio 2")
}

func TestEditarPrecosCampanha_Recusas(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearCritCamp(t, b, "ativa", uuid.Nil)
	cancelada := semearCritCamp(t, b, "cancelada", uuid.Nil)
	mcptest.Preco(t, b.ctx, b.pool, c.camp, c.e2, c.tipo, 7)
	antes := linhasDePreco(t, b, c.camp)
	muitos := make([]any, 301)
	for i := range muitos {
		muitos[i] = precoConsolidado(uuid.New(), 1)
	}

	for nome, caso := range map[string]struct {
		args   map[string]any
		codigo string
		texto  string // trecho esperado da mensagem
	}{
		"campanha cancelada":        {argsPrecos(cancelada.camp, precoConsolidado(cancelada.e1, 10)), "[campanha_cancelada]", ""},
		"campanha inexistente":      {argsPrecos(uuid.New(), precoConsolidado(c.e1, 10)), "[nao_encontrado]", ""},
		"campanha_id inválido":      {map[string]any{"campanha_id": "xyz", "precos": []any{precoConsolidado(c.e1, 10)}}, "[entrada_invalida]", ""},
		"lista vazia":               {argsPrecos(c.camp), "[entrada_invalida]", "ao menos uma emissora"},
		"mais de 300":               {argsPrecos(c.camp, muitos...), "[entrada_invalida]", "no máximo 300"},
		"emissora fora da campanha": {argsPrecos(c.camp, precoConsolidado(c.e3, 10)), "[entrada_invalida]", "Rádio 3"},
		"emissora inexistente":      {argsPrecos(c.camp, precoConsolidado(uuid.New(), 10)), "[entrada_invalida]", "não está nas emissoras-alvo"},
		"emissora_id inválido":      {argsPrecos(c.camp, map[string]any{"emissora_id": "x", "modo": "consolidado", "valor_consolidado": 1}), "[entrada_invalida]", ""},
		"emissora repetida":         {argsPrecos(c.camp, precoConsolidado(c.e1, 10), precoConsolidado(c.e1, 20)), "[entrada_invalida]", "mais de uma vez"},
		"tipo inexistente":          {argsPrecos(c.camp, precoPorInsercao(c.e1, valorDoTipo(uuid.New(), 5))), "[nao_encontrado]", "tipo de material"},
		"tipo_id inválido":          {argsPrecos(c.camp, precoPorInsercao(c.e1, map[string]any{"tipo_id": "x", "valor_unitario": 5})), "[entrada_invalida]", ""},
		"tipo repetido":             {argsPrecos(c.camp, precoPorInsercao(c.e1, valorDoTipo(c.tipo, 5), valorDoTipo(c.tipo, 6))), "[entrada_invalida]", "mais de uma vez"},
		"modo desconhecido":         {argsPrecos(c.camp, map[string]any{"emissora_id": c.e1.String(), "modo": "mensal", "valor_consolidado": 5}), "[entrada_invalida]", "consolidado"},
		"consolidado sem valor":     {argsPrecos(c.camp, map[string]any{"emissora_id": c.e1.String(), "modo": "consolidado"}), "[entrada_invalida]", "exige valor_consolidado"},
		"consolidado com tipos":     {argsPrecos(c.camp, map[string]any{"emissora_id": c.e1.String(), "modo": "consolidado", "valor_consolidado": 5, "por_tipo": []any{valorDoTipo(c.tipo, 5)}}), "[entrada_invalida]", "não aceita por_tipo"},
		"por inserção sem tipos":    {argsPrecos(c.camp, map[string]any{"emissora_id": c.e1.String(), "modo": "por_insercao"}), "[entrada_invalida]", "exige ao menos um item em por_tipo"},
		"por inserção com valor":    {argsPrecos(c.camp, map[string]any{"emissora_id": c.e1.String(), "modo": "por_insercao", "valor_consolidado": 5, "por_tipo": []any{valorDoTipo(c.tipo, 5)}}), "[entrada_invalida]", "não aceita valor_consolidado"},
		"consolidado negativo":      {argsPrecos(c.camp, precoConsolidado(c.e1, -1)), "[entrada_invalida]", "negativo"},
		"unitário negativo":         {argsPrecos(c.camp, precoPorInsercao(c.e1, valorDoTipo(c.tipo, -0.5))), "[entrada_invalida]", "negativo"},
		"mais de 2 casas":           {argsPrecos(c.camp, precoConsolidado(c.e1, 10.005)), "[entrada_invalida]", "2 casas decimais"},
		"unitário com 3 casas":      {argsPrecos(c.camp, precoPorInsercao(c.e1, valorDoTipo(c.tipo, 1.234))), "[entrada_invalida]", "2 casas decimais"},
		"grande demais":             {argsPrecos(c.camp, precoConsolidado(c.e1, 1e10)), "[entrada_invalida]", "menor que"},
		"nada muda":                 {argsPrecos(c.camp, precoPorInsercao(c.e1, valorDoTipo(c.tipo, 10)), precoPorInsercao(c.e2, valorDoTipo(c.tipo, 7))), "[sem_mudanca]", "nada a alterar"},
	} {
		texto := b.chamarErro(t, "editar_precos_campanha", caso.args)
		require.Contains(t, texto, caso.codigo, nome)
		if caso.texto != "" {
			require.Contains(t, texto, caso.texto, nome)
		}
	}
	require.Equal(t, antes, linhasDePreco(t, b, c.camp), "nenhuma recusa gravou")
	require.Zero(t, b.totalAuditorias(t))
	var n int
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT count(*) FROM mcp_acoes_pendentes`).Scan(&n))
	require.Zero(t, n)
	// Um item ruim recusa o lote INTEIRO: o bom do mesmo pedido também não vale.
	b.chamarErro(t, "editar_precos_campanha", argsPrecos(c.camp, precoConsolidado(c.e1, 99), precoConsolidado(c.e3, 1)))
	require.Equal(t, antes, linhasDePreco(t, b, c.camp))
	// Zero é preço válido.
	b.pedePendente(t, "editar_precos_campanha", argsPrecos(c.camp, precoConsolidado(c.e1, 0)))
}

func TestEditarPrecosCampanha_RecusaNaAprovacao(t *testing.T) {
	t.Run("a campanha foi cancelada no meio", func(t *testing.T) {
		b := novaBancadaTool(t)
		c := semearCritCamp(t, b, "ativa", uuid.Nil)
		antes := linhasDePreco(t, b, c.camp)
		out := b.pedePendente(t, "editar_precos_campanha", argsPrecos(c.camp, precoConsolidado(c.e1, 99)))
		mcptest.EstadoCampanha(t, b.ctx, b.pool, c.camp, "cancelada")

		exigeRecusaNaAprovacao(t, b, "editar_precos_campanha", out["pendente_id"].(string), "campanha_cancelada")
		require.Equal(t, antes, linhasDePreco(t, b, c.camp))
	})
	t.Run("a emissora saiu da campanha no meio", func(t *testing.T) {
		b := novaBancadaTool(t)
		c := semearCritCamp(t, b, "ativa", uuid.Nil)
		antes := linhasDePreco(t, b, c.camp)
		out := b.pedePendente(t, "editar_precos_campanha", argsPrecos(c.camp, precoConsolidado(c.e1, 99), precoConsolidado(c.e2, 50)))
		_, err := b.pool.Exec(b.ctx, `UPDATE campaigns SET target_stations = ARRAY[$2]::uuid[] WHERE id = $1`, c.camp, c.e1)
		require.NoError(t, err)

		exigeRecusaNaAprovacao(t, b, "editar_precos_campanha", out["pendente_id"].(string), "entrada_invalida")
		require.Equal(t, antes, linhasDePreco(t, b, c.camp), "o lote é tudo ou nada")
	})
	t.Run("alguém já pôs os mesmos preços", func(t *testing.T) {
		b := novaBancadaTool(t)
		c := semearCritCamp(t, b, "ativa", uuid.Nil)
		out := b.pedePendente(t, "editar_precos_campanha", argsPrecos(c.camp, precoConsolidado(c.e2, 50)))
		_, err := b.pool.Exec(b.ctx, `INSERT INTO campaign_station_pricing (campaign_id, station_id, mode, consolidated_value) VALUES ($1,$2,'consolidated',50)`, c.camp, c.e2)
		require.NoError(t, err)

		exigeRecusaNaAprovacao(t, b, "editar_precos_campanha", out["pendente_id"].(string), "sem_mudanca")
	})
	t.Run("o tipo de material sumiu no meio", func(t *testing.T) {
		b := novaBancadaTool(t)
		c := semearCritCamp(t, b, "ativa", uuid.Nil)
		tipoNovo := mcptest.TipoMaterial(t, b.ctx, b.pool, "Efêmero")
		out := b.pedePendente(t, "editar_precos_campanha", argsPrecos(c.camp, precoPorInsercao(c.e2, valorDoTipo(tipoNovo, 5))))
		_, err := b.pool.Exec(b.ctx, `DELETE FROM material_types WHERE id = $1`, tipoNovo)
		require.NoError(t, err)

		exigeRecusaNaAprovacao(t, b, "editar_precos_campanha", out["pendente_id"].(string), "nao_encontrado")
	})
	t.Run("a campanha foi apagada no meio", func(t *testing.T) {
		b := novaBancadaTool(t)
		c := semearCritCamp(t, b, "ativa", uuid.Nil)
		out := b.pedePendente(t, "editar_precos_campanha", argsPrecos(c.camp, precoConsolidado(c.e1, 99)))
		_, err := b.pool.Exec(b.ctx, `DELETE FROM campaigns WHERE id = $1`, c.camp)
		require.NoError(t, err)

		exigeRecusaNaAprovacao(t, b, "editar_precos_campanha", out["pendente_id"].(string), "nao_encontrado")
	})
}

// ── regras que valem para as quatro ─────────────────────────────────────

func TestCriticasDeCampanha_DescricaoPedeAprovacaoENuncaInventarIds(t *testing.T) {
	for _, tl := range listarToolsDoServidor(t) {
		switch tl.Name {
		case "cancelar_campanha", "apagar_campanha", "editar_cpm_fixo", "editar_precos_campanha":
		default:
			continue
		}
		require.Contains(t, tl.Description, "/pendentes", tl.Name)
		require.Contains(t, tl.Description, "aprovar_em", tl.Name)
		require.Contains(t, tl.Description, "nada aconteceu ainda", tl.Name)
		require.Contains(t, tl.Description, "Nunca invente ids", tl.Name)
		require.True(t, strings.HasSuffix(tl.Description, " Nível: critico."), tl.Name)
	}
}

// Nenhum caminho dessas tools executa na chamada: nem com os argumentos certos,
// nem repetindo o pedido (o pedido igual aberto não duplica).
func TestCriticasDeCampanha_ChamadaRepetidaNaoDuplicaOPedido(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearCritCamp(t, b, "ativa", uuid.Nil)

	primeiro := b.pedePendente(t, "cancelar_campanha", argsCancelar(c.camp))
	segundo := b.pedePendente(t, "cancelar_campanha", argsCancelar(c.camp))

	require.Equal(t, primeiro["pendente_id"], segundo["pendente_id"])
	require.Equal(t, true, segundo["ja_existia"])
	require.Len(t, b.auditoria(t, "mcp.pendente.criada"), 1)
	estado, _ := estadoCancelamento(t, b, c.camp)
	require.Equal(t, "ativa", estado)
}

// ── a escrita gravada nunca vira erro (campanhaFeita) ───────────────────

// quebrarReleituraDeCampanha faz SÓ a releitura da campanha (lerCampanhaCompleta, que lê os
// vínculos de material) falhar: a escrita e o planejamento seguem nos repositórios vivos.
func quebrarReleituraDeCampanha(t *testing.T, b *bancadaTool) {
	t.Helper()
	b.E.Leitura.MateriaisCampanha = catalog.NewCampaignMaterials(b.poolFechado(t))
}

// O cancelamento (ou o CPM) já está gravado quando a releitura cai: o pedido fecha `executada`
// com o resultado mínimo e a ressalva de conferir — nunca `falhou`, que mandaria a pessoa
// aprovar de novo uma escrita que já aconteceu.
func TestCancelarCampanha_ReleituraQueFalhaNaoDeixaOPedidoFalhar(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearCritCamp(t, b, "ativa", uuid.Nil)
	out := b.pedePendente(t, "cancelar_campanha", argsCancelar(c.camp))
	pid := out["pendente_id"].(string)
	quebrarReleituraDeCampanha(t, b)

	ap := b.aprovar(t, pid) // exige "executada"

	estado, canceladaEm := estadoCancelamento(t, b, c.camp)
	require.Equal(t, "cancelada", estado, "a escrita aconteceu")
	require.True(t, canceladaEm)
	require.Equal(t, 1, b.sup.Contagem("StopWorkers", c.camp))
	require.Equal(t, map[string]any{"campanha": map[string]any{"id": c.camp.String(), "nome": "Campanha Escrita"}}, ap.resultadoJSON(t))
	texto := textoDasRessalvas(ap.Ressalvas)
	require.Contains(t, texto, "não consegui reler a campanha")
	require.Contains(t, texto, "obter_campanha")
	require.Contains(t, texto, "workers", "a ressalva da execução segue junto")
	require.Equal(t, ap.Ressalvas, b.ressalvasGravadas(t, pid))
	exigeAuditoriaDaAprovacao(t, b, "cancelar_campanha", c.camp, pid)
}

func TestEditarCPMFixo_ReleituraQueFalhaNaoDeixaOPedidoFalhar(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearCritCamp(t, b, "ativa", uuid.Nil)
	out := b.pedePendente(t, "editar_cpm_fixo", argsCPM(c.camp, 12.5))
	pid := out["pendente_id"].(string)
	quebrarReleituraDeCampanha(t, b)

	ap := b.aprovar(t, pid)

	require.Equal(t, 12.5, *lerCPMFixo(t, b, c.camp), "a escrita aconteceu")
	require.Equal(t, map[string]any{"campanha": map[string]any{"id": c.camp.String(), "nome": "Campanha Escrita"}}, ap.resultadoJSON(t))
	texto := textoDasRessalvas(ap.Ressalvas)
	require.Contains(t, texto, "não consegui reler a campanha")
	require.Contains(t, texto, "CPM no target continua dinâmico")
	exigeAuditoriaDaAprovacao(t, b, "editar_cpm_fixo", c.camp, pid)
}

// O CancelarCampanha devolve (nil, nil) quando só a releitura DELE falha (o cancelamento está
// gravado): a campanha para a resposta vem de uma releitura nossa e, se esta também cai, do plano
// marcada como cancelada — o plano em si não é alterado.
func TestCampanhaCancelada_FallbackDoNilNil(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearCritCamp(t, b, "ativa", uuid.Nil)
	antes, err := b.E.Leitura.Campanhas.Get(b.ctx, c.camp)
	require.NoError(t, err)
	require.Equal(t, "ativa", antes.Status)

	devolvida := &catalog.Campaign{ID: c.camp, Status: "cancelada"}
	require.Same(t, devolvida, campanhaCancelada(b.ctx, b.E.Leitura, devolvida, antes), "com a campanha do acoes não relê")

	feita, err := b.E.Acoes.CancelarCampanha(b.ctx, c.camp)
	require.NoError(t, err)
	require.NotNil(t, feita)
	relida := campanhaCancelada(b.ctx, b.E.Leitura, nil, antes)
	require.Equal(t, "cancelada", relida.Status, "relida do banco")
	require.Equal(t, c.camp, relida.ID)

	morta := *b.E.Leitura
	morta.Campanhas = catalog.NewCampaigns(b.poolFechado(t))
	doPlano := campanhaCancelada(b.ctx, &morta, nil, antes)
	require.Equal(t, "cancelada", doPlano.Status, "sem releitura possível, o plano marcado como cancelado")
	require.Equal(t, antes.ID, doPlano.ID)
	require.Equal(t, antes.Name, doPlano.Name)
	require.Equal(t, "ativa", antes.Status, "o plano não é alterado")
}
