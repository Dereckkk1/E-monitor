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
