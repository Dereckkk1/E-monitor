package fingerprint

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Commercial holds the subset of the commercials row we care about during
// persistence.
type Commercial struct {
	ID      uuid.UUID
	ShortID int32
	Title   string
}

// LookupCommercialByShortID resolves a short_id (the operator-friendly
// integer) to the canonical commercials.id UUID.
func LookupCommercialByShortID(ctx context.Context, pool *pgxpool.Pool, shortID int32) (*Commercial, error) {
	var c Commercial
	err := pool.QueryRow(ctx, `
		SELECT id, short_id, title FROM commercials WHERE short_id = $1
	`, shortID).Scan(&c.ID, &c.ShortID, &c.Title)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("fingerprint: commercial with short_id=%d not found", shortID)
		}
		return nil, fmt.Errorf("fingerprint: lookup commercial: %w", err)
	}
	return &c, nil
}

// PersistOptions controls how Persist writes hashes to fingerprint_hashes.
type PersistOptions struct {
	// CommercialID is the UUID of the commercial these hashes belong to.
	CommercialID uuid.UUID
	// Variant is stored in fingerprint_hashes.variant_id.
	Variant VariantID
	// RateID is the multi-rate identifier (§9.7). Defaults to 0 for the
	// canonical 1.0× rate.
	RateID uint8
	// ReplaceExisting deletes any rows for (commercial_id, variant_id, rate_id)
	// before inserting. Recommended when re-running the pipeline.
	ReplaceExisting bool
}

// Persist inserts hashes into fingerprint_hashes inside a single transaction.
// It uses pgx CopyFrom for throughput. When opts.ReplaceExisting is true the
// existing rows for (commercial_id, variant_id, rate_id) are deleted first.
//
// The commercials row's fingerprint_status / fingerprint_hash_count /
// fingerprint_generated_at columns are updated in the same transaction.
func Persist(ctx context.Context, pool *pgxpool.Pool, hashes []Hash, opts PersistOptions) (int64, error) {
	if opts.CommercialID == uuid.Nil {
		return 0, fmt.Errorf("fingerprint: PersistOptions.CommercialID is required")
	}

	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, fmt.Errorf("fingerprint: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if opts.ReplaceExisting {
		if _, err := tx.Exec(ctx, `
			DELETE FROM fingerprint_hashes
			WHERE commercial_id = $1 AND variant_id = $2 AND rate_id = $3
		`, opts.CommercialID, int16(opts.Variant), int16(opts.RateID)); err != nil {
			return 0, fmt.Errorf("fingerprint: delete existing hashes: %w", err)
		}
	}

	rows := make([][]any, 0, len(hashes))
	for _, h := range hashes {
		rows = append(rows, []any{
			opts.CommercialID,
			int16(opts.Variant),
			int16(opts.RateID),
			// Hash.Value is uint32; fingerprint_hashes.hash_value is BIGINT.
			// Use int64 to keep the unsigned value unambiguous on the wire.
			int64(h.Value),
			int32(h.TimeFrame),
		})
	}

	inserted, err := tx.CopyFrom(ctx,
		pgx.Identifier{"fingerprint_hashes"},
		[]string{"commercial_id", "variant_id", "rate_id", "hash_value", "time_frame"},
		pgx.CopyFromRows(rows),
	)
	if err != nil {
		return 0, fmt.Errorf("fingerprint: copy hashes: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE commercials
		SET fingerprint_status = 'ready',
		    fingerprint_generated_at = NOW(),
		    fingerprint_hash_count = $2,
		    updated_at = NOW()
		WHERE id = $1
	`, opts.CommercialID, int32(len(hashes))); err != nil {
		return 0, fmt.Errorf("fingerprint: update commercial status: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("fingerprint: commit: %w", err)
	}
	return inserted, nil
}
