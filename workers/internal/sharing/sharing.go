// Package sharing detects regions of a commercial's master audio that overlap
// with other commercials in the catalog and flags fingerprint_hashes.is_shared
// for the affected rows on every side. The matching engine uses that flag at
// runtime to ignore shared-hash hits when deciding whether enough unique
// evidence has accumulated to confirm a detection — preventing false-positive
// detections of a commercial whose only matches come from a sting it shares
// with the commercial actually playing.
//
// Detection is by matching-engine simulation, not exact hash-value collision.
// The fingerprint pipeline runs ffmpeg's loudnorm per file, so identical audio
// in two different masters produces *different* hash values. Marking by exact
// value misses ~98% of the genuinely shared content (verified empirically with
// AMBIENTAL 30 / AMBIENTAL JINGLE: 39 of ~2300 hashes by value vs 2295 by
// matching-engine simulation). Running the matcher against the existing index
// captures exactly the regions that would cause a false positive in
// production.
package sharing

import (
	"context"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"radiocheck/internal/fingerprint"
	"radiocheck/internal/index"
	"radiocheck/internal/match"
)

const (
	// WindowSeconds is the analysis window length for the shared-region scan.
	// Aligned with the runtime matcher's analysis window.
	WindowSeconds = 4
	// HopSeconds is the hop between consecutive analysis windows.
	HopSeconds = 1
	// MinScore is the histogram peak score that qualifies a window as a
	// shared region. Aligned with the runtime matcher's default minScore so
	// every region the runtime would credit gets flagged here.
	MinScore = 5
	// SubsetThreshold é a fração de janelas em que o scan precisa bater no
	// "outro" comercial para que o par seja classificado como subset/duplicata
	// em vez de sting compartilhado. ≥ threshold → não flag (deixa a
	// disambiguação por duração decidir). < threshold → flag normal.
	//
	// Calibração: em um corte 30s extraído de um master 60s, o scan do 30s
	// bate no 60s em 100% das janelas. No par AMB30/JINGLE (sting de 6,25s
	// em comerciais de 30s), o scan bate em ~11-15% das janelas. Threshold
	// de 0.5 cobre os dois extremos com folga e dá margem pra cortes
	// mal-alinhados (ex: 27s de overlap em 30s = 90%).
	SubsetThreshold = 0.5
)

// MarkSharedHashes scans the given commercial's master audio against the
// existing fingerprint catalog, identifies regions that overlap with other
// commercials' audio, and updates fingerprint_hashes.is_shared = true for
// every affected row on BOTH sides.
//
// Idempotent: re-running for the same (commercial, audio) state issues no-op
// UPDATEs and keeps already-flagged rows flagged.
//
// Heavy by design — decode + MatchWindow per analysis window. Caller should
// run after fingerprint.Persist; failures are returned but should NOT roll
// back the new commercial. An un-flagged commercial defaults to all-unique
// scoring (the pre-fix behaviour), and the next Persist run picks up the
// missed flagging.
func MarkSharedHashes(ctx context.Context, pool *pgxpool.Pool, commercialID uuid.UUID, masterPath string) error {
	// 1. Resolve the commercial's short_id (used to ignore self-matches when
	//    the index already includes this commercial's own freshly-inserted
	//    hashes).
	var shortID int32
	if err := pool.QueryRow(ctx,
		`SELECT short_id FROM commercials WHERE id = $1`, commercialID,
	).Scan(&shortID); err != nil {
		return fmt.Errorf("sharing: lookup short_id: %w", err)
	}

	// 2. Load the matching index from every ready commercial. This includes
	//    the commercial under scan; we drop self-matches inline.
	idx, shortIDToCommercialID, err := loadCatalogIndex(ctx, pool)
	if err != nil {
		return fmt.Errorf("sharing: load catalog: %w", err)
	}
	if len(idx) == 0 {
		// Empty catalog — first commercial in the system, no overlap possible.
		return nil
	}
	store := index.New()
	store.Swap(idx)

	// 3. Decode PCM through the same pipeline used to generate the stored
	//    fingerprints so the live hashes the scan generates align with the
	//    catalog hashes that came from the same audio.
	pcm, err := fingerprint.DecodePCM(ctx, masterPath, fingerprint.VariantClean)
	if err != nil {
		return fmt.Errorf("sharing: decode master: %w", err)
	}

	// 4. Slide a 4s @ 1s hop window over A's PCM, run MatchWindow against the
	//    catalog index, and accumulate per-(other commercial) ranges plus
	//    window-hit counts. The window-hit counts let step 5 classify each
	//    pair (A, X) as subset/duplicate (skip flagging) or sting (flag).
	scan := scanForSharedRegions(pcm, store, shortID, shortIDToCommercialID, commercialID)

	// 5. Classify each pair (A, X) and produce the final rangesByCommercial.
	//    Pairs whose hit ratio ≥ SubsetThreshold are treated as subset/dup
	//    relationships (where the disambiguation-by-duration layer is the
	//    appropriate defense), so we do NOT flag them as shared.
	rangesByCommercial := classifyAndFilter(scan, SubsetThreshold)
	if len(rangesByCommercial) == 0 {
		return nil
	}

	// 6. Merge overlapping/contiguous frame ranges per commercial, then issue
	//    one UPDATE per merged range.
	for cid, frs := range rangesByCommercial {
		merged := mergeRanges(frs)
		for _, fr := range merged {
			if _, err := pool.Exec(ctx, `
				UPDATE fingerprint_hashes
				SET is_shared = true
				WHERE commercial_id = $1
				  AND time_frame >= $2
				  AND time_frame < $3
				  AND is_shared = false
			`, cid, fr.from, fr.until); err != nil {
				return fmt.Errorf("sharing: flag commercial %s frame [%d,%d): %w",
					cid, fr.from, fr.until, err)
			}
		}
	}
	return nil
}

// frameRange is a half-open interval [from, until) on time_frame.
type frameRange struct{ from, until int32 }

// scanReport agrega os dados de um scan de A contra o catálogo: total de
// janelas analisadas e, por outro comercial X, quantas janelas tiveram hit
// + as ranges (em A e em X) que precisariam ser flagged se o par for sting.
//
// Mantido como tipo nomeado para que o filtro subset/sting seja testável
// isoladamente (sem rodar áudio + DB).
type scanReport struct {
	ownCommercialID uuid.UUID
	totalWindows    int
	perOther        map[uuid.UUID]*perOtherScan
}

type perOtherScan struct {
	windowsWithHits int
	ownRanges       []frameRange
	otherRanges     []frameRange
}

// scanForSharedRegions desliza uma janela de WindowSeconds em hops de
// HopSeconds sobre o PCM de A, busca matches no índice e devolve um
// scanReport com totais por outro comercial. Não toca DB.
func scanForSharedRegions(
	pcm []float32,
	store *index.Store,
	ownShortID int32,
	shortIDToCommercialID map[int32]uuid.UUID,
	ownCommercialID uuid.UUID,
) scanReport {
	const sampleRate = fingerprint.SampleRate
	const stftHopSamples = 2048 // matches pkg/audio STFT hop
	windowSamples := sampleRate * WindowSeconds
	hopSamples := sampleRate * HopSeconds

	report := scanReport{
		ownCommercialID: ownCommercialID,
		perOther:        make(map[uuid.UUID]*perOtherScan),
	}

	for off := 0; off+windowSamples <= len(pcm); off += hopSamples {
		report.totalWindows++
		window := pcm[off : off+windowSamples]
		results := match.MatchWindow(window, store, MinScore, 0.0)

		ownStartFrame := int32(off / stftHopSamples)
		ownEndFrame := int32((off + windowSamples) / stftHopSamples)

		seenThisWindow := make(map[uuid.UUID]bool)
		for _, r := range results {
			if r.CommercialShortID == ownShortID {
				continue // self-match
			}
			otherID, ok := shortIDToCommercialID[r.CommercialShortID]
			if !ok {
				continue // catalog inconsistency — skip safely
			}

			scan := report.perOther[otherID]
			if scan == nil {
				scan = &perOtherScan{}
				report.perOther[otherID] = scan
			}
			if !seenThisWindow[otherID] {
				scan.windowsWithHits++
				seenThisWindow[otherID] = true
			}

			scan.ownRanges = append(scan.ownRanges, frameRange{ownStartFrame, ownEndFrame})

			// Other commercial's range: live_time_frame - entry.TimeFrame =
			// OffsetFrames, so entry.TimeFrame = live_time_frame -
			// OffsetFrames. The window covers live frames
			// [ownStartFrame, ownEndFrame].
			xStart := int32(int(ownStartFrame) - r.OffsetFrames)
			xEnd := int32(int(ownEndFrame) - r.OffsetFrames)
			if xStart > xEnd {
				xStart, xEnd = xEnd, xStart
			}
			if xEnd <= 0 {
				continue
			}
			if xStart < 0 {
				xStart = 0
			}
			scan.otherRanges = append(scan.otherRanges, frameRange{xStart, xEnd})
		}
	}
	return report
}

// classifyAndFilter consome um scanReport e devolve o mapa final
// commercial_id → ranges a flagar como is_shared.
//
// Regra: para cada outro comercial X, calcula `fraction = windowsWithHits /
// totalWindows`. Se fraction ≥ subsetThreshold, o par (A, X) é interpretado
// como relação subset/duplicata (ex: corte 30s extraído do master 60s) e
// **não é flagado** — a desambiguação por maior duração no supervisor é
// a defesa apropriada nesse caso. Se fraction < subsetThreshold, é um
// sting compartilhado em comerciais distintos — flag normal.
//
// O caso "duplicata total" (ambos comerciais ≥ subsetThreshold um do outro)
// resulta em nenhum flag por nenhum dos dois scans — comportamento desejado,
// já que duas duplicatas confirmariam ambas e o operador deve remover a
// extra do catálogo. Detectável via SQL de auditoria (shared_pct nas duas
// linhas).
func classifyAndFilter(report scanReport, subsetThreshold float64) map[uuid.UUID][]frameRange {
	out := make(map[uuid.UUID][]frameRange)
	if report.totalWindows == 0 {
		return out
	}
	for otherID, scan := range report.perOther {
		fraction := float64(scan.windowsWithHits) / float64(report.totalWindows)
		if fraction >= subsetThreshold {
			// Subset/duplicate — não flag.
			continue
		}
		out[report.ownCommercialID] = append(out[report.ownCommercialID], scan.ownRanges...)
		out[otherID] = append(out[otherID], scan.otherRanges...)
	}
	return out
}

// loadCatalogIndex loads every fingerprint_hash for ready commercials into an
// index, plus a short_id → commercial_id map so the scan can translate match
// results back to the FK identity needed for UPDATEs.
//
// We load every status here (not just programada/ativa as the runtime loader
// does): the shared-hash flag is a permanent property of the master and we
// want to catch overlap with completed/cancelled campaigns too — they may
// be reactivated later, and the flag is cheap to set even if currently unused.
func loadCatalogIndex(ctx context.Context, pool *pgxpool.Pool) (index.Index, map[int32]uuid.UUID, error) {
	rows, err := pool.Query(ctx, `
		SELECT fh.hash_value, fh.time_frame, fh.variant_id, fh.rate_id, c.short_id, c.id
		FROM fingerprint_hashes fh
		JOIN commercials c ON c.id = fh.commercial_id
		WHERE c.fingerprint_status = 'ready'
	`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	idx := make(index.Index)
	shortIDToID := make(map[int32]uuid.UUID)
	for rows.Next() {
		var hashValue uint32
		var timeFrame int32
		var variantID, rateID int16
		var shortID int32
		var commercialID uuid.UUID
		if err := rows.Scan(&hashValue, &timeFrame, &variantID, &rateID, &shortID, &commercialID); err != nil {
			return nil, nil, err
		}
		if variantID < 0 || variantID > 255 || rateID < 0 || rateID > 255 {
			return nil, nil, fmt.Errorf("sharing: variant_id=%d or rate_id=%d out of uint8 range",
				variantID, rateID)
		}
		idx[hashValue] = append(idx[hashValue], index.Entry{
			CommercialShortID: shortID,
			VariantID:         uint8(variantID),
			RateID:            uint8(rateID),
			TimeFrame:         timeFrame,
			// IsShared is intentionally left false here: the scan re-derives
			// sharing from scratch, and we don't want stale flags to bias the
			// MatchWindow scoring (UniqueScore would change the scan's
			// detection behaviour vs the runtime matcher).
		})
		shortIDToID[shortID] = commercialID
	}
	return idx, shortIDToID, rows.Err()
}

// mergeRanges merges overlapping or contiguous frame ranges into a minimal
// covering set so we issue O(unique-regions) UPDATEs instead of one per
// analysis window.
func mergeRanges(rs []frameRange) []frameRange {
	if len(rs) == 0 {
		return nil
	}
	sort.Slice(rs, func(i, j int) bool { return rs[i].from < rs[j].from })
	merged := []frameRange{rs[0]}
	for _, r := range rs[1:] {
		last := &merged[len(merged)-1]
		if r.from <= last.until {
			if r.until > last.until {
				last.until = r.until
			}
		} else {
			merged = append(merged, r)
		}
	}
	return merged
}
