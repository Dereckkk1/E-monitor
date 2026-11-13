package mcp

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"radiocheck/internal/catalog"
)

// ── Entradas das tools de relatório ─────────────────────────────────────

// lerIDs valida 0..teto uuids e tira repetidos (mantendo a ordem): os
// repositórios contam as campanhas achadas contra as pedidas, e um id repetido
// viraria um falso "não são do cliente". rec != nil é a recusa pronta.
func lerIDs(campo string, ss []string, teto int) ([]uuid.UUID, *sdk.CallToolResult) {
	if len(ss) > teto {
		r, _ := recusa(fmt.Sprintf("%s: no máximo %d ids por chamada (vieram %d); estreite a seleção ou divida em mais de uma chamada",
			campo, teto, len(ss)), "entrada_invalida")
		return nil, r
	}
	out := make([]uuid.UUID, 0, len(ss))
	for _, s := range ss {
		id, err := uuid.Parse(s)
		if err != nil {
			r, _ := recusaEntrada(campo)
			return nil, r
		}
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out, nil
}

// lerIDsObrigatorios: lerIDs com pelo menos um id.
func lerIDsObrigatorios(campo string, ss []string, teto int) ([]uuid.UUID, *sdk.CallToolResult) {
	ids, rec := lerIDs(campo, ss, teto)
	if rec == nil && len(ids) == 0 {
		rec, _ = recusa(fmt.Sprintf("%s precisa de 1 a %d ids (de outra tool; nunca invente)", campo, teto), "entrada_invalida")
	}
	return ids, rec
}

// lerDias lê de/ate (AAAA-MM-DD) com padrão para o lado que não vier.
// rec != nil é a recusa pronta; quando o padrão de um lado inverte a janela,
// a mensagem diz qual padrão foi usado (o modelo não o vê).
func lerDias(de, ate string, padraoDe, padraoAte time.Time) (time.Time, time.Time, *sdk.CallToolResult) {
	ini, fim := padraoDe, padraoAte
	if de != "" {
		d, ok := lerDia(de)
		if !ok {
			r, _ := recusaEntrada("de")
			return ini, fim, r
		}
		ini = d
	}
	if ate != "" {
		d, ok := lerDia(ate)
		if !ok {
			r, _ := recusaEntrada("ate")
			return ini, fim, r
		}
		fim = d
	}
	if !fim.Before(ini) {
		return ini, fim, nil
	}
	var r *sdk.CallToolResult
	switch {
	case de != "" && ate == "":
		r, _ = recusa(fmt.Sprintf("período inválido: ate não veio e o padrão dele é %s, antes de de=%s; passe ate também", dia(fim), dia(ini)), "entrada_invalida")
	case de == "" && ate != "":
		r, _ = recusa(fmt.Sprintf("período inválido: de não veio e o padrão dele é %s, depois de ate=%s; passe de também", dia(ini), dia(fim)), "entrada_invalida")
	default:
		r, _ = recusaCampoDoPeriodo(campoPeriodo)
	}
	return ini, fim, r
}

// comTeto: o teto de página de uma tool cujo item é grande (lista interna,
// muitos campos). Mesmo padrão do limiteTeto global: acima do teto o limit é
// rebaixado (page.limit ecoa o valor usado), e o padrão vira o próprio teto
// quando o teto é menor que limitePadrao.
func (p Paginacao) comTeto(teto int) Paginacao {
	if p.Limit <= 0 {
		p.Limit = min(limitePadrao, teto)
	}
	p.Limit = min(p.Limit, teto)
	return p
}

// ── Pedaços comuns das respostas de relatório ───────────────────────────

// campanhaResumida: a campanha de um relatório, com vigência e estado.
type campanhaResumida struct {
	Campanha ref    `json:"campanha"`
	Inicio   string `json:"inicio"`
	Fim      string `json:"fim"`
	Estado   string `json:"estado"`
}

func campanhaResumidaDe(c *catalog.Campaign) campanhaResumida {
	return campanhaResumida{Campanha: refDe(c.ID, c.Name), Inicio: dia(c.StartDate), Fim: dia(c.EndDate), Estado: c.Status}
}

// ressalvaCanceladaRelatorio: a política "mantida e marcada" dos relatórios
// (docs/features/cancelled-campaign-handling.md). As tools de lista da Fatia 1
// usam ressalvaCancelada, que fala de seletores e KPIs operacionais.
const ressalvaCanceladaRelatorio = "Há campanha cancelada no resultado: os números dela entram aqui como histórico (o que foi ao ar antes do cancelamento), mas ela fica fora da cobrança e dos KPIs operacionais, e o déficit dela está congelado na data do cancelamento."

// links é o bloco de deep-link de toda tool de relatório (spec §4.5, D7).
// Nunca há link direto de arquivo: o download do portal exige o login da
// pessoa no cabeçalho (JWT), e um link com credencial seria URL assinada.
type links struct {
	Tela       *string `json:"tela"`
	ComoBaixar string  `json:"como_baixar,omitempty"`
}

// linkTela: a rota do portal com os filtros na query (nil sem portal configurado).
// Valores vazios são descartados: "?campaigns=" não é filtro, e a tela leria
// a chave presente como uma seleção vazia.
func (c ConfigServidor) linkTela(caminho string, q url.Values) *string {
	limpa := url.Values{}
	for k, vs := range q {
		for _, v := range vs {
			if v != "" {
				limpa.Add(k, v)
			}
		}
	}
	if len(limpa) == 0 {
		return c.link(caminho)
	}
	return c.link(caminho + "?" + limpa.Encode())
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

// cortar devolve no máximo n itens (n < 0 vale 0) e o total real; nunca nil
// (sai [] no JSON).
func cortar[T any](xs []T, n int) ([]T, int) {
	if xs == nil {
		xs = []T{}
	}
	return xs[:min(len(xs), max(n, 0))], len(xs)
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
