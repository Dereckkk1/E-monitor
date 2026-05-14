package catalog

import (
	"context"
	"fmt"
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

// ListPaged returns campaigns filtered by competence (YYYY-MM, month-overlap
// semantics — same rule used by /detections) and free-text search across
// campaign name + client name. Empty competence skips the date filter; empty
// q skips the text filter.
//
// Returns (rows, totalCount). Ordering matches the unpaged List(): lifecycle
// status → programmed-soonest-first → start_date desc, so paging mirrors what
// the user sees in the canonical list.
func (c *Campaigns) ListPaged(ctx context.Context, q, competence string, page, pageSize int) ([]Campaign, int, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}

	// Build month bounds when competence is set. We hand both ends to the
	// query and the SQL uses them only when $2 (competence flag) is non-empty.
	var monthStart, monthEnd time.Time
	if competence != "" {
		t, err := time.Parse("2006-01", competence)
		if err != nil {
			return nil, 0, fmt.Errorf("invalid competence %q: %w", competence, err)
		}
		monthStart = time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
		monthEnd = monthStart.AddDate(0, 1, 0).Add(-time.Nanosecond)
	}

	// $1 = q, $2 = competence (non-empty marker), $3 = monthStart, $4 = monthEnd
	const where = `
		WHERE
		    ($2 = '' OR (c.start_date <= $4 AND c.end_date >= $3))
		    AND ($1 = '' OR unaccent(lower(
		        COALESCE(c.name,'') || ' ' || COALESCE(cl.name,'')
		    )) LIKE '%' || unaccent(lower($1)) || '%')
	`

	// Count: same WHERE, no LIMIT.
	var total int
	if err := c.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM campaigns c
		LEFT JOIN clients cl ON cl.id = c.client_id`+where,
		q, competence, monthStart, monthEnd,
	).Scan(&total); err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	rows, err := c.pool.Query(ctx, `
		SELECT c.id, c.client_id, c.name, c.start_date, c.end_date, c.status, c.target_stations,
		       c.created_at, c.updated_at
		FROM campaigns c
		LEFT JOIN clients cl ON cl.id = c.client_id`+where+`
		ORDER BY CASE c.status
		    WHEN 'ativa'      THEN 1
		    WHEN 'programada' THEN 2
		    WHEN 'concluida'  THEN 3
		    WHEN 'cancelada'  THEN 4
		    ELSE 5
		END,
		CASE WHEN c.status = 'programada' THEN c.start_date ELSE NULL END ASC NULLS LAST,
		c.start_date DESC
		LIMIT $5 OFFSET $6`,
		q, competence, monthStart, monthEnd, pageSize, offset,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var out []Campaign
	for rows.Next() {
		var camp Campaign
		if err := rows.Scan(&camp.ID, &camp.ClientID, &camp.Name, &camp.StartDate,
			&camp.EndDate, &camp.Status, &camp.TargetStations,
			&camp.CreatedAt, &camp.UpdatedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, camp)
	}
	return out, total, rows.Err()
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

// UpdateBasicInput é o subset editável depois que a campanha foi criada.
// client_id é imutável (Step 1 do wizard trava no edit mode — a biblioteca
// de materiais carregada pertence ao cliente original) e status é alterado
// só via lifecycle endpoints (Cancel / PromoteScheduledLifecycle).
type UpdateBasicInput struct {
	Name      string
	StartDate time.Time
	EndDate   time.Time
}

// UpdateBasic edita o trio (name, start_date, end_date) de uma campanha.
// Retorna pgx.ErrNoRows se o id não existir. A lifecycle não é tocada — uma
// campanha 'concluida' continua concluida mesmo que o end_date avance pra
// frente; a próxima rodada do PromoteScheduledLifecycle reverte se for o caso.
func (c *Campaigns) UpdateBasic(ctx context.Context, id uuid.UUID, in UpdateBasicInput) (*Campaign, error) {
	var camp Campaign
	err := c.pool.QueryRow(ctx, `
		UPDATE campaigns
		SET name = $2, start_date = $3, end_date = $4, updated_at = now()
		WHERE id = $1
		RETURNING id, client_id, name, start_date, end_date, status, target_stations,
		          created_at, updated_at`,
		id, in.Name, in.StartDate, in.EndDate,
	).Scan(&camp.ID, &camp.ClientID, &camp.Name, &camp.StartDate, &camp.EndDate,
		&camp.Status, &camp.TargetStations, &camp.CreatedAt, &camp.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &camp, nil
}

// CampaignFinancials carrega um agregado simples por campanha pra alimentar o
// badge de CPM na listagem. Calculado server-side pra evitar N fetches de
// pricing+daily-summary no frontend.
//
// Fórmulas (alinhadas com a especificação 2026-05-12):
//   - per_insertion: invested += unit_value × (in_slot + bonus)
//                    insertions += in_slot + bonus
//   - consolidated:  invested += consolidated_value (independente das plays)
//                    insertions += in_slot + bonus
//   - CPM = invested / insertions × 1000, calculado no caller (frontend)
//     pra ter precisão decimal.
type CampaignFinancials struct {
	CampaignID     uuid.UUID `json:"campaign_id"`
	TotalInvested  float64   `json:"total_invested"`
	TotalInsertions int      `json:"total_insertions"`
}

// FinancialsByCampaign retorna o agregado de TODAS as campanhas. Tabela
// pequena (~100 entradas no pior caso), uma query só.
func (c *Campaigns) FinancialsByCampaign(ctx context.Context) ([]CampaignFinancials, error) {
	const q = `
		WITH per_ins AS (
			-- Investimento e inserções no modo per_insertion: precisa do
			-- unit_value × (in_slot + bonus) somado por campanha.
			SELECT
				p.campaign_id,
				COALESCE(SUM(tp.unit_value * (s.in_slot + s.bonus)), 0)::float8 AS invested,
				COALESCE(SUM(s.in_slot + s.bonus), 0)::int                    AS insertions
			FROM campaign_station_pricing p
			JOIN campaign_station_type_pricing tp
				ON tp.campaign_id = p.campaign_id
			   AND tp.station_id  = p.station_id
			LEFT JOIN daily_play_summary s
				ON s.campaign_id = p.campaign_id
			   AND s.station_id  = p.station_id
			   AND s.type_id     = tp.type_id
			WHERE p.mode = 'per_insertion'
			GROUP BY p.campaign_id
		),
		consolidated_inv AS (
			-- Investimento consolidado: independente das plays, só somar o
			-- consolidated_value por campanha.
			SELECT
				p.campaign_id,
				COALESCE(SUM(p.consolidated_value), 0)::float8 AS invested
			FROM campaign_station_pricing p
			WHERE p.mode = 'consolidated'
			GROUP BY p.campaign_id
		),
		consolidated_ins AS (
			-- Inserções de emissoras em modo consolidado também entram no
			-- denominador do CPM (mesma definição "qtd inserções" pra ambos
			-- os modos).
			SELECT
				p.campaign_id,
				COALESCE(SUM(s.in_slot + s.bonus), 0)::int AS insertions
			FROM campaign_station_pricing p
			LEFT JOIN daily_play_summary s
				ON s.campaign_id = p.campaign_id
			   AND s.station_id  = p.station_id
			WHERE p.mode = 'consolidated'
			GROUP BY p.campaign_id
		)
		SELECT
			c.id,
			COALESCE(per_ins.invested, 0) + COALESCE(consolidated_inv.invested, 0) AS total_invested,
			COALESCE(per_ins.insertions, 0) + COALESCE(consolidated_ins.insertions, 0) AS total_insertions
		FROM campaigns c
		LEFT JOIN per_ins          ON per_ins.campaign_id          = c.id
		LEFT JOIN consolidated_inv ON consolidated_inv.campaign_id = c.id
		LEFT JOIN consolidated_ins ON consolidated_ins.campaign_id = c.id
	`
	rows, err := c.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("campaigns.FinancialsByCampaign: query: %w", err)
	}
	defer rows.Close()
	out := make([]CampaignFinancials, 0)
	for rows.Next() {
		var f CampaignFinancials
		if err := rows.Scan(&f.CampaignID, &f.TotalInvested, &f.TotalInsertions); err != nil {
			return nil, fmt.Errorf("campaigns.FinancialsByCampaign: scan: %w", err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
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
