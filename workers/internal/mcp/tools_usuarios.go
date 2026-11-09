package mcp

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"radiocheck/internal/users"
	"radiocheck/internal/welcome"
)

// papelDe traduz o papel do banco para o vocabulário da tela: viewer é "cliente".
func papelDe(role string) string {
	if role == "viewer" {
		return "cliente"
	}
	return role
}

// situacaoDe: apagado (soft delete) > inativo > ativo.
func situacaoDe(u users.User) string {
	switch {
	case u.DeletedAt != nil:
		return "apagado"
	case !u.IsActive:
		return "inativo"
	default:
		return "ativo"
	}
}

// papeisDoFiltro: o mesmo de-para do parseRoleFilter do handler (cliente = viewer).
var papeisDoFiltro = map[string]string{
	"admin": "admin", "operator": "operator", "operador": "operator", "cliente": "viewer", "viewer": "viewer",
}

// situacoesDoFiltro: vocabulário da tool → status do users.Repo.List.
var situacoesDoFiltro = map[string]string{
	"": "", "ativos": "active", "inativos": "inactive", "apagados": "deleted", "todos": "all",
}

// boasVindasDTO: estado do convite. token e link NUNCA (abrem a senha inicial).
type boasVindasDTO struct {
	StatusEmail string  `json:"status_email"`
	AbertoEm    *string `json:"aberto_em"`
	Aberturas   int     `json:"aberturas"`
	RevogadoEm  *string `json:"revogado_em"`
	CriadoEm    string  `json:"criado_em"`
}

type usuarioDTO struct {
	ID             string         `json:"id"`
	Nome           string         `json:"nome"`
	Email          string         `json:"email"`
	NomeDeUsuario  *string        `json:"nome_de_usuario"` // segunda forma de login; null = só entra pelo e-mail
	Papel          string         `json:"papel"`
	Situacao       string         `json:"situacao"`
	Clientes       []ref          `json:"clientes"`
	UltimoLogin    *string        `json:"ultimo_login"`
	CriadoEm       string         `json:"criado_em"`
	RecebeAlertas  bool           `json:"recebe_alertas"`
	RecebePosVenda bool           `json:"recebe_pos_venda"`
	BoasVindas     *boasVindasDTO `json:"boas_vindas"`
}

func usuarioDe(u users.User, clientes map[uuid.UUID]string, convites map[uuid.UUID]welcome.Invite) usuarioDTO {
	d := usuarioDTO{
		ID: u.ID.String(), Nome: u.Name, Email: u.Email, NomeDeUsuario: u.Username, Papel: papelDe(u.Role), Situacao: situacaoDe(u),
		Clientes: refs(u.ClientIDs, clientes), UltimoLogin: instanteOpc(u.LastLoginAt), CriadoEm: instante(u.CreatedAt),
		RecebeAlertas: u.ReceiveAlertEmails, RecebePosVenda: u.ReceivePostSaleEmails,
	}
	if inv, ok := convites[u.ID]; ok {
		d.BoasVindas = &boasVindasDTO{
			StatusEmail: inv.EmailStatus, AbertoEm: instanteOpc(inv.OpenedAt), Aberturas: inv.OpenCount,
			RevogadoEm: instanteOpc(inv.RevokedAt), CriadoEm: instante(inv.CreatedAt),
		}
	}
	return d
}

type entradaListarUsuarios struct {
	Busca     string `json:"busca,omitempty" jsonschema:"Trecho do nome, do e-mail ou do nome de usuário."`
	Papel     string `json:"papel,omitempty" jsonschema:"admin, operator ou cliente."`
	ClienteID string `json:"cliente_id,omitempty" jsonschema:"Só quem tem este cliente na carteira (inclusive vínculo secundário de agência)."`
	Situacao  string `json:"situacao,omitempty" jsonschema:"ativos (padrão), inativos, apagados ou todos."`
	Paginacao
}

type entradaUsuario struct {
	UsuarioID string `json:"usuario_id" jsonschema:"Id (uuid) do usuário, vindo de listar_usuarios. Nunca invente: pergunte."`
}

type saidaObterUsuario struct {
	Usuario usuarioDTO `json:"usuario"`
}

func registrarUsuarios(s *sdk.Server, cfg ConfigServidor) {
	ferramentaLeitura(s, cfg, "listar_usuarios",
		"Lista os usuários do E-monitor (a tela /admin/users), do mais novo para o mais velho: nome, e-mail, papel (admin, "+
			"operator, cliente), situação, clientes da carteira, último login e o estado do convite de boas-vindas (enviado, "+
			"aberto, revogado). Filtros: busca, papel, cliente_id, situacao. Lista paginada.",
		func(ctx context.Context, L *Leitura, in entradaListarUsuarios) (*sdk.CallToolResult, error) {
			papel, okP := papeisDoFiltro[in.Papel]
			if in.Papel != "" && !okP {
				return recusa("papel desconhecido: use admin, operator ou cliente", "entrada_invalida")
			}
			situacao, okS := situacoesDoFiltro[in.Situacao]
			if !okS {
				return recusa("situacao desconhecida: use ativos, inativos, apagados ou todos", "entrada_invalida")
			}
			var cliente *uuid.UUID
			if in.ClienteID != "" {
				id, err := uuid.Parse(in.ClienteID)
				if err != nil {
					return recusaEntrada("cliente_id")
				}
				cliente = &id
			}
			pg, err := buscarJanela(in.Paginacao, func(page, size int) ([]users.User, int, error) {
				return L.Usuarios.List(ctx, users.ListInput{
					Role: papel, ClientID: cliente, Status: situacao, Q: in.Busca, Page: page, PageSize: size,
				})
			})
			if err != nil {
				return nil, err
			}
			ids := make([]uuid.UUID, len(pg.Items))
			for i, u := range pg.Items {
				ids[i] = u.ID
			}
			convites, err := L.BoasVindas.Latest(ctx, ids)
			if err != nil {
				return nil, err
			}
			clientes, err := nomesClientes(ctx, L)
			if err != nil {
				return nil, err
			}
			return ok(mapear(pg, func(u users.User) usuarioDTO { return usuarioDe(u, clientes, convites) }))
		})

	ferramentaLeitura(s, cfg, "obter_usuario",
		"Devolve um usuário: papel, situação, clientes da carteira, preferências de e-mail, último login e o estado do convite "+
			"de boas-vindas. O link e a senha inicial nunca saem por aqui. Nunca invente o usuario_id.",
		func(ctx context.Context, L *Leitura, in entradaUsuario) (*sdk.CallToolResult, error) {
			id, err := uuid.Parse(in.UsuarioID)
			if err != nil {
				return recusaEntrada("usuario_id")
			}
			saida, err := montarUsuario(ctx, L, id)
			if errors.Is(err, pgx.ErrNoRows) {
				return naoEncontrado("usuário")
			}
			if err != nil {
				return nil, err
			}
			return ok(saida)
		})
}

// montarUsuario é o usuário no formato de obter_usuario. As tools de escrita de
// usuário (criar/editar/desativar) devolvem exatamente isto. pgx.ErrNoRows =
// o usuário não existe. O link e a senha das boas-vindas nunca entram: o DTO só
// tem o estado do convite.
func montarUsuario(ctx context.Context, L *Leitura, id uuid.UUID) (saidaObterUsuario, error) {
	u, err := L.Usuarios.Get(ctx, id)
	if err != nil {
		return saidaObterUsuario{}, err
	}
	convites, err := L.BoasVindas.Latest(ctx, []uuid.UUID{id})
	if err != nil {
		return saidaObterUsuario{}, err
	}
	clientes, err := nomesClientes(ctx, L)
	if err != nil {
		return saidaObterUsuario{}, err
	}
	return saidaObterUsuario{Usuario: usuarioDe(*u, clientes, convites)}, nil
}
