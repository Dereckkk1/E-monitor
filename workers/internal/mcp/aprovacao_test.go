package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

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

// pedido grava o pedido com o resumo que a escrita de teste (sondaEscrita) mostraria para estes
// args — a aprovação compara o resumo recalculado com o gravado, então os dois têm de ser iguais.
func (b *bancadaAprovacao) pedido(t *testing.T, tool, args string) Pendente {
	t.Helper()
	var in entradaEscritaTeste
	resumo := "r" // args fora do schema (os testes de args_invalidos): o resumo não importa
	if json.Unmarshal([]byte(args), &in) == nil && in.Valor != "" {
		resumo = "vai gravar " + in.Valor
	}
	return b.pedidoComPrevia(t, tool, args, previa{Resumo: resumo})
}

// pedidoComPrevia grava o pedido com o resumo dado (a escrita de teste com Preview próprio).
func (b *bancadaAprovacao) pedidoComPrevia(t *testing.T, tool, args string, pv previa) Pendente {
	t.Helper()
	p, _, err := b.E.Pendentes.Criar(b.ctx, NovaPendente{UserID: principalTeste.UserID, TokenID: principalTeste.TokenID,
		Tool: tool, Args: []byte(args), Motivo: "m", Preview: pv}, b.agora)
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

	out, err := b.apr.Aprovar(b.ctx, pend.ID, b.ator(), pend.Tentativa)
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

	_, err := b.apr.Aprovar(b.ctx, pend.ID, b.ator(), pend.Tentativa)
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

	_, err := b.apr.Aprovar(b.ctx, pend.ID, b.ator(), pend.Tentativa)
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

			_, err := b.apr.Aprovar(b.ctx, pend.ID, b.ator(), pend.Tentativa)
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

	_, err := b.apr.Aprovar(b.ctx, pend.ID, b.ator(), pend.Tentativa)
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

	_, err := b.apr.Aprovar(b.ctx, pend.ID, b.ator(), pend.Tentativa)
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

	_, err := b.apr.Aprovar(b.ctx, pend.ID, &Principal{UserID: uuid.New()}, pend.Tentativa)
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
			_, erros[i] = b.apr.Aprovar(b.ctx, pend.ID, b.ator(), pend.Tentativa)
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

	_, err := b.apr.Aprovar(b.ctx, pend.ID, b.ator(), pend.Tentativa)
	require.NoError(t, err)
	got := b.estado(t, pend.ID)
	require.Equal(t, "executada", got.Estado)
	require.Empty(t, got.Resultado)
}

// A: erro comum ou panic na CONFERÊNCIA (Preview) não executa nada e o pedido
// volta a pendente, aprovável de novo — não vira `falhou`.
func TestAprovacao_PreviewComErroOuPanicVoltaAPendenteSemExecutar(t *testing.T) {
	for nome, sonda := range map[string]*sondaEscrita{
		"erro":  {erroPreview: errors.New("pq: relation secreta does not exist")},
		"panic": {panicaPreview: true},
	} {
		t.Run(nome, func(t *testing.T) {
			b := novaBancadaAprovacao(t, sonda.escrita("t_crit", Critico, false))
			pend := b.pedido(t, "t_crit", `{"valor":"abc"}`)

			_, err := b.apr.Aprovar(b.ctx, pend.ID, b.ator(), pend.Tentativa)
			require.ErrorIs(t, err, ErrConferenciaFalhou)
			require.Equal(t, 0, sonda.execucoes, "o Executar não pode rodar")
			got := b.estado(t, pend.ID)
			require.Equal(t, "pendente", got.Estado)
			require.Equal(t, msgConferenciaFalhou, *got.Erro)
			require.NotContains(t, *got.Erro, "secreta")
			require.Equal(t, "conferencia_falhou", b.meta(t, "mcp.pendente.falhou")["codigo"])

			// aprovável de novo, e agora executa
			sonda.erroPreview, sonda.panicaPreview = nil, false
			out, err := b.apr.Aprovar(b.ctx, pend.ID, b.ator(), got.Tentativa)
			require.NoError(t, err)
			require.Equal(t, "executada", out.Pendente.Estado)
			require.Equal(t, 1, sonda.execucoes)
		})
	}
}

// M10: as mensagens de args_invalidos e tool_desconhecida são fixas (o detalhe
// do validador nunca é gravado) e não estendem o prazo do pedido. O prazo é posto
// a 10 min do fim: se a folga de 1 h entrasse, expira_em subiria — e o teste
// quebraria.
func TestAprovacao_ArgsInvalidosEToolDesconhecidaTemMensagemFixaESemFolga(t *testing.T) {
	sonda := &sondaEscrita{}
	b := novaBancadaAprovacao(t, sonda.escrita("t_crit", Critico, false))
	perto := func(id uuid.UUID) time.Time {
		t.Helper()
		fim := b.agora.Add(10 * time.Minute)
		_, err := b.pool.Exec(b.ctx, `UPDATE mcp_acoes_pendentes SET expira_em = $2 WHERE id = $1`, id, fim)
		require.NoError(t, err)
		return b.estado(t, id).ExpiraEm
	}

	// args fora do schema
	pend := b.pedido(t, "t_crit", `{"valor":42}`)
	antes := perto(pend.ID)
	_, err := b.apr.Aprovar(b.ctx, pend.ID, b.ator(), pend.Tentativa)
	var r *acoes.Recusa
	require.ErrorAs(t, err, &r)
	require.Equal(t, codigoArgsInvalidos, r.Codigo)
	require.Equal(t, msgArgsInvalidos, r.Mensagem)
	got := b.estado(t, pend.ID)
	require.Equal(t, msgArgsInvalidos, *got.Erro)
	require.True(t, got.ExpiraEm.Equal(antes), "prazo não pode mudar: era %s, ficou %s", antes, got.ExpiraEm)

	// tool que sumiu
	outra := b.pedido(t, "nao_existe", `{"valor":"abc"}`)
	antes = perto(outra.ID)
	_, err = b.apr.Aprovar(b.ctx, outra.ID, b.ator(), outra.Tentativa)
	require.ErrorAs(t, err, &r)
	require.Equal(t, "tool_desconhecida", r.Codigo)
	require.Equal(t, msgToolDesconhecida, r.Mensagem)
	got = b.estado(t, outra.ID)
	require.Equal(t, msgToolDesconhecida, *got.Erro)
	require.True(t, got.ExpiraEm.Equal(antes), "prazo não pode mudar: era %s, ficou %s", antes, got.ExpiraEm)
}

// Se a conferência falha E o pedido não volta a pendente (aqui: a linha mudou de
// estado no meio), "tente de novo em instantes" seria mentira: devolve a execução
// interrompida.
func TestAprovacao_ConferenciaFalhaSemConseguirDevolverAPendenteEhExecucaoInterrompida(t *testing.T) {
	sonda := &sondaEscrita{erroPreview: errors.New("conferência quebrou")}
	b := novaBancadaAprovacao(t, sonda.escrita("t_crit", Critico, false))
	pend := b.pedido(t, "t_crit", `{"valor":"abc"}`)
	sonda.aoPreview = func() {
		// a linha sai de aprovada/falhou: MarcarFalhaDeNegocio não acha o que atualizar
		_, err := b.pool.Exec(b.ctx, `UPDATE mcp_acoes_pendentes SET estado = 'recusada' WHERE id = $1`, pend.ID)
		require.NoError(t, err)
	}

	_, err := b.apr.Aprovar(b.ctx, pend.ID, b.ator(), pend.Tentativa)

	require.ErrorIs(t, err, ErrExecucaoInterrompida)
	require.NotErrorIs(t, err, ErrConferenciaFalhou)
}

// M10b: recusa de negócio comum (Preview/Executar) continua ganhando folga.
func TestAprovacao_RecusaDeNegocioContinuaEstendendoOPrazo(t *testing.T) {
	sonda := &sondaEscrita{erroExec: &acoes.Recusa{Codigo: "conflito", Mensagem: "Mudou."}}
	b := novaBancadaAprovacao(t, sonda.escrita("t_crit", Critico, false))
	pend := b.pedido(t, "t_crit", `{"valor":"abc"}`)
	// põe o prazo a 10 min do fim: a falha de negócio sobe para agora+1h
	_, err := b.pool.Exec(b.ctx, `UPDATE mcp_acoes_pendentes SET expira_em = $2 WHERE id = $1`, pend.ID, b.agora.Add(10*time.Minute))
	require.NoError(t, err)
	_, err = b.apr.Aprovar(b.ctx, pend.ID, b.ator(), pend.Tentativa)
	require.Error(t, err)
	require.True(t, b.estado(t, pend.ID).ExpiraEm.After(b.agora.Add(30*time.Minute)))
}

// D: o Aprovado devolve a linha fechada (executada_em preenchido).
func TestAprovacao_AprovadoTrazAExecutadaEmEOResultadoSerializado(t *testing.T) {
	sonda := &sondaEscrita{}
	b := novaBancadaAprovacao(t, sonda.escrita("t_crit", Critico, false))
	pend := b.pedido(t, "t_crit", `{"valor":"abc"}`)
	out, err := b.apr.Aprovar(b.ctx, pend.ID, b.ator(), pend.Tentativa)
	require.NoError(t, err)
	require.Equal(t, "executada", out.Pendente.Estado)
	require.NotNil(t, out.Pendente.ExecutadaEm)
	require.JSONEq(t, `{"valor":"abc"}`, string(out.ResultadoJSON))
}

// M8: o cliente que some depois do UPDATE não pode deixar a linha `aprovada`
// sem executor: o contexto cancelado não derruba nem a aprovação nem a execução.
func TestAprovacao_ContextoCanceladoNaoEncalhaOPedido(t *testing.T) {
	sonda := &sondaEscrita{}
	b := novaBancadaAprovacao(t, sonda.escrita("t_crit", Critico, false))
	pend := b.pedido(t, "t_crit", `{"valor":"abc"}`)
	ctx, cancela := context.WithCancel(b.ctx)
	cancela()
	out, err := b.apr.Aprovar(ctx, pend.ID, b.ator(), pend.Tentativa)
	require.NoError(t, err)
	require.Equal(t, "executada", out.Pendente.Estado)
	require.Equal(t, 1, sonda.execucoes)
}

// O Preview roda de novo na aprovação e as recusas que dependem de QUEM pede
// (editar_usuario/desativar_usuario: nunca a si mesmo) precisam enxergar a pessoa:
// o Preview não recebe o Principal, então ele vem no contexto (PrincipalDe). Na
// aprovação é o dono que aprova, com o token do pedido.
func TestAprovacao_PreviewVeOPrincipalDoDonoNoContexto(t *testing.T) {
	sonda := &sondaEscrita{}
	d := sonda.escrita("t_crit", Critico, false)
	var visto *Principal
	var tinha bool
	d.Preview = func(ctx context.Context, _ *Escritas, _ entradaEscritaTeste) (previa, error) {
		visto, tinha = PrincipalDe(ctx)
		return previa{Resumo: "r"}, nil
	}
	b := novaBancadaAprovacao(t, d)
	pend := b.pedidoComPrevia(t, "t_crit", `{"valor":"abc"}`, previa{Resumo: "r"})

	_, err := b.apr.Aprovar(b.ctx, pend.ID, b.ator(), pend.Tentativa)
	require.NoError(t, err)
	require.True(t, tinha, "o Preview da aprovação tem de ver o principal")
	require.Equal(t, principalTeste.UserID, visto.UserID)
	require.Equal(t, principalTeste.TokenID, visto.TokenID, "o token do pedido, não o da sessão do portal (que não tem)")
}

// As ressalvas do Executar (o aviso "o email não saiu", "gravei mas não consegui
// reler…") são o que sobra para a pessoa depois da aprovação: chegam ao Aprovado e
// ficam gravadas na linha do pedido. Sem ressalva, a lista é vazia (nunca nil).
func TestAprovacao_RessalvasDaExecucaoChegamAoAprovadoEFicamGravadas(t *testing.T) {
	sonda := &sondaEscrita{ressalvas: []string{"o email não saiu", "confira com obter_x"}}
	b := novaBancadaAprovacao(t, sonda.escrita("t_crit", Critico, false))
	pend := b.pedido(t, "t_crit", `{"valor":"abc"}`)
	require.Empty(t, pend.Ressalvas, "o pedido recém-criado não tem ressalva")

	out, err := b.apr.Aprovar(b.ctx, pend.ID, b.ator(), pend.Tentativa)
	require.NoError(t, err)
	require.Equal(t, []string{"o email não saiu", "confira com obter_x"}, out.Ressalvas)
	require.Equal(t, out.Ressalvas, out.Pendente.Ressalvas, "a linha devolvida é a gravada")
	require.Equal(t, []string{"o email não saiu", "confira com obter_x"}, b.estado(t, pend.ID).Ressalvas)

	limpa := &sondaEscrita{}
	b2 := novaBancadaAprovacao(t, limpa.escrita("t_crit", Critico, false))
	limpo := b2.pedido(t, "t_crit", `{"valor":"abc"}`)
	out, err = b2.apr.Aprovar(b2.ctx, limpo.ID, b2.ator(), limpo.Tentativa)
	require.NoError(t, err)
	require.NotNil(t, out.Ressalvas, "sem ressalva a lista é vazia, não nil")
	require.Empty(t, out.Ressalvas)
}

// ── resumo desatualizado (preview_desatualizado) ────────────────────────

// O estado muda entre o pedido e a aprovação sem o Preview recusar: o resumo recalculado já não é
// o que a pessoa viu, então a aprovação NÃO executa, devolve o pedido a pendente com o resumo NOVO
// no lugar do antigo (e o prazo com folga) e só a aprovação seguinte — com o resumo novo à vista —
// executa.
func TestAprovacao_ResumoQueMudouEhRecusaPreviewDesatualizadoEAtualizaOPedido(t *testing.T) {
	sonda := &sondaEscrita{}
	d := sonda.escrita("t_crit", Critico, false)
	afeta := "1 usuário"
	d.Preview = func(_ context.Context, _ *Escritas, in entradaEscritaTeste) (previa, error) {
		return previa{Resumo: "vai gravar " + in.Valor, Efeitos: []string{"Afeta " + afeta}}, nil
	}
	b := novaBancadaAprovacao(t, d)
	pend := b.pedidoComPrevia(t, "t_crit", `{"valor":"abc"}`, previa{Resumo: "vai gravar abc", Efeitos: []string{"Afeta 1 usuário"}})
	// prazo a 10 min do fim: a folga de 1 h da recusa tem de aparecer
	_, err := b.pool.Exec(b.ctx, `UPDATE mcp_acoes_pendentes SET expira_em = $2 WHERE id = $1`, pend.ID, b.agora.Add(10*time.Minute))
	require.NoError(t, err)
	afeta = "2 usuários" // o estado mudou depois do pedido

	_, err = b.apr.Aprovar(b.ctx, pend.ID, b.ator(), pend.Tentativa)

	var r *acoes.Recusa
	require.ErrorAs(t, err, &r)
	require.Equal(t, codigoPreviewDesatualizado, r.Codigo)
	require.Equal(t, 0, sonda.execucoes, "nada executa com o resumo velho")
	got := b.estado(t, pend.ID)
	require.Equal(t, "pendente", got.Estado)
	require.Nil(t, got.AprovadaEm)
	require.Equal(t, msgPreviewDesatualizado, *got.Erro)
	require.Equal(t, []string{"Afeta 2 usuários"}, got.Preview.Efeitos, "o resumo gravado passa a ser o novo")
	require.True(t, got.ExpiraEm.After(b.agora.Add(30*time.Minute)), "a recusa ganha folga de prazo: %s", got.ExpiraEm)
	require.Equal(t, codigoPreviewDesatualizado, b.meta(t, "mcp.pendente.falhou")["codigo"])

	out, err := b.apr.Aprovar(b.ctx, pend.ID, b.ator(), got.Tentativa)
	require.NoError(t, err, "com o resumo novo gravado, a próxima aprovação executa")
	require.Equal(t, "executada", out.Pendente.Estado)
	require.Equal(t, 1, sonda.execucoes)
}

// O resumo gravado passou pelo JSONB (números e listas voltam como float64 e []any): o que diz a
// mesma coisa não pode virar preview_desatualizado, ou nenhuma aprovação passaria.
func TestAprovacao_ResumoIgualDepoisDoJSONBNaoEhDesatualizado(t *testing.T) {
	cheia := func(in entradaEscritaTeste) previa {
		return previa{
			Resumo: "vai gravar " + in.Valor,
			Mudancas: []mudanca{
				{Campo: "cpm", De: 12.5, Para: 1500.75},
				{Campo: "ativo", De: false, Para: true},
				{Campo: "eventos", De: nil, Para: []string{"detection.confirmed", "webhook.test"}},
				{Campo: "nome", De: "A", Para: "B"},
			},
			Efeitos:   []string{"efeito um", "efeito dois"},
			Ressalvas: []string{"ressalva"},
		}
	}
	sonda := &sondaEscrita{}
	d := sonda.escrita("t_crit", Critico, false)
	d.Preview = func(_ context.Context, _ *Escritas, in entradaEscritaTeste) (previa, error) { return cheia(in), nil }
	b := novaBancadaAprovacao(t, d)
	pend := b.pedidoComPrevia(t, "t_crit", `{"valor":"abc"}`, cheia(entradaEscritaTeste{Valor: "abc"}))

	out, err := b.apr.Aprovar(b.ctx, pend.ID, b.ator(), pend.Tentativa)

	require.NoError(t, err)
	require.Equal(t, "executada", out.Pendente.Estado)
	require.Equal(t, 1, sonda.execucoes)
}

// Cada parte do resumo conta: efeito, ressalva, mudança ou o resumo em si.
func TestAprovacao_QualquerParteDoResumoQueMudaEhDesatualizada(t *testing.T) {
	base := previa{
		Resumo:    "vai gravar abc",
		Mudancas:  []mudanca{{Campo: "x", De: "a", Para: "b"}},
		Efeitos:   []string{"efeito"},
		Ressalvas: []string{"ressalva"},
	}
	for nome, muda := range map[string]func(*previa){
		"resumo":   func(p *previa) { p.Resumo += "!" },
		"mudança":  func(p *previa) { p.Mudancas[0].Para = "c" },
		"efeito":   func(p *previa) { p.Efeitos = append(p.Efeitos, "outro") },
		"ressalva": func(p *previa) { p.Ressalvas = nil },
	} {
		t.Run(nome, func(t *testing.T) {
			sonda := &sondaEscrita{}
			d := sonda.escrita("t_crit", Critico, false)
			d.Preview = func(context.Context, *Escritas, entradaEscritaTeste) (previa, error) {
				nova := previa{Resumo: base.Resumo, Mudancas: append([]mudanca(nil), base.Mudancas...),
					Efeitos: append([]string(nil), base.Efeitos...), Ressalvas: append([]string(nil), base.Ressalvas...)}
				muda(&nova)
				return nova, nil
			}
			b := novaBancadaAprovacao(t, d)
			pend := b.pedidoComPrevia(t, "t_crit", `{"valor":"abc"}`, base)

			_, err := b.apr.Aprovar(b.ctx, pend.ID, b.ator(), pend.Tentativa)

			var r *acoes.Recusa
			require.ErrorAs(t, err, &r)
			require.Equal(t, codigoPreviewDesatualizado, r.Codigo)
			require.Equal(t, 0, sonda.execucoes)
		})
	}
}

// A ação que falha DEPOIS de gravar e sabe dizer o que ficou feito (*acoes.ErroAposEscrita): o
// pedido fica `falhou` com o TEXTO DA AÇÃO (não o genérico), o erro técnico não vai para o banco, e
// a execução interrompida carrega esse texto para quem responde à pessoa.
func TestAprovacao_ErroAposEscritaViraFalhouComOTextoDaAcao(t *testing.T) {
	const texto = "O usuário x@y.teste foi criado, mas a carteira não foi gravada por inteiro: confira em /admin/users."
	sonda := &sondaEscrita{erroExec: &acoes.ErroAposEscrita{Mensagem: texto, Causa: errors.New("pq: relation secreta does not exist")}}
	b := novaBancadaAprovacao(t, sonda.escrita("t_crit", Critico, false))
	pend := b.pedido(t, "t_crit", `{"valor":"abc"}`)

	_, err := b.apr.Aprovar(b.ctx, pend.ID, b.ator(), pend.Tentativa)

	require.ErrorIs(t, err, ErrExecucaoInterrompida)
	require.Equal(t, texto, mensagemDaInterrupcao(err))
	got := b.estado(t, pend.ID)
	require.Equal(t, "falhou", got.Estado)
	require.Equal(t, texto, *got.Erro)
	require.NotContains(t, *got.Erro, "secreta")
	require.Equal(t, "falha_apos_escrita", b.meta(t, "mcp.pendente.falhou")["codigo"])
	// o erro interno comum segue com o texto genérico
	require.Equal(t, msgInterrompida, mensagemDaInterrupcao(ErrExecucaoInterrompida))
}
