// diag is an offline diagnostic tool: it loads the live fingerprint index from
// Postgres, decodes an audio file via ffmpeg with the same parameters the
// stream worker uses, slides a 4s window across the PCM and prints the top
// match score per window plus a per-commercial summary at the end.
//
// Usage: diag <audio-file>
package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"log"
	"math"
	"os"
	"os/exec"
	"sort"

	"github.com/jackc/pgx/v5/pgxpool"

	"radiocheck/internal/index"
	"radiocheck/internal/match"
)

const (
	sampleRate = 16000
	windowSize = 4 * sampleRate // 4s
	stepSize   = sampleRate / 2 // 0.5s slide
)

func main() {
	if len(os.Args) < 2 {
		log.Fatalf("usage: %s <audio-file>", os.Args[0])
	}
	path := os.Args[1]

	ctx := context.Background()

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL not set")
	}

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatalf("db connect: %v", err)
	}
	defer pool.Close()

	store, names, err := loadIndex(ctx, pool)
	if err != nil {
		log.Fatalf("load index: %v", err)
	}
	idx := store.Load()
	fmt.Printf("index loaded: %d distinct hashes, %d commercials\n", len(idx), len(names))

	pcm, err := decodePCM(path)
	if err != nil {
		log.Fatalf("decode: %v", err)
	}
	fmt.Printf("decoded %d samples (%.1fs)\n", len(pcm), float64(len(pcm))/sampleRate)

	if len(pcm) < windowSize {
		log.Fatalf("file shorter than 4s window")
	}

	// Per-commercial best score across all windows.
	best := make(map[int32]int)
	bestVariant := make(map[int32]uint8)
	bestOffsetSec := make(map[int32]float64)

	fmt.Printf("\n%-7s %-7s %-15s %s\n", "t(s)", "top", "top_id", "title")
	fmt.Println("-----------------------------------------------------")

	windowsScanned := 0
	for offset := 0; offset+windowSize <= len(pcm); offset += stepSize {
		win := pcm[offset : offset+windowSize]
		results := match.MatchWindow(win, store, 3, 0.0) // coverage=0 to surface anything
		// Update best per commercial
		for _, r := range results {
			if r.Score > best[r.CommercialShortID] {
				best[r.CommercialShortID] = r.Score
				bestVariant[r.CommercialShortID] = r.VariantID
				bestOffsetSec[r.CommercialShortID] = float64(offset) / sampleRate
			}
		}

		// Top score across all commercials in this window for the timeline.
		scores := match.ScanScores(win, store)
		var topID int32
		topScore := 0
		for id, sc := range scores {
			if sc > topScore {
				topScore = sc
				topID = id
			}
		}
		// Update per-commercial best from raw scan too (in case threshold filtered).
		for id, sc := range scores {
			if sc > best[id] {
				best[id] = sc
				bestOffsetSec[id] = float64(offset) / sampleRate
			}
		}
		if topScore >= 5 {
			fmt.Printf("%-7.2f %-7d %-15d %s\n", float64(offset)/sampleRate, topScore, topID, names[topID])
		}
		windowsScanned++
	}
	fmt.Printf("\nscanned %d windows\n\n", windowsScanned)

	// Per-commercial summary, sorted by score desc.
	type row struct {
		ID      int32
		Title   string
		Score   int
		Variant uint8
		OffSec  float64
	}
	var rows []row
	for id, sc := range best {
		rows = append(rows, row{id, names[id], sc, bestVariant[id], bestOffsetSec[id]})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Score > rows[j].Score })

	fmt.Println("=== best match per commercial ===")
	fmt.Printf("%-5s %-7s %-7s %-7s %s\n", "id", "score", "var", "t(s)", "title")
	fmt.Println("-----------------------------------------------------")
	for _, r := range rows {
		fmt.Printf("%-5d %-7d %-7d %-7.2f %s\n", r.ID, r.Score, r.Variant, r.OffSec, r.Title)
	}
}

// loadIndex pulls every fingerprint hash for ready commercials in active
// campaigns, identical to the production loader query.
func loadIndex(ctx context.Context, pool *pgxpool.Pool) (*index.Store, map[int32]string, error) {
	rows, err := pool.Query(ctx, `
		SELECT fh.hash_value, fh.time_frame, fh.variant_id, fh.rate_id, c.short_id, c.title
		FROM fingerprint_hashes fh
		JOIN commercials c  ON c.id  = fh.commercial_id
		JOIN campaigns   ca ON ca.id = c.campaign_id
		WHERE c.fingerprint_status = 'ready'
		  AND ca.status = 'ativa'
	`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	newIdx := make(index.Index)
	names := make(map[int32]string)
	for rows.Next() {
		var hashValue uint32
		var timeFrame int32
		var variantID, rateID int16
		var shortID int32
		var title string
		if err := rows.Scan(&hashValue, &timeFrame, &variantID, &rateID, &shortID, &title); err != nil {
			return nil, nil, err
		}
		newIdx[hashValue] = append(newIdx[hashValue], index.Entry{
			CommercialShortID: shortID,
			VariantID:         uint8(variantID),
			RateID:            uint8(rateID),
			TimeFrame:         timeFrame,
		})
		names[shortID] = title
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	store := index.New()
	store.Swap(newIdx)
	return store, names, nil
}

// decodePCM runs ffmpeg with the same params as the stream worker to convert
// any audio file into a float32 mono 16kHz PCM slice.
func decodePCM(path string) ([]float32, error) {
	cmd := exec.Command("ffmpeg",
		"-hide_banner", "-loglevel", "error",
		"-i", path,
		"-ac", "1", "-ar", fmt.Sprintf("%d", sampleRate),
		"-f", "f32le", "-c:a", "pcm_f32le",
		"-",
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg: %w (stderr: %s)", err, stderr.String())
	}
	if len(out)%4 != 0 {
		return nil, fmt.Errorf("ffmpeg output not aligned to float32: %d bytes", len(out))
	}
	pcm := make([]float32, len(out)/4)
	for i := range pcm {
		bits := binary.LittleEndian.Uint32(out[i*4:])
		pcm[i] = math.Float32frombits(bits)
	}
	return pcm, nil
}
