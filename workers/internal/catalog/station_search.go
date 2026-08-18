package catalog

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

// ─── Busca de emissoras ─────────────────────────────────────────────────────
//
// Padrão multi-token AND / multi-campo OR: `Jb 99.9 RJ` casa com a emissora
// cujo NOME tem "jb", o DIAL é 99.9 e a UF é RJ — cada token pode casar com um
// campo diferente. Documentado em docs/features/broadcaster-search.md.
//
// `metadata` fica de fora de propósito: ele guarda coverage_cities/states, e
// incluí-lo fazia a busca por uma cidade devolver toda emissora que a *cobre*
// em vez das que ficam *nela* (paridade com o /marketplace do E-radios).

// dialTextExpr normaliza frequency_mhz para comparação textual. A coluna é
// NUMERIC(6,2), então 99.9 é armazenado e renderizado como "99.90" — sem
// normalizar, quem digita "99,9" (vírgula, como se escreve dial no Brasil) não
// acha nada.
//
// O regexp corta o zero morto do decimal sem comer o zero significativo do
// inteiro: 99.90 → 99.9, 100.00 → 100, 1080.00 → 1080.
const dialTextExpr = `regexp_replace(COALESCE(frequency_mhz::text,''), '\.?0*$', '')`

var dialTokenRe = regexp.MustCompile(`^\d+([.,]\d+)?$`)

// normalizeDial devolve o token no mesmo formato que dialTextExpr produz, ou ""
// quando o token não é numérico.
func normalizeDial(tok string) string {
	if !dialTokenRe.MatchString(tok) {
		return ""
	}
	s := strings.ReplaceAll(tok, ",", ".")
	if strings.Contains(s, ".") {
		s = strings.TrimRight(s, "0")
		s = strings.TrimSuffix(s, ".")
	}
	return s
}

// tokenSearch é o SQL gerado para os tokens de uma query: os predicados (um por
// token, combinados por AND pelo chamador), a expressão de relevância e os
// argumentos posicionais correspondentes.
type tokenSearch struct {
	Where []string
	Score string // "" quando a query está vazia
	Args  []any
	Next  int // próximo placeholder livre ($Next)
}

// buildTokenSearch monta o predicado e o score de relevância da busca de
// emissoras, começando os placeholders em startN.
//
// Score por token: nome-prefixo 3, UF/banda exata 3, nome-contém 2,
// dial-exato 2, dial-prefixo 1, cidade-prefixo 1. Somado entre tokens, é o que
// faz `Jb 99.9 RJ` colocar a JB FM acima de quem casou só pela UF.
//
// UF vale tanto quanto nome-prefixo porque uma sigla de 2 letras casa por
// acidente dentro de nome com frequência: sem isso, `rj` põe "Gurjão"/PB e
// "NRJ FM"/BA na frente de uma emissora que é de fato do Rio.
func buildTokenSearch(q string, startN int) tokenSearch {
	ts := tokenSearch{Next: startN}
	var scores []string

	for _, tok := range strings.Fields(q) {
		n := ts.Next
		ts.Args = append(ts.Args, tok)
		ts.Next++

		// UF e banda casam por igualdade: com 2 caracteres o curinga não
		// acrescenta nada e só traz ruído.
		ors := []string{
			fmt.Sprintf(`unaccent(name) ILIKE '%%'||unaccent($%d)||'%%'`, n),
			fmt.Sprintf(`unaccent(COALESCE(city,'')) ILIKE '%%'||unaccent($%d)||'%%'`, n),
			fmt.Sprintf(`COALESCE(state,'') ILIKE $%d`, n),
			fmt.Sprintf(`band ILIKE $%d`, n),
		}
		parts := []string{
			fmt.Sprintf(`CASE WHEN unaccent(name) ILIKE unaccent($%d)||'%%' THEN 3 WHEN unaccent(name) ILIKE '%%'||unaccent($%d)||'%%' THEN 2 ELSE 0 END`, n, n),
			fmt.Sprintf(`CASE WHEN unaccent(COALESCE(city,'')) ILIKE unaccent($%d)||'%%' THEN 1 ELSE 0 END`, n),
			fmt.Sprintf(`CASE WHEN COALESCE(state,'') ILIKE $%d OR band ILIKE $%d THEN 3 ELSE 0 END`, n, n),
		}

		if dial := normalizeDial(tok); dial != "" {
			d := ts.Next
			ts.Args = append(ts.Args, dial)
			ts.Next++
			ors = append(ors,
				fmt.Sprintf(`%s = $%d`, dialTextExpr, d),
				fmt.Sprintf(`%s LIKE $%d||'%%'`, dialTextExpr, d),
			)
			parts = append(parts, fmt.Sprintf(
				`CASE WHEN %s = $%d THEN 2 WHEN %s LIKE $%d||'%%' THEN 1 ELSE 0 END`,
				dialTextExpr, d, dialTextExpr, d))
		}

		ts.Where = append(ts.Where, "("+strings.Join(ors, " OR ")+")")
		scores = append(scores, "("+strings.Join(parts, " + ")+")")
	}

	if len(scores) > 0 {
		ts.Score = strings.Join(scores, " + ")
	}
	return ts
}

// stationOrderBy é o desempate estável de qualquer listagem de emissoras:
// monitoradas primeiro, depois audiência, depois nome.
const stationOrderBy = `
	  CASE monitoring_status
	    WHEN 'active'      THEN 0
	    WHEN 'calibrating' THEN 1
	    WHEN 'paused'      THEN 2
	    ELSE 3
	  END,
	  pmm DESC NULLS LAST,
	  name`

// ─── Suggest (autocomplete do campo de busca) ───────────────────────────────

const (
	SuggestMinChars     = 2
	suggestStationLimit = 6
	suggestCityLimit    = 4
	suggestStateLimit   = 3
)

type SuggestInput struct {
	Q    string
	Band string
}

type SuggestStation struct {
	ID               uuid.UUID `json:"id"`
	Name             string    `json:"name"`
	Band             string    `json:"band"`
	FrequencyMHz     *float64  `json:"frequency_mhz,omitempty"`
	City             *string   `json:"city,omitempty"`
	State            *string   `json:"state,omitempty"`
	LogoURL          *string   `json:"logo_url,omitempty"`
	MonitoringStatus string    `json:"monitoring_status"`
}

type SuggestCity struct {
	City  string  `json:"city"`
	State *string `json:"state,omitempty"`
	Count int64   `json:"count"`
}

type SuggestState struct {
	State string `json:"state"`
	Count int64  `json:"count"`
}

type SuggestOutput struct {
	Stations []SuggestStation `json:"stations"`
	Cities   []SuggestCity    `json:"cities"`
	States   []SuggestState   `json:"states"`
}

var ufRe = regexp.MustCompile(`^[A-Za-z]{2}$`)

// Suggest alimenta o dropdown de busca de /stations com três grupos.
//
// A contagem de cidade/UF é agregada sobre a tabela inteira — o dropdown antigo
// contava só dentro das 50 linhas que a página tinha carregado, e exibia um
// número que quase nunca era o certo.
func (s *Stations) Suggest(ctx context.Context, in SuggestInput) (SuggestOutput, error) {
	out := SuggestOutput{Stations: []SuggestStation{}, Cities: []SuggestCity{}, States: []SuggestState{}}
	q := strings.TrimSpace(in.Q)
	if len([]rune(q)) < SuggestMinChars {
		return out, nil
	}

	var err error
	if out.Stations, err = s.suggestStations(ctx, q, in.Band); err != nil {
		return out, err
	}
	if out.Cities, err = s.suggestCities(ctx, q, in.Band); err != nil {
		return out, err
	}
	if out.States, err = s.suggestStates(ctx, q, in.Band); err != nil {
		return out, err
	}
	return out, nil
}

func (s *Stations) suggestStations(ctx context.Context, q, band string) ([]SuggestStation, error) {
	ts := buildTokenSearch(q, 1)
	if len(ts.Where) == 0 {
		return []SuggestStation{}, nil
	}
	where, args, n := ts.Where, ts.Args, ts.Next
	if band != "" {
		where = append(where, fmt.Sprintf("band = $%d", n))
		args = append(args, band)
		n++
	}
	args = append(args, suggestStationLimit)

	rows, err := s.pool.Query(ctx, fmt.Sprintf(`
		SELECT id, name, band, frequency_mhz, city, state, logo_url, monitoring_status
		FROM stations
		WHERE %s
		ORDER BY (%s) DESC, %s
		LIMIT $%d`,
		strings.Join(where, " AND "), ts.Score, stationOrderBy, n), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []SuggestStation{}
	for rows.Next() {
		var st SuggestStation
		if err := rows.Scan(&st.ID, &st.Name, &st.Band, &st.FrequencyMHz,
			&st.City, &st.State, &st.LogoURL, &st.MonitoringStatus); err != nil {
			return nil, err
		}
		list = append(list, st)
	}
	return list, rows.Err()
}

// suggestCities exige que TODO token case com city ou state, e que ao menos UM
// deles case com city. Sem a segunda condição, digitar "rj" despejaria as
// quatro maiores cidades do estado — ruído, já que o grupo de UF cobre isso.
//
// O match de cidade aqui é por INÍCIO DE PALAVRA, não substring: sugerir
// "Varjota" e "São Borja" pra quem digitou "rj" é ruído puro. O truque do
// espaço à esquerda (' '||city LIKE '% '||tok||'%') dá início-de-palavra sem
// montar regex — e sem ter que escapar metacaractere de token como "99.9".
// Cuidado: em `List` o match de cidade continua substring de propósito, senão
// "paulo" pararia de achar "São Paulo".
func (s *Stations) suggestCities(ctx context.Context, q, band string) ([]SuggestCity, error) {
	toks := strings.Fields(q)
	if len(toks) == 0 {
		return []SuggestCity{}, nil
	}

	var (
		where    []string
		cityHits []string
		args     []any
	)
	n := 1
	for _, tok := range toks {
		cityHit := fmt.Sprintf(`(' '||unaccent(COALESCE(city,''))) ILIKE '%% '||unaccent($%d)||'%%'`, n)
		where = append(where, fmt.Sprintf(`(%s OR COALESCE(state,'') ILIKE $%d)`, cityHit, n))
		cityHits = append(cityHits, cityHit)
		args = append(args, tok)
		n++
	}
	where = append(where, "("+strings.Join(cityHits, " OR ")+")")
	where = append(where, `COALESCE(city,'') <> ''`)

	if band != "" {
		where = append(where, fmt.Sprintf("band = $%d", n))
		args = append(args, band)
		n++
	}
	args = append(args, suggestCityLimit)

	rows, err := s.pool.Query(ctx, fmt.Sprintf(`
		SELECT city, state, COUNT(*)
		FROM stations
		WHERE %s
		GROUP BY city, state
		ORDER BY COUNT(*) DESC, city
		LIMIT $%d`, strings.Join(where, " AND "), n), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []SuggestCity{}
	for rows.Next() {
		var c SuggestCity
		if err := rows.Scan(&c.City, &c.State, &c.Count); err != nil {
			return nil, err
		}
		list = append(list, c)
	}
	return list, rows.Err()
}

// suggestStates só dispara quando a query INTEIRA é uma sigla de 2 letras — é o
// único caso em que sugerir "todas as emissoras de X" ajuda.
func (s *Stations) suggestStates(ctx context.Context, q, band string) ([]SuggestState, error) {
	if !ufRe.MatchString(q) {
		return []SuggestState{}, nil
	}
	args := []any{q}
	where := []string{`state ILIKE $1`}
	n := 2
	if band != "" {
		where = append(where, fmt.Sprintf("band = $%d", n))
		args = append(args, band)
		n++
	}
	args = append(args, suggestStateLimit)

	rows, err := s.pool.Query(ctx, fmt.Sprintf(`
		SELECT state, COUNT(*)
		FROM stations
		WHERE %s
		GROUP BY state
		ORDER BY COUNT(*) DESC
		LIMIT $%d`, strings.Join(where, " AND "), n), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []SuggestState{}
	for rows.Next() {
		var st SuggestState
		if err := rows.Scan(&st.State, &st.Count); err != nil {
			return nil, err
		}
		list = append(list, st)
	}
	return list, rows.Err()
}
