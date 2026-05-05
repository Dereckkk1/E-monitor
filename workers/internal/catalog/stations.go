package catalog

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Station struct {
	ID                  uuid.UUID  `json:"id"`
	ShortID             int32      `json:"short_id"`
	Name                string     `json:"name"`
	Band                string     `json:"band"`
	FrequencyMHz        *float64   `json:"frequency_mhz,omitempty"`
	City                *string    `json:"city,omitempty"`
	State               *string    `json:"state,omitempty"`
	StreamURL           string     `json:"stream_url"`
	MonitoringStatus    string     `json:"monitoring_status"`
	LastHealthCheck     *time.Time `json:"last_health_check,omitempty"`
	HealthStatus        *string    `json:"health_status,omitempty"`
	ConsecutiveFailures int32      `json:"consecutive_failures"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

type Stations struct {
	pool *pgxpool.Pool
}

func NewStations(pool *pgxpool.Pool) *Stations {
	return &Stations{pool: pool}
}

type CreateStationInput struct {
	Name         string   `json:"name"`
	Band         string   `json:"band"`
	FrequencyMHz *float64 `json:"frequency_mhz,omitempty"`
	City         *string  `json:"city,omitempty"`
	State        *string  `json:"state,omitempty"`
	StreamURL    string   `json:"stream_url"`
}

func (s *Stations) Create(ctx context.Context, in CreateStationInput) (*Station, error) {
	var st Station
	err := s.pool.QueryRow(ctx, `
		INSERT INTO stations (name, band, frequency_mhz, city, state, stream_url)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, short_id, name, band, frequency_mhz, city, state, stream_url,
		          monitoring_status, last_health_check, health_status, consecutive_failures,
		          created_at, updated_at`,
		in.Name, in.Band, in.FrequencyMHz, in.City, in.State, in.StreamURL,
	).Scan(&st.ID, &st.ShortID, &st.Name, &st.Band, &st.FrequencyMHz, &st.City, &st.State,
		&st.StreamURL, &st.MonitoringStatus, &st.LastHealthCheck, &st.HealthStatus,
		&st.ConsecutiveFailures, &st.CreatedAt, &st.UpdatedAt)
	return &st, err
}

func (s *Stations) List(ctx context.Context) ([]Station, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, short_id, name, band, frequency_mhz, city, state, stream_url,
		       monitoring_status, last_health_check, health_status, consecutive_failures,
		       created_at, updated_at
		FROM stations ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Station
	for rows.Next() {
		var st Station
		if err := rows.Scan(&st.ID, &st.ShortID, &st.Name, &st.Band, &st.FrequencyMHz,
			&st.City, &st.State, &st.StreamURL, &st.MonitoringStatus,
			&st.LastHealthCheck, &st.HealthStatus, &st.ConsecutiveFailures,
			&st.CreatedAt, &st.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

func (s *Stations) Get(ctx context.Context, id uuid.UUID) (*Station, error) {
	var st Station
	err := s.pool.QueryRow(ctx, `
		SELECT id, short_id, name, band, frequency_mhz, city, state, stream_url,
		       monitoring_status, last_health_check, health_status, consecutive_failures,
		       created_at, updated_at
		FROM stations WHERE id = $1`, id,
	).Scan(&st.ID, &st.ShortID, &st.Name, &st.Band, &st.FrequencyMHz, &st.City, &st.State,
		&st.StreamURL, &st.MonitoringStatus, &st.LastHealthCheck, &st.HealthStatus,
		&st.ConsecutiveFailures, &st.CreatedAt, &st.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &st, nil
}

func (s *Stations) UpdateMonitoringStatus(ctx context.Context, id uuid.UUID, status string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE stations SET monitoring_status = $2 WHERE id = $1`, id, status)
	return err
}
