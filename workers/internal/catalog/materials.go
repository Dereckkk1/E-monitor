package catalog

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Material represents a row in the materials table.
// Materials are scoped per-client and decoupled from campaigns — a material
// can be reused across many campaigns via campaign_materials.
type Material struct {
	ID                     uuid.UUID  `json:"id"`
	ShortID                int32      `json:"short_id"`
	ClientID               uuid.UUID  `json:"client_id"`
	Title                  string     `json:"title"`
	TypeID                 *uuid.UUID `json:"type_id,omitempty"`
	DurationSeconds        float64    `json:"duration_seconds"`
	MasterStoragePath      string     `json:"master_storage_path"`
	MasterSHA256           string     `json:"master_sha256"`
	FingerprintStatus      string     `json:"fingerprint_status"`
	FingerprintGeneratedAt *time.Time `json:"fingerprint_generated_at,omitempty"`
	FingerprintHashCount   *int32     `json:"fingerprint_hash_count,omitempty"`
	CreatedAt              time.Time  `json:"created_at"`
	UpdatedAt              time.Time  `json:"updated_at"`
}

// Materials is the repository for the materials table.
type Materials struct {
	pool *pgxpool.Pool
}

// NewMaterials returns a new Materials repo backed by pool.
func NewMaterials(pool *pgxpool.Pool) *Materials {
	return &Materials{pool: pool}
}

// CreateMaterialInput holds the fields required to insert a new material row.
type CreateMaterialInput struct {
	ClientID          uuid.UUID
	Title             string
	TypeID            *uuid.UUID
	DurationSeconds   float64
	MasterStoragePath string
	MasterSHA256      string
}

const materialColumns = `id, short_id, client_id, title, type_id, duration_seconds,
       master_storage_path, master_sha256, fingerprint_status,
       fingerprint_generated_at, fingerprint_hash_count, created_at, updated_at`

func scanMaterial(row interface {
	Scan(...any) error
}, m *Material) error {
	return row.Scan(&m.ID, &m.ShortID, &m.ClientID, &m.Title, &m.TypeID,
		&m.DurationSeconds, &m.MasterStoragePath, &m.MasterSHA256,
		&m.FingerprintStatus, &m.FingerprintGeneratedAt, &m.FingerprintHashCount,
		&m.CreatedAt, &m.UpdatedAt)
}

// Create inserts a new material and returns the persisted row.
func (m *Materials) Create(ctx context.Context, in CreateMaterialInput) (*Material, error) {
	var mat Material
	err := m.pool.QueryRow(ctx, `
		INSERT INTO materials (client_id, title, type_id, duration_seconds,
		                       master_storage_path, master_sha256)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING `+materialColumns,
		in.ClientID, in.Title, in.TypeID, in.DurationSeconds,
		in.MasterStoragePath, in.MasterSHA256,
	).Scan(&mat.ID, &mat.ShortID, &mat.ClientID, &mat.Title, &mat.TypeID,
		&mat.DurationSeconds, &mat.MasterStoragePath, &mat.MasterSHA256,
		&mat.FingerprintStatus, &mat.FingerprintGeneratedAt, &mat.FingerprintHashCount,
		&mat.CreatedAt, &mat.UpdatedAt)
	return &mat, err
}

// Get returns a single material by ID. Returns pgx.ErrNoRows when not found.
func (m *Materials) Get(ctx context.Context, id uuid.UUID) (*Material, error) {
	var mat Material
	err := m.pool.QueryRow(ctx,
		`SELECT `+materialColumns+` FROM materials WHERE id = $1`, id,
	).Scan(&mat.ID, &mat.ShortID, &mat.ClientID, &mat.Title, &mat.TypeID,
		&mat.DurationSeconds, &mat.MasterStoragePath, &mat.MasterSHA256,
		&mat.FingerprintStatus, &mat.FingerprintGeneratedAt, &mat.FingerprintHashCount,
		&mat.CreatedAt, &mat.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &mat, nil
}

// ListByClient returns all materials for a client, ordered by creation date
// descending. Pass an empty q for "all"; a non-empty q filters by
// case-insensitive substring match on title.
func (m *Materials) ListByClient(ctx context.Context, clientID uuid.UUID, q string) ([]Material, error) {
	rows, err := m.pool.Query(ctx, `
		SELECT `+materialColumns+`
		FROM materials
		WHERE client_id = $1
		  AND ($2 = '' OR title ILIKE '%' || $2 || '%')
		ORDER BY created_at DESC`,
		clientID, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Material
	for rows.Next() {
		var mat Material
		if err := scanMaterial(rows, &mat); err != nil {
			return nil, err
		}
		out = append(out, mat)
	}
	return out, rows.Err()
}

// UpdateType sets (or clears) the type_id for a material.
func (m *Materials) UpdateType(ctx context.Context, id uuid.UUID, typeID *uuid.UUID) error {
	_, err := m.pool.Exec(ctx,
		`UPDATE materials SET type_id = $2, updated_at = now() WHERE id = $1`, id, typeID)
	return err
}

// Delete removes a material by ID.
func (m *Materials) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := m.pool.Exec(ctx, `DELETE FROM materials WHERE id = $1`, id)
	return err
}

// ReadyMaterialForWorker holds the per-worker fields needed by the
// supervisor's worker setup. Only the matcher-relevant subset, not the
// full Material struct.
type ReadyMaterialForWorker struct {
	ID              uuid.UUID
	ShortID         int32
	DurationSeconds float64
}

// ListReadyByCampaignsForStation returns ready materials that are linked
// (via campaign_materials) to any of the given campaigns AND that include
// the given station in that link's target_stations array. Backfilled
// materials whose UUID also exists in commercials are EXCLUDED — those go
// through the commercials path. Mirrors commercials.ListReadyByCampaignsForStation.
func (m *Materials) ListReadyByCampaignsForStation(ctx context.Context, campaignIDs []uuid.UUID, stationID uuid.UUID) ([]ReadyMaterialForWorker, error) {
	if len(campaignIDs) == 0 {
		return nil, nil
	}
	rows, err := m.pool.Query(ctx, `
		SELECT DISTINCT mat.id, mat.short_id, mat.duration_seconds
		FROM materials mat
		JOIN campaign_materials cm ON cm.material_id = mat.id
		WHERE cm.campaign_id = ANY($1)
		  AND mat.fingerprint_status = 'ready'
		  AND $2 = ANY(cm.target_stations)
		  AND mat.id NOT IN (SELECT id FROM commercials)`,
		campaignIDs, stationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReadyMaterialForWorker
	for rows.Next() {
		var r ReadyMaterialForWorker
		if err := rows.Scan(&r.ID, &r.ShortID, &r.DurationSeconds); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
