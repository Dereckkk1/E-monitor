package catalog

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Client struct {
	ID           uuid.UUID `json:"id"`
	Name         string    `json:"name"`
	ContactEmail *string   `json:"contact_email,omitempty"`
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
	ContactEmail *string `json:"contact_email,omitempty"`
}

func (c *Clients) Create(ctx context.Context, in CreateClientInput) (*Client, error) {
	var cli Client
	err := c.pool.QueryRow(ctx, `
		INSERT INTO clients (name, contact_email)
		VALUES ($1, $2)
		RETURNING id, name, contact_email, created_at, updated_at`,
		in.Name, in.ContactEmail,
	).Scan(&cli.ID, &cli.Name, &cli.ContactEmail, &cli.CreatedAt, &cli.UpdatedAt)
	return &cli, err
}

func (c *Clients) List(ctx context.Context) ([]Client, error) {
	rows, err := c.pool.Query(ctx,
		`SELECT id, name, contact_email, created_at, updated_at FROM clients ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Client
	for rows.Next() {
		var cli Client
		if err := rows.Scan(&cli.ID, &cli.Name, &cli.ContactEmail, &cli.CreatedAt, &cli.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, cli)
	}
	return out, rows.Err()
}
