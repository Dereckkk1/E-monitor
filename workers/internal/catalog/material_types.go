package catalog

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// MaterialType represents a row in the material_types lookup table.
// Seeded by migration 0016 with 6 standard broadcast types.
type MaterialType struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Color       string    `json:"color"`
	Description *string   `json:"description,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// MaterialTypes is the repository for the material_types table.
type MaterialTypes struct {
	pool *pgxpool.Pool
}

// NewMaterialTypes returns a new MaterialTypes repo backed by pool.
func NewMaterialTypes(pool *pgxpool.Pool) *MaterialTypes {
	return &MaterialTypes{pool: pool}
}

// CreateMaterialTypeInput holds the fields required to create or update a
// material type. Update reuses the same input struct (same pattern as other
// repos in this package).
type CreateMaterialTypeInput struct {
	Name        string
	Color       string
	Description *string
}

const materialTypeColumns = `id, name, color, description, created_at`

// List returns all material types ordered by name.
func (r *MaterialTypes) List(ctx context.Context) ([]MaterialType, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+materialTypeColumns+` FROM material_types ORDER BY name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MaterialType
	for rows.Next() {
		var t MaterialType
		if err := rows.Scan(&t.ID, &t.Name, &t.Color, &t.Description, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Get returns a single material type by ID. Returns pgx.ErrNoRows when not
// found.
func (r *MaterialTypes) Get(ctx context.Context, id uuid.UUID) (*MaterialType, error) {
	var t MaterialType
	err := r.pool.QueryRow(ctx,
		`SELECT `+materialTypeColumns+` FROM material_types WHERE id = $1`, id,
	).Scan(&t.ID, &t.Name, &t.Color, &t.Description, &t.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// Create inserts a new material type and returns the persisted row.
// Falls back to "#94a3b8" (slate-400) when Color is empty.
func (r *MaterialTypes) Create(ctx context.Context, in CreateMaterialTypeInput) (*MaterialType, error) {
	color := in.Color
	if color == "" {
		color = "#94a3b8"
	}
	var t MaterialType
	err := r.pool.QueryRow(ctx,
		`INSERT INTO material_types (name, color, description)
		 VALUES ($1, $2, $3)
		 RETURNING `+materialTypeColumns,
		in.Name, color, in.Description,
	).Scan(&t.ID, &t.Name, &t.Color, &t.Description, &t.CreatedAt)
	return &t, err
}

// Update replaces name, color and description for an existing material type.
// Falls back to "#94a3b8" when Color is empty. Returns pgx.ErrNoRows when the
// id does not exist.
func (r *MaterialTypes) Update(ctx context.Context, id uuid.UUID, in CreateMaterialTypeInput) (*MaterialType, error) {
	color := in.Color
	if color == "" {
		color = "#94a3b8"
	}
	var t MaterialType
	err := r.pool.QueryRow(ctx,
		`UPDATE material_types
		 SET name = $2, color = $3, description = $4
		 WHERE id = $1
		 RETURNING `+materialTypeColumns,
		id, in.Name, color, in.Description,
	).Scan(&t.ID, &t.Name, &t.Color, &t.Description, &t.CreatedAt)
	return &t, err
}

// Delete removes a material type by ID.
func (r *MaterialTypes) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM material_types WHERE id = $1`, id)
	return err
}
