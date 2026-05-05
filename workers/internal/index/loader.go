package index

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"go.uber.org/zap"

	"radiocheck/internal/events"
)

// Loader loads fingerprint hashes from Postgres into the Store.
// It also subscribes to NATS index.reload events to trigger hot-reload.
type Loader struct {
	store *Store
	db    *pgxpool.Pool
	nc    *nats.Conn
	log   *zap.Logger
}

// NewLoader creates a new Loader bound to the given store, DB pool, NATS
// connection, and logger.
func NewLoader(store *Store, db *pgxpool.Pool, nc *nats.Conn, log *zap.Logger) *Loader {
	return &Loader{
		store: store,
		db:    db,
		nc:    nc,
		log:   log,
	}
}

// LoadAll loads ALL ready commercials' fingerprints into the index via a
// single JOIN query. It always swaps a non-nil index — even when no rows
// are found. Called once at startup.
func (l *Loader) LoadAll(ctx context.Context) error {
	rows, err := l.db.Query(ctx, `
		SELECT fh.hash_value, fh.time_frame, c.short_id
		FROM fingerprint_hashes fh
		JOIN commercials c ON c.id = fh.commercial_id
		WHERE c.fingerprint_status = 'ready'
	`)
	if err != nil {
		return fmt.Errorf("index loader: query fingerprint_hashes: %w", err)
	}
	defer rows.Close()

	newIndex := make(Index)
	for rows.Next() {
		var hashValue uint32
		var timeFrame int32
		var shortID int32
		if err := rows.Scan(&hashValue, &timeFrame, &shortID); err != nil {
			return fmt.Errorf("index loader: scan row: %w", err)
		}
		newIndex[hashValue] = append(newIndex[hashValue], Entry{
			CommercialShortID: shortID,
			TimeFrame:         timeFrame,
		})
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("index loader: iterate rows: %w", err)
	}

	l.store.Swap(newIndex)
	l.log.Info("index loaded", zap.Int("hashes", len(newIndex)))
	return nil
}

// reloadPayload is the JSON structure expected in "index.reload" messages.
type reloadPayload struct {
	CommercialID string `json:"commercial_id"`
}

// Subscribe starts a NATS subscription to "index.reload".
// When a message arrives with payload {"commercial_id": "uuid"}, it reloads
// only that commercial's fingerprints and merges them into the current index.
// Returns the subscription (caller should defer sub.Unsubscribe()).
func (l *Loader) Subscribe(ctx context.Context) (*nats.Subscription, error) {
	sub, err := l.nc.Subscribe(events.SubjectIndexReload, func(msg *nats.Msg) {
		var payload reloadPayload
		if err := json.Unmarshal(msg.Data, &payload); err != nil {
			l.log.Warn("index.reload: invalid JSON payload",
				zap.Error(err),
				zap.ByteString("data", msg.Data),
			)
			return
		}
		if payload.CommercialID == "" {
			l.log.Warn("index.reload: missing commercial_id in payload")
			return
		}

		// Fetch the commercial's short_id, confirming it is ready.
		var shortID int32
		err := l.db.QueryRow(ctx, `
			SELECT short_id FROM commercials
			WHERE id = $1 AND fingerprint_status = 'ready'
		`, payload.CommercialID).Scan(&shortID)
		if err != nil {
			l.log.Warn("index.reload: commercial not found or not ready",
				zap.String("commercial_id", payload.CommercialID),
				zap.Error(err),
			)
			return
		}

		// Fetch all fingerprint hashes for this commercial.
		rows, err := l.db.Query(ctx, `
			SELECT hash_value, time_frame FROM fingerprint_hashes
			WHERE commercial_id = $1
		`, payload.CommercialID)
		if err != nil {
			l.log.Error("index.reload: query fingerprint_hashes failed",
				zap.String("commercial_id", payload.CommercialID),
				zap.Error(err),
			)
			return
		}
		defer rows.Close()

		// Collect entries for the reloaded commercial.
		type hashEntry struct {
			hash      uint32
			timeFrame int32
		}
		var newEntries []hashEntry
		for rows.Next() {
			var hashValue uint32
			var timeFrame int32
			if err := rows.Scan(&hashValue, &timeFrame); err != nil {
				l.log.Error("index.reload: scan row failed",
					zap.String("commercial_id", payload.CommercialID),
					zap.Error(err),
				)
				return
			}
			newEntries = append(newEntries, hashEntry{hash: hashValue, timeFrame: timeFrame})
		}
		if err := rows.Err(); err != nil {
			l.log.Error("index.reload: iterate rows failed",
				zap.String("commercial_id", payload.CommercialID),
				zap.Error(err),
			)
			return
		}

		// Copy current index into a new map — never mutate the live index.
		current := l.store.Load()
		merged := make(Index, len(current))
		for k, v := range current {
			// Shallow-copy the slice so we don't share backing arrays with the old index.
			dst := make([]Entry, len(v))
			copy(dst, v)
			merged[k] = dst
		}

		// Remove all existing entries for this commercial, then add new ones.
		for k, entries := range merged {
			filtered := entries[:0]
			for _, e := range entries {
				if e.CommercialShortID != shortID {
					filtered = append(filtered, e)
				}
			}
			if len(filtered) == 0 {
				delete(merged, k)
			} else {
				merged[k] = filtered
			}
		}
		for _, ne := range newEntries {
			merged[ne.hash] = append(merged[ne.hash], Entry{
				CommercialShortID: shortID,
				TimeFrame:         ne.timeFrame,
			})
		}

		l.store.Swap(merged)
		l.log.Info("index reloaded for commercial",
			zap.String("commercial_id", payload.CommercialID),
			zap.Int("hashes", len(newEntries)),
		)
	})
	if err != nil {
		return nil, fmt.Errorf("index loader: nats subscribe: %w", err)
	}
	return sub, nil
}
