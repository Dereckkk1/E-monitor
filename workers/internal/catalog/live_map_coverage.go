package catalog

import (
	"context"

	"github.com/google/uuid"
)

// CoverageCity é um município dentro do alcance estimado de uma emissora.
//
// A lista sai de station_coverage_cities, que é preenchida pelo backfill-anatel
// a partir da classe do Plano Básico (ver
// docs/features/anatel-station-class-coverage.md). É dado DERIVADO e ESTÁTICO:
// só muda quando a Anatel republica o plano ou a emissora troca de cadastro.
type CoverageCity struct {
	StationID  uuid.UUID `json:"station_id"`
	IBGECode   int       `json:"ibge_code"`
	City       string    `json:"city"`
	State      string    `json:"state"`
	DistanceKm float64   `json:"distance_km"`
	// IsHome marca o município de licença/cadastro da emissora, que entra na
	// cobertura por premissa da outorga. É o único caso em que DistanceKm pode
	// ultrapassar o raio de alcance da classe — o mapa desenha esses fora do
	// anel sem que isso seja inconsistência.
	IsHome    bool    `json:"is_home"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

type LiveCoverageResult struct {
	Cities []CoverageCity `json:"cities"`
}

// Coverage devolve os municípios ao alcance das emissoras-alvo das campanhas
// pedidas. Endpoint SEPARADO do Get de propósito: o /live-map faz polling de
// 20s e esta lista é estática, então misturá-la ali seria retransmitir dezenas
// de KB imutáveis a cada ciclo, em toda sessão aberta o dia inteiro.
//
// A validação de existência/posse/status é a MESMA do Get — reusa
// validateCampaigns — porque a lista de cidades revela quais emissoras a
// campanha tem: sem a checagem, este endereço viraria o oracle que o Get
// fecha.
func (m *LiveMap) Coverage(ctx context.Context, campaignIDs []uuid.UUID, scopes []uuid.UUID, opts LiveMapOpts) (LiveCoverageResult, error) {
	var res LiveCoverageResult

	ids := dedupUUIDs(campaignIDs)
	if len(ids) == 0 {
		return res, ErrCampaignNotFound
	}
	if err := m.validateCampaigns(ctx, ids, scopes, opts); err != nil {
		return res, err
	}

	rows, err := m.pool.Query(ctx, `
		WITH scoped AS (
		    SELECT target_stations FROM campaigns WHERE id = ANY($1)
		),
		mon_stations AS (
		    SELECT DISTINCT st AS station_id
		    FROM scoped, unnest(target_stations) AS st
		)
		SELECT c.station_id, c.ibge_code, c.city, c.state,
		       c.distance_km, c.is_home, c.latitude, c.longitude
		FROM station_coverage_cities c
		JOIN mon_stations ms ON ms.station_id = c.station_id
		ORDER BY c.station_id, c.distance_km`, ids)
	if err != nil {
		return res, err
	}
	defer rows.Close()

	for rows.Next() {
		var c CoverageCity
		if err := rows.Scan(&c.StationID, &c.IBGECode, &c.City, &c.State,
			&c.DistanceKm, &c.IsHome, &c.Latitude, &c.Longitude); err != nil {
			return res, err
		}
		res.Cities = append(res.Cities, c)
	}
	return res, rows.Err()
}
