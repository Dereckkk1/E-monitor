package catalog

import (
	"context"
	"fmt"
	"strings"
)

// NormalizeDial devolve o dial no formato que a busca da tela compara
// (ponto decimal, sem zeros à direita: "99,90" → "99.9", "100.0" → "100"), ou
// "" se o texto não for um número.
func NormalizeDial(dial string) string {
	return normalizeDial(strings.TrimSpace(dial))
}

// CandidatosPorDial resolve um LOTE de emissoras por dial + UF numa consulta
// só (o resolver_emissoras do MCP: um agente que programa campanha casa as
// emissoras do PI pelo dial e pela UF, e uma chamada por emissora era o que
// mais derrubava o conector dele).
//
// Devolve as emissoras das UFs pedidas cujo dial está em dials — comparado
// pela mesma expressão da busca da tela (dialTextExpr) — e, junto, as SEM dial
// cadastrado que têm algum número no nome ("89 - FM (89.1)"): quem chama confere
// o dial no nome dessas. dials vem normalizado por NormalizeDial.
func (s *Stations) CandidatosPorDial(ctx context.Context, ufs, dials []string) ([]Station, error) {
	if len(ufs) == 0 || len(dials) == 0 {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`
		SELECT %s FROM stations
		WHERE state = ANY($1::text[])
		  AND (%s = ANY($2::text[]) OR (frequency_mhz IS NULL AND name ~ '[0-9]'))
		ORDER BY state, name, id`, stationSelectCols, dialTextExpr), ufs, dials)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Station
	for rows.Next() {
		st, err := scanStationRow(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}
