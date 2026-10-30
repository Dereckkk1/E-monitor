package mcp

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/acoes"
)

// bancadaAprovacao: a bancada do motor mais um Aprovacoes sobre um índice de
// teste (as escritas falsas), com o dono = principalTeste.
type bancadaAprovacao struct {
	*bancadaEscrita
	apr *Aprovacoes
}

func novaBancadaAprovacao(t *testing.T, lista ...escritaRegistravel) *bancadaAprovacao {
	t.Helper()
	b := novaBancadaEscrita(t)
	apr, err := novasAprovacoesCom(ConfigServidor{Versao: "teste", Escritas: b.E}, lista)
	require.NoError(t, err)
	return &bancadaAprovacao{bancadaEscrita: b, apr: apr}
}

func (b *bancadaAprovacao) pedido(t *testing.T, tool, args string) Pendente {
	t.Helper()
	p, _, err := b.E.Pendentes.Criar(b.ctx, NovaPendente{UserID: principalTeste.UserID, TokenID: principalTeste.TokenID,
		Tool: tool, Args: []byte(args), Motivo: "m", Preview: previa{Resumo: "r"}}, b.agora)
	require.NoError(t, err)
	return p
}

// ator é a sessão do portal: sem token.
func (b *bancadaAprovacao) ator() *Principal {
	return &Principal{UserID: principalTeste.UserID, Email: "x@y", Nome: "Dono"}
}

func (b *bancadaAprovacao) estado(t *testing.T, id uuid.UUID) Pendente {
	t.Helper()
	p, err := b.E.Pendentes.Obter(b.ctx, id, principalTeste.UserID)
	require.NoError(t, err)
	return p
}

func (b *bancadaAprovacao) meta(t *testing.T, acao string) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT metadata FROM audit_log WHERE action = $1`, acao).Scan(&m))
	return m
}

// 1
func TestAprovacao_ExecutaUmaVezComOPrincipalDoDonoETokenDoPedido(t *testing.T) {
	sonda := &sondaEscrita{}
	var visto *Principal
	d := sonda.escrita("t_crit", Critico, false)
	exec := d.Executar
	d.Executar = func(ctx context.Context, E *Escritas, p *Principal, in entradaEscritaTeste) (feito, error) {
		visto = p
		require.Equal(t, "abc", in.Valor)
		return exec(ctx, E, p, in)
	}
	b := novaBancadaAprovacao(t, d)
	pend := b.pedido(t, "t_crit", `{"valor":"abc"}`)

	out, err := b.apr.Aprovar(b.ctx, pend.ID, b.ator())
	require.NoError(t, err)
	require.Equal(t, 1, sonda.execucoes)
	require.Equal(t, "executada", out.Pendente.Estado)
	require.Equal(t, principalTeste.UserID, visto.UserID)
	require.Equal(t, principalTeste.TokenID, visto.TokenID)

	got := b.estado(t, pend.ID)
	require.Equal(t, "executada", got.Estado)
	require.JSONEq(t, `{"valor":"abc"}`, string(got.Resultado))

	require.Equal(t, 1, b.auditorias(t, "mcp.pendente.aprovada"))
	require.Equal(t, 1, b.auditorias(t, "mcp.pendente.executada"))
	m := b.meta(t, "mcp.escrita.t_crit")
	mcp := m["mcp"].(map[string]any)
	require.Equal(t, principalTeste.TokenID.String(), mcp["token_id"])
	require.Equal(t, pend.ID.String(), mcp["pendente_id"])
	require.Equal(t, "critico", m["nivel"])
	ex := b.meta(t, "mcp.pendente.executada")
	require.Equal(t, pend.ID.String(), ex["mcp"].(map[string]any)["pendente_id"])
}

// 2
func TestAprovacao_PreviewRecusaNaAprovacaoNaoExecutaEVoltaAPendente(t *testing.T) {
	sonda := &sondaEscrita{}
	d := sonda.escrita("t_crit", Critico, false)
	d.Preview = func(context.Context, *Escritas, entradaEscritaTeste) (previa, error) {
		return previa{}, &acoes.Recusa{Codigo: "nao_achada", Mensagem: "O alvo sumiu."}
	}
	b := novaBancadaAprovacao(t, d)
	pend := b.pedido(t, "t_crit", `{"valor":"abc"}`)

	_, err := b.apr.Aprovar(b.ctx, pend.ID, b.ator())
	var r *acoes.Recusa
	require.ErrorAs(t, err, &r)
	require.Equal(t, "nao_achada", r.Codigo)
	require.Equal(t, 0, sonda.execucoes)
	got := b.estado(t, pend.ID)
	require.Equal(t, "pendente", got.Estado)
	require.Equal(t, "O alvo sumiu.", *got.Erro)
	require.Equal(t, "nao_achada", b.meta(t, "mcp.pendente.falhou")["codigo"])
}

// 3
func TestAprovacao_ExecutarRecusaVoltaAPendente(t *testing.T) {
	sonda := &sondaEscrita{erroExec: &acoes.Recusa{Codigo: "conflito", Mensagem: "Mudou no meio."}}
	b := novaBancadaAprovacao(t, sonda.escrita("t_crit", Critico, false))
	pend := b.pedido(t, "t_crit", `{"valor":"abc"}`)

	_, err := b.apr.Aprovar(b.ctx, pend.ID, b.ator())
	var r *acoes.Recusa
	require.ErrorAs(t, err, &r)
	require.Equal(t, 1, sonda.execucoes)
	got := b.estado(t, pend.ID)
	require.Equal(t, "pendente", got.Estado)
	require.Equal(t, "Mudou no meio.", *got.Erro)
	require.Equal(t, "conflito", b.meta(t, "mcp.pendente.falhou")["codigo"])
	require.Equal(t, 0, b.auditorias(t, "mcp.escrita.t_crit"))
}

// 4
func TestAprovacao_ErroInternoEPanicViramFalhouComMensagemGenerica(t *testing.T) {
	for nome, sonda := range map[string]*sondaEscrita{
		"erro":  {erroExec: errors.New("pq: relation secreta does not exist")},
		"panic": {panica: true},
	} {
		t.Run(nome, func(t *testing.T) {
			b := novaBancadaAprovacao(t, sonda.escrita("t_crit", Critico, false))
			pend := b.pedido(t, "t_crit", `{"valor":"abc"}`)

			_, err := b.apr.Aprovar(b.ctx, pend.ID, b.ator())
			require.ErrorIs(t, err, ErrExecucaoInterrompida)
			got := b.estado(t, pend.ID)
			require.Equal(t, "falhou", got.Estado)
			require.Equal(t, msgInterrompida, *got.Erro)
			require.Equal(t, "interno", b.meta(t, "mcp.pendente.falhou")["codigo"])
		})
	}
}

// 5
func TestAprovacao_ToolQueNaoExisteMaisVoltaAPendente(t *testing.T) {
	b := novaBancadaAprovacao(t, (&sondaEscrita{}).escrita("outra", Critico, false))
	pend := b.pedido(t, "t_crit", `{"valor":"abc"}`)

	_, err := b.apr.Aprovar(b.ctx, pend.ID, b.ator())
	var r *acoes.Recusa
	require.ErrorAs(t, err, &r)
	require.Equal(t, "tool_desconhecida", r.Codigo)
	got := b.estado(t, pend.ID)
	require.Equal(t, "pendente", got.Estado)
	require.NotNil(t, got.Erro)
}

// 6
func TestAprovacao_ArgsGravadosForaDoSchemaViramArgsInvalidos(t *testing.T) {
	sonda := &sondaEscrita{}
	b := novaBancadaAprovacao(t, sonda.escrita("t_crit", Critico, false))
	pend := b.pedido(t, "t_crit", `{"valor":42}`)

	_, err := b.apr.Aprovar(b.ctx, pend.ID, b.ator())
	var r *acoes.Recusa
	require.ErrorAs(t, err, &r)
	require.Equal(t, "args_invalidos", r.Codigo)
	require.Equal(t, 0, sonda.execucoes)
	require.Equal(t, "pendente", b.estado(t, pend.ID).Estado)
}

// 7
func TestAprovacao_OutroAdminNaoAchaOPedido(t *testing.T) {
	sonda := &sondaEscrita{}
	b := novaBancadaAprovacao(t, sonda.escrita("t_crit", Critico, false))
	pend := b.pedido(t, "t_crit", `{"valor":"abc"}`)

	_, err := b.apr.Aprovar(b.ctx, pend.ID, &Principal{UserID: uuid.New()})
	require.ErrorIs(t, err, ErrPendenteNaoAchada)
	require.Equal(t, 0, sonda.execucoes)
	require.Equal(t, "pendente", b.estado(t, pend.ID).Estado)
}

// 8
func TestAprovacao_AprovacoesConcorrentesExecutamUmaVez(t *testing.T) {
	var n int
	var mu sync.Mutex
	sonda := &sondaEscrita{}
	d := sonda.escrita("t_crit", Critico, false)
	d.Executar = func(_ context.Context, _ *Escritas, _ *Principal, _ entradaEscritaTeste) (feito, error) {
		mu.Lock()
		n++
		mu.Unlock()
		return feito{Resultado: "ok"}, nil
	}
	b := novaBancadaAprovacao(t, d)
	pend := b.pedido(t, "t_crit", `{"valor":"abc"}`)

	var wg sync.WaitGroup
	erros := make([]error, 6)
	for i := range erros {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, erros[i] = b.apr.Aprovar(b.ctx, pend.ID, b.ator())
		}()
	}
	wg.Wait()
	require.Equal(t, 1, n)
	ok := 0
	for _, e := range erros {
		if e == nil {
			ok++
		}
	}
	require.Equal(t, 1, ok)
}

// 9
func TestAprovacao_RecusarFechaEAudita(t *testing.T) {
	sonda := &sondaEscrita{}
	b := novaBancadaAprovacao(t, sonda.escrita("t_crit", Critico, false))
	pend := b.pedido(t, "t_crit", `{"valor":"abc"}`)

	got, err := b.apr.Recusar(b.ctx, pend.ID, b.ator())
	require.NoError(t, err)
	require.Equal(t, "recusada", got.Estado)
	require.Equal(t, "recusada", b.estado(t, pend.ID).Estado)
	require.Equal(t, 1, b.auditorias(t, "mcp.pendente.recusada"))
	require.Equal(t, 0, sonda.execucoes)
}

// 10
func TestAprovacao_ResultadoQueNaoSerializaFechaExecutadaSemResultado(t *testing.T) {
	sonda := &sondaEscrita{resultado: map[string]any{"c": make(chan int)}}
	b := novaBancadaAprovacao(t, sonda.escrita("t_crit", Critico, false))
	pend := b.pedido(t, "t_crit", `{"valor":"abc"}`)

	_, err := b.apr.Aprovar(b.ctx, pend.ID, b.ator())
	require.NoError(t, err)
	got := b.estado(t, pend.ID)
	require.Equal(t, "executada", got.Estado)
	require.Empty(t, got.Resultado)
}
