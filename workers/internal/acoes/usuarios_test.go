package acoes

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/mcp/mcptest"
	"radiocheck/internal/users"
)

const senhaBoa = "senhaSuperSegura1"

// emissorFalso registra o que a criação pediu e devolve o desfecho combinado.
type emissorFalso struct {
	chamadas   int
	ultimaSenh string
	ultimoAtor *uuid.UUID
	resposta   *BoasVindas
	erro       error
}

func (e *emissorFalso) Emitir(_ context.Context, _ *users.User, senha string, ator *uuid.UUID) (*BoasVindas, error) {
	e.chamadas++
	e.ultimaSenh = senha
	e.ultimoAtor = ator
	return e.resposta, e.erro
}

func srvUsuarios(t *testing.T) (context.Context, *Servico, uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx, pool := mcptest.DBCatalogo(t)
	a := mcptest.Cliente(t, ctx, pool, "Cliente Usuário A "+uuid.NewString()[:6])
	b := mcptest.Cliente(t, ctx, pool, "Cliente Usuário B "+uuid.NewString()[:6])
	return ctx, NovoServico(pool, nil, nil, nil), a, b
}

func emailUnico() string { return "u" + uuid.NewString()[:8] + "@acoes.test" }

func exigeCodigo(t *testing.T, err error, codigo string) {
	t.Helper()
	var r *Recusa
	require.ErrorAs(t, err, &r)
	require.Equal(t, codigo, r.Codigo)
	require.Equal(t, codigo, r.Mensagem, "a mensagem é o código: a rota o devolve como corpo")
}

func TestCriarUsuario_Validacoes(t *testing.T) {
	ctx, srv, a, _ := srvUsuarios(t)
	ok := EntradaUsuario{Email: emailUnico(), Senha: senhaBoa, Nome: "N", Papel: "admin"}

	casos := []struct {
		nome   string
		mexe   func(e *EntradaUsuario)
		codigo string
	}{
		{"sem email", func(e *EntradaUsuario) { e.Email = "" }, CodigoCamposObrigatorios},
		{"sem senha", func(e *EntradaUsuario) { e.Senha = "" }, CodigoCamposObrigatorios},
		{"sem nome", func(e *EntradaUsuario) { e.Nome = "" }, CodigoCamposObrigatorios},
		{"senha curta", func(e *EntradaUsuario) { e.Senha = "curta" }, CodigoSenhaCurta},
		{"papel operator", func(e *EntradaUsuario) { e.Papel = "operator" }, CodigoPapelInvalido},
		{"papel viewer cru", func(e *EntradaUsuario) { e.Papel = "viewer" }, CodigoPapelInvalido},
		{"cliente sem carteira", func(e *EntradaUsuario) { e.Papel = "client" }, CodigoClienteObrigatorio},
		{"cliente com lista vazia", func(e *EntradaUsuario) { e.Papel = "client"; e.ClienteIDs = []uuid.UUID{} }, CodigoClienteObrigatorio},
		{"admin com client_id", func(e *EntradaUsuario) { e.ClienteID = &a }, CodigoClienteProibidoAdmin},
		{"admin com client_ids", func(e *EntradaUsuario) { e.ClienteIDs = []uuid.UUID{a} }, CodigoClienteProibidoAdmin},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			in := ok
			c.mexe(&in)
			u, bv, err := srv.CriarUsuario(ctx, in)
			exigeCodigo(t, err, c.codigo)
			require.Nil(t, u)
			require.Nil(t, bv)
		})
	}
}

func TestCriarUsuario_AdminEClienteComPapelDaAPI(t *testing.T) {
	ctx, srv, a, _ := srvUsuarios(t)

	adm, bv, err := srv.CriarUsuario(ctx, EntradaUsuario{Email: emailUnico(), Senha: senhaBoa, Nome: "Adm", Papel: "admin"})
	require.NoError(t, err)
	require.Nil(t, bv, "sem EnviarBoasVindas não há bloco de boas-vindas")
	require.Equal(t, "admin", adm.Role)
	require.Nil(t, adm.ClientID)

	cli, _, err := srv.CriarUsuario(ctx, EntradaUsuario{Email: emailUnico(), Senha: senhaBoa, Nome: "Cli", Papel: "client", ClienteID: &a})
	require.NoError(t, err)
	require.Equal(t, "viewer", cli.Role, "client na API é viewer no banco")
	require.Equal(t, a, *cli.ClientID)
	require.Equal(t, []uuid.UUID{a}, cli.ClientIDs)
}

func TestCriarUsuario_CarteiraVenceClienteID(t *testing.T) {
	ctx, srv, a, b := srvUsuarios(t)
	u, _, err := srv.CriarUsuario(ctx, EntradaUsuario{
		Email: emailUnico(), Senha: senhaBoa, Nome: "Ag", Papel: "client",
		ClienteID: &b, ClienteIDs: []uuid.UUID{a, b},
	})
	require.NoError(t, err)
	require.Equal(t, a, *u.ClientID, "o primeiro da carteira é o principal; client_id legado perde")
	require.ElementsMatch(t, []uuid.UUID{a, b}, u.ClientIDs)
}

func TestCriarUsuario_PreferenciasDeEmail(t *testing.T) {
	ctx, srv, _, _ := srvUsuarios(t)
	nao, sim := false, true
	u, _, err := srv.CriarUsuario(ctx, EntradaUsuario{
		Email: emailUnico(), Senha: senhaBoa, Nome: "P", Papel: "admin",
		RecebeAlertas: &nao, RecebePosVenda: &sim,
	})
	require.NoError(t, err)
	require.False(t, u.ReceiveAlertEmails)
	require.True(t, u.ReceivePostSaleEmails)
}

func TestCriarUsuario_EmailEmUso(t *testing.T) {
	ctx, srv, _, _ := srvUsuarios(t)
	em := emailUnico()
	_, _, err := srv.CriarUsuario(ctx, EntradaUsuario{Email: em, Senha: senhaBoa, Nome: "1", Papel: "admin"})
	require.NoError(t, err)
	_, _, err = srv.CriarUsuario(ctx, EntradaUsuario{Email: em, Senha: senhaBoa, Nome: "2", Papel: "admin"})
	exigeCodigo(t, err, CodigoEmailEmUso)
}

func TestCriarUsuario_ClienteInexistente(t *testing.T) {
	ctx, srv, a, _ := srvUsuarios(t)
	fantasma := uuid.New()

	// (a) único da carteira.
	_, _, err := srv.CriarUsuario(ctx, EntradaUsuario{Email: emailUnico(), Senha: senhaBoa, Nome: "G", Papel: "client", ClienteIDs: []uuid.UUID{fantasma}})
	exigeCodigo(t, err, CodigoClienteInexistente)

	// (b) secundário: a conferência vem ANTES de gravar, então NADA fica criado
	// (antes: o usuário parcial ficava gravado e a rota respondia 400).
	em := emailUnico()
	_, _, err = srv.CriarUsuario(ctx, EntradaUsuario{Email: em, Senha: senhaBoa, Nome: "G", Papel: "client", ClienteIDs: []uuid.UUID{a, fantasma}})
	exigeCodigo(t, err, CodigoClienteInexistente)
	_, err = srv.Usuarios.GetByEmail(ctx, em)
	require.ErrorIs(t, err, pgx.ErrNoRows, "nenhum usuário parcial pode ficar gravado")
}

func TestCriarUsuario_BoasVindas(t *testing.T) {
	ctx, srv, _, _ := srvUsuarios(t)
	conv := uuid.New()
	ator := uuid.New()

	t.Run("emite e devolve o desfecho", func(t *testing.T) {
		f := &emissorFalso{resposta: &BoasVindas{ConviteID: &conv, Link: "https://x/boasvindas/tok", EmailStatus: "sent"}}
		srv.BoasVindas = f
		u, bv, err := srv.CriarUsuario(ctx, EntradaUsuario{Email: emailUnico(), Senha: senhaBoa, Nome: "B", Papel: "admin", EnviarBoasVindas: true, Ator: &ator})
		require.NoError(t, err)
		require.NotNil(t, u)
		require.Equal(t, f.resposta, bv)
		require.Equal(t, 1, f.chamadas)
		require.Equal(t, senhaBoa, f.ultimaSenh)
		require.Equal(t, &ator, f.ultimoAtor)
	})

	t.Run("sem o pedido não emite", func(t *testing.T) {
		f := &emissorFalso{resposta: &BoasVindas{EmailStatus: "sent"}}
		srv.BoasVindas = f
		_, bv, err := srv.CriarUsuario(ctx, EntradaUsuario{Email: emailUnico(), Senha: senhaBoa, Nome: "B", Papel: "admin"})
		require.NoError(t, err)
		require.Nil(t, bv)
		require.Zero(t, f.chamadas)
	})

	t.Run("erro do emissor não falha a criação", func(t *testing.T) {
		srv.BoasVindas = &emissorFalso{erro: errors.New("smtp caiu")}
		u, bv, err := srv.CriarUsuario(ctx, EntradaUsuario{Email: emailUnico(), Senha: senhaBoa, Nome: "B", Papel: "admin", EnviarBoasVindas: true})
		require.NoError(t, err)
		require.NotNil(t, u)
		require.Equal(t, &BoasVindas{EmailStatus: "unavailable", EmailErro: "smtp caiu"}, bv)
	})

	t.Run("sem emissor é indisponível", func(t *testing.T) {
		srv.BoasVindas = nil
		u, bv, err := srv.CriarUsuario(ctx, EntradaUsuario{Email: emailUnico(), Senha: senhaBoa, Nome: "B", Papel: "admin", EnviarBoasVindas: true})
		require.NoError(t, err)
		require.NotNil(t, u)
		require.Equal(t, &BoasVindas{EmailStatus: "unavailable"}, bv)
	})

	t.Run("adaptador com serviço nil é indisponível", func(t *testing.T) {
		srv.BoasVindas = BoasVindasDoServico(nil)
		_, bv, err := srv.CriarUsuario(ctx, EntradaUsuario{Email: emailUnico(), Senha: senhaBoa, Nome: "B", Papel: "admin", EnviarBoasVindas: true})
		require.NoError(t, err)
		require.Equal(t, &BoasVindas{EmailStatus: "unavailable"}, bv)
	})
}

func TestEditarUsuario_Recusas(t *testing.T) {
	ctx, srv, a, _ := srvUsuarios(t)
	u, _, err := srv.CriarUsuario(ctx, EntradaUsuario{Email: emailUnico(), Senha: senhaBoa, Nome: "E", Papel: "admin"})
	require.NoError(t, err)

	op, sim := "operator", true
	cli := "client"
	_, err = srv.EditarUsuario(ctx, uuid.New(), EdicaoUsuario{})
	exigeCodigo(t, err, CodigoUsuarioNaoEncontrado)
	_, err = srv.EditarUsuario(ctx, u.ID, EdicaoUsuario{Papel: &op})
	exigeCodigo(t, err, CodigoPapelInvalido)
	_, err = srv.EditarUsuario(ctx, u.ID, EdicaoUsuario{Papel: &cli})
	exigeCodigo(t, err, CodigoClienteObrigatorio)

	// Inexistente na carteira: 23503 do Update/SetClients vira client_not_found.
	_, err = srv.EditarUsuario(ctx, u.ID, EdicaoUsuario{Papel: &cli, ClienteIDs: &[]uuid.UUID{uuid.New()}})
	exigeCodigo(t, err, CodigoClienteInexistente)

	// Reativar apagado.
	require.NoError(t, srv.Usuarios.SoftDelete(ctx, u.ID))
	_, err = srv.EditarUsuario(ctx, u.ID, EdicaoUsuario{Ativo: &sim})
	exigeCodigo(t, err, CodigoNaoReativarApagado)
	_ = a
}

func TestEditarUsuario_CamposSimples(t *testing.T) {
	ctx, srv, _, _ := srvUsuarios(t)
	u, _, err := srv.CriarUsuario(ctx, EntradaUsuario{Email: emailUnico(), Senha: senhaBoa, Nome: "E", Papel: "admin"})
	require.NoError(t, err)

	nome, tel := "Novo Nome", "+55 11 99999-0000"
	nao, sim := false, true
	got, err := srv.EditarUsuario(ctx, u.ID, EdicaoUsuario{Nome: &nome, Telefone: &tel, Ativo: &nao, RecebeAlertas: &nao, RecebePosVenda: &sim})
	require.NoError(t, err)
	require.Equal(t, nome, got.Name)
	require.Equal(t, tel, *got.Phone)
	require.False(t, got.IsActive)
	require.False(t, got.ReceiveAlertEmails)
	require.True(t, got.ReceivePostSaleEmails)
}

func TestEditarUsuario_Carteira(t *testing.T) {
	ctx, srv, a, b := srvUsuarios(t)
	cli := "client"
	adm := "admin"

	t.Run("admin vira cliente só com client_ids", func(t *testing.T) {
		u, _, err := srv.CriarUsuario(ctx, EntradaUsuario{Email: emailUnico(), Senha: senhaBoa, Nome: "P", Papel: "admin"})
		require.NoError(t, err)
		got, err := srv.EditarUsuario(ctx, u.ID, EdicaoUsuario{Papel: &cli, ClienteIDs: &[]uuid.UUID{a, b}})
		require.NoError(t, err)
		require.Equal(t, "viewer", got.Role)
		require.Equal(t, a, *got.ClientID)
		require.ElementsMatch(t, []uuid.UUID{a, b}, got.ClientIDs)
	})

	t.Run("client_ids substitui e client_id sozinho poda", func(t *testing.T) {
		u, _, err := srv.CriarUsuario(ctx, EntradaUsuario{Email: emailUnico(), Senha: senhaBoa, Nome: "Q", Papel: "client", ClienteIDs: []uuid.UUID{a, b}})
		require.NoError(t, err)
		got, err := srv.EditarUsuario(ctx, u.ID, EdicaoUsuario{ClienteID: &b})
		require.NoError(t, err)
		require.Equal(t, []uuid.UUID{b}, got.ClientIDs, "client_id sozinho poda a carteira pra esse cliente")

		got, err = srv.EditarUsuario(ctx, u.ID, EdicaoUsuario{ClienteIDs: &[]uuid.UUID{a, b}})
		require.NoError(t, err)
		require.ElementsMatch(t, []uuid.UUID{a, b}, got.ClientIDs)
	})

	t.Run("client_ids vence client_id", func(t *testing.T) {
		u, _, err := srv.CriarUsuario(ctx, EntradaUsuario{Email: emailUnico(), Senha: senhaBoa, Nome: "R", Papel: "client", ClienteIDs: []uuid.UUID{a}})
		require.NoError(t, err)
		got, err := srv.EditarUsuario(ctx, u.ID, EdicaoUsuario{ClienteID: &a, ClienteIDs: &[]uuid.UUID{a, b}})
		require.NoError(t, err)
		require.ElementsMatch(t, []uuid.UUID{a, b}, got.ClientIDs)
	})

	t.Run("virar admin limpa a carteira", func(t *testing.T) {
		u, _, err := srv.CriarUsuario(ctx, EntradaUsuario{Email: emailUnico(), Senha: senhaBoa, Nome: "S", Papel: "client", ClienteIDs: []uuid.UUID{a, b}})
		require.NoError(t, err)
		got, err := srv.EditarUsuario(ctx, u.ID, EdicaoUsuario{Papel: &adm, ClienteIDs: &[]uuid.UUID{a}})
		require.NoError(t, err)
		require.Equal(t, "admin", got.Role)
		require.Nil(t, got.ClientID)
		require.Empty(t, got.ClientIDs)
	})
}
