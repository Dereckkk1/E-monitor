package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ─── Metadata types (mirrors E-radios broadcaster profile) ──────────────────

type Gender struct {
	Male   float64 `json:"male"`
	Female float64 `json:"female"`
}

type SocialClass struct {
	ClasseAB float64 `json:"classeAB"`
	ClasseC  float64 `json:"classeC"`
	ClasseDE float64 `json:"classeDE"`
}

type AudienceProfile struct {
	Gender      *Gender      `json:"gender,omitempty"`
	AgeRange    *string      `json:"ageRange,omitempty"`
	SocialClass *SocialClass `json:"socialClass,omitempty"`
}

type StationMeta struct {
	Categories      []string          `json:"categories,omitempty"`
	AudienceProfile *AudienceProfile  `json:"audience_profile,omitempty"`
	CoverageStates  []string          `json:"coverage_states,omitempty"`
	CoverageCities  []string          `json:"coverage_cities,omitempty"`
	TotalPopulation *int64            `json:"total_population,omitempty"`
	SocialMedia     map[string]string `json:"social_media,omitempty"`
	Website         *string           `json:"website,omitempty"`
	CommercialEmail *string           `json:"commercial_email,omitempty"`
	FoundationYear  *int              `json:"foundation_year,omitempty"`
	PowerWatts      *float64          `json:"power_watts,omitempty"`
	AntennaClass    *string           `json:"antenna_class,omitempty"`
	CompanyName     *string           `json:"company_name,omitempty"`
	FantasyName     *string           `json:"fantasy_name,omitempty"`
	CNPJ            *string           `json:"cnpj,omitempty"`
}

// ─── Station ────────────────────────────────────────────────────────────────

type Station struct {
	ID                  uuid.UUID    `json:"id"`
	ShortID             int32        `json:"short_id"`
	Name                string       `json:"name"`
	Band                string       `json:"band"`
	FrequencyMHz        *float64     `json:"frequency_mhz,omitempty"`
	City                *string      `json:"city,omitempty"`
	State               *string      `json:"state,omitempty"`
	StreamURL           string       `json:"stream_url"`
	LogoURL             *string      `json:"logo_url,omitempty"`
	PMM                 *float64     `json:"pmm,omitempty"`
	Latitude            *float64     `json:"latitude,omitempty"`
	Longitude           *float64     `json:"longitude,omitempty"`
	Meta                *StationMeta `json:"meta,omitempty"`
	MonitoringStatus    string       `json:"monitoring_status"`
	LastHealthCheck     *time.Time   `json:"last_health_check,omitempty"`
	HealthStatus        *string      `json:"health_status,omitempty"`
	ConsecutiveFailures int32        `json:"consecutive_failures"`
	CreatedAt           time.Time    `json:"created_at"`
	UpdatedAt           time.Time    `json:"updated_at"`
}

func parseMeta(raw *string) *StationMeta {
	if raw == nil || *raw == "" || *raw == "null" {
		return nil
	}
	var m StationMeta
	if json.Unmarshal([]byte(*raw), &m) != nil {
		return nil
	}
	return &m
}

// ─── Repository ─────────────────────────────────────────────────────────────

type Stations struct {
	pool *pgxpool.Pool
}

func NewStations(pool *pgxpool.Pool) *Stations {
	return &Stations{pool: pool}
}

// ─── List (search + filter + pagination) ────────────────────────────────────

type ListInput struct {
	Q     string
	Band  string
	State string
	Page  int
	Limit int
}

type ListOutput struct {
	Data  []Station `json:"data"`
	Total int64     `json:"total"`
	Page  int       `json:"page"`
	Limit int       `json:"limit"`
	Pages int       `json:"pages"`
}

const stationSelectCols = `
	id, short_id, name, band, frequency_mhz, city, state, stream_url,
	logo_url, pmm, latitude, longitude,
	monitoring_status, last_health_check, health_status, consecutive_failures,
	created_at, updated_at, metadata::text`

func scanStationRow(scan func(...any) error) (Station, error) {
	var st Station
	var metaRaw *string
	err := scan(
		&st.ID, &st.ShortID, &st.Name, &st.Band, &st.FrequencyMHz,
		&st.City, &st.State, &st.StreamURL,
		&st.LogoURL, &st.PMM, &st.Latitude, &st.Longitude,
		&st.MonitoringStatus, &st.LastHealthCheck, &st.HealthStatus,
		&st.ConsecutiveFailures, &st.CreatedAt, &st.UpdatedAt, &metaRaw,
	)
	if err != nil {
		return Station{}, err
	}
	st.Meta = parseMeta(metaRaw)
	return st, nil
}

func (s *Stations) List(ctx context.Context, in ListInput) (ListOutput, error) {
	if in.Limit <= 0 {
		in.Limit = 20
	}
	if in.Page <= 0 {
		in.Page = 1
	}
	offset := (in.Page - 1) * in.Limit

	var whereParts []string
	args := []any{}
	n := 1

	// Each token must match at least one of: name, city, state, band, frequency.
	// metadata is intentionally excluded — it contains coverage_cities/states
	// which made searches for a city return every station that *covers* it
	// rather than stations *located* in it (paridade com /marketplace do E-radios).
	for _, tok := range strings.Fields(in.Q) {
		whereParts = append(whereParts, fmt.Sprintf(
			`(name ILIKE '%%'||$%d||'%%' OR city ILIKE '%%'||$%d||'%%' OR state ILIKE '%%'||$%d||'%%' OR band ILIKE '%%'||$%d||'%%' OR COALESCE(frequency_mhz::text,'') ILIKE '%%'||$%d||'%%')`,
			n, n, n, n, n,
		))
		args = append(args, tok)
		n++
	}
	if in.Band != "" {
		whereParts = append(whereParts, fmt.Sprintf("band = $%d", n))
		args = append(args, in.Band)
		n++
	}
	if in.State != "" {
		whereParts = append(whereParts, fmt.Sprintf("state = $%d", n))
		args = append(args, in.State)
		n++
	}

	where := ""
	if len(whereParts) > 0 {
		where = "WHERE " + strings.Join(whereParts, " AND ")
	}

	args = append(args, in.Limit, offset)
	limitN, offsetN := n, n+1

	rows, err := s.pool.Query(ctx, fmt.Sprintf(`
		SELECT %s, COUNT(*) OVER() AS total_count
		FROM stations
		%s
		ORDER BY
		  CASE monitoring_status
		    WHEN 'active'      THEN 0
		    WHEN 'calibrating' THEN 1
		    WHEN 'paused'      THEN 2
		    ELSE 3
		  END,
		  pmm DESC NULLS LAST,
		  name
		LIMIT $%d OFFSET $%d`, stationSelectCols, where, limitN, offsetN),
		args...)
	if err != nil {
		return ListOutput{}, err
	}
	defer rows.Close()

	out := ListOutput{Page: in.Page, Limit: in.Limit, Data: []Station{}}

	for rows.Next() {
		var st Station
		var metaRaw *string
		var totalCount int64
		if err := rows.Scan(
			&st.ID, &st.ShortID, &st.Name, &st.Band, &st.FrequencyMHz,
			&st.City, &st.State, &st.StreamURL,
			&st.LogoURL, &st.PMM, &st.Latitude, &st.Longitude,
			&st.MonitoringStatus, &st.LastHealthCheck, &st.HealthStatus,
			&st.ConsecutiveFailures, &st.CreatedAt, &st.UpdatedAt,
			&metaRaw, &totalCount,
		); err != nil {
			return ListOutput{}, err
		}
		st.Meta = parseMeta(metaRaw)
		out.Total = totalCount
		out.Data = append(out.Data, st)
	}
	if err := rows.Err(); err != nil {
		return ListOutput{}, err
	}

	if in.Limit > 0 && out.Total > 0 {
		out.Pages = int((out.Total + int64(in.Limit) - 1) / int64(in.Limit))
	} else {
		out.Pages = 1
	}

	return out, nil
}

// ─── Get ────────────────────────────────────────────────────────────────────

func (s *Stations) Get(ctx context.Context, id uuid.UUID) (*Station, error) {
	st, err := scanStationRow(s.pool.QueryRow(ctx, fmt.Sprintf(`
		SELECT %s FROM stations WHERE id = $1`, stationSelectCols), id).Scan)
	if err != nil {
		return nil, err
	}
	return &st, nil
}

// ─── Create ─────────────────────────────────────────────────────────────────

type CreateStationInput struct {
	Name         string   `json:"name"`
	Band         string   `json:"band"`
	FrequencyMHz *float64 `json:"frequency_mhz,omitempty"`
	City         *string  `json:"city,omitempty"`
	State        *string  `json:"state,omitempty"`
	StreamURL    string   `json:"stream_url"`
}

func (s *Stations) Create(ctx context.Context, in CreateStationInput) (*Station, error) {
	st, err := scanStationRow(s.pool.QueryRow(ctx, fmt.Sprintf(`
		INSERT INTO stations (name, band, frequency_mhz, city, state, stream_url)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING %s`, stationSelectCols),
		in.Name, in.Band, in.FrequencyMHz, in.City, in.State, in.StreamURL,
	).Scan)
	if err != nil {
		return nil, err
	}
	return &st, nil
}

// ─── Update ─────────────────────────────────────────────────────────────────

type UpdateStationInput struct {
	Name         string       `json:"name"`
	Band         string       `json:"band"`
	FrequencyMHz *float64     `json:"frequency_mhz"`
	City         *string      `json:"city"`
	State        *string      `json:"state"`
	StreamURL    string       `json:"stream_url"`
	LogoURL      *string      `json:"logo_url"`
	PMM          *float64     `json:"pmm"`
	Meta         *StationMeta `json:"meta"`
}

func (s *Stations) Update(ctx context.Context, id uuid.UUID, in UpdateStationInput) (*Station, error) {
	metaJSON := []byte("{}")
	if in.Meta != nil {
		b, err := json.Marshal(in.Meta)
		if err != nil {
			return nil, fmt.Errorf("marshal meta: %w", err)
		}
		metaJSON = b
	}

	st, err := scanStationRow(s.pool.QueryRow(ctx, fmt.Sprintf(`
		UPDATE stations SET
		  name          = $2,
		  band          = $3,
		  frequency_mhz = $4,
		  city          = $5,
		  state         = $6,
		  stream_url    = $7,
		  logo_url      = $8,
		  pmm           = $9,
		  metadata      = $10::jsonb,
		  updated_at    = NOW()
		WHERE id = $1
		RETURNING %s`, stationSelectCols),
		id, in.Name, in.Band, in.FrequencyMHz, in.City, in.State,
		in.StreamURL, in.LogoURL, in.PMM, string(metaJSON),
	).Scan)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, pgx.ErrNoRows
		}
		return nil, err
	}
	return &st, nil
}

// ─── Status helpers (used by workers) ───────────────────────────────────────

func (s *Stations) UpdateMonitoringStatus(ctx context.Context, id uuid.UUID, status string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE stations SET monitoring_status = $2 WHERE id = $1`, id, status)
	return err
}

func (s *Stations) UpdateHealthCheck(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE stations SET last_health_check = now() WHERE id = $1`, id)
	return err
}

// AllForWorkers returns all stations without pagination (used internally by workers).
func (s *Stations) AllForWorkers(ctx context.Context) ([]Station, error) {
	out, err := s.List(ctx, ListInput{Page: 1, Limit: 10_000})
	if err != nil {
		return nil, err
	}
	return out.Data, nil
}

// ListAll kept for backward compatibility with monitoring worker.
func (s *Stations) ListAll(ctx context.Context) ([]Station, error) {
	return s.AllForWorkers(ctx)
}

// ListActive returns all stations with monitoring_status = 'active', ordered by name.
// Used by the stream-health API to build the health dashboard list.
func (s *Stations) ListActive(ctx context.Context) ([]Station, error) {
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`
		SELECT %s FROM stations
		WHERE monitoring_status = 'active'
		ORDER BY name`, stationSelectCols))
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

// GetThreshold returns the min_hashes threshold for the station from
// station_thresholds (fase2 calibration §9.4). Returns the default of 5
// if no threshold row exists yet.
func (s *Stations) GetThreshold(ctx context.Context, stationID uuid.UUID) (int, error) {
	var minHashes int
	err := s.pool.QueryRow(ctx,
		`SELECT min_hashes FROM station_thresholds WHERE station_id = $1`,
		stationID,
	).Scan(&minHashes)
	if errors.Is(err, pgx.ErrNoRows) {
		return 5, nil
	}
	return minHashes, err
}

// CalibrationStatus holds calibration state for a station (§9.4 fase2).
type CalibrationStatus struct {
	CalibrationMode bool `json:"calibration_mode"`
	MinHashes       int  `json:"min_hashes"`
	DaysElapsed     int  `json:"days_elapsed"`
}

// GetCalibrationStatus returns the full calibration state for a station.
// If no threshold row exists the station is treated as still in calibration mode.
func (s *Stations) GetCalibrationStatus(ctx context.Context, stationID uuid.UUID) (CalibrationStatus, error) {
	var mode bool
	var minHashes int
	var startedAt time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT calibration_mode, min_hashes, COALESCE(calibration_started_at, NOW())
		FROM station_thresholds WHERE station_id = $1
	`, stationID).Scan(&mode, &minHashes, &startedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return CalibrationStatus{CalibrationMode: true, MinHashes: 5, DaysElapsed: 0}, nil
	}
	if err != nil {
		return CalibrationStatus{}, err
	}
	return CalibrationStatus{
		CalibrationMode: mode,
		MinHashes:       minHashes,
		DaysElapsed:     int(time.Since(startedAt).Hours() / 24),
	}, nil
}
