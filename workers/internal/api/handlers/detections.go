package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"radiocheck/internal/auth"
	"radiocheck/internal/catalog"
	"radiocheck/internal/reportcsv"
	"radiocheck/internal/storage"
)

type DetectionsHandler struct {
	Repo         *catalog.Detections
	CampaignRepo *catalog.Campaigns
	Storage      *storage.Client
	SummaryRepo  *catalog.DailySummaryRepo
}

func (h *DetectionsHandler) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	// When ?page=N is passed we go down the paginated path (airtime report
	// consumer). Without page we keep the legacy shape — array wrapped in
	// {data: [...]} with offset/limit — that DayDetailModal already eats.
	if q.Get("page") != "" {
		h.listPaged(w, r)
		return
	}

	f := catalog.ListFilter{
		ClientID: auth.ClientScopeFromContext(r.Context()),
	}
	if v := q.Get("campaign_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			http.Error(w, "invalid campaign_id", 400)
			return
		}
		f.CampaignID = &id
	}
	if v := q.Get("station_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			http.Error(w, "invalid station_id", 400)
			return
		}
		f.StationID = &id
	}
	if v := q.Get("start_date"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			http.Error(w, "invalid start_date (use RFC3339)", 400)
			return
		}
		f.StartDate = &t
	}
	if v := q.Get("end_date"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			http.Error(w, "invalid end_date (use RFC3339)", 400)
			return
		}
		f.EndDate = &t
	}
	if v := q.Get("limit"); v != "" {
		n, _ := strconv.Atoi(v)
		f.Limit = n
	}
	if v := q.Get("offset"); v != "" {
		n, _ := strconv.Atoi(v)
		f.Offset = n
	}
	items, err := h.Repo.List(r.Context(), f)
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	writeJSON(w, 200, map[string]any{"data": items})
}

// listPaged is invoked when ?page=N is present on /detections. Accepts
// campaign_id, from/to (RFC3339), q (search), sort, page_size (1..200,
// default 10), page (>= 1). Returns the ListPagedResult shape consumed by
// the airtime report.
func (h *DetectionsHandler) listPaged(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := catalog.ListPagedFilter{
		ClientID: auth.ClientScopeFromContext(r.Context()),
	}

	if v := q.Get("campaign_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			http.Error(w, "invalid campaign_id", 400)
			return
		}
		f.CampaignID = &id
	}
	if v := q.Get("from"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			http.Error(w, "invalid from (use RFC3339)", 400)
			return
		}
		f.StartDate = &t
	}
	if v := q.Get("to"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			http.Error(w, "invalid to (use RFC3339)", 400)
			return
		}
		f.EndDate = &t
	}
	if v := q.Get("q"); v != "" {
		f.Q = v
	}
	if v := q.Get("sort"); v != "" {
		f.Sort = v
	}
	if v := q.Get("page"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			http.Error(w, "invalid page (>=1)", 400)
			return
		}
		f.Page = n
	}
	if v := q.Get("page_size"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			http.Error(w, "invalid page_size (1..200)", 400)
			return
		}
		f.PageSize = n
	}

	res, err := h.Repo.ListPaged(r.Context(), f)
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	writeJSON(w, 200, res)
}

func (h *DetectionsHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}
	// Viewer scope: verify campaign ownership before fetching full detail.
	if scope := auth.ClientScopeFromContext(r.Context()); scope != nil {
		clientID, err := h.Repo.GetClientID(r.Context(), id)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				http.Error(w, "not found", 404)
			} else {
				http.Error(w, "internal error", 500)
			}
			return
		}
		if *clientID != *scope {
			http.Error(w, "not found", 404)
			return
		}
	}
	det, err := h.Repo.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", 404)
		} else {
			http.Error(w, "internal error", 500)
		}
		return
	}
	writeJSON(w, 200, det)
}

// EvidenceURL returns a short-lived presigned GET URL for the detection's
// evidence clip. Used by the internal frontend so <audio> / <a download>
// tags — which cannot send the Authorization header — can fetch the clip
// directly from object storage. The TTL is short (5 min) because the URL
// inherits no auth once issued; the frontend should re-fetch it close to
// expiry. Returns 404 with the same semantics as Evidence (detection not
// found, or clip not yet available).
func (h *DetectionsHandler) EvidenceURL(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}
	// Viewer scope: verify campaign ownership before returning evidence URL.
	if scope := auth.ClientScopeFromContext(r.Context()); scope != nil {
		clientID, err := h.Repo.GetClientID(r.Context(), id)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				http.Error(w, "not found", 404)
			} else {
				http.Error(w, "internal error", 500)
			}
			return
		}
		if *clientID != *scope {
			http.Error(w, "not found", 404)
			return
		}
	}
	det, err := h.Repo.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", 404)
		} else {
			http.Error(w, "internal error", 500)
		}
		return
	}
	if det.EvidenceStatus != "available" || det.EvidenceKey == nil {
		http.Error(w, "evidence not available", 404)
		return
	}
	const ttl = 5 * time.Minute
	url, expiresAt, err := h.Storage.PresignGet(r.Context(), *det.EvidenceKey, ttl)
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	writeJSON(w, 200, map[string]any{
		"url":        url,
		"expires_at": expiresAt.UTC().Format(time.RFC3339),
	})
}

func (h *DetectionsHandler) Evidence(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}
	// Viewer scope: verify campaign ownership before serving evidence audio.
	if scope := auth.ClientScopeFromContext(r.Context()); scope != nil {
		clientID, err := h.Repo.GetClientID(r.Context(), id)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				http.Error(w, "not found", 404)
			} else {
				http.Error(w, "internal error", 500)
			}
			return
		}
		if *clientID != *scope {
			http.Error(w, "not found", 404)
			return
		}
	}
	det, err := h.Repo.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", 404)
		} else {
			http.Error(w, "internal error", 500)
		}
		return
	}
	if det.EvidenceStatus != "available" || det.EvidenceKey == nil {
		http.Error(w, "evidence not available", 404)
		return
	}
	body, ct, _, err := h.Storage.Get(r.Context(), *det.EvidenceKey)
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	defer body.Close()
	if ct == "" {
		ct = "audio/mp4"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Disposition", "inline; filename=\""+id.String()+".m4a\"")
	// Streaming direto S3→cliente: sem io.ReadAll (bufferizava o objeto inteiro no
	// heap do processo que também roda os ffmpeg). Trade-off consciente: sem
	// Accept-Ranges — os arquivos têm poucos MB, o player faz seek client-side.
	if _, err := io.Copy(w, body); err != nil {
		return // cliente desconectou no meio; nada útil a fazer (header já foi)
	}
}

// manualAudioMIME mapeia content-types aceitos no upload da "censura" → extensão
// usada na chave S3. Browsers costumam mandar audio/mpeg (mp3), audio/mp4 (m4a)
// e audio/wav. Outros formatos são rejeitados pra evitar binário arbitrário
// sentando no bucket de evidências.
var manualAudioMIME = map[string]string{
	"audio/mpeg":  "mp3",
	"audio/mp3":   "mp3",
	"audio/mp4":   "m4a",
	"audio/x-m4a": "m4a",
	"audio/aac":   "aac",
	"audio/wav":   "wav",
	"audio/x-wav": "wav",
	"audio/wave":  "wav",
	"audio/ogg":   "ogg",
}

// manualAudioExtFallback mapeia a extensão do filename → extensão canônica na
// chave S3. Usado quando o Content-Type do browser não bate no manualAudioMIME
// (ex.: .mpeg costuma vir como video/mpeg; drag-drop às vezes manda
// application/octet-stream ou vazio). Espelha a validação por extensão do
// upload de material (/campaigns passo 4), pra que um arquivo aceito lá também
// suba como censura em /detections.
var manualAudioExtFallback = map[string]string{
	"mp3":  "mp3",
	"mpeg": "mp3",
	"m4a":  "m4a",
	"mp4":  "m4a",
	"wav":  "wav",
	"aac":  "aac",
	"ogg":  "ogg",
}

// manualAudioCanonicalMIME dá um Content-Type de áudio "limpo" pra gravar no S3
// quando o formato foi resolvido por extensão (o browser pode ter mandado
// video/mpeg / octet-stream, que não queremos persistir na evidência).
var manualAudioCanonicalMIME = map[string]string{
	"mp3": "audio/mpeg",
	"m4a": "audio/mp4",
	"wav": "audio/wav",
	"aac": "audio/aac",
	"ogg": "audio/ogg",
}

// resolveManualAudioExt decide a extensão canônica do áudio de censura e o
// Content-Type a persistir no S3. Tenta primeiro o Content-Type (mantendo o do
// browser quando reconhecido) e, como fallback, a extensão do filename (com um
// Content-Type canônico). ok=false quando nenhum dos dois é um áudio conhecido
// → o handler responde 415.
func resolveManualAudioExt(contentType, filename string) (ext, storeContentType string, ok bool) {
	ct := strings.ToLower(strings.TrimSpace(contentType))
	if i := strings.IndexByte(ct, ';'); i >= 0 { // descarta "; charset=..."
		ct = strings.TrimSpace(ct[:i])
	}
	if e, found := manualAudioMIME[ct]; found {
		return e, contentType, true
	}
	if i := strings.LastIndexByte(filename, '.'); i >= 0 {
		if e, found := manualAudioExtFallback[strings.ToLower(filename[i+1:])]; found {
			return e, manualAudioCanonicalMIME[e], true
		}
	}
	return "", "", false
}

const manualAudioMaxBytes = 25 << 20 // 25 MB

// CreateManual is the admin "Adicionar veiculação manualmente" action. The
// payload mirrors the form on the DayDetailModal: campaign + station + the
// chosen material + the declared timestamp + an optional note + an optional
// audio file (the "censura" recorded by the broadcaster). The repo validates
// the material↔station↔campaign link and runs the same categorizer the real
// engine uses, so the inserted row participates in agregados just like an
// automatic detection.
//
// Aceita JSON (sem áudio) OU multipart/form-data (com áudio opcional no campo
// `audio`). O JSON-only path preserva o uso por API ou testes que só querem
// inserir metadados.
func (h *DetectionsHandler) CreateManual(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.ClaimsFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var (
		campaignID, stationID, commercialID uuid.UUID
		detectedAt                          time.Time
		note                                string
		audioReader                         io.Reader
		audioSize                           int64
		audioExt                            string
		audioContentType                    string
	)

	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/form-data") {
		r.Body = http.MaxBytesReader(w, r.Body, manualAudioMaxBytes+(1<<20))
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			http.Error(w, "invalid multipart payload (limite 25MB)", http.StatusBadRequest)
			return
		}
		var err error
		if campaignID, err = uuid.Parse(r.FormValue("campaign_id")); err != nil {
			http.Error(w, "invalid campaign_id", http.StatusBadRequest)
			return
		}
		if stationID, err = uuid.Parse(r.FormValue("station_id")); err != nil {
			http.Error(w, "invalid station_id", http.StatusBadRequest)
			return
		}
		if commercialID, err = uuid.Parse(r.FormValue("commercial_id")); err != nil {
			http.Error(w, "invalid commercial_id", http.StatusBadRequest)
			return
		}
		if detectedAt, err = time.Parse(time.RFC3339, r.FormValue("detected_at")); err != nil {
			http.Error(w, "invalid detected_at (use RFC3339)", http.StatusBadRequest)
			return
		}
		note = r.FormValue("note")

		file, header, ferr := r.FormFile("audio")
		if ferr == nil {
			defer file.Close()
			ext, storeCT, accepted := resolveManualAudioExt(header.Header.Get("Content-Type"), header.Filename)
			if !accepted {
				http.Error(w, "formato de áudio não suportado (use mp3, m4a, wav, aac, mpeg ou ogg)", http.StatusUnsupportedMediaType)
				return
			}
			audioContentType = storeCT
			audioExt = ext
			audioSize = header.Size
			audioReader = file
		}
	} else {
		// JSON fallback.
		var in struct {
			CampaignID   uuid.UUID `json:"campaign_id"`
			StationID    uuid.UUID `json:"station_id"`
			CommercialID uuid.UUID `json:"commercial_id"`
			DetectedAt   time.Time `json:"detected_at"`
			Note         string    `json:"note"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		campaignID, stationID, commercialID = in.CampaignID, in.StationID, in.CommercialID
		detectedAt = in.DetectedAt
		note = in.Note
	}

	if campaignID == uuid.Nil || stationID == uuid.Nil || commercialID == uuid.Nil {
		http.Error(w, "campaign_id, station_id and commercial_id are required", http.StatusBadRequest)
		return
	}
	if detectedAt.IsZero() {
		http.Error(w, "detected_at is required", http.StatusBadRequest)
		return
	}
	if detectedAt.After(time.Now().Add(5 * time.Minute)) {
		http.Error(w, "detected_at cannot be in the future", http.StatusBadRequest)
		return
	}

	det, err := h.Repo.CreateManual(r.Context(), catalog.CreateManualInput{
		StationID:    stationID,
		CommercialID: commercialID,
		CampaignID:   campaignID,
		DetectedAt:   detectedAt,
		ManualBy:     claims.UserID,
		ManualNote:   note,
	})
	if err != nil {
		if errors.Is(err, catalog.ErrMaterialNotLinkedToStation) {
			http.Error(w, "material is not linked to this station in this campaign", http.StatusUnprocessableEntity)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Upload do áudio acontece DEPOIS do insert pra usar o ID gerado pelo
	// banco na chave S3 (mesmo padrão do evidence.Service automático). Se o
	// upload falhar, a detection permanece com evidence_status='missing' —
	// o admin pode reinserir ou subir o áudio depois (futuro endpoint).
	if audioReader != nil && h.Storage != nil {
		key := fmt.Sprintf("evidences/%s/%s/%s/%s/%s.%s",
			detectedAt.UTC().Format("2006"),
			detectedAt.UTC().Format("01"),
			detectedAt.UTC().Format("02"),
			stationID,
			det.ID,
			audioExt,
		)
		if err := h.Storage.Put(r.Context(), key, audioReader, audioContentType); err != nil {
			// Insere ficou OK, só o áudio falhou — retorna a detection
			// como está (evidence_status='missing') com 207 pra o caller
			// poder logar/reagir.
			writeJSON(w, http.StatusMultiStatus, map[string]any{
				"detection":     det,
				"audio_error":   err.Error(),
				"audio_warning": "veiculação criada mas o upload do áudio falhou — tente desconsiderar e reinserir",
			})
			return
		}
		if err := h.Repo.UpdateEvidence(r.Context(), det.ID, det.DetectedAt, "available", key, audioSize); err != nil {
			http.Error(w, "internal error updating evidence", http.StatusInternalServerError)
			return
		}
		if updated, err := h.Repo.Get(r.Context(), det.ID); err == nil {
			det = updated
		}
	}

	writeJSON(w, http.StatusCreated, det)
}

// Ignore is the admin "desconsiderar veiculação" action: stamps ignored_at
// on the row so daily_play_summary skips it. Reversible via Restore. The
// audio evidence and category column are preserved for auditing — only the
// aggregate counters drop the row.
func (h *DetectionsHandler) Ignore(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	claims, ok := auth.ClaimsFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	// Existence + idempotency guard: surface 404 if the row doesn't exist,
	// otherwise the UPDATE silently no-ops.
	det, err := h.Repo.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", http.StatusNotFound)
		} else {
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}
	if err := h.Repo.Ignore(r.Context(), det.ID, claims.UserID); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	updated, err := h.Repo.Get(r.Context(), id)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// Restore reverts Ignore by clearing ignored_at/ignored_by. The row counts
// in daily_play_summary again immediately.
func (h *DetectionsHandler) Restore(w http.ResponseWriter, r *http.Request) {
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
	if err := h.Repo.Restore(r.Context(), det.ID); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	updated, err := h.Repo.Get(r.Context(), id)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// Export streams detections as CSV (semicolon-separated, BOM-prefixed UTF-8
// so Excel pt-BR opens it correctly). Admin-only — gated at the router.
// Uses IterateForExport so memory stays bounded regardless of row count.
func (h *DetectionsHandler) Export(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := catalog.ListPagedFilter{}

	if v := q.Get("campaign_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			http.Error(w, "invalid campaign_id", http.StatusBadRequest)
			return
		}
		f.CampaignID = &id
	}
	if v := q.Get("from"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			http.Error(w, "invalid from (use RFC3339)", http.StatusBadRequest)
			return
		}
		f.StartDate = &t
	}
	if v := q.Get("to"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			http.Error(w, "invalid to (use RFC3339)", http.StatusBadRequest)
			return
		}
		f.EndDate = &t
	}
	if v := q.Get("q"); v != "" {
		f.Q = v
	}
	if v := q.Get("sort"); v != "" {
		f.Sort = v
	}

	filename := fmt.Sprintf("veiculacoes_%s.csv", time.Now().Format("20060102_150405"))
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.WriteHeader(http.StatusOK)

	// BOM so Excel pt-BR detects UTF-8.
	// A formatação mora em internal/reportcsv porque o bundle .zip do pós-venda
	// escreve o MESMO arquivo em memória. Erro aqui não vira 500: o header já
	// foi escrito, então só cortamos o stream.
	_ = reportcsv.WriteDetailed(w, func(cb func(catalog.DetectionEnriched) error) error {
		return h.Repo.IterateForExport(r.Context(), f, cb)
	})
}

// categoryLabelPT delega pro vocabulário canônico em reportcsv. Continua aqui
// porque outros handlers deste arquivo o usam.
func categoryLabelPT(c string) string { return reportcsv.CategoryLabelPT(c) }

func strOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// AggregateByMaterial backs the airtime-report sidebar panel. Requires
// campaign_id; accepts from/to (RFC3339) and q (same semantics as the
// paginated list so the sidebar stays in sync with the lista's filters).
func (h *DetectionsHandler) AggregateByMaterial(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	cidStr := q.Get("campaign_id")
	if cidStr == "" {
		http.Error(w, "campaign_id required", http.StatusBadRequest)
		return
	}
	cid, err := uuid.Parse(cidStr)
	if err != nil {
		http.Error(w, "invalid campaign_id", http.StatusBadRequest)
		return
	}
	// Viewer scope: verify campaign ownership before aggregating.
	if scope := auth.ClientScopeFromContext(r.Context()); scope != nil {
		camp, err := h.CampaignRepo.Get(r.Context(), cid)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				http.Error(w, "not found", http.StatusNotFound)
			} else {
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
			return
		}
		if camp.ClientID != *scope {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
	}
	f := catalog.AggregateFilter{CampaignID: cid}
	if v := q.Get("from"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			http.Error(w, "invalid from (use RFC3339)", http.StatusBadRequest)
			return
		}
		f.StartDate = &t
	}
	if v := q.Get("to"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			http.Error(w, "invalid to (use RFC3339)", http.StatusBadRequest)
			return
		}
		f.EndDate = &t
	}
	if v := q.Get("q"); v != "" {
		f.Q = v
	}
	res, err := h.Repo.AggregateByMaterial(r.Context(), f)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *DetectionsHandler) DailySummary(w http.ResponseWriter, r *http.Request) {
	campaignID, err := uuid.Parse(chi.URLParam(r, "campaignID"))
	if err != nil {
		http.Error(w, "invalid campaignID", http.StatusBadRequest)
		return
	}
	// Viewer scope: verify campaign ownership before returning summary.
	if scope := auth.ClientScopeFromContext(r.Context()); scope != nil && h.CampaignRepo != nil {
		camp, err := h.CampaignRepo.Get(r.Context(), campaignID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				http.Error(w, "not found", http.StatusNotFound)
			} else {
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
			return
		}
		if camp.ClientID != *scope {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
	}
	fromStr := r.URL.Query().Get("from")
	toStr := r.URL.Query().Get("to")
	from, err := time.Parse("2006-01-02", fromStr)
	if err != nil {
		http.Error(w, "from must be YYYY-MM-DD", http.StatusBadRequest)
		return
	}
	to, err := time.Parse("2006-01-02", toStr)
	if err != nil {
		http.Error(w, "to must be YYYY-MM-DD", http.StatusBadRequest)
		return
	}
	rows, err := h.SummaryRepo.ListByCampaign(r.Context(), campaignID, from, to)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if rows == nil {
		rows = []catalog.DailySummaryRow{}
	}
	writeJSON(w, http.StatusOK, rows)
}
