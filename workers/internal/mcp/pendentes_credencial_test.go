package mcp

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// A aprovação vale para a versão que a pessoa leu (a tentativa): depois de uma tentativa que trocou o
// resumo gravado (preview_desatualizado), quem ainda mostra o resumo velho não aprova nada.
func TestPendentes_AprovarExigeATentativaVigente(t *testing.T) {
	ctx, _, s, dono, _ := pendentesDB(t)
	p, _, _ := s.Criar(ctx, novaDeTeste(dono, `{"id":1}`), t0)

	// tentativa de uma versão que nunca existiu: nada muda
	_, err := s.Aprovar(ctx, p.ID, dono, 7, t0)
	require.ErrorIs(t, err, ErrPendenteMudou)
	_, err = s.Aprovar(ctx, p.ID, dono, -1, t0)
	require.ErrorIs(t, err, ErrPendenteMudou)
	o, _ := s.Obter(ctx, p.ID, dono)
	require.Equal(t, "pendente", o.Estado)
	require.Equal(t, 0, o.Tentativa)

	// a aba A aprova com a versão 0 e a conferência troca o resumo (preview_desatualizado)
	a, err := s.Aprovar(ctx, p.ID, dono, 0, t0)
	require.NoError(t, err)
	novo := previa{Resumo: "arquivar (13 dias)"}
	ok, err := s.MarcarFalhaDeNegocio(ctx, p.ID, a.Tentativa, "o estado mudou", t0, true, &novo)
	require.NoError(t, err)
	require.True(t, ok)

	// a aba B ainda mostra o resumo da versão 0: pendente_mudou e nada executa
	_, err = s.Aprovar(ctx, p.ID, dono, 0, t0)
	require.ErrorIs(t, err, ErrPendenteMudou)
	o, _ = s.Obter(ctx, p.ID, dono)
	require.Equal(t, "pendente", o.Estado)
	require.Equal(t, 1, o.Tentativa, "a aprovação recusada não gasta tentativa")
	require.Equal(t, "arquivar (13 dias)", o.Preview.Resumo)

	// quem leu o resumo novo (tentativa 1) aprova
	b, err := s.Aprovar(ctx, p.ID, dono, o.Tentativa, t0)
	require.NoError(t, err)
	require.Equal(t, 2, b.Tentativa)
}

// ── Credencial revogada x pedido pendente ────────────────────────────────────────────────────

func pedidoComToken(t *testing.T, ctx context.Context, s *Pendentes, dono, token uuid.UUID, args string) Pendente {
	t.Helper()
	n := novaDeTeste(dono, args)
	n.TokenID = token
	p, _, err := s.Criar(ctx, n, t0)
	require.NoError(t, err)
	return p
}

// tentarAprovarVigente aprova com a tentativa vigente do pedido.
func tentarAprovarVigente(ctx context.Context, s *Pendentes, p Pendente, dono uuid.UUID) error {
	_, err := s.Aprovar(ctx, p.ID, dono, p.Tentativa, t0)
	return err
}

func TestPendentes_AprovarComTokenPessoalRevogadoFechaComoExpirada(t *testing.T) {
	ctx, pool, s, dono, _ := pendentesDB(t)
	tok := inserirToken(t, ctx, pool, dono, "pessoal", nil, "1 day")
	p := pedidoComToken(t, ctx, s, dono, tok, `{"id":1}`)
	// Revogado direto no banco: o caminho proativo não rodou. A aprovação ainda tem de recusar.
	_, err := pool.Exec(ctx, `UPDATE mcp_tokens SET revoked_at = NOW() WHERE id = $1`, tok)
	require.NoError(t, err)

	require.ErrorIs(t, tentarAprovarVigente(ctx, s, p, dono), ErrCredencialRevogada)
	o, _ := s.Obter(ctx, p.ID, dono)
	require.Equal(t, "expirada", o.Estado)
	require.Equal(t, 0, o.Tentativa, "nada foi aprovado")
	require.NotNil(t, o.Erro)
	require.Equal(t, msgCredencialRevogada, *o.Erro)

	// Já fechado: o 409 seguinte explica o motivo (e a recusa também)
	err = tentarAprovarVigente(ctx, s, p, dono)
	require.Equal(t, ErrEstadoPendente{Estado: "expirada", Motivo: msgCredencialRevogada}, err)
	_, err = s.Recusar(ctx, p.ID, dono, t0)
	require.Equal(t, ErrEstadoPendente{Estado: "expirada", Motivo: msgCredencialRevogada}, err)
}

func TestPendentes_AprovarComTokenVencidoOuSumidoNaoAprova(t *testing.T) {
	ctx, pool, s, dono, _ := pendentesDB(t)

	vencido := inserirToken(t, ctx, pool, dono, "pessoal", nil, "-1 minute")
	p := pedidoComToken(t, ctx, s, dono, vencido, `{"id":1}`)
	require.ErrorIs(t, tentarAprovarVigente(ctx, s, p, dono), ErrCredencialRevogada)

	// sem token_id
	q := pedidoComToken(t, ctx, s, dono, uuid.Nil, `{"id":2}`)
	require.Nil(t, q.TokenID)
	require.ErrorIs(t, tentarAprovarVigente(ctx, s, q, dono), ErrCredencialRevogada)

	// o token foi apagado: a FK vira NULL
	vivo := inserirToken(t, ctx, pool, dono, "pessoal", nil, "1 day")
	r := pedidoComToken(t, ctx, s, dono, vivo, `{"id":3}`)
	_, err := pool.Exec(ctx, `DELETE FROM mcp_tokens WHERE id = $1`, vivo)
	require.NoError(t, err)
	r, _ = s.Obter(ctx, r.ID, dono)
	require.Nil(t, r.TokenID, "ON DELETE SET NULL")
	require.ErrorIs(t, tentarAprovarVigente(ctx, s, r, dono), ErrCredencialRevogada)
}

// O token da chamada OAuth é o access token (dura uma hora); o pedido dura 24. Vale a CONEXÃO: o
// pedido é aprovável enquanto o usuário tiver algum token vivo daquele cliente.
func TestPendentes_AprovarPedidoOAuthVaiPelaConexao(t *testing.T) {
	ctx, pool, s, dono, outro := pendentesDB(t)
	inserirClienteOAuth(t, ctx, pool, "cli-a")
	inserirClienteOAuth(t, ctx, pool, "cli-b")
	cli, outroCli := "cli-a", "cli-b"

	acesso := inserirToken(t, ctx, pool, dono, "oauth_access", &cli, "-30 minutes") // já venceu
	refresh := inserirToken(t, ctx, pool, dono, "oauth_refresh", &cli, "10 days")
	p := pedidoComToken(t, ctx, s, dono, acesso, `{"id":1}`)

	// o access token venceu, o refresh segue: a conexão está viva
	require.NoError(t, tentarAprovarVigente(ctx, s, p, dono))

	// conexão revogada por inteiro: recusa
	q := pedidoComToken(t, ctx, s, dono, acesso, `{"id":2}`)
	_, err := pool.Exec(ctx, `UPDATE mcp_tokens SET revoked_at = NOW() WHERE id = $1`, refresh)
	require.NoError(t, err)
	require.ErrorIs(t, tentarAprovarVigente(ctx, s, q, dono), ErrCredencialRevogada)

	// um token vivo de OUTRO cliente do mesmo usuário não salva o pedido
	inserirToken(t, ctx, pool, dono, "oauth_refresh", &outroCli, "10 days")
	r := pedidoComToken(t, ctx, s, dono, acesso, `{"id":3}`)
	require.ErrorIs(t, tentarAprovarVigente(ctx, s, r, dono), ErrCredencialRevogada)

	// nem o token vivo de OUTRO usuário no mesmo cliente
	inserirToken(t, ctx, pool, outro, "oauth_refresh", &cli, "10 days")
	u := pedidoComToken(t, ctx, s, dono, acesso, `{"id":4}`)
	require.ErrorIs(t, tentarAprovarVigente(ctx, s, u, dono), ErrCredencialRevogada)
}

// O trigger de users (0072, função trocada na 0074) fecha também os pedidos `pendente` do usuário, e
// o texto gravado é o mesmo que o Go usa.
func TestTrigger_RevogacaoDoUsuarioExpiraSeusPedidosPendentes(t *testing.T) {
	casos := []struct{ nome, sql string }{
		{"redefinir senha", `UPDATE users SET password_hash = 'outro-hash' WHERE id = $1`},
		{"desativar", `UPDATE users SET is_active = false WHERE id = $1`},
		{"rebaixar de admin", `UPDATE users SET role = 'operator' WHERE id = $1`},
		{"apagar (soft delete)", `UPDATE users SET deleted_at = NOW() WHERE id = $1`},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			ctx, pool, s, dono, outro := pendentesDB(t)
			a, _, _ := s.Criar(ctx, novaDeTeste(dono, `{"id":1}`), t0)
			b, _, _ := s.Criar(ctx, novaDeTeste(dono, `{"id":2}`), t0)
			x, _, _ := s.Criar(ctx, novaDeTeste(outro, `{"id":1}`), t0)
			// b está sendo executado: a pessoa já clicou, o trigger não a derruba
			_, err := s.Aprovar(ctx, b.ID, dono, 0, t0)
			require.NoError(t, err)

			_, err = pool.Exec(ctx, c.sql, dono)
			require.NoError(t, err)

			e, erro := estadoPend(t, ctx, pool, a.ID)
			require.Equal(t, "expirada", e)
			require.NotNil(t, erro)
			require.Equal(t, msgCredencialRevogada, *erro, "o texto do trigger é o do Go")
			e, _ = estadoPend(t, ctx, pool, b.ID)
			require.Equal(t, "aprovada", e)
			e, _ = estadoPend(t, ctx, pool, x.ID)
			require.Equal(t, "pendente", e, "pedido de outra pessoa")

			// e o pedido expirado não é aprovável: o 409 traz o motivo
			err = tentarAprovarVigente(ctx, s, a, dono)
			require.Equal(t, ErrEstadoPendente{Estado: "expirada", Motivo: msgCredencialRevogada}, err)
		})
	}
}

func TestTrigger_AlteracaoSemRevogacaoNaoMexeNosPedidos(t *testing.T) {
	ctx, pool, s, dono, _ := pendentesDB(t)
	a, _, _ := s.Criar(ctx, novaDeTeste(dono, `{"id":1}`), t0)
	_, err := pool.Exec(ctx, `UPDATE users SET name = 'Outro Nome' WHERE id = $1`, dono)
	require.NoError(t, err)
	e, _ := estadoPend(t, ctx, pool, a.ID)
	require.Equal(t, "pendente", e)
}
