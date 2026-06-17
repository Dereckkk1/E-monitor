package catalog

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"radiocheck/internal/categorizer"
)

type Detection struct {
	ID                 uuid.UUID `json:"id"`
	StationID          uuid.UUID `json:"station_id"`
	StationName        string    `json:"station_name"`
	CommercialID       uuid.UUID `json:"commercial_id"`
	CommercialName     string    `json:"commercial_name"`
	CampaignID         uuid.UUID `json:"campaign_id"`
	DetectedAt         time.Time `json:"detected_at"`
	MatchStartOffsetMs int32     `json:"match_start_offset_ms"`
	MatchEndOffsetMs   int32     `json:"match_end_offset_ms"`
	Confidence         float64   `json:"confidence"`
	HashCount          int32     `json:"hash_count"`
	TemporalCoverage   *float64  `json:"temporal_coverage,omitempty"`
	VariantUsed        *int16    `json:"variant_used,omitempty"`
	RateUsed           *int16    `json:"rate_used,omitempty"`
	EvidenceStatus     string    `json:"evidence_status"`
	EvidenceKey        *string   `json:"evidence_key,omitempty"`
	EvidenceSizeBytes  *int64    `json:"evidence_size_bytes,omitempty"`
	// Category is one of in_slot|out_slot|out_date|orphan (migration 0018).
	// Consumed by the DayDetailModal to group detections under their category
	// section; without it, the modal renders an empty list even when filtered
	// detections exist.
	Category string `json:"category"`
	// TypeID is the type_id of the detected material, resolved via JOIN
	// materials (migration 0019 made distribution rules type-keyed; the
	// frontend filters/groups detections by type using this field).
	// Nil when the material has no type assigned (legacy).
	TypeID *uuid.UUID `json:"type_id,omitempty"`
	// RetractedAt is set when §18.2.2 disambiguation overruled this row in
	// favour of a longer cut from the same client; nil otherwise.
	RetractedAt *time.Time `json:"retracted_at,omitempty"`
	// IgnoredAt / IgnoredBy are set when an admin manually disregards this
	// veiculação via the "Desconsiderar" action on the detection detail page.
	// The daily_play_summary view skips ignored rows, so bonus/deficit
	// recompute automatically. Reversible: clearing IgnoredAt reactivates.
	IgnoredAt *time.Time `json:"ignored_at,omitempty"`
	IgnoredBy *uuid.UUID `json:"ignored_by,omitempty"`
	// ManualAt / ManualBy / ManualNote populate quando um admin sobe a
	// veiculação retroativamente via "Adicionar veiculação manualmente" na
	// modal de /detections. A linha conta normalmente em agregados (o
	// categorizer roda igual a uma detection real); a tripla é só pra
	// auditoria + badge + nota na detail page.
	ManualAt   *time.Time `json:"manual_at,omitempty"`
	ManualBy   *uuid.UUID `json:"manual_by,omitempty"`
	ManualNote *string    `json:"manual_note,omitempty"`
	// CommercialScript mirrors materials.script for the detected material.
	// Populated by the Get handler (single-detection detail page); the bulk
	// list endpoints leave it nil to keep the payload tight.
	CommercialScript *string   `json:"commercial_script,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
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
	// Categoriza inline antes de inserir. Acessa campaigns + distribution_rules
	// pra alimentar o categorizador puro. Mantém categorização consistente
	// com o que a view daily_play_summary espera.
	category, err := d.categorize(ctx, in)
	if err != nil {
		return nil, err
	}

	var det Detection
	err = d.pool.QueryRow(ctx, `
		INSERT INTO detections (station_id, commercial_id, campaign_id, detected_at,
		                        match_start_offset_ms, match_end_offset_ms, confidence,
		                        hash_count, temporal_coverage, variant_used, rate_used,
		                        category)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING id, station_id, commercial_id, campaign_id, detected_at,
		          match_start_offset_ms, match_end_offset_ms, confidence, hash_count,
		          temporal_coverage, variant_used, rate_used,
		          evidence_status, evidence_key, evidence_size_bytes, category, retracted_at, created_at`,
		in.StationID, in.CommercialID, in.CampaignID, in.DetectedAt,
		in.MatchStartOffsetMs, in.MatchEndOffsetMs, in.Confidence, in.HashCount,
		in.TemporalCoverage, in.VariantUsed, in.RateUsed, category,
	).Scan(&det.ID, &det.StationID, &det.CommercialID, &det.CampaignID, &det.DetectedAt,
		&det.MatchStartOffsetMs, &det.MatchEndOffsetMs, &det.Confidence, &det.HashCount,
		&det.TemporalCoverage, &det.VariantUsed, &det.RateUsed,
		&det.EvidenceStatus, &det.EvidenceKey, &det.EvidenceSizeBytes, &det.Category, &det.RetractedAt, &det.CreatedAt)
	return &det, err
}

// categorize resolves the detection's category by loading the campaign,
// applicable rules, AND any override on (campaign, type, station, date),
// then invoking the pure categorizer.
//
// Migration 0019: rules and overrides are keyed by material TYPE. We look
// up the type of the detected material via JOIN materials.
// Migration 0031: overrides now carry their own time_start/time_end and
// supersede rules for the cell+day when present.
func (d *Detections) categorize(ctx context.Context, in CreateDetectionInput) (string, error) {
	var cmpStart, cmpEnd time.Time
	err := d.pool.QueryRow(ctx,
		`SELECT start_date, end_date FROM campaigns WHERE id = $1`, in.CampaignID,
	).Scan(&cmpStart, &cmpEnd)
	if err != nil {
		return categorizer.CatOrphan, err
	}

	rows, err := d.pool.Query(ctx, `
		SELECT r.start_date, r.end_date, r.weekday_mask,
		       r.time_start::text, r.time_end::text, r.plays_per_day
		FROM distribution_rules r
		WHERE r.campaign_id = $1
		  AND r.type_id = (SELECT type_id FROM materials WHERE id = $2)
		  AND $3 = ANY(r.station_ids)`,
		in.CampaignID, in.CommercialID, in.StationID)
	if err != nil {
		return categorizer.CatOrphan, err
	}
	defer rows.Close()

	var rules []categorizer.Rule
	for rows.Next() {
		var r categorizer.Rule
		var tsStr, teStr string
		var plays int16
		if err := rows.Scan(&r.StartDate, &r.EndDate, &r.WeekdayMask,
			&tsStr, &teStr, &plays); err != nil {
			return categorizer.CatOrphan, err
		}
		r.TimeStart, _ = time.Parse("15:04:05", tsStr)
		r.TimeEnd, _ = time.Parse("15:04:05", teStr)
		r.PlaysPerDay = plays
		rules = append(rules, r)
	}
	if err := rows.Err(); err != nil {
		return categorizer.CatOrphan, err
	}

	// Override lookup. (campaign, type, station, for_date) é PK em
	// distribution_overrides. for_date é a data local em São Paulo
	// derivada da timestamp da detection. Quando há override, o
	// categorizador ignora rules pra essa célula+dia (D1/D7 do spec).
	var (
		ov      *categorizer.Override
		ovPlays int16
		ovTsStr string
		ovTeStr string
	)
	err = d.pool.QueryRow(ctx, `
		SELECT plays_expected, time_start::text, time_end::text
		FROM distribution_overrides
		WHERE campaign_id = $1
		  AND type_id = (SELECT type_id FROM materials WHERE id = $2)
		  AND station_id = $3
		  AND for_date = ($4::timestamptz AT TIME ZONE 'America/Sao_Paulo')::date`,
		in.CampaignID, in.CommercialID, in.StationID, in.DetectedAt,
	).Scan(&ovPlays, &ovTsStr, &ovTeStr)
	switch {
	case err == nil:
		ts, _ := time.Parse("15:04:05", ovTsStr)
		te, _ := time.Parse("15:04:05", ovTeStr)
		ov = &categorizer.Override{PlaysExpected: ovPlays, TimeStart: ts, TimeEnd: te}
	case errors.Is(err, pgx.ErrNoRows):
		// Sem override — ov fica nil, comportamento antigo.
	default:
		return categorizer.CatOrphan, err
	}

	return categorizer.Categorize(
		in.DetectedAt,
		categorizer.Campaign{StartDate: cmpStart, EndDate: cmpEnd},
		rules,
		ov,
	), nil
}

// CreateManualInput é o payload da inserção retroativa "Adicionar veiculação
// manualmente" que aparece na DayDetailModal. Os campos espelham a entrada
// real (campaign / commercial / station / detected_at) mais a tripla de
// auditoria que vai pra detections.manual_*. O categorizador roda igual à
// engine — então out_slot / out_date / orphan funcionam exatamente como
// veiculação real.
type CreateManualInput struct {
	StationID    uuid.UUID
	CommercialID uuid.UUID
	CampaignID   uuid.UUID
	DetectedAt   time.Time
	ManualBy     uuid.UUID
	ManualNote   string // pode ser vazio → vai como NULL
}

// CreateManual valida o vínculo material × emissora × campanha (rejeita se a
// emissora não estiver em campaign_materials.target_stations pro material), e
// insere a detection com:
//   - confidence = 1.0 (declarado, ground-truth)
//   - hash_count = 0, *_offset_ms = 0
//   - evidence_status = 'missing' (sem áudio)
//   - manual_at = now(), manual_by, manual_note
//
// A categorização (in_slot/out_slot/out_date/orphan) sai do mesmo
// categorizer.Categorize() que a engine real usa.
func (d *Detections) CreateManual(ctx context.Context, in CreateManualInput) (*Detection, error) {
	// Validação do vínculo: a emissora precisa estar no target_stations
	// do material dentro daquela campanha. Sem isso o operador podia subir
	// veiculação de um material que nem está atribuído à emissora — gerando
	// dado contraditório com o restante do sistema.
	var linked bool
	err := d.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM campaign_materials
			WHERE campaign_id = $1
			  AND material_id = $2
			  AND $3 = ANY(target_stations)
		)`, in.CampaignID, in.CommercialID, in.StationID,
	).Scan(&linked)
	if err != nil {
		return nil, err
	}
	if !linked {
		return nil, ErrMaterialNotLinkedToStation
	}

	// Reutiliza o categorizer existente passando os mesmos inputs.
	cat, err := d.categorize(ctx, CreateDetectionInput{
		StationID:    in.StationID,
		CommercialID: in.CommercialID,
		CampaignID:   in.CampaignID,
		DetectedAt:   in.DetectedAt,
	})
	if err != nil {
		return nil, err
	}

	var note *string
	if trimmed := strings.TrimSpace(in.ManualNote); trimmed != "" {
		note = &trimmed
	}

	row := d.pool.QueryRow(ctx, `
		INSERT INTO detections (
		    station_id, commercial_id, campaign_id, detected_at,
		    match_start_offset_ms, match_end_offset_ms,
		    confidence, hash_count, category,
		    evidence_status,
		    manual_at, manual_by, manual_note
		) VALUES (
		    $1, $2, $3, $4,
		    0, 0,
		    1.0, 0, $5,
		    'missing',
		    now(), $6, $7
		)
		RETURNING id`,
		in.StationID, in.CommercialID, in.CampaignID, in.DetectedAt,
		cat, in.ManualBy, note,
	)
	var id uuid.UUID
	if err := row.Scan(&id); err != nil {
		return nil, err
	}
	return d.Get(ctx, id)
}

// ErrMaterialNotLinkedToStation sinaliza tentativa de inserir veiculação
// manual de material que não está atribuído à emissora alvo na campanha.
var ErrMaterialNotLinkedToStation = errors.New("material is not linked to this station in this campaign")

func (d *Detections) UpdateEvidence(ctx context.Context, id uuid.UUID, detectedAt time.Time,
	status, key string, sizeBytes int64) error {
	_, err := d.pool.Exec(ctx, `
		UPDATE detections
		SET evidence_status = $3, evidence_key = $4, evidence_size_bytes = $5
		WHERE id = $1 AND detected_at = $2`,
		id, detectedAt, status, key, sizeBytes)
	return err
}

// SetAuditCoverage records the §9.9 audit coverage (master frames matched in the
// clip / total) for a detection that passed the audit. The coverage-based version
// disambiguation (§18.2.2 v2) reads it on a sibling cut to decide which cut
// actually aired (the most-covered master wins, not the longest); /detections/:id
// shows it. detected_at is in the WHERE for partition pruning (detections is
// partitioned by detected_at), mirroring UpdateEvidence.
func (d *Detections) SetAuditCoverage(ctx context.Context, id uuid.UUID, detectedAt time.Time, coverage float64) error {
	_, err := d.pool.Exec(ctx,
		`UPDATE detections SET audit_coverage = $3 WHERE id = $1 AND detected_at = $2`,
		id, detectedAt, coverage)
	return err
}

// SiblingCut is another cut (master) of the same client — the unit the
// coverage-based version disambiguation (§18.2.2 v2) re-audits the evidence clip
// against to decide which cut actually aired.
type SiblingCut struct {
	ID              uuid.UUID
	ShortID         int32
	DurationSeconds int
}

// FindCutWithSiblings, given an attributed master UUID, returns that master's own
// (short_id, duration) plus the OTHER ready material masters of the same client.
// The siblings come from the catalog (materials), NOT from detection rows — so a
// cut that was suppressed/retracted and has no row is still found. This is what
// lets the audit re-fingerprint the clip against every cut of the client and pick
// the one it really matches.
//
// When the master UUID is not a material (legacy commercial), self is zero and
// siblings is empty — the caller then leaves attribution unchanged (the material
// library is where the 15s/30s confusion lives).
func (d *Detections) FindCutWithSiblings(ctx context.Context, masterID uuid.UUID) (self SiblingCut, siblings []SiblingCut, err error) {
	var clientID uuid.UUID
	var dur float64
	err = d.pool.QueryRow(ctx,
		`SELECT short_id, duration_seconds, client_id FROM materials WHERE id = $1`, masterID,
	).Scan(&self.ShortID, &dur, &clientID)
	if errors.Is(err, pgx.ErrNoRows) {
		return SiblingCut{}, nil, nil // not a material master → no siblings
	}
	if err != nil {
		return SiblingCut{}, nil, err
	}
	self.ID = masterID
	self.DurationSeconds = int(dur + 0.5)

	rows, err := d.pool.Query(ctx, `
		SELECT id, short_id, duration_seconds
		FROM materials
		WHERE client_id = $1 AND id <> $2 AND fingerprint_status = 'ready'`,
		clientID, masterID)
	if err != nil {
		return self, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var s SiblingCut
		var d2 float64
		if err := rows.Scan(&s.ID, &s.ShortID, &d2); err != nil {
			return self, nil, err
		}
		s.DurationSeconds = int(d2 + 0.5)
		siblings = append(siblings, s)
	}
	return self, siblings, rows.Err()
}

// ReattributeDetection re-points a detection at a different cut (§18.2.2 v2): the
// coverage-based audit found the evidence clip matches newCommercialID better
// than the cut it was first attributed to. Sets commercial_id + campaign_id and
// re-runs the categorizer for the new (campaign, cut, station, day) so in_slot /
// out_slot / out_date / orphan stays consistent. detected_at is in the WHERE for
// partition pruning.
func (d *Detections) ReattributeDetection(ctx context.Context, detectionID uuid.UUID, detectedAt time.Time,
	newCommercialID, newCampaignID, stationID uuid.UUID) error {
	cat, err := d.categorize(ctx, CreateDetectionInput{
		StationID:    stationID,
		CommercialID: newCommercialID,
		CampaignID:   newCampaignID,
		DetectedAt:   detectedAt,
	})
	if err != nil {
		return err
	}
	_, err = d.pool.Exec(ctx, `
		UPDATE detections
		SET commercial_id = $3, campaign_id = $4, category = $5
		WHERE id = $1 AND detected_at = $2`,
		detectionID, detectedAt, newCommercialID, newCampaignID, cat)
	return err
}

type ListFilter struct {
	CampaignID *uuid.UUID
	StationID  *uuid.UUID
	StartDate  *time.Time
	EndDate    *time.Time
	Limit      int
	Offset     int
	// ClientID, when non-nil, restricts results to detections whose campaign
	// belongs to this client (viewer JWT scope).
	ClientID *uuid.UUID
}

// ListPagedFilter mirrors ListFilter but with page-based pagination and an
// optional case/accent-insensitive search over station/material/type/client
// text fields. Separate from ListFilter because the paginated path returns a
// different shape (ListPagedResult); keeping the types distinct avoids
// breaking the unpaginated consumers (DayDetailModal).
type ListPagedFilter struct {
	CampaignID *uuid.UUID
	StartDate  *time.Time
	EndDate    *time.Time
	Q          string // free text; empty disables the filter
	Sort       string // "detected_at_desc" (default) | "detected_at_asc"
	Page       int    // 1-based
	PageSize   int    // 1..200
	// ClientID, when non-nil, restricts results to detections whose campaign
	// belongs to this client (viewer JWT scope).
	ClientID *uuid.UUID
}

// ListPagedResult is the wire format returned to the frontend. Total is a
// separate count(*) so the paginator can render "X of N" + last-page jump.
type ListPagedResult struct {
	Data       []DetectionEnriched `json:"data"`
	Page       int                 `json:"page"`
	PageSize   int                 `json:"page_size"`
	Total      int                 `json:"total"`
	TotalPages int                 `json:"total_pages"`
}

// DetectionEnriched extends Detection with the joined columns the airtime
// report card needs in one round-trip. Adding new fields is safe: JSON
// decoders ignore unknown keys, and the airtime-report card consumes a
// dedicated hook (useDetectionsPaged) that knows the shape.
type DetectionEnriched struct {
	Detection
	StationFrequencyMHz *float64   `json:"station_frequency_mhz,omitempty"`
	StationBand         *string    `json:"station_band,omitempty"`
	StationCity         *string    `json:"station_city,omitempty"`
	StationState        *string    `json:"station_state,omitempty"`
	StationLogoURL      *string    `json:"station_logo_url,omitempty"`
	StationPMM          *float64   `json:"station_pmm,omitempty"`
	MaterialDurationSec *float64   `json:"material_duration_sec,omitempty"`
	MaterialTypeName    *string    `json:"material_type_name,omitempty"`
	MaterialTypeColor   *string    `json:"material_type_color,omitempty"`
	ClientID            *uuid.UUID `json:"client_id,omitempty"`
	ClientName          *string    `json:"client_name,omitempty"`
}

// ListPaged is the cronological detection list backing /reports/airtime.
// Performs a single query with COUNT(*) OVER () for total. Excludes ignored
// and retracted rows so the airtime report matches what daily_play_summary
// counts.
func (d *Detections) ListPaged(ctx context.Context, f ListPagedFilter) (*ListPagedResult, error) {
	if f.Page < 1 {
		f.Page = 1
	}
	if f.PageSize < 1 || f.PageSize > 200 {
		f.PageSize = 10
	}
	order := "DESC"
	if f.Sort == "detected_at_asc" {
		order = "ASC"
	}

	var qTokens any = nil
	if q := strings.TrimSpace(f.Q); q != "" {
		toks := strings.Fields(q)
		if len(toks) > 4 {
			toks = toks[:4]
		}
		qTokens = toks
	}

	sql := `
		SELECT d.id, d.station_id, COALESCE(s.name, ''), d.commercial_id, COALESCE(m.title, c.title, ''),
		       d.campaign_id, d.detected_at,
		       d.match_start_offset_ms, d.match_end_offset_ms, d.confidence, d.hash_count,
		       d.temporal_coverage, d.variant_used, d.rate_used,
		       d.evidence_status, d.evidence_key, d.evidence_size_bytes, d.category,
		       m.type_id, d.retracted_at, d.ignored_at, d.ignored_by,
		       d.manual_at, d.manual_by, d.manual_note, d.created_at,
		       s.frequency_mhz, s.band, s.city, s.state, s.logo_url, s.pmm,
		       m.duration_seconds, mt.name, mt.color,
		       cmp.client_id, cli.name,
		       COUNT(*) OVER () AS total
		FROM detections d
		LEFT JOIN stations s        ON s.id = d.station_id
		LEFT JOIN commercials c     ON c.id = d.commercial_id
		LEFT JOIN materials m       ON m.id = d.commercial_id
		LEFT JOIN material_types mt ON mt.id = m.type_id
		LEFT JOIN campaigns cmp     ON cmp.id = d.campaign_id
		LEFT JOIN clients cli       ON cli.id = cmp.client_id
		WHERE ($1::uuid IS NULL OR d.campaign_id = $1)
		  AND ($2::timestamptz IS NULL OR d.detected_at >= $2)
		  AND ($3::timestamptz IS NULL OR d.detected_at <= $3)
		  AND ($7::uuid IS NULL OR cmp.client_id = $7)
		  AND d.ignored_at IS NULL
		  AND d.retracted_at IS NULL
		  AND d.evidence_status <> 'audit_rejected'
		  AND ($4::text[] IS NULL OR (
		      SELECT bool_and(
		          unaccent(lower(
		              COALESCE(s.name,'') || ' ' || COALESCE(s.city,'') || ' ' ||
		              COALESCE(s.state,'') || ' ' || COALESCE(s.band,'') || ' ' ||
		              COALESCE(s.frequency_mhz::text,'') || ' ' ||
		              COALESCE(m.title, c.title, '') || ' ' || COALESCE(mt.name,'') || ' ' ||
		              COALESCE(cli.name,'')
		          )) LIKE '%' || unaccent(lower(tok)) || '%'
		      )
		      FROM unnest($4::text[]) AS tok
		  ))
		ORDER BY d.detected_at ` + order + `
		LIMIT $5 OFFSET $6`

	offset := (f.Page - 1) * f.PageSize
	rows, err := d.pool.Query(ctx, sql,
		f.CampaignID, f.StartDate, f.EndDate, qTokens, f.PageSize, offset, f.ClientID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var (
		out   []DetectionEnriched
		total int
	)
	for rows.Next() {
		var det DetectionEnriched
		if err := rows.Scan(&det.ID, &det.StationID, &det.StationName, &det.CommercialID, &det.CommercialName,
			&det.CampaignID, &det.DetectedAt, &det.MatchStartOffsetMs, &det.MatchEndOffsetMs,
			&det.Confidence, &det.HashCount, &det.TemporalCoverage, &det.VariantUsed,
			&det.RateUsed, &det.EvidenceStatus, &det.EvidenceKey,
			&det.EvidenceSizeBytes, &det.Category, &det.TypeID, &det.RetractedAt,
			&det.IgnoredAt, &det.IgnoredBy,
			&det.ManualAt, &det.ManualBy, &det.ManualNote, &det.CreatedAt,
			&det.StationFrequencyMHz, &det.StationBand, &det.StationCity, &det.StationState,
			&det.StationLogoURL, &det.StationPMM,
			&det.MaterialDurationSec, &det.MaterialTypeName, &det.MaterialTypeColor,
			&det.ClientID, &det.ClientName,
			&total); err != nil {
			return nil, err
		}
		out = append(out, det)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	totalPages := (total + f.PageSize - 1) / f.PageSize
	if totalPages < 1 {
		totalPages = 1
	}
	if out == nil {
		out = []DetectionEnriched{}
	}
	return &ListPagedResult{
		Data:       out,
		Page:       f.Page,
		PageSize:   f.PageSize,
		Total:      total,
		TotalPages: totalPages,
	}, nil
}

func (d *Detections) List(ctx context.Context, f ListFilter) ([]Detection, error) {
	if f.Limit <= 0 || f.Limit > 1000 {
		f.Limit = 100
	}
	rows, err := d.pool.Query(ctx, `
		SELECT d.id, d.station_id, COALESCE(s.name, ''), d.commercial_id, COALESCE(m.title, c.title, ''),
		       d.campaign_id, d.detected_at,
		       d.match_start_offset_ms, d.match_end_offset_ms, d.confidence, d.hash_count,
		       d.temporal_coverage, d.variant_used, d.rate_used,
		       d.evidence_status, d.evidence_key, d.evidence_size_bytes, d.category,
		       m.type_id, d.retracted_at, d.ignored_at, d.ignored_by,
		       d.manual_at, d.manual_by, d.manual_note, d.created_at
		FROM detections d
		LEFT JOIN stations s ON s.id = d.station_id
		LEFT JOIN commercials c ON c.id = d.commercial_id
		LEFT JOIN materials m ON m.id = d.commercial_id
		LEFT JOIN campaigns cmp ON cmp.id = d.campaign_id
		WHERE ($1::uuid IS NULL OR d.campaign_id = $1)
		  AND ($2::uuid IS NULL OR d.station_id = $2)
		  AND ($3::timestamptz IS NULL OR d.detected_at >= $3)
		  AND ($4::timestamptz IS NULL OR d.detected_at <= $4)
		  AND ($7::uuid IS NULL OR cmp.client_id = $7)
		  AND d.evidence_status <> 'audit_rejected'
		ORDER BY d.detected_at DESC
		LIMIT $5 OFFSET $6`,
		f.CampaignID, f.StationID, f.StartDate, f.EndDate, f.Limit, f.Offset, f.ClientID)
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
			&det.EvidenceSizeBytes, &det.Category, &det.TypeID, &det.RetractedAt,
			&det.IgnoredAt, &det.IgnoredBy,
			&det.ManualAt, &det.ManualBy, &det.ManualNote, &det.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, det)
	}
	return out, rows.Err()
}

// IterateForExport streams enriched detections without paging, invoking the
// callback once per row. Stops if cb returns an error. Uses the same WHERE
// clause as ListPaged so filters/q behave identically.
func (d *Detections) IterateForExport(ctx context.Context, f ListPagedFilter,
	cb func(DetectionEnriched) error) error {
	var qTokens any = nil
	if q := strings.TrimSpace(f.Q); q != "" {
		toks := strings.Fields(q)
		if len(toks) > 4 {
			toks = toks[:4]
		}
		qTokens = toks
	}
	order := "DESC"
	if f.Sort == "detected_at_asc" {
		order = "ASC"
	}

	rows, err := d.pool.Query(ctx, `
		SELECT d.id, d.station_id, COALESCE(s.name, ''), d.commercial_id, COALESCE(m.title, c.title, ''),
		       d.campaign_id, d.detected_at,
		       d.match_start_offset_ms, d.match_end_offset_ms, d.confidence, d.hash_count,
		       d.temporal_coverage, d.variant_used, d.rate_used,
		       d.evidence_status, d.evidence_key, d.evidence_size_bytes, d.category,
		       m.type_id, d.retracted_at, d.ignored_at, d.ignored_by,
		       d.manual_at, d.manual_by, d.manual_note, d.created_at,
		       s.frequency_mhz, s.band, s.city, s.state, s.logo_url, s.pmm,
		       m.duration_seconds, mt.name, mt.color,
		       cmp.client_id, cli.name
		FROM detections d
		LEFT JOIN stations s        ON s.id = d.station_id
		LEFT JOIN commercials c     ON c.id = d.commercial_id
		LEFT JOIN materials m       ON m.id = d.commercial_id
		LEFT JOIN material_types mt ON mt.id = m.type_id
		LEFT JOIN campaigns cmp     ON cmp.id = d.campaign_id
		LEFT JOIN clients cli       ON cli.id = cmp.client_id
		WHERE ($1::uuid IS NULL OR d.campaign_id = $1)
		  AND ($2::timestamptz IS NULL OR d.detected_at >= $2)
		  AND ($3::timestamptz IS NULL OR d.detected_at <= $3)
		  AND d.ignored_at IS NULL
		  AND d.retracted_at IS NULL
		  AND d.evidence_status <> 'audit_rejected'
		  AND ($4::text[] IS NULL OR (
		      SELECT bool_and(
		          unaccent(lower(
		              COALESCE(s.name,'') || ' ' || COALESCE(s.city,'') || ' ' ||
		              COALESCE(s.state,'') || ' ' || COALESCE(s.band,'') || ' ' ||
		              COALESCE(s.frequency_mhz::text,'') || ' ' ||
		              COALESCE(m.title, c.title, '') || ' ' || COALESCE(mt.name,'') || ' ' ||
		              COALESCE(cli.name,'')
		          )) LIKE '%' || unaccent(lower(tok)) || '%'
		      )
		      FROM unnest($4::text[]) AS tok
		  ))
		ORDER BY d.detected_at `+order,
		f.CampaignID, f.StartDate, f.EndDate, qTokens)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var det DetectionEnriched
		if err := rows.Scan(&det.ID, &det.StationID, &det.StationName, &det.CommercialID, &det.CommercialName,
			&det.CampaignID, &det.DetectedAt, &det.MatchStartOffsetMs, &det.MatchEndOffsetMs,
			&det.Confidence, &det.HashCount, &det.TemporalCoverage, &det.VariantUsed,
			&det.RateUsed, &det.EvidenceStatus, &det.EvidenceKey,
			&det.EvidenceSizeBytes, &det.Category, &det.TypeID, &det.RetractedAt,
			&det.IgnoredAt, &det.IgnoredBy,
			&det.ManualAt, &det.ManualBy, &det.ManualNote, &det.CreatedAt,
			&det.StationFrequencyMHz, &det.StationBand, &det.StationCity, &det.StationState,
			&det.StationLogoURL, &det.StationPMM,
			&det.MaterialDurationSec, &det.MaterialTypeName, &det.MaterialTypeColor,
			&det.ClientID, &det.ClientName); err != nil {
			return err
		}
		if err := cb(det); err != nil {
			return err
		}
	}
	return rows.Err()
}

// MaterialAggregateRow is one entry of the airtime-report sidebar panel.
type MaterialAggregateRow struct {
	MaterialID          uuid.UUID  `json:"material_id"`
	MaterialShortID     *int32     `json:"material_short_id,omitempty"`
	MaterialTitle       string     `json:"material_title"`
	MaterialDurationSec *float64   `json:"material_duration_sec,omitempty"`
	MaterialTypeID      *uuid.UUID `json:"material_type_id,omitempty"`
	MaterialTypeName    *string    `json:"material_type_name,omitempty"`
	MaterialTypeColor   *string    `json:"material_type_color,omitempty"`
	Count               int        `json:"count"`
}

// MaterialAggregateResult is the wire format of /aggregate-by-material.
type MaterialAggregateResult struct {
	Data              []MaterialAggregateRow `json:"data"`
	TotalDetections   int                    `json:"total_detections"`
	DistinctMaterials int                    `json:"distinct_materials"`
}

// AggregateFilter is the query input — campaign is required, the rest mirror
// ListPagedFilter so the panel stays consistent with the list.
type AggregateFilter struct {
	CampaignID uuid.UUID
	StartDate  *time.Time
	EndDate    *time.Time
	Q          string
	// ClientID, when non-nil, is used by the handler to verify campaign
	// ownership before calling AggregateByMaterial (viewer scope guard).
	// Not applied as a SQL filter here because campaign_id is already required.
	ClientID *uuid.UUID
}

// AggregateByMaterial counts non-ignored, non-retracted detections grouped by
// material for the airtime-report sidebar panel. Same WHERE clause as
// ListPaged so the panel matches the list under any filter combination.
func (d *Detections) AggregateByMaterial(ctx context.Context, f AggregateFilter) (*MaterialAggregateResult, error) {
	var qTokens any = nil
	if q := strings.TrimSpace(f.Q); q != "" {
		toks := strings.Fields(q)
		if len(toks) > 4 {
			toks = toks[:4]
		}
		qTokens = toks
	}

	rows, err := d.pool.Query(ctx, `
		SELECT d.commercial_id, m.short_id, COALESCE(m.title, c.title, ''),
		       m.duration_seconds, m.type_id, mt.name, mt.color,
		       COUNT(*) AS cnt
		FROM detections d
		LEFT JOIN commercials c     ON c.id = d.commercial_id
		LEFT JOIN materials m       ON m.id = d.commercial_id
		LEFT JOIN material_types mt ON mt.id = m.type_id
		LEFT JOIN stations s        ON s.id = d.station_id
		LEFT JOIN campaigns cmp     ON cmp.id = d.campaign_id
		LEFT JOIN clients cli       ON cli.id = cmp.client_id
		WHERE d.campaign_id = $1
		  AND ($2::timestamptz IS NULL OR d.detected_at >= $2)
		  AND ($3::timestamptz IS NULL OR d.detected_at <= $3)
		  AND d.ignored_at IS NULL
		  AND d.retracted_at IS NULL
		  AND d.evidence_status <> 'audit_rejected'
		  AND ($4::text[] IS NULL OR (
		      SELECT bool_and(
		          unaccent(lower(
		              COALESCE(s.name,'') || ' ' || COALESCE(s.city,'') || ' ' ||
		              COALESCE(s.state,'') || ' ' || COALESCE(s.band,'') || ' ' ||
		              COALESCE(s.frequency_mhz::text,'') || ' ' ||
		              COALESCE(m.title, c.title, '') || ' ' || COALESCE(mt.name,'') || ' ' ||
		              COALESCE(cli.name,'')
		          )) LIKE '%' || unaccent(lower(tok)) || '%'
		      )
		      FROM unnest($4::text[]) AS tok
		  ))
		GROUP BY d.commercial_id, m.short_id, m.title, c.title, m.duration_seconds, m.type_id, mt.name, mt.color
		ORDER BY cnt DESC, c.title ASC`,
		f.CampaignID, f.StartDate, f.EndDate, qTokens)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var (
		out   []MaterialAggregateRow
		total int
	)
	for rows.Next() {
		var r MaterialAggregateRow
		if err := rows.Scan(&r.MaterialID, &r.MaterialShortID, &r.MaterialTitle,
			&r.MaterialDurationSec, &r.MaterialTypeID, &r.MaterialTypeName, &r.MaterialTypeColor,
			&r.Count); err != nil {
			return nil, err
		}
		out = append(out, r)
		total += r.Count
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if out == nil {
		out = []MaterialAggregateRow{}
	}
	return &MaterialAggregateResult{
		Data:              out,
		TotalDetections:   total,
		DistinctMaterials: len(out),
	}, nil
}

func (d *Detections) Get(ctx context.Context, id uuid.UUID) (*Detection, error) {
	var det Detection
	err := d.pool.QueryRow(ctx, `
		SELECT d.id, d.station_id, COALESCE(s.name, ''), d.commercial_id, COALESCE(m.title, c.title, ''),
		       d.campaign_id, d.detected_at,
		       d.match_start_offset_ms, d.match_end_offset_ms, d.confidence, d.hash_count,
		       d.temporal_coverage, d.variant_used, d.rate_used,
		       d.evidence_status, d.evidence_key, d.evidence_size_bytes, d.category,
		       m.type_id, d.retracted_at, d.ignored_at, d.ignored_by,
		       d.manual_at, d.manual_by, d.manual_note, m.script, d.created_at
		FROM detections d
		LEFT JOIN stations s ON s.id = d.station_id
		LEFT JOIN commercials c ON c.id = d.commercial_id
		LEFT JOIN materials m ON m.id = d.commercial_id
		WHERE d.id = $1`, id,
	).Scan(&det.ID, &det.StationID, &det.StationName, &det.CommercialID, &det.CommercialName,
		&det.CampaignID, &det.DetectedAt,
		&det.MatchStartOffsetMs, &det.MatchEndOffsetMs, &det.Confidence, &det.HashCount,
		&det.TemporalCoverage, &det.VariantUsed, &det.RateUsed,
		&det.EvidenceStatus, &det.EvidenceKey, &det.EvidenceSizeBytes, &det.Category, &det.TypeID,
		&det.RetractedAt, &det.IgnoredAt, &det.IgnoredBy,
		&det.ManualAt, &det.ManualBy, &det.ManualNote, &det.CommercialScript, &det.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &det, nil
}

// Ignore stamps ignored_at = now() and ignored_by = userID on the detection
// so daily_play_summary excludes it from aggregates. Idempotent — a second
// call updates the timestamp but keeps the row in the ignored state.
func (d *Detections) Ignore(ctx context.Context, id, userID uuid.UUID) error {
	_, err := d.pool.Exec(ctx,
		`UPDATE detections SET ignored_at = now(), ignored_by = $2 WHERE id = $1`,
		id, userID)
	return err
}

// Restore clears ignored_at / ignored_by so the detection counts again. No-op
// when the row was never ignored.
func (d *Detections) Restore(ctx context.Context, id uuid.UUID) error {
	_, err := d.pool.Exec(ctx,
		`UPDATE detections SET ignored_at = NULL, ignored_by = NULL WHERE id = $1`,
		id)
	return err
}

// ──────────────────────────────────────────────────────────────────────────
// Campaign reports (CSV consolidated + PDF summary)
// ──────────────────────────────────────────────────────────────────────────

// MaterialStationRow é a granularidade do relatório consolidado: uma linha
// por (material × emissora) com o total de veiculações no período. Inclui
// metadata leve da emissora pra o CSV ficar legível sem JOIN no front, e
// um breakdown por categoria (in_slot/out_slot/out_date/orphan) pra
// fechamento comercial saber quantas veiculações foram bônus, fora-faixa
// etc. dentro de cada combinação material × emissora.
type MaterialStationRow struct {
	MaterialID          uuid.UUID `json:"material_id"`
	MaterialShortID     *int32    `json:"material_short_id,omitempty"`
	MaterialTitle       string    `json:"material_title"`
	MaterialDurationSec *float64  `json:"material_duration_sec,omitempty"`
	MaterialTypeName    *string   `json:"material_type_name,omitempty"`
	StationID           uuid.UUID `json:"station_id"`
	StationName         string    `json:"station_name"`
	StationBand         *string   `json:"station_band,omitempty"`
	StationFrequencyMHz *float64  `json:"station_frequency_mhz,omitempty"`
	StationCity         *string   `json:"station_city,omitempty"`
	StationState        *string   `json:"station_state,omitempty"`
	Count               int       `json:"count"`
	// Breakdown por status — soma sempre == Count.
	InSlotCount     int       `json:"in_slot_count"`
	OutSlotCount    int       `json:"out_slot_count"`
	OutDateCount    int       `json:"out_date_count"`
	OrphanCount     int       `json:"orphan_count"`
	FirstDetectedAt time.Time `json:"first_detected_at"`
	LastDetectedAt  time.Time `json:"last_detected_at"`
}

// AggregateByMaterialStation agrupa as veiculações da campanha por
// (material × emissora). Usa o mesmo WHERE da lista para que o relatório
// consolidado bata exatamente com o que o usuário vê em /reports/airtime
// e /detections sob os mesmos filtros.
func (d *Detections) AggregateByMaterialStation(ctx context.Context, f AggregateFilter) ([]MaterialStationRow, error) {
	rows, err := d.pool.Query(ctx, `
		SELECT d.commercial_id, m.short_id, COALESCE(m.title, c.title, ''),
		       m.duration_seconds, mt.name,
		       d.station_id, COALESCE(s.name, ''),
		       s.band, s.frequency_mhz, s.city, s.state,
		       COUNT(*) AS cnt,
		       COUNT(*) FILTER (WHERE d.category = 'in_slot')  AS in_slot_count,
		       COUNT(*) FILTER (WHERE d.category = 'out_slot') AS out_slot_count,
		       COUNT(*) FILTER (WHERE d.category = 'out_date') AS out_date_count,
		       COUNT(*) FILTER (WHERE d.category = 'orphan')   AS orphan_count,
		       MIN(d.detected_at), MAX(d.detected_at)
		FROM detections d
		LEFT JOIN commercials c     ON c.id = d.commercial_id
		LEFT JOIN materials m       ON m.id = d.commercial_id
		LEFT JOIN material_types mt ON mt.id = m.type_id
		LEFT JOIN stations s        ON s.id = d.station_id
		WHERE d.campaign_id = $1
		  AND ($2::timestamptz IS NULL OR d.detected_at >= $2)
		  AND ($3::timestamptz IS NULL OR d.detected_at <= $3)
		  AND d.ignored_at IS NULL
		  AND d.retracted_at IS NULL
		  AND d.evidence_status <> 'audit_rejected'
		GROUP BY d.commercial_id, m.short_id, m.title, c.title, m.duration_seconds, mt.name,
		         d.station_id, s.name, s.band, s.frequency_mhz, s.city, s.state
		ORDER BY COALESCE(m.title, c.title, '') ASC, s.name ASC`,
		f.CampaignID, f.StartDate, f.EndDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []MaterialStationRow{}
	for rows.Next() {
		var r MaterialStationRow
		if err := rows.Scan(
			&r.MaterialID, &r.MaterialShortID, &r.MaterialTitle,
			&r.MaterialDurationSec, &r.MaterialTypeName,
			&r.StationID, &r.StationName,
			&r.StationBand, &r.StationFrequencyMHz, &r.StationCity, &r.StationState,
			&r.Count,
			&r.InSlotCount, &r.OutSlotCount, &r.OutDateCount, &r.OrphanCount,
			&r.FirstDetectedAt, &r.LastDetectedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// StationAggregateRow alimenta a seção "por emissora" do PDF.
type StationAggregateRow struct {
	StationID           uuid.UUID `json:"station_id"`
	StationName         string    `json:"station_name"`
	StationBand         *string   `json:"station_band,omitempty"`
	StationFrequencyMHz *float64  `json:"station_frequency_mhz,omitempty"`
	StationCity         *string   `json:"station_city,omitempty"`
	StationState        *string   `json:"station_state,omitempty"`
	Count               int       `json:"count"`
}

// AggregateByStation devolve total de veiculações por emissora — usado tanto
// pelo PDF quanto pela seção sumária do relatório consolidado.
func (d *Detections) AggregateByStation(ctx context.Context, f AggregateFilter) ([]StationAggregateRow, error) {
	rows, err := d.pool.Query(ctx, `
		SELECT d.station_id, COALESCE(s.name, ''), s.band, s.frequency_mhz, s.city, s.state,
		       COUNT(*) AS cnt
		FROM detections d
		LEFT JOIN stations s ON s.id = d.station_id
		WHERE d.campaign_id = $1
		  AND ($2::timestamptz IS NULL OR d.detected_at >= $2)
		  AND ($3::timestamptz IS NULL OR d.detected_at <= $3)
		  AND d.ignored_at IS NULL
		  AND d.retracted_at IS NULL
		  AND d.evidence_status <> 'audit_rejected'
		GROUP BY d.station_id, s.name, s.band, s.frequency_mhz, s.city, s.state
		ORDER BY cnt DESC, s.name ASC`,
		f.CampaignID, f.StartDate, f.EndDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []StationAggregateRow{}
	for rows.Next() {
		var r StationAggregateRow
		if err := rows.Scan(
			&r.StationID, &r.StationName, &r.StationBand, &r.StationFrequencyMHz,
			&r.StationCity, &r.StationState, &r.Count,
		); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetClientID returns the client_id of the campaign that owns the detection.
// Used by handlers to verify viewer scope without modifying the Get signature.
// Returns pgx.ErrNoRows when the detection does not exist.
func (d *Detections) GetClientID(ctx context.Context, detectionID uuid.UUID) (*uuid.UUID, error) {
	var clientID uuid.UUID
	err := d.pool.QueryRow(ctx, `
		SELECT cmp.client_id
		FROM detections det
		JOIN campaigns cmp ON cmp.id = det.campaign_id
		WHERE det.id = $1`, detectionID,
	).Scan(&clientID)
	if err != nil {
		return nil, err
	}
	return &clientID, nil
}
