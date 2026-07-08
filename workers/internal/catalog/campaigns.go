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

// ErrCampaignHasForeignProjections is returned by Delete when the campaign owns
// physical detections (detections.campaign_id) that ALSO carry fan-out
// projections belonging to OTHER campaigns (MULTI_ATTRIBUTION). A hard delete
// would DELETE those detections and CASCADE-wipe the sibling campaigns'
// detection_campaigns rows — irreversible loss of another campaign's history
// (audit 2026-07-02 C1). Callers map this to 409 Conflict.
var ErrCampaignHasForeignProjections = errors.New("campaign owns detections projected to other campaigns")

type Campaign struct {
	ID             uuid.UUID   `json:"id"`
	ClientID       uuid.UUID   `json:"client_id"`
	Name           string      `json:"name"`
	StartDate      time.Time   `json:"start_date"`
	EndDate        time.Time   `json:"end_date"`
	Status         string      `json:"status"`
	TargetStations []uuid.UUID `json:"target_stations"`
	// FixedCPM, quando setado, sobrescreve o CPM calculado dinamicamente nas
	// telas de exibição (/campaigns, /insights, dashboard). NULL = usa o
	// cálculo dinâmico (executado / impactos × 1000).
	FixedCPM  *float64  `json:"fixed_cpm"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	// MaterialCount só é populado pelo ListPaged (não pelas outras queries —
	// ficam em zero). Usado pela UI pra mostrar chip "sem material".
	MaterialCount int `json:"material_count"`
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
	// target_stations é NOT NULL DEFAULT '{}'. Inserir nil vira NULL e viola a
	// constraint — coalescemos pra array vazio (mesma semântica do default da
	// coluna) pra ser nil-safe.
	targetStations := in.TargetStations
	if targetStations == nil {
		targetStations = []uuid.UUID{}
	}
	var camp Campaign
	err := c.pool.QueryRow(ctx, `
		INSERT INTO campaigns (client_id, name, start_date, end_date, target_stations)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, client_id, name, start_date, end_date, status, target_stations,
		          fixed_cpm, created_at, updated_at`,
		in.ClientID, in.Name, in.StartDate, in.EndDate, targetStations,
	).Scan(&camp.ID, &camp.ClientID, &camp.Name, &camp.StartDate, &camp.EndDate,
		&camp.Status, &camp.TargetStations, &camp.FixedCPM, &camp.CreatedAt, &camp.UpdatedAt)
	return &camp, err
}

func (c *Campaigns) List(ctx context.Context) ([]Campaign, error) {
	return c.ListFiltered(ctx, nil, nil)
}

// ListPaged returns campaigns filtered by competence (YYYY-MM, month-overlap
// semantics — same rule used by /detections) and free-text search across
// campaign name + client name. Empty competence skips the date filter; empty
// q skips the text filter. clientID, when non-nil, restricts results to that
// client (used when the requester is a viewer with a JWT client scope).
//
// Returns (rows, totalCount). Ordering matches the unpaged List(): lifecycle
// status → programmed-soonest-first → start_date desc, so paging mirrors what
// the user sees in the canonical list.
func (c *Campaigns) ListPaged(ctx context.Context, q, competence string, clientID *uuid.UUID, campaignID *uuid.UUID, page, pageSize int) ([]Campaign, int, error) {
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

	// $1 = q, $2 = competence flag, $3 = monthStart, $4 = monthEnd,
	// $5 = clientID, $6 = campaignID (admin deep-link)
	const where = `
		WHERE
		    ($2 = '' OR (c.start_date <= $4 AND c.end_date >= $3))
		    AND ($1 = '' OR unaccent(lower(
		        COALESCE(c.name,'') || ' ' || COALESCE(cl.name,'')
		    )) LIKE '%' || unaccent(lower($1)) || '%')
		    AND ($5::uuid IS NULL OR c.client_id = $5)
		    AND ($6::uuid IS NULL OR c.id = $6)
	`

	// Count: same WHERE, no LIMIT.
	var total int
	if err := c.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM campaigns c
		LEFT JOIN clients cl ON cl.id = c.client_id`+where,
		q, competence, monthStart, monthEnd, clientID, campaignID,
	).Scan(&total); err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	rows, err := c.pool.Query(ctx, `
		SELECT c.id, c.client_id, c.name, c.start_date, c.end_date, c.status, c.target_stations,
		       c.fixed_cpm, c.created_at, c.updated_at,
		       COALESCE((
		         SELECT COUNT(*)::int
		         FROM campaign_materials cm
		         WHERE cm.campaign_id = c.id
		       ), 0) AS material_count
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
		LIMIT $7 OFFSET $8`,
		q, competence, monthStart, monthEnd, clientID, campaignID, pageSize, offset,
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
			&camp.FixedCPM, &camp.CreatedAt, &camp.UpdatedAt, &camp.MaterialCount); err != nil {
			return nil, 0, err
		}
		out = append(out, camp)
	}
	return out, total, rows.Err()
}

// ListFiltered returns campaigns filtered by status and/or client. If statuses
// is nil/empty, all lifecycle states are returned. clientID, when non-nil,
// restricts results to that client (viewer JWT scope). Ordering follows the
// lifecycle UX rule: ativas → programadas (próximas a entrar) → concluidas/canceladas.
func (c *Campaigns) ListFiltered(ctx context.Context, statuses []string, clientID *uuid.UUID) ([]Campaign, error) {
	const baseQuery = `
		SELECT id, client_id, name, start_date, end_date, status, target_stations,
		       fixed_cpm, created_at, updated_at
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
	switch {
	case len(statuses) == 0 && clientID == nil:
		rows, err = c.pool.Query(ctx, baseQuery+orderClause)
	case len(statuses) == 0:
		rows, err = c.pool.Query(ctx, baseQuery+` WHERE client_id = $1 `+orderClause, clientID)
	case clientID == nil:
		rows, err = c.pool.Query(ctx, baseQuery+` WHERE status = ANY($1) `+orderClause, statuses)
	default:
		rows, err = c.pool.Query(ctx, baseQuery+` WHERE status = ANY($1) AND client_id = $2 `+orderClause, statuses, clientID)
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
			&camp.FixedCPM, &camp.CreatedAt, &camp.UpdatedAt); err != nil {
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
		       fixed_cpm, created_at, updated_at
		FROM campaigns WHERE id = $1`, id,
	).Scan(&camp.ID, &camp.ClientID, &camp.Name, &camp.StartDate, &camp.EndDate,
		&camp.Status, &camp.TargetStations, &camp.FixedCPM, &camp.CreatedAt, &camp.UpdatedAt)
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
		    UPDATE campaigns SET status = 'cancelada', updated_at = now(), cancelled_at = now()
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

// PromoteScheduledLifecycle runs the lifecycle transitions in a single TX:
//   - concluida → ativa/programada  when end_date >= today (RECOVERY, see below)
//   - programada → ativa            when start_date <= today (America/Sao_Paulo)
//   - ativa     → concluida         when end_date < today (America/Sao_Paulo)
//
// Returns the IDs that became 'ativa' (in `activated`, including recovered ones,
// so the scheduler starts their workers) and the IDs that became 'concluida'
// (in `ended`). Idempotent: if no rows match, returns empty slices and nil error.
func (c *Campaigns) PromoteScheduledLifecycle(ctx context.Context) (activated []uuid.UUID, ended []uuid.UUID, err error) {
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback(ctx)

	// RECOVERY: concluida → ativa/programada when the window is STILL open
	// (end_date >= today). Self-heals campaigns wrongly stuck in 'concluida' —
	// the canonical cause is an operator extending end_date AFTER the campaign
	// concluded: UpdateBasic does not touch status, and there is no other path
	// back to ativa (incident 2026-06-05, campaign "200 (MRA) TINTAS RENNER").
	// Legitimately concluded campaigns (end_date < today) are left untouched.
	// Recovered-to-'ativa' ids are appended to `activated` so the scheduler
	// starts their workers, exactly like a programada→ativa transition. This
	// must run BEFORE the two steps below; the status filters keep them from
	// re-processing the rows it just moved.
	rows0, err := tx.Query(ctx, `
		UPDATE campaigns
		   SET status = CASE
		         WHEN start_date <= (now() AT TIME ZONE 'America/Sao_Paulo')::date THEN 'ativa'
		         ELSE 'programada'
		       END,
		       updated_at = now()
		 WHERE status = 'concluida'
		   AND end_date >= (now() AT TIME ZONE 'America/Sao_Paulo')::date
		RETURNING id, status`)
	if err != nil {
		return nil, nil, err
	}
	for rows0.Next() {
		var id uuid.UUID
		var status string
		if err := rows0.Scan(&id, &status); err != nil {
			rows0.Close()
			return nil, nil, err
		}
		if status == "ativa" {
			activated = append(activated, id)
		}
	}
	rows0.Close()
	if err := rows0.Err(); err != nil {
		return nil, nil, err
	}

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
// Retorna pgx.ErrNoRows se o id não existir. A lifecycle não é tocada aqui — mas
// se este edit estende o end_date de uma campanha 'concluida' para o futuro, a
// próxima rodada do PromoteScheduledLifecycle a recupera para ativa/programada
// (passo RECOVERY). Antes de 2026-06-05 essa recuperação NÃO existia e a
// campanha ficava presa em 'concluida' — ver o incidente TINTAS RENNER.
func (c *Campaigns) UpdateBasic(ctx context.Context, id uuid.UUID, in UpdateBasicInput) (*Campaign, error) {
	var camp Campaign
	err := c.pool.QueryRow(ctx, `
		UPDATE campaigns
		SET name = $2, start_date = $3, end_date = $4, updated_at = now()
		WHERE id = $1
		RETURNING id, client_id, name, start_date, end_date, status, target_stations,
		          fixed_cpm, created_at, updated_at`,
		id, in.Name, in.StartDate, in.EndDate,
	).Scan(&camp.ID, &camp.ClientID, &camp.Name, &camp.StartDate, &camp.EndDate,
		&camp.Status, &camp.TargetStations, &camp.FixedCPM, &camp.CreatedAt, &camp.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &camp, nil
}

// UpdateFixedCPM seta (ou limpa, quando value=nil) o CPM fixo da campanha.
// É um endpoint dedicado pq o wizard salva isso no Step 6 de Pricing, separado
// do basic data (Step 1). Quando NULL, o frontend volta a usar o cálculo
// dinâmico de CPM. Retorna pgx.ErrNoRows se o id não existir.
func (c *Campaigns) UpdateFixedCPM(ctx context.Context, id uuid.UUID, value *float64) (*Campaign, error) {
	var camp Campaign
	err := c.pool.QueryRow(ctx, `
		UPDATE campaigns
		SET fixed_cpm = $2, updated_at = now()
		WHERE id = $1
		RETURNING id, client_id, name, start_date, end_date, status, target_stations,
		          fixed_cpm, created_at, updated_at`,
		id, value,
	).Scan(&camp.ID, &camp.ClientID, &camp.Name, &camp.StartDate, &camp.EndDate,
		&camp.Status, &camp.TargetStations, &camp.FixedCPM, &camp.CreatedAt, &camp.UpdatedAt)
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
//     insertions += in_slot + bonus
//     audience  += (in_slot + bonus) × stations.pmm
//   - consolidated:  invested += consolidated_value (independente das plays)
//     insertions += in_slot + bonus
//     audience  += (in_slot + bonus) × stations.pmm
//   - CPM = invested / audience × 1000, calculado no caller (frontend)
//     pra ter precisão decimal. audience = soma de impressões reais
//     (cada inserção em uma emissora vale stations.pmm impressões).
type CampaignFinancials struct {
	CampaignID      uuid.UUID `json:"campaign_id"`
	TotalInvested   float64   `json:"total_invested"`
	TotalInsertions int       `json:"total_insertions"`
	TotalAudience   float64   `json:"total_audience"`
	// FixedCPM, quando setado, sobrescreve o CPM derivado (invested/audience).
	// O frontend usa esse valor diretamente em vez de calcular.
	FixedCPM *float64 `json:"fixed_cpm"`
}

// FinancialsByCampaign retorna o agregado das campanhas. Quando clientID
// não é nil, filtra somente as campanhas do cliente — usado por viewers
// para evitar vazamento cross-client. Admins/operators passam nil e recebem
// todas as campanhas.
func (c *Campaigns) FinancialsByCampaign(ctx context.Context, clientID *uuid.UUID, today time.Time) ([]CampaignFinancials, error) {
	q := `
		WITH per_ins AS (
			-- Investimento, inserções e audiência no modo per_insertion:
			-- audience = (in_slot + bonus) × stations.pmm somado por campanha.
			SELECT
				p.campaign_id,
				COALESCE(SUM(tp.unit_value * (s.in_slot + s.bonus)), 0)::float8 AS invested,
				COALESCE(SUM(s.in_slot + s.bonus), 0)::int                    AS insertions,
				COALESCE(SUM((s.in_slot + s.bonus) * COALESCE(st.pmm, 0)), 0)::float8 AS audience
			FROM campaign_station_pricing p
			JOIN campaign_station_type_pricing tp
				ON tp.campaign_id = p.campaign_id
			   AND tp.station_id  = p.station_id
			LEFT JOIN daily_play_summary s
				ON s.campaign_id = p.campaign_id
			   AND s.station_id  = p.station_id
			   AND s.type_id     = tp.type_id
			LEFT JOIN stations st
				ON st.id = p.station_id
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
			-- Inserções e audiência de emissoras em modo consolidado entram no
			-- denominador do CPM (mesma definição pra ambos os modos).
			SELECT
				p.campaign_id,
				COALESCE(SUM(s.in_slot + s.bonus), 0)::int AS insertions,
				COALESCE(SUM((s.in_slot + s.bonus) * COALESCE(st.pmm, 0)), 0)::float8 AS audience
			FROM campaign_station_pricing p
			LEFT JOIN daily_play_summary s
				ON s.campaign_id = p.campaign_id
			   AND s.station_id  = p.station_id
			LEFT JOIN stations st
				ON st.id = p.station_id
			WHERE p.mode = 'consolidated'
			GROUP BY p.campaign_id
		)
		SELECT
			c.id,
			-- consolidated_value é MENSAL → acumula por ciclo mensal iniciado até
			-- hoje ($2). per_insertion segue pelo entregue. Mesma regra do /insights.
			COALESCE(per_ins.invested, 0)
			  + COALESCE(consolidated_inv.invested, 0) * ` + monthsElapsedSQL("c.start_date", "c.end_date", "$2", "c.start_date", "c.end_date") + ` AS total_invested,
			COALESCE(per_ins.insertions, 0) + COALESCE(consolidated_ins.insertions, 0) AS total_insertions,
			COALESCE(per_ins.audience, 0) + COALESCE(consolidated_ins.audience, 0) AS total_audience,
			c.fixed_cpm
		FROM campaigns c
		LEFT JOIN per_ins          ON per_ins.campaign_id          = c.id
		LEFT JOIN consolidated_inv ON consolidated_inv.campaign_id = c.id
		LEFT JOIN consolidated_ins ON consolidated_ins.campaign_id = c.id
		WHERE ($1::uuid IS NULL OR c.client_id = $1)
	`
	rows, err := c.pool.Query(ctx, q, clientID, orMaxDate(today))
	if err != nil {
		return nil, fmt.Errorf("campaigns.FinancialsByCampaign: query: %w", err)
	}
	defer rows.Close()
	out := make([]CampaignFinancials, 0)
	for rows.Next() {
		var f CampaignFinancials
		if err := rows.Scan(&f.CampaignID, &f.TotalInvested, &f.TotalInsertions, &f.TotalAudience, &f.FixedCPM); err != nil {
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

	// Guard (audit C1): recusa deletar se as detecções desta campanha carregam
	// projeções fan-out de OUTRAS campanhas — o DELETE FROM detections abaixo
	// faria o CASCADE (FK detection_campaigns→detections) varrer o histórico
	// alheio de forma irreversível. Caller mapeia pra 409.
	var hasForeignProjections bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS(
		  SELECT 1
		  FROM detections d
		  JOIN detection_campaigns dc
		    ON dc.detection_id = d.id AND dc.detected_at = d.detected_at
		  WHERE d.campaign_id = $1
		    AND dc.campaign_id <> $1
		)`, id).Scan(&hasForeignProjections); err != nil {
		return err
	}
	if hasForeignProjections {
		return ErrCampaignHasForeignProjections
	}

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

// CountForeignProjections reports how many detection_campaigns rows belonging to
// OTHER campaigns ride on detections owned by this campaign (detections.campaign_id).
// A value > 0 means a hard Delete would CASCADE-wipe another campaign's history —
// the handler uses this to answer 409 before pausing anything (mirrors the
// Clients.CountDependents pattern). See Delete's guard and audit 2026-07-02 C1.
func (c *Campaigns) CountForeignProjections(ctx context.Context, id uuid.UUID) (int, error) {
	var n int
	err := c.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM detections d
		JOIN detection_campaigns dc
		  ON dc.detection_id = d.id AND dc.detected_at = d.detected_at
		WHERE d.campaign_id = $1
		  AND dc.campaign_id <> $1`, id).Scan(&n)
	return n, err
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
