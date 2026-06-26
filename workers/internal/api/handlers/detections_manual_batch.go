package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"radiocheck/internal/auth"
	"radiocheck/internal/catalog"
)

const manualProofMaxBytes = 25 << 20 // 25 MB

// manualBatchMeta é o JSON no campo `meta` do multipart de CreateManualBatch.
type manualBatchMeta struct {
	CampaignID uuid.UUID `json:"campaign_id"`
	StationID  uuid.UUID `json:"station_id"`
	Note       string    `json:"note"`
	Entries    []struct {
		CommercialID uuid.UUID `json:"commercial_id"`
		DetectedAt   time.Time `json:"detected_at"`
		Note         string    `json:"note"`
	} `json:"entries"`
}

// CreateManualBatch — admin "Adicionar veiculações em lote". Multipart:
//   - meta: JSON {campaign_id, station_id, note, entries:[{commercial_id, detected_at, note}]}
//   - proof: PDF comprovante (opcional) → cria 1 manual_proof_batches; todas as linhas o referenciam
//   - audio_0..audio_{N-1}: censura por linha (opcional), indexado pela posição em entries
//
// Tudo-ou-nada nas LINHAS: se qualquer linha falha validação (sintática ou
// vínculo material×emissora×campanha) → 422 com erros por índice, nada criado.
// Upload de PDF/áudio é NÃO-FATAL: a veiculação é criada mesmo se o upload falhar
// (fica evidence_status='missing'; o PDF, se falhar, deixa as linhas sem lote).
func (h *DetectionsHandler) CreateManualBatch(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.ClaimsFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Teto generoso de corpo: PDF (25MB) + N áudios (25MB cada). 600MB cobre ~23 áudios.
	r.Body = http.MaxBytesReader(w, r.Body, 600<<20)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		http.Error(w, "invalid multipart payload", http.StatusBadRequest)
		return
	}

	var meta manualBatchMeta
	if err := json.Unmarshal([]byte(r.FormValue("meta")), &meta); err != nil {
		http.Error(w, "invalid meta json", http.StatusBadRequest)
		return
	}
	if meta.CampaignID == uuid.Nil || meta.StationID == uuid.Nil {
		http.Error(w, "campaign_id and station_id are required", http.StatusBadRequest)
		return
	}
	if len(meta.Entries) == 0 {
		http.Error(w, "entries must not be empty", http.StatusBadRequest)
		return
	}

	entries := make([]catalog.ManualBatchEntry, 0, len(meta.Entries))
	var synErrs []catalog.ManualBatchEntryError
	for i, e := range meta.Entries {
		switch {
		case e.CommercialID == uuid.Nil:
			synErrs = append(synErrs, catalog.ManualBatchEntryError{Index: i, Message: "material obrigatório"})
		case e.DetectedAt.IsZero():
			synErrs = append(synErrs, catalog.ManualBatchEntryError{Index: i, Message: "horário obrigatório"})
		case e.DetectedAt.After(time.Now().Add(5 * time.Minute)):
			synErrs = append(synErrs, catalog.ManualBatchEntryError{Index: i, Message: "horário não pode ser no futuro"})
		default:
			entries = append(entries, catalog.ManualBatchEntry{
				CommercialID: e.CommercialID, DetectedAt: e.DetectedAt, Note: e.Note,
			})
		}
	}
	if len(synErrs) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"errors": synErrs})
		return
	}

	// Vínculo: tudo-ou-nada. Nada é criado se qualquer linha falhar.
	if linkErrs := h.Repo.ValidateBatchLinks(r.Context(), meta.CampaignID, meta.StationID, entries); len(linkErrs) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"errors": linkErrs})
		return
	}

	var warnings []string

	// PDF comprovante (opcional) — upload ANTES dos inserts (precisamos do batch_id
	// pra compor a chave). Não-fatal: se falhar, criamos as veiculações sem lote.
	var proofBatchID *uuid.UUID
	var proofKey string
	var proofSize int64
	if file, header, ferr := r.FormFile("proof"); ferr == nil {
		defer file.Close()
		if header.Size > manualProofMaxBytes {
			http.Error(w, "PDF acima de 25MB", http.StatusRequestEntityTooLarge)
			return
		}
		ctype := header.Header.Get("Content-Type")
		if !strings.HasPrefix(strings.ToLower(ctype), "application/pdf") {
			http.Error(w, "comprovante precisa ser PDF", http.StatusUnsupportedMediaType)
			return
		}
		if h.Storage != nil {
			bid := uuid.New()
			key := fmt.Sprintf("proofs/%s/%s/%s/%s/%s.pdf",
				entries[0].DetectedAt.UTC().Format("2006"),
				entries[0].DetectedAt.UTC().Format("01"),
				entries[0].DetectedAt.UTC().Format("02"),
				meta.StationID, bid)
			if err := h.Storage.Put(r.Context(), key, file, "application/pdf"); err != nil {
				warnings = append(warnings, "upload do PDF comprovante falhou — veiculações criadas sem comprovante")
			} else {
				proofBatchID = &bid
				proofKey = key
				proofSize = header.Size
			}
		}
	}

	out, err := h.Repo.CreateManualBatch(r.Context(), catalog.CreateManualBatchInput{
		CampaignID:   meta.CampaignID,
		StationID:    meta.StationID,
		ManualBy:     claims.UserID,
		BatchNote:    meta.Note,
		ProofBatchID: proofBatchID,
		ProofPDFKey:  proofKey,
		ProofPDFSize: proofSize,
		Entries:      entries,
	})
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Censura por linha (opcional) — audio_i casa com out[i]. Não-fatal.
	// (`header` é *multipart.FileHeader; `file` é multipart.File — upload inline
	// pra não brigar com tipos; cada file é fechado no caminho que o consome.)
	for i, det := range out {
		file, header, ferr := r.FormFile(fmt.Sprintf("audio_%d", i))
		if ferr != nil {
			continue
		}
		ctype := header.Header.Get("Content-Type")
		ext, okExt := manualAudioMIME[strings.ToLower(ctype)]
		if !okExt {
			file.Close()
			warnings = append(warnings, fmt.Sprintf("linha %d: formato de áudio não suportado", i))
			continue
		}
		if header.Size > manualAudioMaxBytes {
			file.Close()
			warnings = append(warnings, fmt.Sprintf("linha %d: áudio acima de 25MB", i))
			continue
		}
		if h.Storage == nil {
			file.Close()
			continue
		}
		key := fmt.Sprintf("evidences/%s/%s/%s/%s/%s.%s",
			det.DetectedAt.UTC().Format("2006"),
			det.DetectedAt.UTC().Format("01"),
			det.DetectedAt.UTC().Format("02"),
			meta.StationID, det.ID, ext)
		if err := h.Storage.Put(r.Context(), key, file, ctype); err != nil {
			file.Close()
			warnings = append(warnings, fmt.Sprintf("linha %d: upload do áudio falhou", i))
			continue
		}
		file.Close()
		if err := h.Repo.UpdateEvidence(r.Context(), det.ID, det.DetectedAt, "available", key, header.Size); err != nil {
			warnings = append(warnings, fmt.Sprintf("linha %d: erro ao gravar evidência", i))
		}
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"batch_id":   proofBatchID,
		"detections": out,
		"warnings":   warnings,
	})
}

// UploadEvidence — admin "Subir censura (áudio)" em /detections/:id. Anexa o
// áudio a uma detecção que ainda NÃO tem áudio (qualquer detecção: manual, via
// lote, ou automática sem evidência). Multipart, campo `audio`.
func (h *DetectionsHandler) UploadEvidence(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	det, err := h.Repo.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", http.StatusNotFound)
		} else {
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}
	// Guard: só quando ainda não há áudio.
	if det.EvidenceStatus == "available" || det.EvidenceKey != nil {
		http.Error(w, "essa veiculação já tem censura", http.StatusConflict)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, manualAudioMaxBytes+(1<<20))
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		http.Error(w, "invalid multipart payload (limite 25MB)", http.StatusBadRequest)
		return
	}
	file, header, ferr := r.FormFile("audio")
	if ferr != nil {
		http.Error(w, "campo 'audio' obrigatório", http.StatusBadRequest)
		return
	}
	defer file.Close()
	ctype := header.Header.Get("Content-Type")
	ext, okExt := manualAudioMIME[strings.ToLower(ctype)]
	if !okExt {
		http.Error(w, "formato de áudio não suportado (use mp3, m4a, wav, aac ou ogg)", http.StatusUnsupportedMediaType)
		return
	}
	if h.Storage == nil {
		http.Error(w, "storage indisponível", http.StatusInternalServerError)
		return
	}
	key := fmt.Sprintf("evidences/%s/%s/%s/%s/%s.%s",
		det.DetectedAt.UTC().Format("2006"),
		det.DetectedAt.UTC().Format("01"),
		det.DetectedAt.UTC().Format("02"),
		det.StationID, det.ID, ext)
	if err := h.Storage.Put(r.Context(), key, file, ctype); err != nil {
		http.Error(w, "falha no upload", http.StatusInternalServerError)
		return
	}
	if err := h.Repo.UpdateEvidence(r.Context(), det.ID, det.DetectedAt, "available", key, header.Size); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	updated, err := h.Repo.Get(r.Context(), det.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// ProofURL — presigned GET do PDF comprovante do lote da detecção. Espelha
// EvidenceURL. 404 quando a detecção não pertence a nenhum lote.
func (h *DetectionsHandler) ProofURL(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	key, err := h.Repo.ProofKeyForDetection(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "sem comprovante", http.StatusNotFound)
		} else {
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}
	const ttl = 5 * time.Minute
	url, expiresAt, err := h.Storage.PresignGet(r.Context(), key, ttl)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"url":        url,
		"expires_at": expiresAt.UTC().Format(time.RFC3339),
	})
}
