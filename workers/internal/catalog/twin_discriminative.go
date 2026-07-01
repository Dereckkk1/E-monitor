package catalog

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"radiocheck/internal/audit"
)

// complementRanges returns [0,total) minus the union of overlap ranges — the
// frames of the master that are NOT shared with its twin (the discriminative
// region). overlap ranges are sorted and clamped to [0,total); ranges past the
// end (window straddle) shrink the result but never introduce shared frames, so
// the complement is always a subset of the true differing region (conservative).
// Returns nil when nothing differs (full overlap => pair not separable by audio).
func complementRanges(overlap []audit.FrameRange, total int) []audit.FrameRange {
	clamp := func(v, lo, hi int32) int32 {
		if v < lo {
			return lo
		}
		if v > hi {
			return hi
		}
		return v
	}
	totalF := int32(total)

	// sort a COPY by Lo (don't mutate the caller's slice).
	sorted := make([]audit.FrameRange, len(overlap))
	copy(sorted, overlap)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Lo < sorted[j].Lo })

	var out []audit.FrameRange
	cursor := int32(0)
	for _, r := range sorted {
		lo := clamp(r.Lo, 0, totalF)
		hi := clamp(r.Hi, 0, totalF)
		if lo > cursor {
			out = append(out, audit.FrameRange{Lo: cursor, Hi: lo})
		}
		if hi > cursor {
			cursor = hi
		}
	}
	if cursor < totalF {
		out = append(out, audit.FrameRange{Lo: cursor, Hi: totalF})
	}
	return out
}

// sumFrames totals the frame length of a set of ranges.
func sumFrames(rs []audit.FrameRange) int {
	total := 0
	for _, r := range rs {
		total += int(r.Hi - r.Lo)
	}
	return total
}

// TwinDiscriminative persists, per acoustic-twin pair, the discriminative region
// of a material vs its twin (the frames unique to the material), stored flattened
// as INT[] in table material_twin_discriminative (migration 0046).
type TwinDiscriminative struct{ pool *pgxpool.Pool }

func NewTwinDiscriminative(pool *pgxpool.Pool) *TwinDiscriminative {
	return &TwinDiscriminative{pool: pool}
}

// Upsert stores the discriminative region of material vs twin. disc_ranges is
// flattened to INT[] as [lo0,hi0,lo1,hi1,...]. discFrames is the total
// discriminative frame count (0 => pair not separable by audio).
func (r *TwinDiscriminative) Upsert(ctx context.Context, materialID, twinID uuid.UUID, disc []audit.FrameRange, discFrames int) error {
	flat := make([]int32, 0, len(disc)*2)
	for _, fr := range disc {
		flat = append(flat, fr.Lo, fr.Hi)
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO material_twin_discriminative (material_id, twin_id, disc_ranges, disc_frames)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (material_id, twin_id)
		DO UPDATE SET disc_ranges = EXCLUDED.disc_ranges,
		              disc_frames = EXCLUDED.disc_frames,
		              updated_at = now()
	`, materialID, twinID, flat, discFrames)
	if err != nil {
		return fmt.Errorf("catalog: upsert twin discriminative: %w", err)
	}
	return nil
}

// Get returns the discriminative ranges + frame count for (material, twin), or
// (nil, 0, nil) if the pair has no stored row.
func (r *TwinDiscriminative) Get(ctx context.Context, materialID, twinID uuid.UUID) ([]audit.FrameRange, int, error) {
	var flat []int32
	var frames int
	err := r.pool.QueryRow(ctx, `
		SELECT disc_ranges, disc_frames FROM material_twin_discriminative
		WHERE material_id = $1 AND twin_id = $2
	`, materialID, twinID).Scan(&flat, &frames)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, fmt.Errorf("catalog: get twin discriminative: %w", err)
	}
	out := make([]audit.FrameRange, 0, len(flat)/2)
	for i := 0; i+1 < len(flat); i += 2 {
		out = append(out, audit.FrameRange{Lo: flat[i], Hi: flat[i+1]})
	}
	if len(out) == 0 {
		out = nil
	}
	return out, frames, nil
}
