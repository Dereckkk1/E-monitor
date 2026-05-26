package catalog

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

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
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

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
	var cli Client
	err := c.pool.QueryRow(ctx, `
		INSERT INTO clients (name, logo_url, contact_email, contact_name, phone, cnpj, cep, city, state)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, name, logo_url, contact_email, contact_name, phone, cnpj, cep, city, state, created_at, updated_at`,
		in.Name, in.LogoURL, in.ContactEmail, in.ContactName, in.Phone, in.CNPJ, in.CEP, in.City, in.State,
	).Scan(&cli.ID, &cli.Name, &cli.LogoURL, &cli.ContactEmail, &cli.ContactName, &cli.Phone, &cli.CNPJ, &cli.CEP, &cli.City, &cli.State, &cli.CreatedAt, &cli.UpdatedAt)
	return &cli, err
}

func (c *Clients) Update(ctx context.Context, id uuid.UUID, in UpdateClientInput) (*Client, error) {
	var cli Client
	err := c.pool.QueryRow(ctx, `
		UPDATE clients
		SET name=$1, logo_url=$2, contact_email=$3, contact_name=$4, phone=$5, cnpj=$6, cep=$7, city=$8, state=$9, updated_at=NOW()
		WHERE id=$10
		RETURNING id, name, logo_url, contact_email, contact_name, phone, cnpj, cep, city, state, created_at, updated_at`,
		in.Name, in.LogoURL, in.ContactEmail, in.ContactName, in.Phone, in.CNPJ, in.CEP, in.City, in.State, id,
	).Scan(&cli.ID, &cli.Name, &cli.LogoURL, &cli.ContactEmail, &cli.ContactName, &cli.Phone, &cli.CNPJ, &cli.CEP, &cli.City, &cli.State, &cli.CreatedAt, &cli.UpdatedAt)
	return &cli, err
}

func (c *Clients) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := c.pool.Exec(ctx, `DELETE FROM clients WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// Get returns a single client by ID, or pgx.ErrNoRows if not found.
func (c *Clients) Get(ctx context.Context, id uuid.UUID) (*Client, error) {
	var cli Client
	err := c.pool.QueryRow(ctx,
		`SELECT id, name, logo_url, contact_email, contact_name, phone, cnpj, cep, city, state, created_at, updated_at
		 FROM clients WHERE id = $1`, id,
	).Scan(&cli.ID, &cli.Name, &cli.LogoURL, &cli.ContactEmail, &cli.ContactName, &cli.Phone, &cli.CNPJ, &cli.CEP, &cli.City, &cli.State, &cli.CreatedAt, &cli.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &cli, nil
}

func (c *Clients) List(ctx context.Context) ([]Client, error) {
	rows, err := c.pool.Query(ctx,
		`SELECT id, name, logo_url, contact_email, contact_name, phone, cnpj, cep, city, state, created_at, updated_at
		 FROM clients ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Client
	for rows.Next() {
		var cli Client
		if err := rows.Scan(&cli.ID, &cli.Name, &cli.LogoURL, &cli.ContactEmail, &cli.ContactName, &cli.Phone, &cli.CNPJ, &cli.CEP, &cli.City, &cli.State, &cli.CreatedAt, &cli.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, cli)
	}
	return out, rows.Err()
}

// ListPaged returns a paginated slice of clients filtered by a free-text
// query. Search is case- and accent-insensitive across name/city/state/cnpj/
// contact_email/contact_name (same vocabulary the frontend offered locally,
// now pushed to SQL so it composes with paging). Returns (rows, totalCount).
func (c *Clients) ListPaged(ctx context.Context, q string, page, pageSize int) ([]Client, int, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	const where = `
		WHERE
		    $1 = '' OR
		    unaccent(lower(
		        COALESCE(name,'') || ' ' ||
		        COALESCE(city,'') || ' ' ||
		        COALESCE(state,'') || ' ' ||
		        COALESCE(cnpj,'') || ' ' ||
		        COALESCE(contact_name,'') || ' ' ||
		        COALESCE(contact_email,'')
		    )) LIKE '%' || unaccent(lower($1)) || '%'
	`

	var total int
	if err := c.pool.QueryRow(ctx, `SELECT COUNT(*) FROM clients`+where, q).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := c.pool.Query(ctx, `
		SELECT id, name, logo_url, contact_email, contact_name, phone, cnpj, cep, city, state, created_at, updated_at
		FROM clients`+where+`
		ORDER BY name
		LIMIT $2 OFFSET $3`, q, pageSize, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []Client
	for rows.Next() {
		var cli Client
		if err := rows.Scan(&cli.ID, &cli.Name, &cli.LogoURL, &cli.ContactEmail, &cli.ContactName, &cli.Phone, &cli.CNPJ, &cli.CEP, &cli.City, &cli.State, &cli.CreatedAt, &cli.UpdatedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, cli)
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
