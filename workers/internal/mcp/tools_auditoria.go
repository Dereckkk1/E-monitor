package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/google/uuid"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"radiocheck/internal/mcp/auditoria"
)

type entradaAuditoria struct {
	Acao      string `json:"acao,omitempty" jsonschema:"Prefixo da ação (ex.: mcp.token, mcp.oauth). Vazio = todas."`
	UsuarioID string `json:"usuario_id,omitempty" jsonschema:"Só deste usuário (de listar_usuarios)."`
	Cursor    string `json:"cursor,omitempty" jsonschema:"page.next_cursor da página anterior, exatamente como veio. Vazio = do mais recente."`
	Limit     int    `json:"limit,omitempty" jsonschema:"Quantos eventos: padrão e máximo 20."`
}

// tetoPaginaAuditoria: cada evento leva até tetoDetalhesBytes de metadata
// livre; com 50 por página o pior caso (detalhes logo abaixo do teto) passava
// de 100 KB. Medido em TestAuditoriaMCP_PiorCasoCabe.
const tetoPaginaAuditoria = 20

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

// tetoDetalhesBytes: o metadata de um evento é livre; acima disto ele sai
// resumido (tamanho e chaves), para um evento não ocupar a página inteira.
const tetoDetalhesBytes = 2048

const ressalvaDetalhesResumidos = "Algum evento tinha detalhes acima de 2 KB: eles saem resumidos em detalhes._resumo (tamanho e chaves); o evento completo fica no audit_log."

// detalhesComTeto devolve os detalhes, ou o resumo deles quando passam do teto.
func detalhesComTeto(d map[string]any) (map[string]any, bool) {
	if d == nil {
		return map[string]any{}, false
	}
	b, err := json.Marshal(d)
	if err == nil && len(b) <= tetoDetalhesBytes {
		return d, false
	}
	chaves := slices.Sorted(maps.Keys(d))
	resumo, _ := cortarTexto(fmt.Sprintf("%d bytes omitidos; chaves: %s", len(b), strings.Join(chaves, ", ")), 300)
	return map[string]any{"_resumo": resumo}, true
}

func eventoDe(e auditoria.Evento) (eventoDTO, bool) {
	detalhes, resumido := detalhesComTeto(e.Detalhes)
	d := eventoDTO{ID: e.ID.String(), Quando: instante(e.Quando), TipoAtor: e.TipoAtor, Acao: e.Acao, Recurso: e.Recurso, Detalhes: detalhes}
	if e.AtorID != nil {
		d.Ator = &atorDTO{ID: e.AtorID.String(), Nome: e.AtorNome, Email: e.AtorEmail}
	}
	if e.RecursoID != nil {
		s := e.RecursoID.String()
		d.RecursoID = &s
	}
	return d, resumido
}

func registrarAuditoria(s *sdk.Server, cfg ConfigServidor) {
	ferramentaLeitura(s, cfg, "auditoria_mcp",
		"Lista o que foi registrado pelo MCP do E-monitor (tokens criados/revogados/recusados, autorizações OAuth, clientes "+
			"cadastrados por CLI e, a partir da Fatia 3, as escritas), do mais recente ao mais antigo. Filtros: acao (prefixo), "+
			"usuario_id. Pagina por cursor (até 20 por página): page.total vem null (não contado); siga page.next_cursor enquanto "+
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
			algumResumido := false
			for i, e := range evs {
				var resumido bool
				out[i], resumido = eventoDe(e)
				algumResumido = algumResumido || resumido
			}
			l := novaListaCursor(out, limit, prox)
			if algumResumido {
				l.Ressalvas = []string{ressalvaDetalhesResumidos}
			}
			return ok(l)
		})
}
