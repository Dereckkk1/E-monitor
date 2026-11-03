package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// ── Controles de direção (bidi) no resumo ────────────────────────────────────────────────────

const (
	rlo = "‮" // right-to-left override
	lri = "⁦"
	pdi = "⁩"
	lrm = "‎"
	rlm = "‏"
)

func TestSemBidi_TiraSoOsControlesDeDirecao(t *testing.T) {
	require.Equal(t, "R$ 100", semBidi("R$ "+rlo+"100"))
	require.Equal(t, "abc", semBidi(lri+"a"+pdi+"b"+lrm+"c"+rlm))
	for r := '‪'; r <= '‮'; r++ {
		require.Equal(t, "ab", semBidi("a"+string(r)+"b"), "U+%04X", r)
	}
	for r := '⁦'; r <= '⁩'; r++ {
		require.Equal(t, "ab", semBidi("a"+string(r)+"b"), "U+%04X", r)
	}
	// o que NÃO é controle de direção fica: acento, árabe/hebraico de verdade, emoji, ZWJ
	for _, ok := range []string{"ação", "مرحبا", "שלום", "👩‍👩‍👧", "a‍b", "tab\t"} {
		require.Equal(t, ok, semBidi(ok))
	}
}

func TestLimparPrevia_LimpaTodosOsTextosEPoupaOResto(t *testing.T) {
	p := previa{
		Resumo: "Cancelar " + rlo + "a campanha",
		Mudancas: []mudanca{
			{Campo: "no" + lri + "me", De: "A" + rlo, Para: lrm + "B"},
			{Campo: "n", De: 12.5, Para: nil},
			{Campo: "lista", De: []string{"x" + rlo}, Para: []any{"y" + pdi, 3.0, map[string]any{"k" + rlm: "v" + rlo}}},
			{Campo: "ok", De: true, Para: false},
		},
		Efeitos:   []string{"e" + rlo + "1", "e2"},
		Ressalvas: []string{rlm + "r"},
	}
	l := limparPrevia(p)
	bruto, err := json.Marshal(l)
	require.NoError(t, err)
	for _, c := range []string{rlo, lri, pdi, lrm, rlm} {
		require.NotContains(t, string(bruto), c)
		require.NotContains(t, string(bruto), `\u`+map[string]string{rlo: "202e", lri: "2066", pdi: "2069", lrm: "200e", rlm: "200f"}[c])
	}
	require.Equal(t, "Cancelar a campanha", l.Resumo)
	require.Equal(t, "nome", l.Mudancas[0].Campo)
	require.Equal(t, "A", l.Mudancas[0].De)
	require.Equal(t, "B", l.Mudancas[0].Para)
	require.Equal(t, 12.5, l.Mudancas[1].De)
	require.Nil(t, l.Mudancas[1].Para)
	require.Equal(t, []string{"x"}, l.Mudancas[2].De)
	require.Equal(t, []any{"y", 3.0, map[string]any{"k": "v"}}, l.Mudancas[2].Para)
	require.Equal(t, true, l.Mudancas[3].De)
	require.Equal(t, []string{"e1", "e2"}, l.Efeitos)
	require.Equal(t, []string{"r"}, l.Ressalvas)
	// a entrada não foi alterada
	require.Contains(t, p.Resumo, rlo)

	// vazios continuam vazios (omitempty no JSON)
	v := limparPrevia(previa{Resumo: "x"})
	require.Nil(t, v.Mudancas)
	require.Nil(t, v.Efeitos)
	require.Nil(t, v.Ressalvas)
}

// O resumo que o Preview calcula traz controles de direção (veio do que o modelo digitou): o pedido é
// GRAVADO limpo e a conferência da aprovação compara limpo com limpo — sem isto, o gravado nunca
// bateria com o recalculado e toda aprovação viraria preview_desatualizado.
func TestEscrita_PreviewComBidiEhGravadoLimpoEAAprovacaoExecuta(t *testing.T) {
	sonda := &sondaEscrita{}
	d := sonda.escrita("escreve_crit", Critico, false)
	d.Preview = func(_ context.Context, _ *Escritas, in entradaEscritaTeste) (previa, error) {
		return previa{
			Resumo:    "vai gravar " + rlo + in.Valor,
			Mudancas:  []mudanca{{Campo: "valor", De: "10" + rlo, Para: lri + "100" + pdi}},
			Efeitos:   []string{"efeito" + rlo},
			Ressalvas: []string{rlm + "cuidado"},
		}, nil
	}
	b := novaBancadaEscrita(t)
	apr, err := novasAprovacoesCom(ConfigServidor{Versao: "teste", Escritas: b.E}, []escritaRegistravel{d})
	require.NoError(t, err)

	out := chama(t, b.servidor(principalTeste, d), "escreve_crit", map[string]any{"valor": "abc"})
	pv := out["preview"].(map[string]any)
	require.Equal(t, "vai gravar abc", pv["resumo"], "a resposta da tool também sai limpa")
	id := uuid.MustParse(out["pendente_id"].(string))

	var gravado string
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT preview::text FROM mcp_acoes_pendentes WHERE id = $1`, id).Scan(&gravado))
	for _, c := range []string{rlo, lri, pdi, rlm, `‮`, `⁦`, `⁩`, `‏`} {
		require.NotContains(t, gravado, c)
	}

	_, err = apr.Aprovar(b.ctx, id, &Principal{UserID: principalTeste.UserID, Email: "x@y", Nome: "Dono"}, 0)
	require.NoError(t, err, "limpo na criação e limpo na conferência: não é preview_desatualizado")
	require.Equal(t, 1, sonda.execucoes)
}
