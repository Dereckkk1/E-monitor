package mcp

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"radiocheck/internal/catalog"
	"radiocheck/internal/users"
)

type clienteDTO struct {
	ID           string  `json:"id"`
	Nome         string  `json:"nome"`
	Ativo        bool    `json:"ativo"`
	HubID        *string `json:"hub_id"`
	CNPJ         *string `json:"cnpj"`
	Cidade       *string `json:"cidade"`
	UF           *string `json:"uf"`
	ContatoNome  *string `json:"contato_nome"`
	ContatoEmail *string `json:"contato_email"`
	Telefone     *string `json:"telefone"`
	RotuloTarget *string `json:"rotulo_target"`
	CriadoEm     string  `json:"criado_em"`
}

func clienteDe(c catalog.Client, hubIDs map[uuid.UUID]string) clienteDTO {
	d := clienteDTO{
		ID: c.ID.String(), Nome: c.Name, Ativo: c.IsActive, CNPJ: c.CNPJ, Cidade: c.City, UF: c.State,
		ContatoNome: c.ContactName, ContatoEmail: c.ContactEmail, Telefone: c.Phone,
		RotuloTarget: c.TargetLabel, CriadoEm: instante(c.CreatedAt),
	}
	if h, ok := hubIDs[c.ID]; ok {
		d.HubID = &h
	}
	return d
}

type entradaListarClientes struct {
	Busca           string `json:"busca,omitempty" jsonschema:"Texto livre: nome, cidade, UF, CNPJ ou contato. Ignora acento e caixa."`
	IncluirInativos bool   `json:"incluir_inativos,omitempty" jsonschema:"true para trazer também os clientes desativados (padrão: só ativos)."`
	Paginacao
}

type entradaCliente struct {
	ClienteID string `json:"cliente_id" jsonschema:"Id (uuid) do cliente no E-monitor, vindo de listar_clientes. Nunca invente: pergunte ao usuário."`
}

type entradaClientePaginada struct {
	ClienteID string `json:"cliente_id" jsonschema:"Id (uuid) do cliente no E-monitor, vindo de listar_clientes. Nunca invente: pergunte ao usuário."`
	Paginacao
}

type vinculosCliente struct {
	Campanhas int `json:"campanhas"`
	Materiais int `json:"materiais"`
	Usuarios  int `json:"usuarios"`
}

type pessoaDaCarteira struct {
	ID       string `json:"id"`
	Nome     string `json:"nome"`
	Email    string `json:"email"`
	Papel    string `json:"papel"`
	Situacao string `json:"situacao"`
}

type saidaObterCliente struct {
	Cliente   clienteDTO         `json:"cliente"`
	Vinculos  vinculosCliente    `json:"vinculos"`
	Usuarios  []pessoaDaCarteira `json:"usuarios"`
	Ressalvas []string           `json:"ressalvas,omitempty"`
}

type linhaPMMTarget struct {
	Emissora      ref      `json:"emissora"`
	Banda         *string  `json:"banda"`
	FrequenciaMHz *float64 `json:"frequencia_mhz"`
	Cidade        *string  `json:"cidade"`
	UF            *string  `json:"uf"`
	PMM           *float64 `json:"pmm"`
	PMMTarget     *int     `json:"pmm_target"`
}

// tetoCarteira: quantas pessoas obter_cliente lista (o resto, listar_usuarios).
const tetoCarteira = 100

const ressalvaPMMTarget = "pmm_target null = a emissora não tem linha de PMM no target deste cliente: ela fica FORA do total de impactos no target e do contador de emissoras com target (não é zero). pmm_target 0 conta e soma nada."

// lerCliente valida o id e confirma que o cliente existe; rec != nil é a recusa pronta.
func lerCliente(ctx context.Context, L *Leitura, s string) (*catalog.Client, *sdk.CallToolResult, error) {
	id, err := uuid.Parse(s)
	if err != nil {
		rec, _ := recusaEntrada("cliente_id")
		return nil, rec, nil
	}
	c, err := L.Clientes.Get(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		rec, _ := naoEncontrado("cliente")
		return nil, rec, nil
	}
	if err != nil {
		return nil, nil, err
	}
	return c, nil, nil
}

// lerClienteCompleto monta o que obter_cliente devolve para um cliente já lido;
// as escritas que mexem num cliente devolvem o MESMO formato depois de gravar.
func lerClienteCompleto(ctx context.Context, L *Leitura, c *catalog.Client) (*saidaObterCliente, error) {
	hub, err := L.Clientes.HubIDs(ctx, []uuid.UUID{c.ID})
	if err != nil {
		return nil, err
	}
	dep, err := L.Clientes.CountDependents(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	pessoas, total, err := L.Usuarios.List(ctx, users.ListInput{ClientID: &c.ID, Status: "all", PageSize: tetoCarteira})
	if err != nil {
		return nil, err
	}
	out := &saidaObterCliente{
		Cliente:  clienteDe(*c, hub),
		Vinculos: vinculosCliente{Campanhas: dep.Campaigns, Materiais: dep.Materials, Usuarios: dep.Users},
		Usuarios: make([]pessoaDaCarteira, 0, len(pessoas)),
	}
	for _, u := range pessoas {
		out.Usuarios = append(out.Usuarios, pessoaDaCarteira{
			ID: u.ID.String(), Nome: u.Name, Email: u.Email, Papel: papelDe(u.Role), Situacao: situacaoDe(u),
		})
	}
	if total > len(pessoas) {
		out.Ressalvas = append(out.Ressalvas, fmt.Sprintf(
			"mostrando %d de %d usuários; listar_usuarios com cliente_id traz todos", len(pessoas), total))
	}
	return out, nil
}

func registrarClientes(s *sdk.Server, cfg ConfigServidor) {
	ferramentaLeitura(s, cfg, "listar_clientes",
		"Lista os clientes (anunciantes) do E-monitor em ordem de nome: id, nome, se está ativo, CNPJ, cidade/UF, contato e hub_id "+
			"(o id do mesmo cliente no hub). Use para achar o cliente_id que as outras tools pedem. Por padrão só os ativos; "+
			"incluir_inativos traz os desativados. Lista paginada: siga page.has_more.",
		func(ctx context.Context, L *Leitura, in entradaListarClientes) (*sdk.CallToolResult, error) {
			pg, err := buscarJanela(in.Paginacao, func(page, size int) ([]catalog.Client, int, error) {
				return L.Clientes.ListPaged(ctx, in.Busca, page, size, in.IncluirInativos)
			})
			if err != nil {
				return nil, err
			}
			ids := make([]uuid.UUID, len(pg.Items))
			for i, c := range pg.Items {
				ids[i] = c.ID
			}
			hub, err := L.Clientes.HubIDs(ctx, ids)
			if err != nil {
				return nil, err
			}
			return ok(mapear(pg, func(c catalog.Client) clienteDTO { return clienteDe(c, hub) }))
		})

	ferramentaLeitura(s, cfg, "obter_cliente",
		"Devolve o cadastro de um cliente (com hub_id, a ponte para o hub), quantas campanhas, materiais e usuários estão "+
			"vinculados a ele e as pessoas da carteira (papel e situação). Um usuário de agência pode ter vários clientes: ele "+
			"aparece em cada um. usuarios inclui também pessoas inativas e apagadas, cada uma com a situacao: não leia a lista como só os usuários ativos. Nunca invente o cliente_id: pegue de listar_clientes ou pergunte.",
		func(ctx context.Context, L *Leitura, in entradaCliente) (*sdk.CallToolResult, error) {
			c, rec, err := lerCliente(ctx, L, in.ClienteID)
			if rec != nil || err != nil {
				return rec, err
			}
			out, err := lerClienteCompleto(ctx, L, c)
			if err != nil {
				return nil, err
			}
			return ok(out)
		})

	ferramentaLeitura(s, cfg, "pmm_target_cliente",
		"Lista as emissoras das campanhas de um cliente com o PMM geral e o PMM no target do cliente (pmm_target), base dos "+
			"impactos no target. A armadilha: pmm_target null NÃO é zero — a emissora fica fora do total. Nunca invente o "+
			"cliente_id. Lista paginada.",
		func(ctx context.Context, L *Leitura, in entradaClientePaginada) (*sdk.CallToolResult, error) {
			c, rec, err := lerCliente(ctx, L, in.ClienteID)
			if rec != nil || err != nil {
				return rec, err
			}
			rows, err := L.PMMTarget.ListForClient(ctx, c.ID)
			if err != nil {
				return nil, err
			}
			linhas := make([]linhaPMMTarget, len(rows))
			for i, r := range rows {
				linhas[i] = linhaPMMTarget{
					Emissora: refDe(r.StationID, r.Name), Banda: r.Band, FrequenciaMHz: r.FrequencyMHz,
					Cidade: r.City, UF: r.State, PMM: r.PMM, PMMTarget: r.PMMTarget,
				}
			}
			l := paginar(linhas, in.Paginacao)
			l.Ressalvas = []string{ressalvaPMMTarget}
			return ok(l)
		})
}
