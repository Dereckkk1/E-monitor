package catalog

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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
	return c.ListFiltered(ctx, nil)
}

// ListFiltered returns campaigns filtered by status. If statuses is nil/empty,
// all campaigns are returned. Ordering follows the lifecycle UX rule:
// ativas → programadas (próximas a entrar) → concluidas/canceladas (histórico).
func (c *Campaigns) ListFiltered(ctx context.Context, statuses []string) ([]Campaign, error) {
	const baseQuery = `
		SELECT id, client_id, name, start_date, end_date, status, target_stations,
		       created_at, updated_at
		FROM campaigns
	`
	const orderClause = `
		ORDER BY CASE status
		    WHEN 'ativa'      THEN 1
		    WHEN 'programada' THEN 2
		    WHEN 'concluida'  THEN 3
		    WHEN 'cancelada'  THEN 4
		    ELSE 5
		END,
		CASE
		    WHEN status = 'programada' THEN start_date
		    ELSE NULL
		END ASC NULLS LAST,
		start_date DESC
	`
	var (
		rows pgx.Rows
		err  error
	)
	if len(statuses) == 0 {
		rows, err = c.pool.Query(ctx, baseQuery+orderClause)
	} else {
		rows, err = c.pool.Query(ctx, baseQuery+` WHERE status = ANY($1) `+orderClause, statuses)
	}
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
		`UPDATE campaigns SET status = $2, updated_at = now() WHERE id = $1`, id, status)
	return err
}

// CancelCampaign transitions a campaign from programada/ativa to cancelada.
// Returns (true, prevStatus, nil) when the cancellation succeeded,
// (false, currentStatus, nil) when the campaign is already in a terminal
// state (concluida/cancelada) — the caller should treat this as 409 Conflict.
// (false, "", pgx.ErrNoRows) when the id does not exist.
func (c *Campaigns) CancelCampaign(ctx context.Context, id uuid.UUID) (bool, string, error) {
	// Use a CTE to capture the OLD status atomically while applying the UPDATE.
	var prev string
	var changed bool
	err := c.pool.QueryRow(ctx, `
		WITH old AS (
		    SELECT id, status FROM campaigns WHERE id = $1 FOR UPDATE
		),
		updated AS (
		    UPDATE campaigns SET status = 'cancelada', updated_at = now()
		     WHERE id = $1 AND status IN ('programada','ativa')
		    RETURNING id
		)
		SELECT old.status, EXISTS(SELECT 1 FROM updated) AS changed
		  FROM old`, id).Scan(&prev, &changed)
	if err != nil {
		return false, "", err
	}
	return changed, prev, nil
}

// CountByStatus returns the number of campaigns grouped by status.
// Used by the lifecycle scheduler to publish gauge metrics.
func (c *Campaigns) CountByStatus(ctx context.Context) (map[string]int, error) {
	rows, err := c.pool.Query(ctx, `
		SELECT status, COUNT(*) FROM campaigns GROUP BY status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]int)
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return nil, err
		}
		out[status] = n
	}
	return out, rows.Err()
}

// PromoteScheduledLifecycle runs both lifecycle transitions in a single TX:
//   - programada → ativa  when start_date <= today (America/Sao_Paulo)
//   - ativa     → concluida when end_date < today (America/Sao_Paulo)
//
// Returns the IDs that transitioned for each direction. Idempotent: if no rows
// match, returns empty slices and nil error.
func (c *Campaigns) PromoteScheduledLifecycle(ctx context.Context) (activated []uuid.UUID, ended []uuid.UUID, err error) {
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback(ctx)

	// programada → ativa
	rows1, err := tx.Query(ctx, `
		UPDATE campaigns
		   SET status = 'ativa', updated_at = now()
		 WHERE status = 'programada'
		   AND start_date <= (now() AT TIME ZONE 'America/Sao_Paulo')::date
		RETURNING id`)
	if err != nil {
		return nil, nil, err
	}
	for rows1.Next() {
		var id uuid.UUID
		if err := rows1.Scan(&id); err != nil {
			rows1.Close()
			return nil, nil, err
		}
		activated = append(activated, id)
	}
	rows1.Close()
	if err := rows1.Err(); err != nil {
		return nil, nil, err
	}

	// ativa → concluida
	rows2, err := tx.Query(ctx, `
		UPDATE campaigns
		   SET status = 'concluida', updated_at = now()
		 WHERE status = 'ativa'
		   AND end_date < (now() AT TIME ZONE 'America/Sao_Paulo')::date
		RETURNING id`)
	if err != nil {
		return nil, nil, err
	}
	for rows2.Next() {
		var id uuid.UUID
		if err := rows2.Scan(&id); err != nil {
			rows2.Close()
			return nil, nil, err
		}
		ended = append(ended, id)
	}
	rows2.Close()
	if err := rows2.Err(); err != nil {
		return nil, nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, nil, err
	}
	return activated, ended, nil
}

// UpdateTargetStations replaces the target_stations list for a campaign.
func (c *Campaigns) UpdateTargetStations(ctx context.Context, id uuid.UUID, stationIDs []uuid.UUID) error {
	if stationIDs == nil {
		stationIDs = []uuid.UUID{}
	}
	_, err := c.pool.Exec(ctx,
		`UPDATE campaigns SET target_stations = $2, updated_at = now() WHERE id = $1`,
		id, stationIDs)
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
		WHERE status = 'ativa' AND $1 = ANY(target_stations)`, stationID)
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
