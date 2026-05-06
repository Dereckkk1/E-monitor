package catalog

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Commercial struct {
	ID                     uuid.UUID  `json:"id"`
	ShortID                int32      `json:"short_id"`
	CampaignID             uuid.UUID  `json:"campaign_id"`
	Title                  string     `json:"title"`
	CutLabel               *string    `json:"cut_label,omitempty"`
	DurationSeconds        float64    `json:"duration_seconds"`
	MasterStoragePath      string     `json:"master_storage_path"`
	MasterSHA256           string     `json:"master_sha256"`
	FingerprintStatus      string     `json:"fingerprint_status"`
	FingerprintGeneratedAt *time.Time `json:"fingerprint_generated_at,omitempty"`
	FingerprintHashCount   *int32     `json:"fingerprint_hash_count,omitempty"`
	CreatedAt              time.Time  `json:"created_at"`
	UpdatedAt              time.Time  `json:"updated_at"`
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

func (c *Commercials) Create(ctx context.Context, in CreateCommercialInput) (*Commercial, error) {
	var com Commercial
	err := c.pool.QueryRow(ctx, `
		INSERT INTO commercials (campaign_id, title, cut_label, duration_seconds,
		                         master_storage_path, master_sha256)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, short_id, campaign_id, title, cut_label, duration_seconds,
		          master_storage_path, master_sha256, fingerprint_status,
		          fingerprint_generated_at, fingerprint_hash_count, created_at, updated_at`,
		in.CampaignID, in.Title, in.CutLabel, in.DurationSeconds,
		in.MasterStoragePath, in.MasterSHA256,
	).Scan(&com.ID, &com.ShortID, &com.CampaignID, &com.Title, &com.CutLabel,
		&com.DurationSeconds, &com.MasterStoragePath, &com.MasterSHA256,
		&com.FingerprintStatus, &com.FingerprintGeneratedAt, &com.FingerprintHashCount,
		&com.CreatedAt, &com.UpdatedAt)
	return &com, err
}

func (c *Commercials) Get(ctx context.Context, id uuid.UUID) (*Commercial, error) {
	var com Commercial
	err := c.pool.QueryRow(ctx, `
		SELECT id, short_id, campaign_id, title, cut_label, duration_seconds,
		       master_storage_path, master_sha256, fingerprint_status,
		       fingerprint_generated_at, fingerprint_hash_count, created_at, updated_at
		FROM commercials WHERE id = $1`, id,
	).Scan(&com.ID, &com.ShortID, &com.CampaignID, &com.Title, &com.CutLabel,
		&com.DurationSeconds, &com.MasterStoragePath, &com.MasterSHA256,
		&com.FingerprintStatus, &com.FingerprintGeneratedAt, &com.FingerprintHashCount,
		&com.CreatedAt, &com.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &com, nil
}

func (c *Commercials) ListByCampaign(ctx context.Context, campaignID uuid.UUID) ([]Commercial, error) {
	rows, err := c.pool.Query(ctx, `
		SELECT id, short_id, campaign_id, title, cut_label, duration_seconds,
		       master_storage_path, master_sha256, fingerprint_status,
		       fingerprint_generated_at, fingerprint_hash_count, created_at, updated_at
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
			&com.CreatedAt, &com.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, com)
	}
	return out, rows.Err()
}

// ListReadyByCampaigns returns commercials with fingerprint_status='ready' for the given campaigns.
func (c *Commercials) ListReadyByCampaigns(ctx context.Context, campaignIDs []uuid.UUID) ([]Commercial, error) {
	if len(campaignIDs) == 0 {
		return nil, nil
	}
	rows, err := c.pool.Query(ctx, `
		SELECT id, short_id, campaign_id, title, cut_label, duration_seconds,
		       master_storage_path, master_sha256, fingerprint_status,
		       fingerprint_generated_at, fingerprint_hash_count, created_at, updated_at
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
			&com.CreatedAt, &com.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, com)
	}
	return out, rows.Err()
}
