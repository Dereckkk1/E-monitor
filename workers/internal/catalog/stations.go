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

	"radiocheck/internal/geo"
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

type AgeRanges struct {
	Range18To24 float64 `json:"range18to24"`
	Range25To49 float64 `json:"range25to49"`
	Range50Plus float64 `json:"range50plus"`
}

type AudienceProfile struct {
	Gender         *Gender      `json:"gender,omitempty"`
	AgeRanges      *AgeRanges   `json:"ageRanges,omitempty"`
	AgeRangeLegado *string      `json:"ageRangeLegado,omitempty"`
	AgeRange       *string      `json:"ageRange,omitempty"`
	SocialClass    *SocialClass `json:"socialClass,omitempty"`
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

	// Contract descreve o vínculo da emissora com o cliente pelo qual a
	// listagem foi escopada. nil quando a listagem não é escopada por cliente
	// — o que cai de graça do LEFT JOIN: sem match, as colunas vêm NULL.
	Contract *StationContract `json:"contract,omitempty"`
}

// StationContract é o "por que esta emissora é minha": em quantas campanhas
// vigentes ela entra, se já está no ar e, quando ainda não está, quando começa.
type StationContract struct {
	Campaigns int `json:"campaigns"`
	// OnAir = existe campanha `ativa` usando a emissora hoje.
	OnAir bool `json:"on_air"`
	// StartsAt = início da campanha `programada` mais próxima. Só vem quando
	// não há nenhuma ativa — é o que a UI mostra como "a partir de DD/MM".
	StartsAt *time.Time `json:"starts_at,omitempty"`
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
	geo  *geo.Geocoder
}

func NewStations(pool *pgxpool.Pool) *Stations {
	return &Stations{pool: pool, geo: geo.Default()}
}

// ─── List (search + filter + pagination) ────────────────────────────────────

type ListInput struct {
	Q     string
	Band  string
	State string
	// City filtra pela cidade EXATA (accent-insensitive). É o que o clique numa
	// sugestão de cidade aplica. Passar o nome da cidade em Q traria de quebra
	// emissoras de outra cidade que tivessem esse nome no `name`.
	City string
	// StationID filtra uma emissora específica — o clique numa sugestão de
	// emissora. Nome não serve como chave: há homônimos entre praças.
	StationID *uuid.UUID
	// ContractedBy restringe às emissoras que estes clientes têm contratadas
	// AGORA (campanha `ativa` ou `programada`). Lista, e não id único, porque
	// usuário de agência tem carteira com vários clientes.
	//
	// É filtro que atravessa tenant: quem monta esse slice tem que ter passado
	// cada id por auth.ScopeAllows antes. Ver StationsHandler.List.
	ContractedBy []uuid.UUID
	// IDs resolve um conjunto FECHADO de emissoras de uma vez — quem já tem os
	// uuids na mão e só precisa dos rótulos (nome, logo, dial, praça). É o caso
	// do seletor de emissoras do /insights, que parte de
	// campaigns.target_stations.
	//
	// Existe porque as duas alternativas eram ruins: sem filtro, List devolve
	// as 20 primeiras por monitoring_status/pmm/nome — em prod (7,5 mil
	// emissoras) isso praticamente nunca inclui as da campanha, e o seletor
	// aparecia VAZIO sem nenhum erro. Subir o limit corrigiria a listagem e
	// criaria um problema de payload: o catálogo inteiro serializado passa de
	// 10 MB.
	//
	// Com IDs preenchido a paginação é ignorada — ver List.
	IDs   []uuid.UUID
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
	// Conjunto fechado por id: o caller quer as N emissoras que pediu, não uma
	// página delas. Truncar em 20 aqui devolveria um subconjunto em silêncio —
	// que é exatamente o modo de falha que este filtro veio corrigir.
	if len(in.IDs) > 0 {
		in.Limit = len(in.IDs)
		in.Page = 1
	}
	if in.Page <= 0 {
		in.Page = 1
	}
	offset := (in.Page - 1) * in.Limit

	// Busca multi-token: cada token precisa casar com ao menos um de name,
	// city, state, band ou dial — tokens diferentes podem casar com campos
	// diferentes. Ver buildTokenSearch em station_search.go.
	ts := buildTokenSearch(in.Q, 1)
	whereParts := ts.Where
	args := ts.Args
	n := ts.Next

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
	if in.City != "" {
		whereParts = append(whereParts, fmt.Sprintf("unaccent(COALESCE(city,'')) ILIKE unaccent($%d)", n))
		args = append(args, in.City)
		n++
	}
	if in.StationID != nil {
		whereParts = append(whereParts, fmt.Sprintf("id = $%d", n))
		args = append(args, *in.StationID)
		n++
	}
	if len(in.IDs) > 0 {
		whereParts = append(whereParts, fmt.Sprintf("id = ANY($%d::uuid[])", n))
		args = append(args, in.IDs)
		n++
	}

	// Emissoras contratadas pelo cliente. O JOIN com a CTE entra SEMPRE — com
	// a lista vazia a CTE não devolve linha nenhuma, as três colunas vêm NULL
	// e o campo `contract` some do JSON. É o que permite uma query só pros dois
	// modos, sem SQL dinâmico nem ramo de scan.
	contractedN := n
	args = append(args, in.ContractedBy)
	n++
	if len(in.ContractedBy) > 0 {
		whereParts = append(whereParts, "ct.station_id IS NOT NULL")
	}

	where := ""
	if len(whereParts) > 0 {
		where = "WHERE " + strings.Join(whereParts, " AND ")
	}

	args = append(args, in.Limit, offset)
	limitN, offsetN := n, n+1

	// Relevância só entra quando há busca. Sem Q a ordenação continua sendo
	// exatamente a de sempre — o wizard de campanha pré-carrega 10.000
	// emissoras contando com ela (ver comentário do cap de limit no handler).
	orderBy := stationOrderBy
	if ts.Score != "" {
		orderBy = fmt.Sprintf("(%s) DESC,%s", ts.Score, stationOrderBy)
	}

	// A CTE parte das CAMPANHAS (poucas) e cai na PK de stations. O sentido
	// inverso — varrer as ~7.500 emissoras testando `target_stations @> id` —
	// foi medido em 303ms contra 24ms deste.
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`
		WITH contracted AS (
			SELECT st AS station_id,
			       COUNT(*)::int                                          AS campaigns,
			       bool_or(c.status = 'ativa')                            AS on_air,
			       MIN(c.start_date) FILTER (WHERE c.status = 'programada') AS starts_at
			FROM campaigns c, LATERAL unnest(c.target_stations) st
			WHERE c.client_id = ANY($%d) AND c.status IN ('ativa','programada')
			GROUP BY st
		)
		SELECT %s, ct.campaigns, ct.on_air, ct.starts_at, COUNT(*) OVER() AS total_count
		FROM stations
		LEFT JOIN contracted ct ON ct.station_id = stations.id
		%s
		ORDER BY %s
		LIMIT $%d OFFSET $%d`,
		contractedN, stationSelectCols, where, orderBy, limitN, offsetN),
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
		var campaigns *int
		var onAir *bool
		var startsAt *time.Time
		if err := rows.Scan(
			&st.ID, &st.ShortID, &st.Name, &st.Band, &st.FrequencyMHz,
			&st.City, &st.State, &st.StreamURL,
			&st.LogoURL, &st.PMM, &st.Latitude, &st.Longitude,
			&st.MonitoringStatus, &st.LastHealthCheck, &st.HealthStatus,
			&st.ConsecutiveFailures, &st.CreatedAt, &st.UpdatedAt,
			&metaRaw, &campaigns, &onAir, &startsAt, &totalCount,
		); err != nil {
			return ListOutput{}, err
		}
		st.Meta = parseMeta(metaRaw)
		if campaigns != nil {
			c := StationContract{Campaigns: *campaigns, OnAir: onAir != nil && *onAir}
			// "a partir de" só faz sentido enquanto nada está no ar: com uma
			// campanha ativa a emissora já está entregando hoje.
			if !c.OnAir {
				c.StartsAt = startsAt
			}
			st.Contract = &c
		}
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
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("create station: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var lat, lng *float64
	if in.City != nil && in.State != nil {
		if glat, glng, ok := s.geo.Lookup(*in.City, *in.State); ok {
			lat, lng = &glat, &glng
		}
	}

	st, err := scanStationRow(tx.QueryRow(ctx, fmt.Sprintf(`
		INSERT INTO stations (name, band, frequency_mhz, city, state, stream_url, latitude, longitude)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING %s`, stationSelectCols),
		in.Name, in.Band, in.FrequencyMHz, in.City, in.State, in.StreamURL, lat, lng,
	).Scan)
	if err != nil {
		return nil, err
	}

	// Seed the station_thresholds row in the same transaction so the worker
	// has a place to record noise samples from the first ingestion window.
	// Without this row, RecordNoiseSample's UPDATE silently affects 0 rows
	// (the WHERE never matches), the calibration job never picks the station
	// up (it filters on noise_samples > 0), and min_hashes stays at the
	// permissive default of 5 forever — which causes false positives on
	// stations with noisier streams. Defaults here mirror the table DDL:
	// calibration_mode=true, min_hashes=5, empty noise_samples buffer.
	if _, err := tx.Exec(ctx, `
		INSERT INTO station_thresholds (station_id) VALUES ($1)
	`, st.ID); err != nil {
		return nil, fmt.Errorf("create station: seed thresholds: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("create station: commit: %w", err)
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

	// Re-deriva a coordenada da cidade+UF recebida. lat/lng ficam nil quando não
	// há match; o COALESCE abaixo então preserva o valor atual (não destrói dado
	// por causa de um lookup que falhou). Como não há caminho na API pra setar
	// coordenada à mão, re-derivar é idempotente: editar só o nome reenvia a
	// mesma cidade e produz a mesma coordenada.
	var lat, lng *float64
	if in.City != nil && in.State != nil {
		if glat, glng, ok := s.geo.Lookup(*in.City, *in.State); ok {
			lat, lng = &glat, &glng
		}
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
		  latitude      = COALESCE($11, latitude),
		  longitude     = COALESCE($12, longitude),
		  updated_at    = NOW()
		WHERE id = $1
		RETURNING %s`, stationSelectCols),
		id, in.Name, in.Band, in.FrequencyMHz, in.City, in.State,
		in.StreamURL, in.LogoURL, in.PMM, string(metaJSON), lat, lng,
	).Scan)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, pgx.ErrNoRows
		}
		return nil, err
	}
	return &st, nil
}

// UpdateStreamURL atualiza SOMENTE a stream_url da emissora (e updated_at).
// Existe separado do Update porque o Update reescreve todas as colunas — usar
// ele pra trocar só a URL zeraria city/state/frequency_mhz/logo_url/pmm/metadata
// quando o caller não reenvia tudo. A etapa Conexão do wizard troca a URL de
// forma cirúrgica, então usa este caminho. Ver
// docs/features/campaign-connection-step.md.
func (s *Stations) UpdateStreamURL(ctx context.Context, id uuid.UUID, streamURL string) (*Station, error) {
	st, err := scanStationRow(s.pool.QueryRow(ctx, fmt.Sprintf(`
		UPDATE stations SET
		  stream_url = $2,
		  updated_at = NOW()
		WHERE id = $1
		RETURNING %s`, stationSelectCols),
		id, streamURL,
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
