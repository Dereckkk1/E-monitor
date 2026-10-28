package catalog

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Leituras que só o MCP usa hoje (spec 2026-10-27 §6 Fatia 1). São leituras
// de coluna, sem regra de negócio: a tela não as tem porque não mostra o dado
// (ou o mostra por outro caminho).

// HubIDs devolve o hub_id (ponte com a Central de Clientes, migration 0068)
// dos clientes pedidos. Cliente sem ponte não aparece no mapa.
func (c *Clients) HubIDs(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	out := make(map[uuid.UUID]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := c.pool.Query(ctx,
		`SELECT id, hub_id FROM clients WHERE id = ANY($1) AND hub_id IS NOT NULL`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var hub string
		if err := rows.Scan(&id, &hub); err != nil {
			return nil, err
		}
		out[id] = hub
	}
	return out, rows.Err()
}

// ListByHubCode devolve as campanhas com este código do hub, da mais nova
// para a mais velha. O código NÃO é único (dois PIs podem dividi-lo — decisão
// 5 da spec do hub 2026-09-18), por isso é lista. code tem de vir canônico
// (hub.NormalizaCodigo), como é gravado.
func (c *Campaigns) ListByHubCode(ctx context.Context, code string) ([]Campaign, error) {
	rows, err := c.pool.Query(ctx, `
		SELECT id, client_id, name, start_date, end_date, status, target_stations,
		       fixed_cpm, hub_code, hub_notified_at, created_at, updated_at
		FROM campaigns WHERE hub_code = $1
		ORDER BY start_date DESC, id`, code)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Campaign
	for rows.Next() {
		var camp Campaign
		if err := rows.Scan(&camp.ID, &camp.ClientID, &camp.Name, &camp.StartDate, &camp.EndDate,
			&camp.Status, &camp.TargetStations, &camp.FixedCPM, &camp.HubCode, &camp.HubNotifiedAt,
			&camp.CreatedAt, &camp.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, camp)
	}
	return out, rows.Err()
}

// CancelledAtFor devolve cancelled_at (migration 0044) das campanhas
// canceladas entre ids. Campaign não carrega a coluna; quem precisa dizer
// "déficit congelado em" pergunta aqui.
func (c *Campaigns) CancelledAtFor(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]time.Time, error) {
	out := map[uuid.UUID]time.Time{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := c.pool.Query(ctx, `
		SELECT id, cancelled_at FROM campaigns
		WHERE id = ANY($1) AND status = 'cancelada' AND cancelled_at IS NOT NULL`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var em time.Time
		if err := rows.Scan(&id, &em); err != nil {
			return nil, err
		}
		out[id] = em
	}
	return out, rows.Err()
}

// StationAnatel é o cruzamento da emissora com os planos da Anatel
// (migration 0066, docs/features/anatel-station-class-coverage.md).
// Ponteiro nil = indeterminado, NUNCA zero (AM não tem raio por classe).
type StationAnatel struct {
	Class      *string
	CoverageKm *float64 // contorno protegido (normativo)
	ReachKm    *float64 // transbordo = contorno × 1,5 (gera as cidades no alcance)
	MatchTier  *string  // exact | geo | radcom
	PlanCity   *string
	PlanState  *string
	MatchedAt  *time.Time
}

// Anatel lê o cruzamento Anatel de uma emissora. pgx.ErrNoRows se a emissora
// não existe.
func (s *Stations) Anatel(ctx context.Context, id uuid.UUID) (*StationAnatel, error) {
	var a StationAnatel
	err := s.pool.QueryRow(ctx, `
		SELECT anatel_class, anatel_coverage_km::float8, anatel_reach_km::float8,
		       anatel_match_tier, anatel_plan_city, anatel_plan_state, anatel_matched_at
		FROM stations WHERE id = $1`, id).
		Scan(&a.Class, &a.CoverageKm, &a.ReachKm, &a.MatchTier, &a.PlanCity, &a.PlanState, &a.MatchedAt)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// CoverageCities: as cidades no alcance (transbordo) de UMA emissora, da mais
// perto para a mais longe. A mesma tabela que o /live-map lê por campanha
// (LiveMap.Coverage).
func (s *Stations) CoverageCities(ctx context.Context, id uuid.UUID) ([]CoverageCity, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT station_id, ibge_code, city, state, distance_km::float8, is_home,
		       latitude::float8, longitude::float8
		FROM station_coverage_cities WHERE station_id = $1
		ORDER BY distance_km, city`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CoverageCity{}
	for rows.Next() {
		var c CoverageCity
		if err := rows.Scan(&c.StationID, &c.IBGECode, &c.City, &c.State, &c.DistanceKm,
			&c.IsHome, &c.Latitude, &c.Longitude); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// DetectionAttribution é uma projeção da tocada (detection_campaigns, F-119):
// a tocada conta para cada campanha com a própria categoria.
type DetectionAttribution struct {
	CampaignID     uuid.UUID
	CampaignName   string
	CommercialID   uuid.UUID
	CommercialName string
	Category       string
}

// Attributions lista as projeções de uma tocada, por nome de campanha.
// Get devolve só a canônica (detections.campaign_id).
func (d *Detections) Attributions(ctx context.Context, id uuid.UUID) ([]DetectionAttribution, error) {
	rows, err := d.pool.Query(ctx, `
		SELECT dc.campaign_id, COALESCE(cmp.name, ''), dc.commercial_id,
		       COALESCE(m.title, c.title, ''), dc.category
		FROM detection_campaigns dc
		LEFT JOIN campaigns cmp ON cmp.id = dc.campaign_id
		LEFT JOIN materials m   ON m.id = dc.commercial_id
		LEFT JOIN commercials c ON c.id = dc.commercial_id
		WHERE dc.detection_id = $1
		ORDER BY cmp.name, dc.campaign_id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DetectionAttribution{}
	for rows.Next() {
		var a DetectionAttribution
		if err := rows.Scan(&a.CampaignID, &a.CampaignName, &a.CommercialID, &a.CommercialName, &a.Category); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
