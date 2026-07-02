package catalog

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Material represents a row in the materials table.
// Materials are scoped per-client and decoupled from campaigns — a material
// can be reused across many campaigns via campaign_materials.
type Material struct {
	ID                     uuid.UUID       `json:"id"`
	ShortID                int32           `json:"short_id"`
	ClientID               uuid.UUID       `json:"client_id"`
	Title                  string          `json:"title"`
	TypeID                 *uuid.UUID      `json:"type_id,omitempty"`
	DurationSeconds        float64         `json:"duration_seconds"`
	MasterStoragePath      string          `json:"master_storage_path"`
	MasterSHA256           string          `json:"master_sha256"`
	FingerprintStatus      string          `json:"fingerprint_status"`
	FingerprintGeneratedAt *time.Time      `json:"fingerprint_generated_at,omitempty"`
	FingerprintHashCount   *int32          `json:"fingerprint_hash_count,omitempty"`
	SimilarityCheckStatus  string          `json:"similarity_check_status"`
	MostSimilarMaterialID  *uuid.UUID      `json:"most_similar_material_id,omitempty"`
	SimilarityScore        *float32        `json:"similarity_score,omitempty"`
	SimilarityAckdAt       *time.Time      `json:"similarity_acknowledged_at,omitempty"`
	SimilaritySegments     json.RawMessage `json:"similarity_segments,omitempty"`
	// Script is the spoken-copy of the commercial — optional free text the
	// operator fills in at upload or later via the wizard. Surfaced on
	// /detections/:id when the detected material has one.
	Script    *string   `json:"script,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Materials is the repository for the materials table.
type Materials struct {
	pool *pgxpool.Pool
}

// NewMaterials returns a new Materials repo backed by pool.
func NewMaterials(pool *pgxpool.Pool) *Materials {
	return &Materials{pool: pool}
}

// ListReadyIDsByCampaign returns the IDs of 'ready' materials linked to the
// campaign via campaign_materials. Used by Supervisor.Reload to publish
// index.reload so a freshly-linked (or reused/backfilled) material's
// fingerprints enter the in-memory matching index without waiting for a full
// restart (audit 2026-07-02 E3: INFINITE PAY — a ready material reused in a new
// campaign never entered the index because no link path published a reload).
func (m *Materials) ListReadyIDsByCampaign(ctx context.Context, campaignID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := m.pool.Query(ctx, `
		SELECT DISTINCT mt.id
		FROM materials mt
		JOIN campaign_materials cm ON cm.material_id = mt.id
		WHERE cm.campaign_id = $1
		  AND mt.fingerprint_status = 'ready'`, campaignID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// CreateMaterialInput holds the fields required to insert a new material row.
type CreateMaterialInput struct {
	ClientID          uuid.UUID
	Title             string
	TypeID            *uuid.UUID
	DurationSeconds   float64
	MasterStoragePath string
	MasterSHA256      string
	// Script is optional spoken-copy of the commercial. Empty string is
	// treated as NULL on insert.
	Script *string
}

const materialColumns = `id, short_id, client_id, title, type_id, duration_seconds,
       master_storage_path, master_sha256, fingerprint_status,
       fingerprint_generated_at, fingerprint_hash_count,
       similarity_check_status, most_similar_material_id, similarity_score,
       similarity_acknowledged_at, similarity_segments, script, created_at, updated_at`

func scanMaterial(row interface {
	Scan(...any) error
}, m *Material) error {
	return row.Scan(&m.ID, &m.ShortID, &m.ClientID, &m.Title, &m.TypeID,
		&m.DurationSeconds, &m.MasterStoragePath, &m.MasterSHA256,
		&m.FingerprintStatus, &m.FingerprintGeneratedAt, &m.FingerprintHashCount,
		&m.SimilarityCheckStatus, &m.MostSimilarMaterialID, &m.SimilarityScore,
		&m.SimilarityAckdAt, &m.SimilaritySegments, &m.Script, &m.CreatedAt, &m.UpdatedAt)
}

// Create inserts a new material and returns the persisted row.
func (m *Materials) Create(ctx context.Context, in CreateMaterialInput) (*Material, error) {
	var mat Material
	row := m.pool.QueryRow(ctx, `
		INSERT INTO materials (client_id, title, type_id, duration_seconds,
		                       master_storage_path, master_sha256, script)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING `+materialColumns,
		in.ClientID, in.Title, in.TypeID, in.DurationSeconds,
		in.MasterStoragePath, in.MasterSHA256, in.Script,
	)
	if err := scanMaterial(row, &mat); err != nil {
		return nil, err
	}
	return &mat, nil
}

// Get returns a single material by ID. Returns pgx.ErrNoRows when not found.
func (m *Materials) Get(ctx context.Context, id uuid.UUID) (*Material, error) {
	var mat Material
	row := m.pool.QueryRow(ctx,
		`SELECT `+materialColumns+` FROM materials WHERE id = $1`, id)
	if err := scanMaterial(row, &mat); err != nil {
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

// UpdateScript sets (or clears) the script for a material. Pass nil — or a
// pointer to "" — to clear; the handler is responsible for normalizing empty
// input. Returns pgx.ErrNoRows when the id does not exist.
func (m *Materials) UpdateScript(ctx context.Context, id uuid.UUID, script *string) error {
	tag, err := m.pool.Exec(ctx,
		`UPDATE materials SET script = $2, updated_at = now() WHERE id = $1`, id, script)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// Delete removes a material by ID.
func (m *Materials) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := m.pool.Exec(ctx, `DELETE FROM materials WHERE id = $1`, id)
	return err
}

// Acknowledge marks the similarity warning for the given material as resolved
// (the operator chose "Manter assim mesmo" in the UI). Idempotent — repeated
// calls just refresh the timestamp.
func (m *Materials) Acknowledge(ctx context.Context, id uuid.UUID) error {
	_, err := m.pool.Exec(ctx,
		`UPDATE materials SET similarity_acknowledged_at = NOW(), updated_at = NOW() WHERE id = $1`,
		id)
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

// ListReadyByCampaignsForStation returns ready materials linked (via
// campaign_materials) to any of the given campaigns AND including the given
// station in that link's target_stations. campaign_materials is authoritative,
// so backfilled materials (UUID also in commercials) reused in a new campaign
// ARE returned here — the commercials path only covers pure-legacy rows
// (id NOT IN materials), so each short_id still loads exactly once.
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
		  AND $2 = ANY(cm.target_stations)`,
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
