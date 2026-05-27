package catalog

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// LiveStation é uma emissora monitorada a plotar no mapa ao vivo. Só retornamos
// emissoras com coordenada não-nula (geocoding pula internacionais/distritos).
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

// LiveDetection é uma linha do feed "Últimas Veiculações" (modelo data/hora).
type LiveDetection struct {
	ID             uuid.UUID `json:"id"`
	StationName    string    `json:"station_name"`
	Band           string    `json:"band"`
	FrequencyMHz   *float64  `json:"frequency_mhz,omitempty"`
	City           *string   `json:"city,omitempty"`
	State          *string   `json:"state,omitempty"`
	DetectedAt     time.Time `json:"detected_at"`
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

// Get retorna o payload do mapa escopado ao requester. scope == nil =
// admin/operator (vê todas as emissoras monitoradas + todas as veiculações);
// scope != nil = client_id do viewer (só emissoras das campanhas ativas dele +
// só as veiculações dele). $1 nulo na SQL alterna os dois caminhos.
func (m *LiveMap) Get(ctx context.Context, scope *uuid.UUID) (LiveMapResult, error) {
	var res LiveMapResult

	var scopeArg any
	if scope != nil {
		scopeArg = *scope
	}

	stations, err := m.queryStations(ctx, scopeArg)
	if err != nil {
		return res, err
	}
	res.Stations = stations

	dets, err := m.queryRecentDetections(ctx, scopeArg)
	if err != nil {
		return res, err
	}
	res.RecentDetections = dets
	return res, nil
}

func (m *LiveMap) queryStations(ctx context.Context, scopeArg any) ([]LiveStation, error) {
	rows, err := m.pool.Query(ctx, `
		SELECT s.id, s.name, s.band, s.frequency_mhz, s.city, s.state,
		       s.latitude, s.longitude, s.health_status,
		       (SELECT MAX(d.detected_at)
		          FROM detections d
		          LEFT JOIN campaigns c2 ON c2.id = d.campaign_id
		         WHERE d.station_id = s.id
		           AND ($1::uuid IS NULL OR c2.client_id = $1)
		           AND d.evidence_status <> 'audit_rejected'
		           AND d.ignored_at IS NULL) AS last_detection_at
		FROM stations s
		WHERE s.latitude IS NOT NULL AND s.longitude IS NOT NULL
		  AND (
		        ($1::uuid IS NULL AND s.monitoring_status = 'active')
		     OR ($1::uuid IS NOT NULL AND EXISTS (
		            SELECT 1 FROM campaigns cmp
		             WHERE cmp.client_id = $1
		               AND cmp.status = 'ativa'
		               AND s.id = ANY(cmp.target_stations)))
		      )
		ORDER BY s.name`, scopeArg)
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

func (m *LiveMap) queryRecentDetections(ctx context.Context, scopeArg any) ([]LiveDetection, error) {
	rows, err := m.pool.Query(ctx, `
		SELECT d.id, COALESCE(s.name, ''), COALESCE(s.band, ''),
		       s.frequency_mhz, s.city, s.state,
		       d.detected_at, COALESCE(m.title, c.title, ''), cli.name
		FROM detections d
		LEFT JOIN stations s    ON s.id = d.station_id
		LEFT JOIN commercials c ON c.id = d.commercial_id
		LEFT JOIN materials m   ON m.id = d.commercial_id
		LEFT JOIN campaigns cmp ON cmp.id = d.campaign_id
		LEFT JOIN clients cli   ON cli.id = cmp.client_id
		WHERE ($1::uuid IS NULL OR cmp.client_id = $1)
		  AND d.evidence_status <> 'audit_rejected'
		  AND d.ignored_at IS NULL
		  AND d.retracted_at IS NULL
		ORDER BY d.detected_at DESC
		LIMIT 50`, scopeArg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []LiveDetection
	for rows.Next() {
		var d LiveDetection
		if err := rows.Scan(&d.ID, &d.StationName, &d.Band, &d.FrequencyMHz,
			&d.City, &d.State, &d.DetectedAt, &d.CommercialName, &d.ClientName); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
