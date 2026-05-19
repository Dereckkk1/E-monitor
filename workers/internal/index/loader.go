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

// indexEligibleStatuses is the set of campaign statuses whose commercials
// belong in the in-memory matching index. We deliberately include
// 'programada' alongside 'ativa' so that the lifecycle scheduler's
// programada → ativa transition (§18.2.1) does not race with index
// population: by the time the worker comes up, the hashes are already there.
//
// Excluded by design:
//   - 'concluida'  → terminal, no worker ever runs against it
//   - 'cancelada'  → terminal, same reasoning; also avoids "stuck" hashes
//                    if an operator cancels a campaign whose fingerprints
//                    are still being generated
//
// Pre-2026-05-08 we filtered ca.status='ativa' only. That meant any
// fingerprint completion arriving on the NATS reload subject before
// Supervisor.Start fired was silently rejected — and if Start failed for
// any reason (DB blip, NATS hiccup), the hashes never made it into memory.
// See docs/worker-commercial-reconciler.md.
const indexEligibleStatuses = `('programada', 'ativa')`

// LoadAll loads ALL ready commercials' fingerprints into the index via a
// single JOIN query. It always swaps a non-nil index — even when no rows
// are found. Called once at startup.
func (l *Loader) LoadAll(ctx context.Context) error {
	rows, err := l.db.Query(ctx, `
		-- Path 1: commercials (legacy + backfilled).
		SELECT fh.hash_value, fh.time_frame, fh.variant_id, fh.rate_id, fh.is_shared, c.short_id
		FROM fingerprint_hashes fh
		JOIN commercials c  ON c.id  = fh.commercial_id
		JOIN campaigns   ca ON ca.id = c.campaign_id
		WHERE c.fingerprint_status = 'ready'
		  AND ca.status IN `+indexEligibleStatuses+`

		UNION ALL

		-- Path 2: materials (new uploads from the campaign wizard, plus
		-- legacy materials linked via campaign_materials). We exclude any
		-- material whose UUID also exists in commercials to avoid duplicate
		-- entries — those are handled by Path 1.
		SELECT fh.hash_value, fh.time_frame, fh.variant_id, fh.rate_id, fh.is_shared, m.short_id
		FROM fingerprint_hashes fh
		JOIN materials m ON m.id = fh.commercial_id
		WHERE m.fingerprint_status = 'ready'
		  AND m.id NOT IN (SELECT id FROM commercials)
		  AND EXISTS (
		      SELECT 1 FROM campaign_materials cm
		      JOIN campaigns ca ON ca.id = cm.campaign_id
		      WHERE cm.material_id = m.id
		        AND ca.status IN `+indexEligibleStatuses+`
		  )
	`)
	if err != nil {
		return fmt.Errorf("index loader: query fingerprint_hashes: %w", err)
	}
	defer rows.Close()

	newIndex := make(Index)
	for rows.Next() {
		var hashValue uint32
		var timeFrame int32
		var variantID int16
		var rateID int16
		var isShared bool
		var shortID int32
		if err := rows.Scan(&hashValue, &timeFrame, &variantID, &rateID, &isShared, &shortID); err != nil {
			return fmt.Errorf("index loader: scan row: %w", err)
		}
		if variantID < 0 || variantID > 255 || rateID < 0 || rateID > 255 {
			return fmt.Errorf("index loader: variant_id=%d or rate_id=%d out of uint8 range", variantID, rateID)
		}
		newIndex[hashValue] = append(newIndex[hashValue], Entry{
			CommercialShortID: shortID,
			VariantID:         uint8(variantID),
			RateID:            uint8(rateID),
			TimeFrame:         timeFrame,
			IsShared:          isShared,
		})
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("index loader: iterate rows: %w", err)
	}

	l.store.Swap(newIndex)
	l.log.Info("index loaded", zap.Int("hashes", len(newIndex)))
	return nil
}

// reloadPayload accepts both legacy commercial_id and new material_id
// keys. Exactly one is expected to be non-empty per message; if both are
// present, commercial_id takes precedence (matches Python daemon behavior).
type reloadPayload struct {
	CommercialID string `json:"commercial_id,omitempty"`
	MaterialID   string `json:"material_id,omitempty"`
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
		if payload.CommercialID == "" && payload.MaterialID == "" {
			l.log.Warn("index.reload: payload missing both commercial_id and material_id")
			return
		}

		var entityID string
		var entityKind string
		var shortID int32
		var err error
		if payload.CommercialID != "" {
			entityID = payload.CommercialID
			entityKind = "commercial"
			err = l.db.QueryRow(ctx, `
				SELECT c.short_id FROM commercials c
				JOIN campaigns ca ON ca.id = c.campaign_id
				WHERE c.id = $1
				  AND c.fingerprint_status = 'ready'
				  AND ca.status IN `+indexEligibleStatuses+`
			`, entityID).Scan(&shortID)
		} else {
			entityID = payload.MaterialID
			entityKind = "material"
			err = l.db.QueryRow(ctx, `
				SELECT m.short_id FROM materials m
				WHERE m.id = $1
				  AND m.fingerprint_status = 'ready'
				  AND m.id NOT IN (SELECT id FROM commercials)
				  AND EXISTS (
				      SELECT 1 FROM campaign_materials cm
				      JOIN campaigns ca ON ca.id = cm.campaign_id
				      WHERE cm.material_id = m.id
				        AND ca.status IN `+indexEligibleStatuses+`
				  )
			`, entityID).Scan(&shortID)
		}
		if err != nil {
			l.log.Warn("index.reload: entity not eligible or not found",
				zap.String("entity_id", entityID),
				zap.String("kind", entityKind),
				zap.Error(err),
			)
			return
		}

		// Fetch all fingerprint hashes for this entity.
		rows, err := l.db.Query(ctx, `
			SELECT hash_value, time_frame, variant_id, rate_id, is_shared
			FROM fingerprint_hashes
			WHERE commercial_id = $1
		`, entityID)
		if err != nil {
			l.log.Error("index.reload: query fingerprint_hashes failed",
				zap.String("entity_id", entityID),
				zap.String("kind", entityKind),
				zap.Error(err),
			)
			return
		}
		defer rows.Close()

		// Collect entries for the reloaded entity.
		type hashEntry struct {
			hash      uint32
			timeFrame int32
			variantID int16
			rateID    int16
			isShared  bool
		}
		var newEntries []hashEntry
		for rows.Next() {
			var hashValue uint32
			var timeFrame int32
			var variantID int16
			var rateID int16
			var isShared bool
			if err := rows.Scan(&hashValue, &timeFrame, &variantID, &rateID, &isShared); err != nil {
				l.log.Error("index.reload: scan row failed",
					zap.String("entity_id", entityID),
					zap.String("kind", entityKind),
					zap.Error(err),
				)
				return
			}
			if variantID < 0 || variantID > 255 || rateID < 0 || rateID > 255 {
				l.log.Error("index.reload: variant_id or rate_id out of uint8 range",
					zap.String("entity_id", entityID),
					zap.String("kind", entityKind),
					zap.Int16("variant_id", variantID),
					zap.Int16("rate_id", rateID),
				)
				return
			}
			newEntries = append(newEntries, hashEntry{hash: hashValue, timeFrame: timeFrame, variantID: variantID, rateID: rateID, isShared: isShared})
		}
		if err := rows.Err(); err != nil {
			l.log.Error("index.reload: iterate rows failed",
				zap.String("entity_id", entityID),
				zap.String("kind", entityKind),
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

		// Remove all existing entries for this entity, then add new ones.
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
				VariantID:         uint8(ne.variantID),
				RateID:            uint8(ne.rateID),
				TimeFrame:         ne.timeFrame,
				IsShared:          ne.isShared,
			})
		}

		l.store.Swap(merged)
		l.log.Info("index reloaded for entity",
			zap.String("entity_id", entityID),
			zap.String("kind", entityKind),
			zap.Int("hashes", len(newEntries)),
		)
	})
	if err != nil {
		return nil, fmt.Errorf("index loader: nats subscribe: %w", err)
	}
	return sub, nil
}
