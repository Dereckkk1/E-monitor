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
	"errors"
	"fmt"
	"math"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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
	// SubsetThreshold é a fração de frames de áudio coberta pela região
	// compartilhada (em qualquer um dos dois lados do par) que separa subset
	// de sting. A classificação usa max(ownCoverage, otherCoverage) porque
	// pares assimétricos (pequeno ⊂ grande) só denunciam o subset olhando
	// para o lado *menor* — o lado grande sempre vê só uma fração pequena
	// de janelas batendo no pequeno.
	//
	// Calibração:
	//   - VERÃO 30 ⊂ VERÃO 60: own=100% (todo o 30s), other=50% (metade do 60s) → max=100% → subset
	//   - PULSO (7s) ⊂ X (30s): own=100% (todo o PULSO), other=23% (7s/30s) → max=100% → subset
	//   - AMB30/JINGLE sting 6,25s em 30s: own=20%, other=20% → max=20% → sting (flag normal)
	// Threshold 0.5 dá margem confortável para os dois extremos.
	SubsetThreshold = 0.5
	// MinShareableDurationSeconds é a duração mínima (em segundos) que um
	// comercial precisa ter para participar do shared-hash flagging. Comerciais
	// mais curtos têm poucas janelas de análise distintas (PULSO de 7s gera
	// apenas 4 janelas em 4s @ 1s hop) e UMA única janela já cobre >50% da
	// duração total, o que impede a classificação subset/sting bidirecional
	// de funcionar com a resolução necessária.
	//
	// Além disso, matches de outros comerciais contra um comercial curto
	// podem produzir `xRange` calculado fora dos bounds do comercial pequeno
	// — gerando fatias finas (e.g. [0, 7] frames) que individualmente passam
	// abaixo do SubsetThreshold mas, cumulativamente entre múltiplos scans,
	// flagam quase tudo do comercial vítima. Sintoma observado em prod com
	// RÔGGA PULSO SONORO (~67% flagged depois do backfill).
	//
	// Para comerciais < MinShareableDurationSeconds, a defesa shared-hash
	// é pulada **dos dois lados** (não flaga o curto e não usa ele para
	// flagar os outros). O caso de conflito real (X de 30s toca, e contém
	// o áudio de PULSO de 7s) cai pra defesa de
	// `version-disambiguation` — supervisor retrata PULSO em favor de X
	// pela regra de maior duração no momento da confirmação.
	MinShareableDurationSeconds = 10.0
)

// MinShareableDurationFrames é MinShareableDurationSeconds convertido para
// frames usando a mesma fórmula que o resto do pipeline
// (sampleRate / stftHopSamples = 16000 / 2048 ≈ 7,8125 frames/s).
// Computado em init() porque Go não converte uma constante float fracionária
// para int em tempo de compilação.
var MinShareableDurationFrames int

func init() {
	// math.Floor quebra a redução em constante de tempo de compilação que
	// Go faria com a expressão pura; o resultado ainda é 78 para 10s.
	MinShareableDurationFrames = int(math.Floor(MinShareableDurationSeconds * float64(fingerprint.SampleRate) / 2048.0))
}

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
	// 1. Resolve the entity's short_id (used to ignore self-matches when the
	//    index already includes its own freshly-inserted hashes). The entity
	//    may be a commercial OR a material — try commercials first, fall back
	//    to materials. This mirrors the rest of the pipeline (Phase 4 index
	//    loader UNION) where backfilled materials are reached via the
	//    commercials path and net-new materials via the materials path.
	var shortID int32
	err := pool.QueryRow(ctx,
		`SELECT short_id FROM commercials WHERE id = $1`, commercialID,
	).Scan(&shortID)
	if errors.Is(err, pgx.ErrNoRows) {
		err = pool.QueryRow(ctx,
			`SELECT short_id FROM materials WHERE id = $1`, commercialID,
		).Scan(&shortID)
	}
	if err != nil {
		return fmt.Errorf("sharing: lookup short_id: %w", err)
	}

	// 2. Load the matching index from every ready commercial. This includes
	//    the commercial under scan; we drop self-matches inline.
	idx, shortIDToCommercialID, totalFramesByID, err := loadCatalogIndex(ctx, pool)
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
	//    catalog index, and accumulate per-(other commercial) ranges + the
	//    own/other total frame counts that step 5 needs to compute coverage
	//    on both sides of each pair.
	scan := scanForSharedRegions(pcm, store, shortID, shortIDToCommercialID, commercialID, totalFramesByID)

	// 5. Classify each pair (A, X) and produce the final rangesByCommercial.
	//    A pair is "subset" when the matched audio covers ≥ SubsetThreshold
	//    of *either* commercial — typical of cuts of the same master. Subset
	//    pairs are NOT flagged (disambiguation-by-duration is the correct
	//    defense). Below threshold on both sides = sting → flag normal.
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

// scanReport agrega os dados de um scan de A contra o catálogo: o tamanho
// total de A em frames e, por outro comercial X, as ranges em A e em X
// + tamanho total de X. Tudo o que `classifyAndFilter` precisa para decidir
// subset vs sting de forma simétrica.
//
// Mantido como tipo nomeado para que o filtro subset/sting seja testável
// isoladamente (sem rodar áudio + DB).
type scanReport struct {
	ownCommercialID uuid.UUID
	ownTotalFrames  int
	perOther        map[uuid.UUID]*perOtherScan
}

type perOtherScan struct {
	otherTotalFrames int
	ownRanges        []frameRange
	otherRanges      []frameRange
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
	totalFramesByID map[uuid.UUID]int,
) scanReport {
	const sampleRate = fingerprint.SampleRate
	const stftHopSamples = 2048 // matches pkg/audio STFT hop
	windowSamples := sampleRate * WindowSeconds
	hopSamples := sampleRate * HopSeconds

	report := scanReport{
		ownCommercialID: ownCommercialID,
		ownTotalFrames:  len(pcm) / stftHopSamples,
		perOther:        make(map[uuid.UUID]*perOtherScan),
	}

	for off := 0; off+windowSamples <= len(pcm); off += hopSamples {
		window := pcm[off : off+windowSamples]
		results := match.MatchWindow(window, store, MinScore, 0.0)

		ownStartFrame := int32(off / stftHopSamples)
		ownEndFrame := int32((off + windowSamples) / stftHopSamples)

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
				scan = &perOtherScan{otherTotalFrames: totalFramesByID[otherID]}
				report.perOther[otherID] = scan
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

// frameCoverage returns the total number of frames covered by the union of
// the input ranges (i.e. the merged length).
func frameCoverage(rs []frameRange) int {
	if len(rs) == 0 {
		return 0
	}
	merged := mergeRanges(rs)
	total := 0
	for _, r := range merged {
		total += int(r.until - r.from)
	}
	return total
}

// classifyAndFilter consome um scanReport e devolve o mapa final
// commercial_id → ranges a flagar como is_shared.
//
// Regra (simétrica): para cada outro comercial X, calcula
//
//	ownCov   = (frames de A cobertos pelos matches) / ownTotalFrames
//	otherCov = (frames de X cobertos pelos matches) / otherTotalFrames
//	score    = max(ownCov, otherCov)
//
// Se score ≥ subsetThreshold → subset/duplicata (não flag — deixa a
// disambiguação por duração resolver). Caso contrário → sting → flag normal.
//
// Por que max e não cada lado independente: pares assimétricos (pequeno ⊂
// grande) só revelam a relação subset pelo lado *menor* — o lado grande vê
// só uma fatia pequena de hits. Pegar o máximo garante que basta um dos
// dois lados estar fortemente coberto pra evitar o flag indevido.
//
// O caso "duplicata total" (ambos os lados ≈ 100%) resulta em nenhum flag —
// comportamento desejado, já que duas duplicatas confirmariam ambas e o
// operador deve remover a extra do catálogo. Detectável via SQL de
// auditoria (shared_pct = 0 nas duas linhas).
func classifyAndFilter(report scanReport, subsetThreshold float64) map[uuid.UUID][]frameRange {
	out := make(map[uuid.UUID][]frameRange)
	if report.ownTotalFrames == 0 {
		return out
	}
	// Comerciais curtos não participam do shared-hash flagging (ver doc da
	// constante MinShareableDurationSeconds). Pula a scan inteira se o
	// próprio comercial é curto demais.
	if report.ownTotalFrames < MinShareableDurationFrames {
		return out
	}
	for otherID, scan := range report.perOther {
		// Pula pares onde o **outro** comercial é curto demais — evita
		// flagar fatias finas espúrias num vinheta/sting vítima e mantém
		// a simetria do skip dos dois lados.
		if scan.otherTotalFrames < MinShareableDurationFrames {
			continue
		}
		ownCov := float64(frameCoverage(scan.ownRanges)) / float64(report.ownTotalFrames)
		var otherCov float64
		if scan.otherTotalFrames > 0 {
			otherCov = float64(frameCoverage(scan.otherRanges)) / float64(scan.otherTotalFrames)
		}
		score := ownCov
		if otherCov > score {
			score = otherCov
		}
		if score >= subsetThreshold {
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
// Also returns totalFramesByID, the total frame count per commercial derived
// from commercials.duration_seconds (sampleRate / stftHop = 16000 / 2048 =
// 7.8125 frames per second). The classifier needs this to compute coverage
// of the matched ranges on the *other* side of each pair.
//
// We load every status here (not just programada/ativa as the runtime loader
// does): the shared-hash flag is a permanent property of the master and we
// want to catch overlap with completed/cancelled campaigns too — they may
// be reactivated later, and the flag is cheap to set even if currently unused.
func loadCatalogIndex(ctx context.Context, pool *pgxpool.Pool) (index.Index, map[int32]uuid.UUID, map[uuid.UUID]int, error) {
	rows, err := pool.Query(ctx, `
		-- Path 1: commercials.
		SELECT fh.hash_value, fh.time_frame, fh.variant_id, fh.rate_id,
		       c.short_id, c.id, c.duration_seconds
		FROM fingerprint_hashes fh
		JOIN commercials c ON c.id = fh.commercial_id
		WHERE c.fingerprint_status = 'ready'

		UNION ALL

		-- Path 2: materials (new uploads not backfilled into commercials).
		-- Loaded regardless of campaign link status — is_shared is a permanent
		-- property of the master, see the file-level comment above.
		SELECT fh.hash_value, fh.time_frame, fh.variant_id, fh.rate_id,
		       m.short_id, m.id, m.duration_seconds
		FROM fingerprint_hashes fh
		JOIN materials m ON m.id = fh.commercial_id
		WHERE m.fingerprint_status = 'ready'
		  AND m.id NOT IN (SELECT id FROM commercials)
	`)
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows.Close()

	idx := make(index.Index)
	shortIDToID := make(map[int32]uuid.UUID)
	totalFramesByID := make(map[uuid.UUID]int)
	for rows.Next() {
		var hashValue uint32
		var timeFrame int32
		var variantID, rateID int16
		var shortID int32
		var commercialID uuid.UUID
		var durationSec float64
		if err := rows.Scan(&hashValue, &timeFrame, &variantID, &rateID, &shortID, &commercialID, &durationSec); err != nil {
			return nil, nil, nil, err
		}
		if variantID < 0 || variantID > 255 || rateID < 0 || rateID > 255 {
			return nil, nil, nil, fmt.Errorf("sharing: variant_id=%d or rate_id=%d out of uint8 range",
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
		// duration_seconds * (sampleRate / stftHopSamples). Idempotent across
		// rows for the same commercial — last write wins but all rows agree.
		totalFramesByID[commercialID] = int(durationSec * float64(fingerprint.SampleRate) / 2048.0)
	}
	return idx, shortIDToID, totalFramesByID, rows.Err()
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
