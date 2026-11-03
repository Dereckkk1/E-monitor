package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// A escrita já aconteceu e a gravação do desfecho falha uma vez (soluço de conexão): a segunda
// tentativa fecha o pedido como `executada`, em vez de deixá-lo `aprovada` até o varredor dá-lo por
// interrompido (e a pessoa ler "falhou" para uma ação que rodou).
func TestAprovacao_RefechaOPedidoExecutadoSeAPrimeiraGravacaoFalha(t *testing.T) {
	esperaAntes := esperaAntesDeRefechar
	esperaAntesDeRefechar = 0
	t.Cleanup(func() { esperaAntesDeRefechar = esperaAntes })

	sonda := &sondaEscrita{}
	b := novaBancadaAprovacao(t, sonda.escrita("t_crit", Critico, false))
	chamadas := 0
	b.apr.marcarExecutada = func(ctx context.Context, id uuid.UUID, tentativa int, res json.RawMessage, ress []string, agora time.Time) (Pendente, bool, error) {
		chamadas++
		if chamadas == 1 {
			return Pendente{}, false, erroTexto("conexão caiu")
		}
		return b.E.Pendentes.MarcarExecutada(ctx, id, tentativa, res, ress, agora)
	}
	pend := b.pedido(t, "t_crit", `{"valor":"abc"}`)

	out, err := b.apr.Aprovar(b.ctx, pend.ID, b.ator(), pend.Tentativa)
	require.NoError(t, err)
	require.Equal(t, 1, sonda.execucoes)
	require.Equal(t, 2, chamadas, "uma tentativa a mais, e só uma")
	require.Equal(t, "executada", out.Pendente.Estado)
	require.NotNil(t, out.Pendente.ExecutadaEm, "a linha devolvida é a gravada na segunda tentativa")
	require.Equal(t, "executada", b.estado(t, pend.ID).Estado)
}

// Se as duas gravações falham, a pessoa ainda lê o desfecho verdadeiro (a ação rodou) e a linha
// fica para o varredor; o log e a auditoria não somem.
func TestAprovacao_SeNenhumaGravacaoFechaOPedidoAindaDizExecutada(t *testing.T) {
	esperaAntes := esperaAntesDeRefechar
	esperaAntesDeRefechar = 0
	t.Cleanup(func() { esperaAntesDeRefechar = esperaAntes })

	sonda := &sondaEscrita{}
	b := novaBancadaAprovacao(t, sonda.escrita("t_crit", Critico, false))
	chamadas := 0
	b.apr.marcarExecutada = func(context.Context, uuid.UUID, int, json.RawMessage, []string, time.Time) (Pendente, bool, error) {
		chamadas++
		return Pendente{}, false, erroTexto("banco fora")
	}
	pend := b.pedido(t, "t_crit", `{"valor":"abc"}`)

	out, err := b.apr.Aprovar(b.ctx, pend.ID, b.ator(), pend.Tentativa)
	require.NoError(t, err)
	require.Equal(t, 2, chamadas)
	require.Equal(t, "executada", out.Pendente.Estado)
	require.Equal(t, "aprovada", b.estado(t, pend.ID).Estado, "a linha ficou para o varredor")
	require.Equal(t, 1, b.auditorias(t, "mcp.pendente.executada"))
}

// !ok (a linha já não está em aprovada/falhou desta tentativa) não é erro do banco: não se repete.
func TestAprovacao_NaoRefechaQuandoALinhaJaNaoEhDaTentativa(t *testing.T) {
	esperaAntes := esperaAntesDeRefechar
	esperaAntesDeRefechar = 0
	t.Cleanup(func() { esperaAntesDeRefechar = esperaAntes })

	sonda := &sondaEscrita{}
	b := novaBancadaAprovacao(t, sonda.escrita("t_crit", Critico, false))
	chamadas := 0
	b.apr.marcarExecutada = func(context.Context, uuid.UUID, int, json.RawMessage, []string, time.Time) (Pendente, bool, error) {
		chamadas++
		return Pendente{}, false, nil
	}
	pend := b.pedido(t, "t_crit", `{"valor":"abc"}`)
	_, err := b.apr.Aprovar(b.ctx, pend.ID, b.ator(), pend.Tentativa)
	require.NoError(t, err)
	require.Equal(t, 1, chamadas)
}
