package catalog

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrClientHasDependents is returned by Delete when a client cannot be removed
// because rows in other tables still reference it (campaigns NO ACTION,
// materials/users RESTRICT). Callers map this to 409 Conflict and offer the
// reversible "deactivate" path instead. See CountDependents for the breakdown.
var ErrClientHasDependents = errors.New("client has dependent records")

type Client struct {
	ID           uuid.UUID `json:"id"`
	Name         string    `json:"name"`
	LogoURL      *string   `json:"logo_url,omitempty"`
	ContactEmail *string   `json:"contact_email,omitempty"`
	ContactName  *string   `json:"contact_name,omitempty"`
	Phone        *string   `json:"phone,omitempty"`
	CNPJ         *string   `json:"cnpj,omitempty"`
	CEP          *string   `json:"cep,omitempty"`
	City         *string   `json:"city,omitempty"`
	State        *string   `json:"state,omitempty"`
	IsActive     bool      `json:"is_active"`
	// TargetLabel é o rótulo do público-alvo do cliente ("Homens 25-49"),
	// usado como sufixo dos números "no target" nas telas/relatórios. Sem
	// omitempty de propósito: o frontend precisa receber `null` explícito
	// para distinguir "sem rótulo" de string vazia (mesma convenção de
	// TargetPMMRow.PMMTarget).
	TargetLabel *string   `json:"target_label"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// clientColumns is the canonical SELECT/RETURNING projection, kept in one place
// so the column order never drifts from scanClient's Scan order.
const clientColumns = `id, name, logo_url, contact_email, contact_name, phone, cnpj, cep, city, state, is_active, created_at, updated_at, target_label`

// scanClient reads one row in clientColumns order. Works with both QueryRow
// (single) and Rows (loop) since both satisfy pgx.Row.
func scanClient(row pgx.Row) (*Client, error) {
	var c Client
	err := row.Scan(&c.ID, &c.Name, &c.LogoURL, &c.ContactEmail, &c.ContactName,
		&c.Phone, &c.CNPJ, &c.CEP, &c.City, &c.State, &c.IsActive,
		&c.CreatedAt, &c.UpdatedAt, &c.TargetLabel)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// DependentCounts reports how many rows in each blocking table reference a
// client. Used to build the 409 message when a hard-delete is refused.
type DependentCounts struct {
	Campaigns int `json:"campaigns"`
	Materials int `json:"materials"`
	Users     int `json:"users"`
}

// Total is the sum across blocking tables; zero means the client is safe to
// hard-delete.
func (d DependentCounts) Total() int { return d.Campaigns + d.Materials + d.Users }

type Clients struct {
	pool *pgxpool.Pool
}

func NewClients(pool *pgxpool.Pool) *Clients {
	return &Clients{pool: pool}
}

type CreateClientInput struct {
	Name         string  `json:"name"`
	LogoURL      *string `json:"logo_url,omitempty"`
	ContactEmail *string `json:"contact_email,omitempty"`
	ContactName  *string `json:"contact_name,omitempty"`
	Phone        *string `json:"phone,omitempty"`
	CNPJ         *string `json:"cnpj,omitempty"`
	CEP          *string `json:"cep,omitempty"`
	City         *string `json:"city,omitempty"`
	State        *string `json:"state,omitempty"`
	// TargetLabel: rótulo do público-alvo (texto livre). Sem omitempty —
	// `null` explícito significa "sem rótulo".
	TargetLabel *string `json:"target_label"`
}

type UpdateClientInput struct {
	Name         string  `json:"name"`
	LogoURL      *string `json:"logo_url"`
	ContactEmail *string `json:"contact_email"`
	ContactName  *string `json:"contact_name"`
	Phone        *string `json:"phone"`
	CNPJ         *string `json:"cnpj"`
	CEP          *string `json:"cep"`
	City         *string `json:"city"`
	State        *string `json:"state"`
	TargetLabel  *string `json:"target_label"`
}

// WebhookConfig represents a client's webhook delivery configuration (§13.1.4).
//
// Secret is stored in plaintext for the PoC (per spec). The HTTP API never
// returns the full secret; handlers expose only the first 4 chars + ellipsis.
type WebhookConfig struct {
	URL     string   `json:"url"`
	Secret  string   `json:"-"` // never serialized through the API
	Enabled bool     `json:"enabled"`
	Events  []string `json:"events"`
}

// UpdateWebhookInput is the payload accepted by PATCH /clients/{id}/webhook.
// All fields are optional pointers so the caller can update a single field.
type UpdateWebhookInput struct {
	URL     *string  `json:"webhook_url,omitempty"`
	Secret  *string  `json:"webhook_secret,omitempty"`
	Enabled *bool    `json:"webhook_enabled,omitempty"`
	Events  []string `json:"webhook_events,omitempty"`
}

func (c *Clients) Create(ctx context.Context, in CreateClientInput) (*Client, error) {
	return scanClient(c.pool.QueryRow(ctx, `
		INSERT INTO clients (name, logo_url, contact_email, contact_name, phone, cnpj, cep, city, state, target_label)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING `+clientColumns,
		in.Name, in.LogoURL, in.ContactEmail, in.ContactName, in.Phone, in.CNPJ, in.CEP, in.City, in.State, in.TargetLabel,
	))
}

func (c *Clients) Update(ctx context.Context, id uuid.UUID, in UpdateClientInput) (*Client, error) {
	return scanClient(c.pool.QueryRow(ctx, `
		UPDATE clients
		SET name=$1, logo_url=$2, contact_email=$3, contact_name=$4, phone=$5, cnpj=$6, cep=$7, city=$8, state=$9, target_label=$10, updated_at=NOW()
		WHERE id=$11
		RETURNING `+clientColumns,
		in.Name, in.LogoURL, in.ContactEmail, in.ContactName, in.Phone, in.CNPJ, in.CEP, in.City, in.State, in.TargetLabel, id,
	))
}

// Delete hard-deletes a client. Returns pgx.ErrNoRows if the id doesn't exist,
// or ErrClientHasDependents when a foreign-key (SQLSTATE 23503) blocks the
// removal (the client still owns campaigns/materials/users). Any other DB
// error is returned as-is.
func (c *Clients) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := c.pool.Exec(ctx, `DELETE FROM clients WHERE id = $1`, id)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return ErrClientHasDependents
		}
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// CountDependents returns how many rows reference the client across the tables
// that block a hard-delete. Counts raw FK references (e.g. soft-deleted users
// still hold the FK and still block RESTRICT), so the breakdown truthfully
// explains why Delete was refused.
//
// Users are counted through user_clients (the portfolio, migration 0062), NOT
// through users.client_id: an agency login linked to this client only as a
// SECONDARY still holds a RESTRICT FK, so the delete is refused and the
// breakdown has to say so — counting the principal alone would report zero
// dependents right after refusing the delete. The 0062 trigger materializes
// the principal in user_clients too, so this is a superset, never a
// double-count (PK is (user_id, client_id)).
func (c *Clients) CountDependents(ctx context.Context, id uuid.UUID) (DependentCounts, error) {
	var d DependentCounts
	err := c.pool.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*) FROM campaigns    WHERE client_id = $1),
			(SELECT COUNT(*) FROM materials    WHERE client_id = $1),
			(SELECT COUNT(*) FROM user_clients WHERE client_id = $1)`, id,
	).Scan(&d.Campaigns, &d.Materials, &d.Users)
	return d, err
}

// SetActive flips a client's is_active flag (deactivate/reactivate). Returns
// the updated row, or pgx.ErrNoRows if the id doesn't exist.
func (c *Clients) SetActive(ctx context.Context, id uuid.UUID, active bool) (*Client, error) {
	cli, err := scanClient(c.pool.QueryRow(ctx, `
		UPDATE clients SET is_active=$2, updated_at=NOW()
		WHERE id=$1
		RETURNING `+clientColumns, id, active,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, pgx.ErrNoRows
	}
	return cli, err
}

// Get returns a single client by ID, or pgx.ErrNoRows if not found.
func (c *Clients) Get(ctx context.Context, id uuid.UUID) (*Client, error) {
	return scanClient(c.pool.QueryRow(ctx,
		`SELECT `+clientColumns+` FROM clients WHERE id = $1`, id))
}

// ListByIDs devolve os clientes pedidos, ordenados por nome. Ids inexistentes
// são simplesmente omitidos (sem erro) — é o que a lista scope-aware precisa:
// um vínculo órfão não pode derrubar a tela inteira. Como List, devolve ativos
// E inativos: a carteira do JWT já é filtrada por atividade no login, e o
// cliente desativado no meio da sessão ainda precisa resolver nome/logo.
func (c *Clients) ListByIDs(ctx context.Context, ids []uuid.UUID) ([]Client, error) {
	rows, err := c.pool.Query(ctx,
		`SELECT `+clientColumns+` FROM clients WHERE id = ANY($1) ORDER BY name`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Client
	for rows.Next() {
		cli, err := scanClient(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *cli)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if out == nil {
		out = []Client{}
	}
	return out, nil
}

// List returns the full catalog (active AND inactive), ordered by name. This
// is the lookup-map mode: callers resolve a client name/logo by id (campaign
// rows, detection cells, dropdowns), so inactive clients must stay resolvable.
// The management page uses ListPaged, which hides inactive by default.
func (c *Clients) List(ctx context.Context) ([]Client, error) {
	rows, err := c.pool.Query(ctx,
		`SELECT `+clientColumns+` FROM clients ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Client
	for rows.Next() {
		cli, err := scanClient(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *cli)
	}
	return out, rows.Err()
}

// ListPaged returns a paginated slice of clients filtered by a free-text
// query. Search is case- and accent-insensitive across name/city/state/cnpj/
// contact_email/contact_name (same vocabulary the frontend offered locally,
// now pushed to SQL so it composes with paging). Returns (rows, totalCount).
//
// includeInactive=false (the default for the management page) hides clients
// with is_active=false; pass true for the "mostrar inativos" toggle so the
// operator can find and reactivate them.
func (c *Clients) ListPaged(ctx context.Context, q string, page, pageSize int, includeInactive bool) ([]Client, int, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	where := `
		WHERE (
		    $1 = '' OR
		    unaccent(lower(
		        COALESCE(name,'') || ' ' ||
		        COALESCE(city,'') || ' ' ||
		        COALESCE(state,'') || ' ' ||
		        COALESCE(cnpj,'') || ' ' ||
		        COALESCE(contact_name,'') || ' ' ||
		        COALESCE(contact_email,'')
		    )) LIKE '%' || unaccent(lower($1)) || '%'
		)`
	if !includeInactive {
		where += ` AND is_active = TRUE`
	}

	var total int
	if err := c.pool.QueryRow(ctx, `SELECT COUNT(*) FROM clients`+where, q).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := c.pool.Query(ctx, `
		SELECT `+clientColumns+`
		FROM clients`+where+`
		ORDER BY name
		LIMIT $2 OFFSET $3`, q, pageSize, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []Client
	for rows.Next() {
		cli, err := scanClient(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *cli)
	}
	return out, total, rows.Err()
}

// GetWebhookConfig returns the webhook configuration for a client. Returns nil
// (and no error) when the client has no URL configured — callers treat this
// as "webhooks disabled for this client".
func (c *Clients) GetWebhookConfig(ctx context.Context, id uuid.UUID) (*WebhookConfig, error) {
	var (
		url     *string
		secret  *string
		enabled bool
		events  []string
	)
	err := c.pool.QueryRow(ctx, `
		SELECT webhook_url, webhook_secret, COALESCE(webhook_enabled, false), COALESCE(webhook_events, ARRAY['detection.confirmed']::TEXT[])
		FROM clients WHERE id = $1`, id,
	).Scan(&url, &secret, &enabled, &events)
	if err != nil {
		return nil, err
	}
	if url == nil || *url == "" {
		return nil, nil
	}
	cfg := &WebhookConfig{
		URL:     *url,
		Enabled: enabled,
		Events:  events,
	}
	if secret != nil {
		cfg.Secret = *secret
	}
	return cfg, nil
}

// UpdateWebhookConfig applies a partial update of the webhook config columns
// in `clients`. Pointers left nil are not modified, so the caller can change
// a single field without round-tripping the rest.
func (c *Clients) UpdateWebhookConfig(ctx context.Context, id uuid.UUID, in UpdateWebhookInput) (*WebhookConfig, error) {
	tag, err := c.pool.Exec(ctx, `
		UPDATE clients SET
			webhook_url     = COALESCE($2, webhook_url),
			webhook_secret  = COALESCE($3, webhook_secret),
			webhook_enabled = COALESCE($4, webhook_enabled),
			webhook_events  = CASE WHEN $5::text[] IS NULL THEN webhook_events ELSE $5 END,
			updated_at      = NOW()
		WHERE id = $1`,
		id, in.URL, in.Secret, in.Enabled, in.Events,
	)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, pgx.ErrNoRows
	}
	return c.GetWebhookConfig(ctx, id)
}
