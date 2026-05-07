package calibration

import (
	"context"
	"math"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// RecordNoiseSample appends the window's max hash count to noise_samples for
// stations still in calibration mode. No-op if calibration_mode is false.
func RecordNoiseSample(ctx context.Context, db *pgxpool.Pool, stationID uuid.UUID, hashCount int) error {
	_, err := db.Exec(ctx, `
		UPDATE station_thresholds
		SET noise_samples = CASE
		        WHEN array_length(noise_samples, 1) >= 5000 THEN noise_samples
		        ELSE array_append(noise_samples, $2::float)
		    END,
		    updated_at = NOW()
		WHERE station_id = $1 AND calibration_mode = true
	`, stationID, float64(hashCount))
	return err
}

// RunCalibrationJob checks all stations in calibration mode for 7+ days,
// computes noise_p99, sets min_hashes = max(p99*1.5, 5), and exits calibration.
func RunCalibrationJob(ctx context.Context, db *pgxpool.Pool, log *zap.Logger) error {
	rows, err := db.Query(ctx, `
		SELECT station_id, noise_samples
		FROM station_thresholds
		WHERE calibration_mode = true
		  AND calibration_started_at < NOW() - INTERVAL '7 days'
		  AND array_length(noise_samples, 1) > 0
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var stationID uuid.UUID
		var samples []float64
		if err := rows.Scan(&stationID, &samples); err != nil {
			log.Warn("calibration: scan failed", zap.Error(err))
			continue
		}
		p99 := percentile99(samples)
		minHashes := int(math.Max(p99*1.5, 5))

		if _, err := db.Exec(ctx, `
			UPDATE station_thresholds
			SET calibration_mode = false,
			    noise_p99 = $2,
			    min_hashes = $3,
			    updated_at = NOW()
			WHERE station_id = $1
		`, stationID, p99, minHashes); err != nil {
			log.Warn("calibration: update failed",
				zap.String("station_id", stationID.String()), zap.Error(err))
			continue
		}

		log.Info("calibration: complete",
			zap.String("station_id", stationID.String()),
			zap.Float64("noise_p99", p99),
			zap.Int("min_hashes", minHashes),
		)
	}
	return rows.Err()
}

func percentile99(data []float64) float64 {
	if len(data) == 0 {
		return 5
	}
	sorted := make([]float64, len(data))
	copy(sorted, data)
	sort.Float64s(sorted)
	idx := int(math.Ceil(0.99*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	return sorted[idx]
}
