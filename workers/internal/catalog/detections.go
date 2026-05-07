package catalog

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Detection struct {
	ID                 uuid.UUID  `json:"id"`
	StationID          uuid.UUID  `json:"station_id"`
	StationName        string     `json:"station_name"`
	CommercialID       uuid.UUID  `json:"commercial_id"`
	CommercialName     string     `json:"commercial_name"`
	CampaignID         uuid.UUID  `json:"campaign_id"`
	DetectedAt         time.Time  `json:"detected_at"`
	MatchStartOffsetMs int32      `json:"match_start_offset_ms"`
	MatchEndOffsetMs   int32      `json:"match_end_offset_ms"`
	Confidence         float64    `json:"confidence"`
	HashCount          int32      `json:"hash_count"`
	TemporalCoverage   *float64   `json:"temporal_coverage,omitempty"`
	VariantUsed        *int16     `json:"variant_used,omitempty"`
	RateUsed           *int16     `json:"rate_used,omitempty"`
	EvidenceStatus     string     `json:"evidence_status"`
	EvidenceKey        *string    `json:"evidence_key,omitempty"`
	EvidenceSizeBytes  *int64     `json:"evidence_size_bytes,omitempty"`
	// RetractedAt is set when §18.2.2 disambiguation overruled this row in
	// favour of a longer cut from the same client; nil otherwise.
	RetractedAt *time.Time `json:"retracted_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

type Detections struct {
	pool *pgxpool.Pool
}

func NewDetections(pool *pgxpool.Pool) *Detections {
	return &Detections{pool: pool}
}

type CreateDetectionInput struct {
	StationID          uuid.UUID
	CommercialID       uuid.UUID
	CampaignID         uuid.UUID
	DetectedAt         time.Time
	MatchStartOffsetMs int32
	MatchEndOffsetMs   int32
	Confidence         float64
	HashCount          int32
	TemporalCoverage   float64
	VariantUsed        int16
	RateUsed           int16
}

func (d *Detections) Create(ctx context.Context, in CreateDetectionInput) (*Detection, error) {
	var det Detection
	err := d.pool.QueryRow(ctx, `
		INSERT INTO detections (station_id, commercial_id, campaign_id, detected_at,
		                        match_start_offset_ms, match_end_offset_ms, confidence,
		                        hash_count, temporal_coverage, variant_used, rate_used)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING id, station_id, commercial_id, campaign_id, detected_at,
		          match_start_offset_ms, match_end_offset_ms, confidence, hash_count,
		          temporal_coverage, variant_used, rate_used,
		          evidence_status, evidence_key, evidence_size_bytes, retracted_at, created_at`,
		in.StationID, in.CommercialID, in.CampaignID, in.DetectedAt,
		in.MatchStartOffsetMs, in.MatchEndOffsetMs, in.Confidence, in.HashCount,
		in.TemporalCoverage, in.VariantUsed, in.RateUsed,
	).Scan(&det.ID, &det.StationID, &det.CommercialID, &det.CampaignID, &det.DetectedAt,
		&det.MatchStartOffsetMs, &det.MatchEndOffsetMs, &det.Confidence, &det.HashCount,
		&det.TemporalCoverage, &det.VariantUsed, &det.RateUsed,
		&det.EvidenceStatus, &det.EvidenceKey, &det.EvidenceSizeBytes, &det.RetractedAt, &det.CreatedAt)
	return &det, err
}

func (d *Detections) UpdateEvidence(ctx context.Context, id uuid.UUID, detectedAt time.Time,
	status, key string, sizeBytes int64) error {
	_, err := d.pool.Exec(ctx, `
		UPDATE detections
		SET evidence_status = $3, evidence_key = $4, evidence_size_bytes = $5
		WHERE id = $1 AND detected_at = $2`,
		id, detectedAt, status, key, sizeBytes)
	return err
}

type ListFilter struct {
	CampaignID *uuid.UUID
	StationID  *uuid.UUID
	StartDate  *time.Time
	EndDate    *time.Time
	Limit      int
	Offset     int
}

func (d *Detections) List(ctx context.Context, f ListFilter) ([]Detection, error) {
	if f.Limit <= 0 || f.Limit > 1000 {
		f.Limit = 100
	}
	rows, err := d.pool.Query(ctx, `
		SELECT d.id, d.station_id, COALESCE(s.name, ''), d.commercial_id, COALESCE(c.title, ''),
		       d.campaign_id, d.detected_at,
		       d.match_start_offset_ms, d.match_end_offset_ms, d.confidence, d.hash_count,
		       d.temporal_coverage, d.variant_used, d.rate_used,
		       d.evidence_status, d.evidence_key, d.evidence_size_bytes, d.retracted_at, d.created_at
		FROM detections d
		LEFT JOIN stations s ON s.id = d.station_id
		LEFT JOIN commercials c ON c.id = d.commercial_id
		WHERE ($1::uuid IS NULL OR d.campaign_id = $1)
		  AND ($2::uuid IS NULL OR d.station_id = $2)
		  AND ($3::timestamptz IS NULL OR d.detected_at >= $3)
		  AND ($4::timestamptz IS NULL OR d.detected_at <= $4)
		ORDER BY d.detected_at DESC
		LIMIT $5 OFFSET $6`,
		f.CampaignID, f.StationID, f.StartDate, f.EndDate, f.Limit, f.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Detection
	for rows.Next() {
		var det Detection
		if err := rows.Scan(&det.ID, &det.StationID, &det.StationName, &det.CommercialID, &det.CommercialName,
			&det.CampaignID, &det.DetectedAt, &det.MatchStartOffsetMs, &det.MatchEndOffsetMs,
			&det.Confidence, &det.HashCount, &det.TemporalCoverage, &det.VariantUsed,
			&det.RateUsed, &det.EvidenceStatus, &det.EvidenceKey,
			&det.EvidenceSizeBytes, &det.RetractedAt, &det.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, det)
	}
	return out, rows.Err()
}

func (d *Detections) Get(ctx context.Context, id uuid.UUID) (*Detection, error) {
	var det Detection
	err := d.pool.QueryRow(ctx, `
		SELECT d.id, d.station_id, COALESCE(s.name, ''), d.commercial_id, COALESCE(c.title, ''),
		       d.campaign_id, d.detected_at,
		       d.match_start_offset_ms, d.match_end_offset_ms, d.confidence, d.hash_count,
		       d.temporal_coverage, d.variant_used, d.rate_used,
		       d.evidence_status, d.evidence_key, d.evidence_size_bytes, d.retracted_at, d.created_at
		FROM detections d
		LEFT JOIN stations s ON s.id = d.station_id
		LEFT JOIN commercials c ON c.id = d.commercial_id
		WHERE d.id = $1`, id,
	).Scan(&det.ID, &det.StationID, &det.StationName, &det.CommercialID, &det.CommercialName,
		&det.CampaignID, &det.DetectedAt,
		&det.MatchStartOffsetMs, &det.MatchEndOffsetMs, &det.Confidence, &det.HashCount,
		&det.TemporalCoverage, &det.VariantUsed, &det.RateUsed,
		&det.EvidenceStatus, &det.EvidenceKey, &det.EvidenceSizeBytes, &det.RetractedAt, &det.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &det, nil
}
