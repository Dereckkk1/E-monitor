package catalog

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ManagementOverview agrega a "Visão Gerencial" (/management): uma visão
// cross-campanha/cross-cliente da operação inteira. Admin-only — o gating é
// feito no router (subgrupo admin/operator), por isso o repo não recebe scope.
//
// Convenção: os filtros (client/campaigns/status + sobreposição de período)
// definem QUAIS campanhas entram na visão. Sobre essas campanhas o mapa mostra
// as emissoras-alvo (pulso = health_status atual) e o feed as últimas
// detecções (sempre "agora"). O recorte de período só limita os KPIs
// históricos (airings_total). Reusa os tipos LiveStation/LiveDetection do
// live_map.go.
type ManagementOverview struct {
	pool *pgxpool.Pool
}

func NewManagementOverview(pool *pgxpool.Pool) *ManagementOverview {
	return &ManagementOverview{pool: pool}
}

// ManagementParams são os filtros opcionais já validados.
// ClientID nil = todos os clientes. CampaignIDs vazio = todas as campanhas.
// Status "" = todos os status. From/To delimitam o período (KPIs históricos).
type ManagementParams struct {
	ClientID    *uuid.UUID
	CampaignIDs []uuid.UUID
	Status      string
	From        time.Time
	To          time.Time
}

// ManagementKPIs são os 4 números (+ contexto) da coluna esquerda da tela.
type ManagementKPIs struct {
	StationsMonitored  int   `json:"stations_monitored"`  // distintas no período
	StationsLive       int   `json:"stations_live"`       // health_status='ok' agora
	StatesCount        int   `json:"states_count"`        // UFs distintas
	MaterialsMonitored int   `json:"materials_monitored"` // materiais vinculados
	CampaignsCount     int   `json:"campaigns_count"`     // campanhas no recorte
	AiringsTotal       int64 `json:"airings_total"`       // veiculações no período
	AiringsToday       int64 `json:"airings_today"`       // veiculações hoje
}

type ManagementResult struct {
	KPIs             ManagementKPIs  `json:"kpis"`
	Stations         []LiveStation   `json:"stations"`
	RecentDetections []LiveDetection `json:"recent_detections"`
	// MonitoredStationIDs: todas as emissoras monitoradas no recorte (não só as
	// geocodadas em Stations). Não serializado — o handler cruza esses ids com
	// o snapshot de workers ativos do supervisor pra calcular StationsLive
	// ("monitorando agora"), mesma fonte da /operations. A coluna
	// stations.health_status não é populada pelo sistema, então não serve.
	MonitoredStationIDs []uuid.UUID `json:"-"`
}

// scopeArgs monta os 5 args compartilhados pelas três queries, na ordem
// $1=client_id (nullable), $2=campaign_ids, $3=status, $4=from, $5=to.
func (m *ManagementOverview) scopeArgs(p ManagementParams) []any {
	ids := p.CampaignIDs
	if ids == nil {
		ids = []uuid.UUID{}
	}
	return []any{p.ClientID, ids, p.Status, p.From, p.To}
}

func (m *ManagementOverview) Get(ctx context.Context, p ManagementParams) (ManagementResult, error) {
	var res ManagementResult

	ids, err := m.queryMonitoredStationIDs(ctx, p)
	if err != nil {
		return res, err
	}
	res.MonitoredStationIDs = ids

	kpis, err := m.queryKPIs(ctx, p)
	if err != nil {
		return res, err
	}
	// StationsMonitored = nº distinto de emissoras monitoradas. StationsLive
	// fica 0 aqui — o handler preenche cruzando ids com os workers ativos do
	// supervisor (a coluna health_status no banco não é populada).
	kpis.StationsMonitored = len(ids)
	res.KPIs = kpis

	stations, err := m.queryStations(ctx, p)
	if err != nil {
		return res, err
	}
	res.Stations = stations

	dets, err := m.queryRecentDetections(ctx, p)
	if err != nil {
		return res, err
	}
	res.RecentDetections = dets
	return res, nil
}

// scoped CTE: campanhas que casam com os filtros + sobreposição de período.
const mgmtScopedCTE = `
	WITH scoped AS (
	    SELECT id, target_stations
	    FROM campaigns
	    WHERE ($1::uuid IS NULL OR client_id = $1)
	      AND ($2::uuid[] = '{}' OR id = ANY($2))
	      AND ($3 = '' OR status = $3)
	      AND start_date <= $5::date
	      AND end_date   >= $4::date
	),
	mon_stations AS (
	    SELECT DISTINCT st AS station_id
	    FROM scoped, unnest(target_stations) AS st
	)`

func (m *ManagementOverview) queryKPIs(ctx context.Context, p ManagementParams) (ManagementKPIs, error) {
	var k ManagementKPIs
	err := m.pool.QueryRow(ctx, mgmtScopedCTE+`
		, joined AS (
		    SELECT s.state
		    FROM mon_stations ms JOIN stations s ON s.id = ms.station_id
		)
		SELECT
		  (SELECT COUNT(DISTINCT state) FROM joined WHERE state IS NOT NULL)         AS states_count,
		  (SELECT COUNT(*) FROM scoped)                                              AS campaigns_count,
		  (SELECT COUNT(DISTINCT cm.material_id) FROM campaign_materials cm
		         WHERE cm.campaign_id IN (SELECT id FROM scoped))                    AS materials_monitored,
		  (SELECT COUNT(*) FROM detections d
		         WHERE d.campaign_id IN (SELECT id FROM scoped)
		           AND d.evidence_status <> 'audit_rejected'
		           AND d.ignored_at IS NULL AND d.retracted_at IS NULL
		           AND d.detected_at::date BETWEEN $4::date AND $5::date)            AS airings_total,
		  (SELECT COUNT(*) FROM detections d
		         WHERE d.campaign_id IN (SELECT id FROM scoped)
		           AND d.evidence_status <> 'audit_rejected'
		           AND d.ignored_at IS NULL AND d.retracted_at IS NULL
		           AND (d.detected_at AT TIME ZONE 'America/Sao_Paulo')::date
		               = (now() AT TIME ZONE 'America/Sao_Paulo')::date)            AS airings_today
	`, m.scopeArgs(p)...).Scan(
		&k.StatesCount, &k.CampaignsCount, &k.MaterialsMonitored,
		&k.AiringsTotal, &k.AiringsToday,
	)
	return k, err
}

// queryMonitoredStationIDs devolve os ids das emissoras monitoradas no recorte
// (distintas, que existem na tabela stations). Usado pra contar StationsMonitored
// e pra cruzar com os workers ativos do supervisor (StationsLive).
func (m *ManagementOverview) queryMonitoredStationIDs(ctx context.Context, p ManagementParams) ([]uuid.UUID, error) {
	rows, err := m.pool.Query(ctx, mgmtScopedCTE+`
		SELECT s.id
		FROM mon_stations ms JOIN stations s ON s.id = ms.station_id
	`, m.scopeArgs(p)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (m *ManagementOverview) queryStations(ctx context.Context, p ManagementParams) ([]LiveStation, error) {
	rows, err := m.pool.Query(ctx, mgmtScopedCTE+`
		SELECT s.id, s.name, s.band, s.frequency_mhz, s.city, s.state,
		       s.latitude, s.longitude, s.health_status,
		       (SELECT MAX(d.detected_at)
		          FROM detections d
		         WHERE d.station_id = s.id
		           AND d.campaign_id IN (SELECT id FROM scoped)
		           AND `+ApprovedDetectionsFilter+`) AS last_detection_at
		FROM stations s
		JOIN mon_stations ms ON ms.station_id = s.id
		WHERE s.latitude IS NOT NULL AND s.longitude IS NOT NULL
		ORDER BY s.name
	`, m.scopeArgs(p)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []LiveStation
	for rows.Next() {
		var st LiveStation
		if err := rows.Scan(&st.ID, &st.Name, &st.Band, &st.FrequencyMHz,
			&st.City, &st.State, &st.Latitude, &st.Longitude,
			&st.HealthStatus, &st.LastDetectionAt); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

func (m *ManagementOverview) queryRecentDetections(ctx context.Context, p ManagementParams) ([]LiveDetection, error) {
	rows, err := m.pool.Query(ctx, `
		SELECT d.id, d.station_id, COALESCE(s.name, ''), s.logo_url,
		       COALESCE(s.band, ''), s.frequency_mhz, s.city, s.state,
		       d.detected_at, d.commercial_id, COALESCE(m.title, c.title, ''), cli.name,
		       d.evidence_status
		FROM detections d
		LEFT JOIN stations s    ON s.id = d.station_id
		LEFT JOIN commercials c ON c.id = d.commercial_id
		LEFT JOIN materials m   ON m.id = d.commercial_id
		LEFT JOIN campaigns cmp ON cmp.id = d.campaign_id
		LEFT JOIN clients cli   ON cli.id = cmp.client_id
		WHERE d.campaign_id IN (
		    SELECT id FROM campaigns
		    WHERE ($1::uuid IS NULL OR client_id = $1)
		      AND ($2::uuid[] = '{}' OR id = ANY($2))
		      AND ($3 = '' OR status = $3)
		      AND start_date <= $5::date
		      AND end_date   >= $4::date
		)
		  AND d.evidence_status <> 'audit_rejected'
		  AND d.ignored_at IS NULL
		  AND d.retracted_at IS NULL
		ORDER BY d.detected_at DESC
		LIMIT 50
	`, m.scopeArgs(p)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []LiveDetection
	for rows.Next() {
		var d LiveDetection
		if err := rows.Scan(&d.ID, &d.StationID, &d.StationName, &d.StationLogoURL,
			&d.Band, &d.FrequencyMHz, &d.City, &d.State,
			&d.DetectedAt, &d.CommercialID, &d.CommercialName, &d.ClientName,
			&d.EvidenceStatus); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
