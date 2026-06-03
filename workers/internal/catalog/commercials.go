package catalog

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrCommercialHasDetections is returned by Delete when the commercial cannot
// be removed because it already has detection history.
var ErrCommercialHasDetections = errors.New("commercial has detection history; cannot delete")

type Commercial struct {
	ID                     uuid.UUID   `json:"id"`
	ShortID                int32       `json:"short_id"`
	CampaignID             uuid.UUID   `json:"campaign_id"`
	Title                  string      `json:"title"`
	CutLabel               *string     `json:"cut_label,omitempty"`
	DurationSeconds        float64     `json:"duration_seconds"`
	MasterStoragePath      string      `json:"master_storage_path"`
	MasterSHA256           string      `json:"master_sha256"`
	FingerprintStatus      string      `json:"fingerprint_status"`
	FingerprintGeneratedAt *time.Time  `json:"fingerprint_generated_at,omitempty"`
	FingerprintHashCount   *int32      `json:"fingerprint_hash_count,omitempty"`
	TargetStations         []uuid.UUID `json:"target_stations"`
	CreatedAt              time.Time   `json:"created_at"`
	UpdatedAt              time.Time   `json:"updated_at"`
}

type Commercials struct {
	pool *pgxpool.Pool
}

func NewCommercials(pool *pgxpool.Pool) *Commercials {
	return &Commercials{pool: pool}
}

type CreateCommercialInput struct {
	CampaignID        uuid.UUID
	Title             string
	CutLabel          *string
	DurationSeconds   float64
	MasterStoragePath string
	MasterSHA256      string
}

const commercialColumns = `id, short_id, campaign_id, title, cut_label, duration_seconds,
       master_storage_path, master_sha256, fingerprint_status,
       fingerprint_generated_at, fingerprint_hash_count, target_stations, created_at, updated_at`

func scanCommercial(row interface {
	Scan(...any) error
}, c *Commercial) error {
	return row.Scan(&c.ID, &c.ShortID, &c.CampaignID, &c.Title, &c.CutLabel,
		&c.DurationSeconds, &c.MasterStoragePath, &c.MasterSHA256,
		&c.FingerprintStatus, &c.FingerprintGeneratedAt, &c.FingerprintHashCount,
		&c.TargetStations, &c.CreatedAt, &c.UpdatedAt)
}

func (c *Commercials) Create(ctx context.Context, in CreateCommercialInput) (*Commercial, error) {
	var com Commercial
	err := c.pool.QueryRow(ctx, `
		INSERT INTO commercials (campaign_id, title, cut_label, duration_seconds,
		                         master_storage_path, master_sha256)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING `+commercialColumns,
		in.CampaignID, in.Title, in.CutLabel, in.DurationSeconds,
		in.MasterStoragePath, in.MasterSHA256,
	).Scan(&com.ID, &com.ShortID, &com.CampaignID, &com.Title, &com.CutLabel,
		&com.DurationSeconds, &com.MasterStoragePath, &com.MasterSHA256,
		&com.FingerprintStatus, &com.FingerprintGeneratedAt, &com.FingerprintHashCount,
		&com.TargetStations, &com.CreatedAt, &com.UpdatedAt)
	return &com, err
}

func (c *Commercials) Get(ctx context.Context, id uuid.UUID) (*Commercial, error) {
	var com Commercial
	err := c.pool.QueryRow(ctx, `
		SELECT `+commercialColumns+`
		FROM commercials WHERE id = $1`, id,
	).Scan(&com.ID, &com.ShortID, &com.CampaignID, &com.Title, &com.CutLabel,
		&com.DurationSeconds, &com.MasterStoragePath, &com.MasterSHA256,
		&com.FingerprintStatus, &com.FingerprintGeneratedAt, &com.FingerprintHashCount,
		&com.TargetStations, &com.CreatedAt, &com.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &com, nil
}

func (c *Commercials) ListByCampaign(ctx context.Context, campaignID uuid.UUID) ([]Commercial, error) {
	rows, err := c.pool.Query(ctx, `
		SELECT `+commercialColumns+`
		FROM commercials WHERE campaign_id = $1 ORDER BY created_at ASC`, campaignID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Commercial
	for rows.Next() {
		var com Commercial
		if err := rows.Scan(&com.ID, &com.ShortID, &com.CampaignID, &com.Title,
			&com.CutLabel, &com.DurationSeconds, &com.MasterStoragePath, &com.MasterSHA256,
			&com.FingerprintStatus, &com.FingerprintGeneratedAt, &com.FingerprintHashCount,
			&com.TargetStations, &com.CreatedAt, &com.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, com)
	}
	return out, rows.Err()
}

// ListReadyByCampaigns returns commercials with fingerprint_status='ready' for the given campaigns.
// Used for fingerprint index loading — returns ALL ready commercials regardless of target_stations.
func (c *Commercials) ListReadyByCampaigns(ctx context.Context, campaignIDs []uuid.UUID) ([]Commercial, error) {
	if len(campaignIDs) == 0 {
		return nil, nil
	}
	rows, err := c.pool.Query(ctx, `
		SELECT `+commercialColumns+`
		FROM commercials
		WHERE campaign_id = ANY($1) AND fingerprint_status = 'ready'`, campaignIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Commercial
	for rows.Next() {
		var com Commercial
		if err := rows.Scan(&com.ID, &com.ShortID, &com.CampaignID, &com.Title,
			&com.CutLabel, &com.DurationSeconds, &com.MasterStoragePath, &com.MasterSHA256,
			&com.FingerprintStatus, &com.FingerprintGeneratedAt, &com.FingerprintHashCount,
			&com.TargetStations, &com.CreatedAt, &com.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, com)
	}
	return out, rows.Err()
}

// ListReadyByCampaignsForStation returns ready commercials for the given campaigns that
// explicitly target the given station. A commercial with an empty target_stations
// is treated as inactive (runs on no station) — operators must explicitly link the
// commercial to at least one station for it to be detected.
//
// Backfilled commercials (id present in materials) are excluded — those load via
// the materials path (campaign_materials is authoritative), keeping each short_id
// in the worker's allow-list exactly once. This path now serves only pure-legacy
// commercials with no material row.
func (c *Commercials) ListReadyByCampaignsForStation(ctx context.Context, campaignIDs []uuid.UUID, stationID uuid.UUID) ([]Commercial, error) {
	if len(campaignIDs) == 0 {
		return nil, nil
	}
	rows, err := c.pool.Query(ctx, `
		SELECT `+commercialColumns+`
		FROM commercials
		WHERE campaign_id = ANY($1)
		  AND fingerprint_status = 'ready'
		  AND $2 = ANY(target_stations)
		  AND id NOT IN (SELECT id FROM materials)`,
		campaignIDs, stationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Commercial
	for rows.Next() {
		var com Commercial
		if err := rows.Scan(&com.ID, &com.ShortID, &com.CampaignID, &com.Title,
			&com.CutLabel, &com.DurationSeconds, &com.MasterStoragePath, &com.MasterSHA256,
			&com.FingerprintStatus, &com.FingerprintGeneratedAt, &com.FingerprintHashCount,
			&com.TargetStations, &com.CreatedAt, &com.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, com)
	}
	return out, rows.Err()
}

// CommercialDedupInfo holds the per-detection metadata that the supervisor
// needs to apply §18.2.2 version disambiguation: the owning client, the
// commercial duration (used to pick the longer cut on conflict) and the
// per-campaign dedup window override (defaults to 5s).
type CommercialDedupInfo struct {
	CommercialID       uuid.UUID
	ClientID           uuid.UUID
	DurationSeconds    int
	DedupWindowSeconds int
}

// LookupForDedup returns the disambiguation context for the given short id
// (joining commercials → campaigns to get client_id and the per-campaign
// dedup window). When the short id is not found among commercials, the
// lookup falls back to materials (linked via campaign_materials to a
// currently-active campaign); the returned CommercialID is then a material
// UUID — the field is polymorphic, mirroring the unified short_id sequence
// shared between the two tables. Returns pgx.ErrNoRows when the short id
// is unknown or its fingerprint is not yet ready in either table.
func (c *Commercials) LookupForDedup(ctx context.Context, shortID int32) (CommercialDedupInfo, error) {
	var info CommercialDedupInfo
	var dur float64
	err := c.pool.QueryRow(ctx, `
		SELECT c.id, ca.client_id, c.duration_seconds, COALESCE(ca.dedup_window_seconds, 5)
		FROM commercials c
		JOIN campaigns ca ON ca.id = c.campaign_id
		WHERE c.short_id = $1 AND c.fingerprint_status = 'ready'
		LIMIT 1`,
		shortID,
	).Scan(&info.CommercialID, &info.ClientID, &dur, &info.DedupWindowSeconds)
	if errors.Is(err, pgx.ErrNoRows) {
		// Maybe this short_id is a material — try the materials path.
		// Materials aren't single-valued by campaign; pick the most recently
		// added link to a currently-active campaign (consistent with the
		// attribution rule used by evidence/service.go in Phase 7).
		err = c.pool.QueryRow(ctx, `
			SELECT m.id,
			       ca.client_id,
			       m.duration_seconds,
			       COALESCE(ca.dedup_window_seconds, 5)
			FROM materials m
			JOIN campaign_materials cm ON cm.material_id = m.id
			JOIN campaigns ca           ON ca.id = cm.campaign_id
			WHERE m.short_id = $1
			  AND m.fingerprint_status = 'ready'
			  AND ca.status IN ('programada','ativa')
			ORDER BY cm.added_at DESC
			LIMIT 1`,
			shortID,
		).Scan(&info.CommercialID, &info.ClientID, &dur, &info.DedupWindowSeconds)
	}
	if err != nil {
		return CommercialDedupInfo{}, err
	}
	// Round half-up; durations in DB are NUMERIC and may store fractional seconds.
	info.DurationSeconds = int(dur + 0.5)
	return info, nil
}

// UpdateStations sets the target_stations for a commercial.
// An empty slice means the commercial is inactive (runs on no station).
func (c *Commercials) UpdateStations(ctx context.Context, id uuid.UUID, stationIDs []uuid.UUID) error {
	if stationIDs == nil {
		stationIDs = []uuid.UUID{}
	}
	_, err := c.pool.Exec(ctx,
		`UPDATE commercials SET target_stations = $2, updated_at = now() WHERE id = $1`,
		id, stationIDs)
	return err
}

// Delete removes a commercial along with its fingerprint hashes. Returns the
// master_storage_path so callers can remove the file from disk and the
// campaign_id so the supervisor can reload affected workers. Returns
// ErrCommercialHasDetections if the commercial already has detection history,
// in which case removal is refused to preserve audit integrity.
func (c *Commercials) Delete(ctx context.Context, id uuid.UUID) (campaignID uuid.UUID, masterPath string, err error) {
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, "", fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var detectionCount int
	if err = tx.QueryRow(ctx,
		`SELECT COUNT(*) FROM detections WHERE commercial_id = $1`, id,
	).Scan(&detectionCount); err != nil {
		return uuid.Nil, "", fmt.Errorf("count detections: %w", err)
	}
	if detectionCount > 0 {
		return uuid.Nil, "", ErrCommercialHasDetections
	}

	if err = tx.QueryRow(ctx,
		`SELECT campaign_id, master_storage_path FROM commercials WHERE id = $1`, id,
	).Scan(&campaignID, &masterPath); err != nil {
		return uuid.Nil, "", err
	}

	if _, err = tx.Exec(ctx, `DELETE FROM fingerprint_hashes WHERE commercial_id = $1`, id); err != nil {
		return uuid.Nil, "", fmt.Errorf("delete hashes: %w", err)
	}
	if _, err = tx.Exec(ctx, `DELETE FROM commercials WHERE id = $1`, id); err != nil {
		return uuid.Nil, "", fmt.Errorf("delete commercial: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return uuid.Nil, "", fmt.Errorf("commit: %w", err)
	}
	return campaignID, masterPath, nil
}
