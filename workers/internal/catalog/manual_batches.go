package catalog

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ManualBatchEntry é uma linha do lote de veiculações manuais: qual material,
// quando tocou e uma descrição opcional. O áudio (censura) e o PDF do lote são
// tratados fora daqui (no handler, via storage).
type ManualBatchEntry struct {
	CommercialID uuid.UUID
	DetectedAt   time.Time
	Note         string
}

// ManualBatchEntryError aponta uma linha inválida do lote pelo índice (posição
// no array enviado), pro frontend destacar a linha certa.
type ManualBatchEntryError struct {
	Index   int    `json:"index"`
	Message string `json:"message"`
}

// CreateManualBatchInput é o payload do POST /detections/manual/batch. Quando
// ProofBatchID != nil, uma linha em manual_proof_batches é inserida com esse id
// (gerado no handler ANTES do upload do PDF, pra compor a chave S3).
type CreateManualBatchInput struct {
	CampaignID   uuid.UUID
	StationID    uuid.UUID
	ManualBy     uuid.UUID
	BatchNote    string
	ProofBatchID *uuid.UUID
	ProofPDFKey  string
	ProofPDFSize int64
	Entries      []ManualBatchEntry
}

// ValidateBatchLinks confere, linha a linha, se o material está vinculado à
// emissora naquela campanha (campaign_materials.target_stations). Mesmo gate do
// CreateManual single (ErrMaterialNotLinkedToStation). Retorna a lista de linhas
// inválidas pelo índice; vazia = tudo ok. Read-only — não cria nada.
func (d *Detections) ValidateBatchLinks(ctx context.Context, campaignID, stationID uuid.UUID, entries []ManualBatchEntry) []ManualBatchEntryError {
	var errs []ManualBatchEntryError
	for i, e := range entries {
		var linked bool
		err := d.pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM campaign_materials
				WHERE campaign_id = $1 AND material_id = $2 AND $3 = ANY(target_stations)
			)`, campaignID, e.CommercialID, stationID).Scan(&linked)
		if err != nil {
			errs = append(errs, ManualBatchEntryError{Index: i, Message: "erro ao validar vínculo"})
			continue
		}
		if !linked {
			errs = append(errs, ManualBatchEntryError{Index: i, Message: "material não vinculado a essa emissora nessa campanha"})
		}
	}
	return errs
}

// trimToPtr devolve nil quando a string é vazia após trim — pra colunas TEXT
// nullable (manual_note, manual_proof_batches.note) ficarem NULL em vez de ''.
func trimToPtr(s string) *string {
	if t := strings.TrimSpace(s); t != "" {
		return &t
	}
	return nil
}

// CreateManualBatch insere o lote inteiro numa transação (tudo-ou-nada nos
// inserts). Pré-condição: as linhas já passaram por ValidateBatchLinks (o
// handler garante). Quando ProofBatchID != nil, grava a linha de
// manual_proof_batches ANTES das detecções. Cada detecção:
//
//	confidence=1.0, hash_count=0, *_offset=0, evidence_status='missing',
//	manual_at=now(), manual_by, manual_note, proof_batch_id.
//
// Também grava a projeção canônica detection_campaigns (1:1) por linha — sem ela
// a veiculação some da grade que lê detection_campaigns (F-119). O categorizer
// roda igual ao CreateManual/Create. Retorna as detecções criadas (via Get), em
// ordem das entries, pro handler mapear áudios audio_i -> linha i.
func (d *Detections) CreateManualBatch(ctx context.Context, in CreateManualBatchInput) ([]*Detection, error) {
	// Defesa do repo: um lote sem linhas (com PDF) gravaria um manual_proof_batches
	// órfão. O handler já barra antes, mas a pré-condição fica explícita aqui.
	if len(in.Entries) == 0 {
		return nil, errors.New("CreateManualBatch: entries must not be empty")
	}

	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	if in.ProofBatchID != nil {
		if _, err := tx.Exec(ctx, `
			INSERT INTO manual_proof_batches
				(id, campaign_id, station_id, proof_pdf_key, proof_pdf_size, note, created_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			*in.ProofBatchID, in.CampaignID, in.StationID, in.ProofPDFKey, in.ProofPDFSize,
			trimToPtr(in.BatchNote), in.ManualBy); err != nil {
			return nil, err
		}
	}

	created := make([]uuid.UUID, 0, len(in.Entries))

	for _, e := range in.Entries {
		// Fechamento por célula-dia na MESMA tx: cada entry já enxerga as
		// anteriores do lote (inseridas acima), então a cota do dia é disputada
		// pelo lote inteiro na ordem cronológica, não linha a linha isolada.
		cat, err := d.settleCellDay(ctx, tx, CreateDetectionInput{
			StationID:    in.StationID,
			CommercialID: e.CommercialID,
			CampaignID:   in.CampaignID,
			DetectedAt:   e.DetectedAt,
		}, nil, true)
		if err != nil {
			return nil, err
		}

		var id uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO detections (
			    station_id, commercial_id, campaign_id, detected_at,
			    match_start_offset_ms, match_end_offset_ms,
			    confidence, hash_count, category,
			    evidence_status,
			    manual_at, manual_by, manual_note, proof_batch_id
			) VALUES (
			    $1, $2, $3, $4,
			    0, 0,
			    1.0, 0, $5,
			    'missing',
			    now(), $6, $7, $8
			)
			RETURNING id`,
			in.StationID, e.CommercialID, in.CampaignID, e.DetectedAt,
			cat, in.ManualBy, trimToPtr(e.Note), in.ProofBatchID,
		).Scan(&id); err != nil {
			return nil, err
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO detection_campaigns (detection_id, detected_at, campaign_id, commercial_id, category)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (detection_id, detected_at, campaign_id) DO NOTHING`,
			id, e.DetectedAt, in.CampaignID, e.CommercialID, cat); err != nil {
			return nil, err
		}

		created = append(created, id)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	out := make([]*Detection, 0, len(created))
	for _, id := range created {
		det, err := d.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, det)
	}
	return out, nil
}

// ProofKeyForDetection resolve a chave S3 do PDF comprovante a partir de uma
// detecção do lote (JOIN manual_proof_batches via proof_batch_id). Devolve
// pgx.ErrNoRows quando a detecção não pertence a nenhum lote (ou não existe) —
// o handler mapeia pra 404.
func (d *Detections) ProofKeyForDetection(ctx context.Context, id uuid.UUID) (string, error) {
	var key string
	err := d.pool.QueryRow(ctx, `
		SELECT b.proof_pdf_key
		FROM detections d
		JOIN manual_proof_batches b ON b.id = d.proof_batch_id
		WHERE d.id = $1`, id).Scan(&key)
	return key, err
}
