package mcp

// Revisão do C3 sobre criar/editar/apagar_regra_distribuicao: escalada pela data só
// conta dia que a máscara cobre, regra legada sem emissora, limite fora da campanha
// nomeado e "só renomear também escala".

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/mcp/mcptest"
)

// ── escalada pela data só conta dia que a máscara cobre ─────────────────

// Hoje é quinta-feira 29/10/2026; a segunda desta semana é 26/10 e o sábado passado, 24/10.
func TestRegras_EscaladaPelaDataSoContaDiaQueAMascaraCobre(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	c := semearRegra(t, b, uuid.Nil, uuid.Nil, "2026-10-01", "2026-12-31")
	sab := func(ini string) map[string]any {
		return argsCriarRegraTeste(c, ini, "2026-12-31", map[string]any{"dias": []any{"sab"}})
	}

	// Criar: regra só de sábado começando na segunda desta semana — de segunda a quarta
	// (os dias passados da vigência) não há sábado: nenhum déficit pode mudar.
	pre := b.chamar(t, "criar_regra_distribuicao", sab("2026-10-26"))
	require.NotEmpty(t, pre["confirmacao"], "a vigência tem dias passados, mas nenhum que a máscara cubra")
	require.NotContains(t, pre, "nivel")
	// Com um sábado passado na vigência (24/10), escala.
	out := b.pedePendente(t, "criar_regra_distribuicao", sab("2026-10-24"))
	require.Contains(t, out["motivo"], "a partir de 2026-10-24")
	require.Contains(t, out["motivo"], "dias da semana: sab")
	// Regra só de domingo a partir de segunda 26/10: o domingo passado (25/10) fica de fora.
	pre = b.chamar(t, "criar_regra_distribuicao", argsCriarRegraTeste(c, "2026-10-26", "2026-12-31", map[string]any{"dias": []any{"dom"}}))
	require.NotEmpty(t, pre["confirmacao"])
	// ...e a de domingo a partir do próprio domingo 25/10 escala.
	b.pedePendente(t, "criar_regra_distribuicao", argsCriarRegraTeste(c, "2026-10-25", "2026-12-31", map[string]any{"dias": []any{"dom"}}))

	// Editar: regra de sábado desde a segunda; mexer na meta não escala...
	rSab := seedRegra(t, b, c, c.tipoA, "2026-10-26", "2026-12-31", 64, "08:00", "12:00", 2, "Sábados", []uuid.UUID{c.e1}, nil)
	pre = b.chamar(t, "editar_regra_distribuicao", argsEditarRegra(c, rSab, map[string]any{"insercoes_por_dia": 5}))
	require.NotEmpty(t, pre["confirmacao"])
	// ...trocar os dias para segunda (a NOVA máscara cobre a segunda 26/10, já passada) escala só pela nova.
	out = b.pedePendente(t, "editar_regra_distribuicao", argsEditarRegra(c, rSab, map[string]any{"dias": []any{"seg"}}))
	require.Contains(t, out["motivo"], "vigência nova")
	require.NotContains(t, out["motivo"], "vigência atual")
	// A regra de segunda (a ANTIGA máscara cobre a segunda passada) que passa a sábados escala só pela antiga.
	rSeg := seedRegra(t, b, c, c.tipoA, "2026-10-26", "2026-12-31", 2, "14:00", "16:00", 1, "Segundas", []uuid.UUID{c.e1}, nil)
	out = b.pedePendente(t, "editar_regra_distribuicao", argsEditarRegra(c, rSeg, map[string]any{"dias": []any{"sab"}}))
	require.Contains(t, out["motivo"], "vigência atual")
	require.NotContains(t, out["motivo"], "vigência nova")
	// Uma regra de sábado que começa num sábado passado escala só por mexer na meta.
	rSabPassado := seedRegra(t, b, c, c.tipoA, "2026-10-24", "2026-12-31", 64, "18:00", "19:00", 1, "Sábado velho", []uuid.UUID{c.e1}, nil)
	b.pedePendente(t, "editar_regra_distribuicao", argsEditarRegra(c, rSabPassado, map[string]any{"insercoes_por_dia": 3}))

	// Apagar: a regra de sábado desde a segunda não escala; a que tem sábado passado, sim.
	pre = b.chamar(t, "apagar_regra_distribuicao", map[string]any{"campanha_id": c.camp.String(), "regra_id": rSab.String()})
	require.NotEmpty(t, pre["confirmacao"])
	b.pedePendente(t, "apagar_regra_distribuicao", map[string]any{"campanha_id": c.camp.String(), "regra_id": rSabPassado.String()})
	b.pedePendente(t, "apagar_regra_distribuicao", map[string]any{"campanha_id": c.camp.String(), "regra_id": rSeg.String()})
	require.Zero(t, tokensUsados(t, b))
}

// Qualquer edição reexecuta a recategorização: até uma só do nome escala numa campanha com
// dias passados ou veiculação (e a descrição da tool avisa).
func TestEditarRegra_SoRenomearTambemEscala(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	c := semearRegra(t, b, uuid.Nil, uuid.Nil, "2026-10-01", "2026-12-31")
	require.Contains(t, descricaoDaTool(t, b.cfg, "editar_regra_distribuicao"), "QUALQUER edição — até uma só do nome")

	passada := semearRegraBase(t, b, c, "2026-10-01", "2026-12-31") // seg a sex desde 01/10
	out := b.pedePendente(t, "editar_regra_distribuicao", argsEditarRegra(c, passada, map[string]any{"nome": "Outro nome"}))
	require.Contains(t, out["motivo"], "dias já passados")
	require.Equal(t, "Base", strings.Split(linhasDeRegra(t, b, c.camp)[0], "|")[0], "nada foi renomeado")

	// Regra só no futuro: renomear só escala por veiculação do tipo.
	_, err := b.pool.Exec(b.ctx, `DELETE FROM distribution_rules WHERE campaign_id = $1`, c.camp)
	require.NoError(t, err)
	futura := semearRegraBase(t, b, c, "2026-11-01", "2026-12-31")
	pre := b.chamar(t, "editar_regra_distribuicao", argsEditarRegra(c, futura, map[string]any{"nome": "Outro nome"}))
	require.NotEmpty(t, pre["confirmacao"])
	mcptest.Veiculacao(t, b.ctx, b.pool, c.camp, c.mat, c.e1, "bonus", mcptest.Quando("2026-10-10", "10:00"))
	b.pedePendente(t, "editar_regra_distribuicao", argsEditarRegra(c, futura, map[string]any{"nome": "Outro nome"}))
}

// ── regra legada sem emissora ───────────────────────────────────────────

func TestEditarRegra_RegraLegadaSemEmissoraSegueEditavelNoResto(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	c := semearRegra(t, b, uuid.Nil, uuid.Nil, "2026-12-01", "2026-12-31")
	r := seedRegra(t, b, c, c.tipoA, "2026-12-01", "2026-12-31", 62, "08:00", "12:00", 2, "Sem emissora", []uuid.UUID{}, nil)
	tipo := nomeDoTipo(t, b, c.tipoA)

	// Editar o nome (sem mexer nas emissoras) é aceito, com ressalva, e preserva a lista vazia.
	pre := b.preview(t, "editar_regra_distribuicao", argsEditarRegra(c, r, map[string]any{"nome": "Ainda sem emissora"}))
	require.Contains(t, mustJSON(t, pre["preview"].(map[string]any)["ressalvas"]), "não tem nenhuma emissora")
	b.confirmarEExecutar(t, "editar_regra_distribuicao", argsEditarRegra(c, r, map[string]any{"nome": "Ainda sem emissora"}))
	require.Equal(t, []string{"Ainda sem emissora|" + tipo + "|mask=62|08:00-12:00|2/dia|2026-12-01..2026-12-31|est=|mat="}, linhasDeRegra(t, b, c.camp))

	// Mexer nas emissoras: tirar uma que não está é recusado, e acrescentar resolve a regra.
	require.Contains(t, b.chamarErro(t, "editar_regra_distribuicao", argsEditarRegra(c, r, map[string]any{"remover_emissoras": strsDe(c.e1)})), "não há o que remover")
	b.confirmarEExecutar(t, "editar_regra_distribuicao", argsEditarRegra(c, r, map[string]any{"adicionar_emissoras": strsDe(c.e1)}))
	require.Contains(t, linhasDeRegra(t, b, c.camp)[0], "est=Rádio 1|")
}

// ── o limite fora da campanha é nomeado ─────────────────────────────────

func TestRegras_MensagemDeLimiteForaDaCampanhaNomeiaOLimite(t *testing.T) {
	b := novaBancadaTool(t)
	agoraFixa(b)
	c := semearRegra(t, b, uuid.Nil, uuid.Nil, "2026-12-01", "2026-12-31")
	r := semearRegraBase(t, b, c, "2026-12-01", "2026-12-31")
	legada := seedRegra(t, b, c, c.tipoA, "2026-12-01", "2027-01-31", 62, "14:00", "16:00", 1, "Legada", []uuid.UUID{c.e1}, nil)

	// Edição: só o limite enviado, que é o fora.
	t1 := b.chamarErro(t, "editar_regra_distribuicao", argsEditarRegra(c, r, map[string]any{"fim": "2027-01-15"}))
	require.Contains(t, t1, "fim (2027-01-15) é posterior ao fim da campanha (2026-12-31)")
	require.NotContains(t, t1, "guardado")
	t2 := b.chamarErro(t, "editar_regra_distribuicao", argsEditarRegra(c, r, map[string]any{"inicio": "2026-11-30"}))
	require.Contains(t, t2, "inicio (2026-11-30) é anterior ao início da campanha (2026-12-01)")
	require.NotContains(t, t2, "guardado")
	require.NotContains(t, t2, "fim (")

	// Regra legada com o fim guardado fora: a chamada só enviou o início (que está bom) — a
	// mensagem diz que o limite fora é o valor guardado, não o enviado.
	t3 := b.chamarErro(t, "editar_regra_distribuicao", argsEditarRegra(c, legada, map[string]any{"inicio": "2026-12-05"}))
	require.Contains(t, t3, "fim (2027-01-31) é posterior ao fim da campanha (2026-12-31)")
	require.Contains(t, t3, "já está guardado na regra (esta chamada não enviou fim)")
	require.NotContains(t, t3, "inicio (")
	// Corrigir enviando o fim dentro do período resolve.
	b.confirmarEExecutar(t, "editar_regra_distribuicao", argsEditarRegra(c, legada, map[string]any{"fim": "2026-12-31"}))

	// Criação: os dois limites são do chamador.
	t4 := b.chamarErro(t, "criar_regra_distribuicao", argsCriarRegraTeste(c, "2026-11-20", "2027-01-05", nil))
	require.Contains(t, t4, "inicio (2026-11-20) é anterior ao início da campanha (2026-12-01)")
	require.Contains(t, t4, "fim (2027-01-05) é posterior ao fim da campanha (2026-12-31)")
	require.NotContains(t, t4, "guardado")
}
