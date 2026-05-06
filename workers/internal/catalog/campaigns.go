package catalog

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Campaign struct {
	ID             uuid.UUID   `json:"id"`
	ClientID       uuid.UUID   `json:"client_id"`
	Name           string      `json:"name"`
	StartDate      time.Time   `json:"start_date"`
	EndDate        time.Time   `json:"end_date"`
	Status         string      `json:"status"`
	TargetStations []uuid.UUID `json:"target_stations"`
	CreatedAt      time.Time   `json:"created_at"`
	UpdatedAt      time.Time   `json:"updated_at"`
}

type Campaigns struct {
	pool *pgxpool.Pool
}

func NewCampaigns(pool *pgxpool.Pool) *Campaigns {
	return &Campaigns{pool: pool}
}

type CreateCampaignInput struct {
	ClientID       uuid.UUID   `json:"client_id"`
	Name           string      `json:"name"`
	StartDate      time.Time   `json:"start_date"`
	EndDate        time.Time   `json:"end_date"`
	TargetStations []uuid.UUID `json:"target_stations"`
}

func (c *Campaigns) Create(ctx context.Context, in CreateCampaignInput) (*Campaign, error) {
	var camp Campaign
	err := c.pool.QueryRow(ctx, `
		INSERT INTO campaigns (client_id, name, start_date, end_date, target_stations)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, client_id, name, start_date, end_date, status, target_stations,
		          created_at, updated_at`,
		in.ClientID, in.Name, in.StartDate, in.EndDate, in.TargetStations,
	).Scan(&camp.ID, &camp.ClientID, &camp.Name, &camp.StartDate, &camp.EndDate,
		&camp.Status, &camp.TargetStations, &camp.CreatedAt, &camp.UpdatedAt)
	return &camp, err
}

func (c *Campaigns) List(ctx context.Context) ([]Campaign, error) {
	rows, err := c.pool.Query(ctx, `
		SELECT id, client_id, name, start_date, end_date, status, target_stations,
		       created_at, updated_at
		FROM campaigns ORDER BY start_date DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Campaign
	for rows.Next() {
		var camp Campaign
		if err := rows.Scan(&camp.ID, &camp.ClientID, &camp.Name, &camp.StartDate,
			&camp.EndDate, &camp.Status, &camp.TargetStations,
			&camp.CreatedAt, &camp.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, camp)
	}
	return out, rows.Err()
}

func (c *Campaigns) Get(ctx context.Context, id uuid.UUID) (*Campaign, error) {
	var camp Campaign
	err := c.pool.QueryRow(ctx, `
		SELECT id, client_id, name, start_date, end_date, status, target_stations,
		       created_at, updated_at
		FROM campaigns WHERE id = $1`, id,
	).Scan(&camp.ID, &camp.ClientID, &camp.Name, &camp.StartDate, &camp.EndDate,
		&camp.Status, &camp.TargetStations, &camp.CreatedAt, &camp.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &camp, nil
}

func (c *Campaigns) UpdateStatus(ctx context.Context, id uuid.UUID, status string) error {
	_, err := c.pool.Exec(ctx,
		`UPDATE campaigns SET status = $2 WHERE id = $1`, id, status)
	return err
}

// Delete removes a campaign and all its associated data (detections, fingerprint hashes, commercials).
func (c *Campaigns) Delete(ctx context.Context, id uuid.UUID) error {
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	steps := []string{
		`DELETE FROM detections WHERE campaign_id = $1`,
		`DELETE FROM fingerprint_hashes WHERE commercial_id IN (SELECT id FROM commercials WHERE campaign_id = $1)`,
		`DELETE FROM commercials WHERE campaign_id = $1`,
		`DELETE FROM campaigns WHERE id = $1`,
	}
	for _, q := range steps {
		if _, err := tx.Exec(ctx, q, id); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// ActiveCampaignsForStation returns IDs of all currently-active campaigns that include the given station.
func (c *Campaigns) ActiveCampaignsForStation(ctx context.Context, stationID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := c.pool.Query(ctx, `
		SELECT id FROM campaigns
		WHERE status = 'active' AND $1 = ANY(target_stations)`, stationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
