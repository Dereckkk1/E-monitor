package mcp

import (
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

// links é o bloco de deep-link de toda tool de relatório (spec §4.5, D7).
// Nunca há link direto de arquivo: o download do portal exige o login da
// pessoa no cabeçalho (JWT), e um link com credencial seria URL assinada.
type links struct {
	Tela       *string `json:"tela"`
	ComoBaixar string  `json:"como_baixar,omitempty"`
}

// linkTela: a rota do portal com os filtros na query (nil sem portal configurado).
func (c ConfigServidor) linkTela(caminho string, q url.Values) *string {
	if len(q) == 0 {
		return c.link(caminho)
	}
	return c.link(caminho + "?" + q.Encode())
}

// csvIDs: o formato das listas de id na query das telas ("a,b,c").
func csvIDs(ids []uuid.UUID) string {
	s := make([]string, len(ids))
	for i, id := range ids {
		s[i] = id.String()
	}
	return strings.Join(s, ",")
}

// tetoPorItem: listas DENTRO de um item (quedas, campanhas, emissoras,
// cidades, linhas de checking) saem cortadas aqui, com o total real ao lado —
// o cliente MCP corta a saída em ~25k tokens e paginar só limita a lista
// principal (lição do commit 2414725).
const tetoPorItem = tetoEmissorasPorItem

// cortar devolve no máximo n itens e o total real; nunca nil (sai [] no JSON).
func cortar[T any](xs []T, n int) ([]T, int) {
	if xs == nil {
		xs = []T{}
	}
	if len(xs) > n {
		return xs[:n], len(xs)
	}
	return xs, len(xs)
}

// ── Lista por cursor (contrato §3.3: coleção que só cresce) ────────────────

type paginaCursor struct {
	// Total é sempre null: "não contado", nunca zero.
	Total      *int    `json:"total"`
	Limit      int     `json:"limit"`
	Returned   int     `json:"returned"`
	HasMore    bool    `json:"has_more"`
	NextCursor *string `json:"next_cursor"`
}

type listaCursor[T any] struct {
	Items     []T          `json:"items"`
	Page      paginaCursor `json:"page"`
	Ressalvas []string     `json:"ressalvas,omitempty"`
}

// novaListaCursor: proximo vazio = fim da coleção.
func novaListaCursor[T any](items []T, limit int, proximo string) listaCursor[T] {
	if items == nil {
		items = []T{}
	}
	p := paginaCursor{Limit: limit, Returned: len(items), HasMore: proximo != ""}
	if proximo != "" {
		p.NextCursor = &proximo
	}
	return listaCursor[T]{Items: items, Page: p}
}

// ── Datas de calendário em America/Sao_Paulo ──────────────────────────────

// hojeSP: o dia de hoje em SP como data de calendário (meia-noite UTC, como o DATE do pgx).
func hojeSP(agora time.Time) time.Time {
	t := agora.In(fusoSP)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func ontemSP(agora time.Time) time.Time { return hojeSP(agora).AddDate(0, 0, -1) }

// mesCorrenteSP: primeiro e último dia do mês corrente em SP.
func mesCorrenteSP(agora time.Time) (time.Time, time.Time) {
	h := hojeSP(agora)
	ini := time.Date(h.Year(), h.Month(), 1, 0, 0, 0, 0, time.UTC)
	return ini, ini.AddDate(0, 1, -1)
}
