package catalog

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Quando a campanha pedida não existe OU não pertence ao cliente do viewer,
// devolvemos o catalog.ErrCampaignNotFound já declarado no pacote
// (campaign_failures.go) — anti-oracle: o handler responde 404 nos dois casos,
// sem revelar a existência de campanha de outro cliente.

// LiveStation é uma emissora da campanha a plotar no mapa ao vivo. Só
// retornamos emissoras com coordenada não-nula (geocoding pula
// internacionais/distritos).
type LiveStation struct {
	ID              uuid.UUID  `json:"id"`
	Name            string     `json:"name"`
	Band            string     `json:"band"`
	FrequencyMHz    *float64   `json:"frequency_mhz,omitempty"`
	City            *string    `json:"city,omitempty"`
	State           *string    `json:"state,omitempty"`
	Latitude        float64    `json:"latitude"`
	Longitude       float64    `json:"longitude"`
	HealthStatus    *string    `json:"health_status,omitempty"`
	LastDetectionAt *time.Time `json:"last_detection_at,omitempty"`
}

// LiveDetection é uma linha do feed "Últimas Veiculações" (modelo data/hora —
// espelha os campos que o AirtimeDetectionRow consome, mais leve).
type LiveDetection struct {
	ID             uuid.UUID `json:"id"`
	StationID      uuid.UUID `json:"station_id"`
	StationName    string    `json:"station_name"`
	StationLogoURL *string   `json:"station_logo_url,omitempty"`
	Band           string    `json:"band"`
	FrequencyMHz   *float64  `json:"frequency_mhz,omitempty"`
	City           *string   `json:"city,omitempty"`
	State          *string   `json:"state,omitempty"`
	DetectedAt     time.Time `json:"detected_at"`
	CommercialID   uuid.UUID `json:"commercial_id"`
	CommercialName string    `json:"commercial_name"`
	ClientName     *string   `json:"client_name,omitempty"`
}

type LiveMapResult struct {
	Stations         []LiveStation   `json:"stations"`
	RecentDetections []LiveDetection `json:"recent_detections"`
}

type LiveMap struct {
	pool *pgxpool.Pool
}

func NewLiveMap(pool *pgxpool.Pool) *LiveMap {
	return &LiveMap{pool: pool}
}

// Get retorna o mapa ao vivo de UMA campanha: as emissoras-alvo dela (com
// coordenada) + as últimas veiculações dela. scope == nil = admin/operator;
// scope != nil = client_id do viewer — a campanha precisa pertencer a ele,
// senão ErrCampaignNotFound (anti-oracle).
func (m *LiveMap) Get(ctx context.Context, campaignID uuid.UUID, scope *uuid.UUID) (LiveMapResult, error) {
	var res LiveMapResult

	// Existência + posse: resolve o client_id da campanha uma vez. 404 quando
	// não existe ou quando o viewer tenta uma campanha de outro cliente.
	var clientID uuid.UUID
	err := m.pool.QueryRow(ctx,
		`SELECT client_id FROM campaigns WHERE id = $1`, campaignID,
	).Scan(&clientID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return res, ErrCampaignNotFound
		}
		return res, err
	}
	if scope != nil && *scope != clientID {
		return res, ErrCampaignNotFound
	}

	stations, err := m.queryStations(ctx, campaignID)
	if err != nil {
		return res, err
	}
	res.Stations = stations

	dets, err := m.queryRecentDetections(ctx, campaignID)
	if err != nil {
		return res, err
	}
	res.RecentDetections = dets
	return res, nil
}

func (m *LiveMap) queryStations(ctx context.Context, campaignID uuid.UUID) ([]LiveStation, error) {
	rows, err := m.pool.Query(ctx, `
		SELECT s.id, s.name, s.band, s.frequency_mhz, s.city, s.state,
		       s.latitude, s.longitude, s.health_status,
		       (SELECT MAX(d.detected_at)
		          FROM detections d
		         WHERE d.station_id = s.id
		           AND d.campaign_id = $1
		           AND d.evidence_status <> 'audit_rejected'
		           AND d.ignored_at IS NULL) AS last_detection_at
		FROM stations s
		JOIN campaigns cmp ON cmp.id = $1 AND s.id = ANY(cmp.target_stations)
		WHERE s.latitude IS NOT NULL AND s.longitude IS NOT NULL
		ORDER BY s.name`, campaignID)
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

func (m *LiveMap) queryRecentDetections(ctx context.Context, campaignID uuid.UUID) ([]LiveDetection, error) {
	rows, err := m.pool.Query(ctx, `
		SELECT d.id, d.station_id, COALESCE(s.name, ''), s.logo_url,
		       COALESCE(s.band, ''), s.frequency_mhz, s.city, s.state,
		       d.detected_at, d.commercial_id, COALESCE(m.title, c.title, ''), cli.name
		FROM detections d
		LEFT JOIN stations s    ON s.id = d.station_id
		LEFT JOIN commercials c ON c.id = d.commercial_id
		LEFT JOIN materials m   ON m.id = d.commercial_id
		LEFT JOIN campaigns cmp ON cmp.id = d.campaign_id
		LEFT JOIN clients cli   ON cli.id = cmp.client_id
		WHERE d.campaign_id = $1
		  AND d.evidence_status <> 'audit_rejected'
		  AND d.ignored_at IS NULL
		  AND d.retracted_at IS NULL
		ORDER BY d.detected_at DESC
		LIMIT 50`, campaignID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []LiveDetection
	for rows.Next() {
		var d LiveDetection
		if err := rows.Scan(&d.ID, &d.StationID, &d.StationName, &d.StationLogoURL,
			&d.Band, &d.FrequencyMHz, &d.City, &d.State,
			&d.DetectedAt, &d.CommercialID, &d.CommercialName, &d.ClientName); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
