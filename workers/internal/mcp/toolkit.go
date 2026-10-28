package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime/debug"
	"time"

	"github.com/google/uuid"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"go.uber.org/zap"
)

// resultado põe o MESMO JSON no texto e no structuredContent (contrato §3.3).
// v tem de serializar como objeto JSON.
func resultado(v any) (*sdk.CallToolResult, any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, nil, fmt.Errorf("mcp: serializar resultado: %w", err)
	}
	// structuredContent tem de ser objeto JSON (spec MCP): lista, string ou
	// número aqui é bug de quem escreveu a tool.
	if len(b) == 0 || b[0] != '{' {
		return nil, nil, errors.New("mcp: resultado precisa ser objeto JSON")
	}
	return &sdk.CallToolResult{
		Content:           []sdk.Content{&sdk.TextContent{Text: string(b)}},
		StructuredContent: json.RawMessage(b),
	}, nil, nil
}

// falha é a falha de negócio: isError com "<mensagem> [<código>]" (contrato §3.4).
func falha(mensagem, codigo string) (*sdk.CallToolResult, any, error) {
	texto := mensagem
	if codigo != "" {
		texto = fmt.Sprintf("%s [%s]", mensagem, codigo)
	}
	return &sdk.CallToolResult{
		IsError: true,
		Content: []sdk.Content{&sdk.TextContent{Text: texto}},
	}, nil, nil
}

// anotacoesLeitura: as quatro dicas explícitas — os defaults do protocolo
// (destructive=true, openWorld=true) são errados para leitura.
func anotacoesLeitura() *sdk.ToolAnnotations {
	nao := false
	return &sdk.ToolAnnotations{
		ReadOnlyHint:    true,
		IdempotentHint:  true,
		DestructiveHint: &nao,
		OpenWorldHint:   &nao,
	}
}

// ── Listas (contrato §3.3) ──────────────────────────────────────────────

const (
	limitePadrao = 25
	limiteTeto   = 100
	// offsetTeto: offset sem teto deixaria o modelo pedir OFFSET 10^9 ao banco
	// (lição do hub, offsetSchema sem limite).
	offsetTeto = 100_000
)

// Paginacao entra embutida no argumento de toda tool de lista.
type Paginacao struct {
	Limit  int `json:"limit,omitempty" jsonschema:"Quantos itens devolver: padrão 25, máximo 100."`
	Offset int `json:"offset,omitempty" jsonschema:"Quantos itens pular (padrão 0). Página seguinte: offset + page.returned, enquanto page.has_more for true."`
}

// paginacao devolve a própria Paginacao; promovido a toda entrada que a
// embute, deixa o funil das tools checar o offset sem reflexão.
func (p Paginacao) paginacao() Paginacao { return p }

func (p Paginacao) normalizar() (limit, offset int) {
	limit = p.Limit
	if limit <= 0 {
		limit = limitePadrao
	}
	if limit > limiteTeto {
		limit = limiteTeto
	}
	offset = max(p.Offset, 0)
	if offset > offsetTeto {
		offset = offsetTeto
	}
	return limit, offset
}

type pagina struct {
	Total    int  `json:"total"`
	Limit    int  `json:"limit"`
	Offset   int  `json:"offset"`
	Returned int  `json:"returned"`
	HasMore  bool `json:"has_more"`
}

// lista é o envelope de toda lista: {items, page, ressalvas?}.
type lista[T any] struct {
	Items     []T      `json:"items"`
	Page      pagina   `json:"page"`
	Ressalvas []string `json:"ressalvas,omitempty"`
}

func novaLista[T any](items []T, total, limit, offset int) lista[T] {
	if items == nil {
		items = []T{}
	}
	return lista[T]{Items: items, Page: pagina{
		Total: total, Limit: limit, Offset: offset, Returned: len(items),
		HasMore: offset+len(items) < total && offset < offsetTeto,
	}}
}

// paginar fatia uma lista que já está inteira em memória.
func paginar[T any](todos []T, p Paginacao) lista[T] {
	limit, offset := p.normalizar()
	ini := min(offset, len(todos))
	fim := min(ini+limit, len(todos))
	return novaLista(todos[ini:fim], len(todos), limit, offset)
}

// listaInteira: lista que cabe numa página só (limit nunca fica em 0).
func listaInteira[T any](todos []T) lista[T] {
	return novaLista(todos, len(todos), max(len(todos), 1), 0)
}

// buscarJanela traduz limit/offset para um repositório que pagina por página
// (page 1-based, size). Offset fora do múltiplo de limit lê duas páginas.
// Repositórios que contam com COUNT(*) OVER () devolvem total 0 numa página
// além do fim; aí relê a primeira linha só para ter o total real.
func buscarJanela[T any](p Paginacao, buscar func(page, size int) ([]T, int, error)) (lista[T], error) {
	limit, offset := p.normalizar()
	pg := offset/limit + 1
	itens, total, err := buscar(pg, limit)
	if err != nil {
		return lista[T]{}, err
	}
	inicio := offset % limit
	if inicio > 0 && len(itens) == limit {
		mais, t2, err := buscar(pg+1, limit)
		if err != nil {
			return lista[T]{}, err
		}
		itens = append(itens, mais...)
		total = max(total, t2)
	}
	inicio = min(inicio, len(itens))
	janela := itens[inicio:min(inicio+limit, len(itens))]
	if len(janela) == 0 && offset > 0 && total == 0 {
		if _, total, err = buscar(1, 1); err != nil {
			return lista[T]{}, err
		}
	}
	return novaLista(janela, total, limit, offset), nil
}

// mapear troca o tipo dos itens mantendo page e ressalvas.
func mapear[A, B any](l lista[A], f func(A) B) lista[B] {
	out := make([]B, len(l.Items))
	for i, a := range l.Items {
		out[i] = f(a)
	}
	return lista[B]{Items: out, Page: l.Page, Ressalvas: l.Ressalvas}
}

// ── Referências, datas e fuso ───────────────────────────────────────────

// ref: todo id vem com o nome (contrato §3.2), para o modelo não precisar de
// uma segunda chamada.
type ref struct {
	ID   string `json:"id"`
	Nome string `json:"nome"`
}

func refDe(id uuid.UUID, nome string) ref { return ref{ID: id.String(), Nome: nome} }

// refs monta refs na ordem dos ids; id sem nome conhecido vem com nome "".
func refs(ids []uuid.UUID, nomes map[uuid.UUID]string) []ref {
	out := make([]ref, len(ids))
	for i, id := range ids {
		out[i] = refDe(id, nomes[id])
	}
	return out
}

// fusoSP: America/Sao_Paulo, com fallback UTC-3 se faltar tzdata (o Brasil
// não tem horário de verão desde 2019) — o mesmo do handlers/insights.go.
var fusoSP = func() *time.Location {
	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		return time.FixedZone("BRT", -3*3600)
	}
	return loc
}()

// dia formata uma data de calendário (DATE do Postgres chega como meia-noite UTC).
func dia(t time.Time) string { return t.Format("2006-01-02") }

// instante formata um timestamptz em ISO 8601 UTC (contrato §3.3).
func instante(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func instanteOpc(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := instante(*t)
	return &s
}

// lerDia lê AAAA-MM-DD como data de calendário (meia-noite UTC, igual ao DATE do pgx).
func lerDia(s string) (time.Time, bool) {
	d, err := time.Parse("2006-01-02", s)
	return d, err == nil
}

// limitesDoDiaSP: [00:00, 23:59:59.999] do dia d em America/Sao_Paulo —
// o mesmo recorte que o DayDetailModal manda ao GET /detections.
func limitesDoDiaSP(d time.Time) (time.Time, time.Time) {
	ini := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, fusoSP)
	return ini, ini.AddDate(0, 0, 1).Add(-time.Millisecond)
}

// ── Registro e respostas das tools de leitura ───────────────────────────

// ok e recusa são as formas de dois valores de resultado/falha, para as
// funções registradas por ferramentaLeitura.
func ok(v any) (*sdk.CallToolResult, error) {
	r, _, err := resultado(v)
	return r, err
}

func recusa(mensagem, codigo string) (*sdk.CallToolResult, error) {
	r, _, _ := falha(mensagem, codigo)
	return r, nil
}

func recusaEntrada(campo string) (*sdk.CallToolResult, error) {
	return recusa(fmt.Sprintf("%s inválido: passe o valor exato que outra tool devolveu (nunca invente; pergunte ao usuário)", campo), "entrada_invalida")
}

func naoEncontrado(oQue string) (*sdk.CallToolResult, error) {
	return recusa(fmt.Sprintf("%s: não existe no E-monitor", oQue), "nao_encontrado")
}

// falhaInterna loga o erro e devolve uma falha genérica: o SDK repassaria
// err.Error() cru ao modelo (SQLSTATE, nome de tabela).
func falhaInterna(log *zap.Logger, tool string, err error) (*sdk.CallToolResult, any, error) {
	log.Error("mcp: falha interna numa tool", zap.String("tool", tool), zap.Error(err))
	return falha("erro interno do E-monitor; tente de novo e, se persistir, avise o suporte", "interno")
}

// ferramentaLeitura registra uma tool de leitura: anotações de leitura,
// falha fechada sem Leitura configurada e nenhum erro Go chegando ao modelo.
func ferramentaLeitura[In any](s *sdk.Server, cfg ConfigServidor, nome, descricao string,
	fn func(ctx context.Context, L *Leitura, in In) (*sdk.CallToolResult, error)) {
	sdk.AddTool(s, &sdk.Tool{Name: nome, Description: descricao, Annotations: anotacoesLeitura()},
		func(ctx context.Context, _ *sdk.CallToolRequest, in In) (res *sdk.CallToolResult, out any, err error) {
			// O SDK roda cada request numa goroutine sem recover: um panic aqui
			// derrubaria o processo inteiro da API (supervisor de workers incluso).
			defer func() {
				if r := recover(); r != nil {
					res, out, err = falhaInterna(cfg.log(), nome, fmt.Errorf("panic: %v\n%s", r, debug.Stack()))
				}
			}()
			if cfg.Leitura == nil {
				return falhaInterna(cfg.log(), nome, fmt.Errorf("mcp: Leitura não configurada"))
			}
			// O clamp de normalizar manteria has_more=true e devolveria a mesma
			// página para sempre: acima do teto a entrada é recusada.
			if pp, ok := any(in).(interface{ paginacao() Paginacao }); ok && pp.paginacao().Offset > offsetTeto {
				r, e := recusa(fmt.Sprintf("offset máximo %d: estreite a busca com filtros (período, emissora, cliente…)", offsetTeto), "entrada_invalida")
				return r, nil, e
			}
			r, e := fn(ctx, cfg.Leitura, in)
			if e != nil {
				return falhaInterna(cfg.log(), nome, e)
			}
			if r == nil {
				return falhaInterna(cfg.log(), nome, errors.New("tool devolveu resultado nil"))
			}
			return r, nil, nil
		})
}
