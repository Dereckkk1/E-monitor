package mcp

import (
	"context"
	"errors"

	"github.com/google/uuid"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"radiocheck/internal/mcp/auditoria"
)

type entradaAuditoria struct {
	Acao      string `json:"acao,omitempty" jsonschema:"Prefixo da ação (ex.: mcp.token, mcp.oauth). Vazio = todas."`
	UsuarioID string `json:"usuario_id,omitempty" jsonschema:"Só deste usuário (de listar_usuarios)."`
	Cursor    string `json:"cursor,omitempty" jsonschema:"page.next_cursor da página anterior, exatamente como veio. Vazio = do mais recente."`
	Limit     int    `json:"limit,omitempty" jsonschema:"Quantos eventos: padrão 25, máximo 50."`
}

// tetoPaginaAuditoria: cada evento leva o metadata livre da ação; com 100 por
// página o pior caso passava de 65 KB. Medido em TestAuditoriaMCP_PiorCasoCabe.
const tetoPaginaAuditoria = 50

type atorDTO struct {
	ID    string  `json:"id"`
	Nome  *string `json:"nome"`
	Email *string `json:"email"`
}

type eventoDTO struct {
	ID       string   `json:"id"`
	Quando   string   `json:"quando"`
	Ator     *atorDTO `json:"ator"` // null = ação de CLI na VM (detalhes.pelo diz qual)
	TipoAtor string   `json:"tipo_ator"`
	Acao     string   `json:"acao"`
	Recurso  string   `json:"recurso"`
	// RecursoID: null quando a ação não aponta um registro.
	RecursoID *string        `json:"recurso_id"`
	Detalhes  map[string]any `json:"detalhes"`
}

func eventoDe(e auditoria.Evento) eventoDTO {
	d := eventoDTO{ID: e.ID.String(), Quando: instante(e.Quando), TipoAtor: e.TipoAtor, Acao: e.Acao, Recurso: e.Recurso, Detalhes: e.Detalhes}
	if d.Detalhes == nil {
		d.Detalhes = map[string]any{}
	}
	if e.AtorID != nil {
		d.Ator = &atorDTO{ID: e.AtorID.String(), Nome: e.AtorNome, Email: e.AtorEmail}
	}
	if e.RecursoID != nil {
		s := e.RecursoID.String()
		d.RecursoID = &s
	}
	return d
}

func registrarAuditoria(s *sdk.Server, cfg ConfigServidor) {
	ferramentaLeitura(s, cfg, "auditoria_mcp",
		"Lista o que foi registrado pelo MCP do E-monitor (tokens criados/revogados/recusados, autorizações OAuth, clientes "+
			"cadastrados por CLI e, a partir da Fatia 3, as escritas), do mais recente ao mais antigo. Filtros: acao (prefixo), "+
			"usuario_id. Pagina por cursor (até 50 por página): page.total vem null (não contado); siga page.next_cursor enquanto "+
			"has_more for true.",
		func(ctx context.Context, L *Leitura, in entradaAuditoria) (*sdk.CallToolResult, error) {
			limit, _ := Paginacao{Limit: in.Limit}.comTeto(tetoPaginaAuditoria).normalizar()
			f := auditoria.Filtro{AcaoPrefixo: in.Acao, Cursor: in.Cursor, Limite: limit}
			if in.UsuarioID != "" {
				id, err := uuid.Parse(in.UsuarioID)
				if err != nil {
					return recusaEntrada("usuario_id")
				}
				f.AtorID = &id
			}
			evs, prox, err := L.Auditoria.Listar(ctx, f)
			if errors.Is(err, auditoria.ErrCursorInvalido) {
				return recusa("cursor inválido: use o page.next_cursor exato da página anterior, ou nenhum", "entrada_invalida")
			}
			if err != nil {
				return nil, err
			}
			out := make([]eventoDTO, len(evs))
			for i, e := range evs {
				out[i] = eventoDe(e)
			}
			return ok(novaListaCursor(out, limit, prox))
		})
}
