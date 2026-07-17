package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"radiocheck/internal/auth"
	"radiocheck/internal/catalog"
	"radiocheck/internal/storage"
	"radiocheck/internal/users"
)

// SuggestionsHandler serves the "Central de Sugestões" endpoints. Persona
// gating (dev vs autor) is enforced here, server-side: the JWT only carries
// UserID/Role/ClientID (no email), so isDev loads the user's email and
// compares it, case-insensitively, to DevEmail (config SUGGESTIONS_DEV_EMAIL).
// See docs/features/suggestions-board.md.
type SuggestionsHandler struct {
	Repo     *catalog.Suggestions
	Users    *users.Repo
	Storage  *storage.Client
	DevEmail string
}

const suggestionAttachmentMaxBytes = 10 << 20 // 10 MB por imagem

// suggestionImageMIME is the allow-list of content-types accepted as
// attachments → file extension used in the storage key.
var suggestionImageMIME = map[string]string{
	"image/png":  "png",
	"image/jpeg": "jpg",
	"image/webp": "webp",
	"image/gif":  "gif",
}

var (
	suggestionTypes         = map[string]bool{"bug": true, "melhoria": true, "feature": true, "duvida": true}
	suggestionReqPriorities = map[string]bool{"baixa": true, "media": true, "alta": true}
	suggestionStatuses      = map[string]bool{"nova": true, "em_analise": true, "aceita": true, "em_progresso": true, "concluida": true, "recusada": true}
	suggestionDevPriorities = map[string]bool{"urgente": true, "alta": true, "media": true, "baixa": true}
	suggestionEfforts       = map[string]bool{"P": true, "M": true, "G": true}
)

// isDev reports whether the caller is the dev (email == DevEmail) and returns
// the caller's UserID. The JWT lacks email, so it hits the users repo.
func (h *SuggestionsHandler) isDev(ctx context.Context) (bool, uuid.UUID, error) {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return false, uuid.Nil, errors.New("no claims in context")
	}
	u, err := h.Users.Get(ctx, claims.UserID)
	if err != nil {
		return false, claims.UserID, err
	}
	return strings.EqualFold(u.Email, h.DevEmail), claims.UserID, nil
}

// suggestionDetailDTO embeds the suggestion and nests its thread/attachments/
// timeline so the frontend receives one flat object (fields promoted).
type suggestionDetailDTO struct {
	*catalog.Suggestion
	Comments    []catalog.SuggestionComment    `json:"comments"`
	Attachments []catalog.SuggestionAttachment `json:"attachments"`
	Events      []catalog.SuggestionEvent      `json:"events"`
}

// Create — POST /suggestions. Any admin/operator can file a suggestion.
func (h *SuggestionsHandler) Create(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.ClaimsFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var body struct {
		Title             string `json:"title"`
		Description       string `json:"description"`
		Type              string `json:"type"`
		TargetScreen      string `json:"target_screen"`
		RequesterPriority string `json:"requester_priority"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	errs := map[string]string{}
	if strings.TrimSpace(body.Title) == "" {
		errs["title"] = "título é obrigatório"
	}
	if strings.TrimSpace(body.Description) == "" {
		errs["description"] = "descrição é obrigatória"
	}
	if !suggestionTypes[body.Type] {
		errs["type"] = "tipo inválido"
	}
	if !suggestionReqPriorities[body.RequesterPriority] {
		errs["requester_priority"] = "prioridade inválida"
	}
	if len(errs) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"errors": errs})
		return
	}
	sug, err := h.Repo.Create(r.Context(), catalog.CreateSuggestionInput{
		CreatedBy:         claims.UserID,
		Title:             strings.TrimSpace(body.Title),
		Description:       strings.TrimSpace(body.Description),
		Type:              body.Type,
		TargetScreen:      strings.TrimSpace(body.TargetScreen),
		RequesterPriority: body.RequesterPriority,
	})
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, sug)
}

// List — GET /suggestions. Non-dev callers are hard-scoped to their own
// suggestions server-side; dev sees all with optional filters.
func (h *SuggestionsHandler) List(w http.ResponseWriter, r *http.Request) {
	isDev, uid, err := h.isDev(r.Context())
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	q := r.URL.Query()
	filter := catalog.ListSuggestionsFilter{
		Query:       q.Get("q"),
		Sort:        q.Get("sort"),
		ViewerID:    uid,
		ViewerIsDev: isDev,
	}
	if !isDev {
		filter.OnlyAuthorID = &uid
	} else {
		filter.Status = q.Get("status")
		filter.Type = q.Get("type")
		filter.Priority = q.Get("priority")
		if aid := q.Get("author_id"); aid != "" {
			if parsed, perr := uuid.Parse(aid); perr == nil {
				filter.AuthorID = &parsed
			}
		}
	}
	list, err := h.Repo.List(r.Context(), filter)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if list == nil {
		list = []catalog.Suggestion{}
	}
	if !isDev {
		for i := range list {
			list[i].DevNotes = nil // never leak private notes to an autor
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": list})
}

// Get — GET /suggestions/{id}. Autor may only read their own (403 otherwise);
// dev_notes is stripped for non-dev. Fires MarkRead. Attachments carry a
// presigned URL.
func (h *SuggestionsHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	isDev, uid, err := h.isDev(r.Context())
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	sug, err := h.Repo.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", http.StatusNotFound)
		} else {
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}
	if !isDev && (sug.CreatedBy == nil || *sug.CreatedBy != uid) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !isDev {
		sug.DevNotes = nil
	}

	comments, err := h.Repo.ListComments(r.Context(), id)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	attachments, err := h.Repo.ListAttachments(r.Context(), id)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	events, err := h.Repo.ListEvents(r.Context(), id)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if comments == nil {
		comments = []catalog.SuggestionComment{}
	}
	if attachments == nil {
		attachments = []catalog.SuggestionAttachment{}
	}
	if events == nil {
		events = []catalog.SuggestionEvent{}
	}
	if h.Storage != nil {
		for i := range attachments {
			if url, _, perr := h.Storage.PresignGet(r.Context(), attachments[i].StorageKey, 5*time.Minute); perr == nil {
				attachments[i].URL = url
			}
		}
	}

	// Mark read for the caller — best-effort, out of band so a slow write
	// never blocks the read. Detached context (request ctx may cancel).
	go func() {
		bg, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = h.Repo.MarkRead(bg, uid, id)
	}()

	writeJSON(w, http.StatusOK, suggestionDetailDTO{
		Suggestion:  sug,
		Comments:    comments,
		Attachments: attachments,
		Events:      events,
	})
}

// Patch — PATCH /suggestions/{id}. Dev-only management fields.
func (h *SuggestionsHandler) Patch(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	isDev, uid, err := h.isDev(r.Context())
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !isDev {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var body struct {
		Status         *string `json:"status"`
		DevPriority    *string `json:"dev_priority"`
		Effort         *string `json:"effort"`
		DevFeedback    *string `json:"dev_feedback"`
		DevNotes       *string `json:"dev_notes"`
		AwaitingAuthor *bool   `json:"awaiting_author"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	errs := map[string]string{}
	if body.Status != nil && !suggestionStatuses[*body.Status] {
		errs["status"] = "status inválido"
	}
	if body.DevPriority != nil && !suggestionDevPriorities[*body.DevPriority] {
		errs["dev_priority"] = "prioridade inválida"
	}
	if body.Effort != nil && !suggestionEfforts[*body.Effort] {
		errs["effort"] = "esforço inválido"
	}
	if len(errs) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"errors": errs})
		return
	}
	sug, err := h.Repo.Update(r.Context(), id, catalog.UpdateSuggestionInput{
		ActorID:        uid,
		Status:         body.Status,
		DevPriority:    body.DevPriority,
		Effort:         body.Effort,
		DevFeedback:    body.DevFeedback,
		DevNotes:       body.DevNotes,
		AwaitingAuthor: body.AwaitingAuthor,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", http.StatusNotFound)
		} else {
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}
	writeJSON(w, http.StatusOK, sug)
}

// AddComment — POST /suggestions/{id}/comments. Owner or dev.
func (h *SuggestionsHandler) AddComment(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	isDev, uid, err := h.isDev(r.Context())
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	sug, err := h.Repo.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", http.StatusNotFound)
		} else {
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}
	isOwner := sug.CreatedBy != nil && *sug.CreatedBy == uid
	if !isDev && !isOwner {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var body struct {
		Body string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(body.Body) == "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"errors": map[string]string{"body": "mensagem vazia"}})
		return
	}
	c, err := h.Repo.AddComment(r.Context(), id, uid, strings.TrimSpace(body.Body))
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// The autor replying clears the "aguardando você" flag (ball back to dev).
	if !isDev && isOwner && sug.AwaitingAuthor {
		cleared := false
		_, _ = h.Repo.Update(r.Context(), id, catalog.UpdateSuggestionInput{ActorID: uid, AwaitingAuthor: &cleared})
	}
	writeJSON(w, http.StatusCreated, c)
}

// UploadAttachment — POST /suggestions/{id}/attachments. Multipart, field
// `file`, optional `comment_id`. Owner or dev. Image MIME allow-list, ~10MB.
func (h *SuggestionsHandler) UploadAttachment(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	isDev, uid, err := h.isDev(r.Context())
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	sug, err := h.Repo.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", http.StatusNotFound)
		} else {
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}
	if !isDev && (sug.CreatedBy == nil || *sug.CreatedBy != uid) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, suggestionAttachmentMaxBytes+(1<<20))
	if err := r.ParseMultipartForm(4 << 20); err != nil {
		http.Error(w, "invalid multipart payload (limite 10MB)", http.StatusBadRequest)
		return
	}
	file, header, ferr := r.FormFile("file")
	if ferr != nil {
		http.Error(w, "campo 'file' obrigatório", http.StatusBadRequest)
		return
	}
	defer file.Close()
	ctype := header.Header.Get("Content-Type")
	ext, okExt := suggestionImageMIME[strings.ToLower(strings.TrimSpace(ctype))]
	if !okExt {
		http.Error(w, "formato não suportado (use png, jpeg, webp ou gif)", http.StatusUnsupportedMediaType)
		return
	}
	if header.Size > suggestionAttachmentMaxBytes {
		http.Error(w, "imagem acima de 10MB", http.StatusRequestEntityTooLarge)
		return
	}
	var commentID *uuid.UUID
	if cidStr := strings.TrimSpace(r.FormValue("comment_id")); cidStr != "" {
		cid, perr := uuid.Parse(cidStr)
		if perr != nil {
			http.Error(w, "comment_id inválido", http.StatusBadRequest)
			return
		}
		commentID = &cid
	}
	if h.Storage == nil {
		http.Error(w, "storage indisponível", http.StatusInternalServerError)
		return
	}
	key := fmt.Sprintf("suggestions/%s/%s.%s", id, uuid.New(), ext)
	if err := h.Storage.Put(r.Context(), key, file, ctype); err != nil {
		http.Error(w, "falha no upload", http.StatusInternalServerError)
		return
	}
	att, err := h.Repo.AddAttachment(r.Context(), catalog.AddAttachmentInput{
		SuggestionID: id,
		CommentID:    commentID,
		StorageKey:   key,
		ContentType:  ctype,
		SizeBytes:    header.Size,
		UploadedBy:   uid,
	})
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if url, _, perr := h.Storage.PresignGet(r.Context(), key, 5*time.Minute); perr == nil {
		att.URL = url
	}
	writeJSON(w, http.StatusCreated, att)
}

// ProxyAttachment — GET /suggestions/attachments/{aid}. Owner or dev. Streams
// the image bytes through the API (with JWT) instead of handing the browser a
// presigned bucket URL. Necessary because in prod the MinIO host baked into a
// presigned URL is `localhost:9000`, which the user's browser cannot reach
// (loopback / connection refused) — the same reason evidence audio moved to a
// proxy in 2026-07-03. See docs/features/evidence-presigned-urls.md and
// docs/features/suggestions-board.md.
func (h *SuggestionsHandler) ProxyAttachment(w http.ResponseWriter, r *http.Request) {
	aid, err := uuid.Parse(chi.URLParam(r, "aid"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	isDev, uid, err := h.isDev(r.Context())
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	att, err := h.Repo.GetAttachment(r.Context(), aid)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", http.StatusNotFound)
		} else {
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}
	if !isDev {
		sug, gerr := h.Repo.Get(r.Context(), att.SuggestionID)
		if gerr != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if sug.CreatedBy == nil || *sug.CreatedBy != uid {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
	}
	if h.Storage == nil {
		http.Error(w, "storage indisponível", http.StatusInternalServerError)
		return
	}
	body, ct, _, err := h.Storage.Get(r.Context(), att.StorageKey)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer body.Close()
	if ct == "" {
		ct = att.ContentType
	}
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Disposition", "inline; filename=\""+aid.String()+"\"")
	w.Header().Set("Cache-Control", "private, max-age=300")
	// Streaming direto S3→cliente: sem io.ReadAll (bufferizava o objeto inteiro no
	// heap do processo que também roda os ffmpeg). Trade-off consciente: sem
	// Accept-Ranges — os arquivos têm poucos MB, o player faz seek client-side.
	if _, err := io.Copy(w, body); err != nil {
		return // cliente desconectou no meio; nada útil a fazer (header já foi)
	}
}

// AttachmentURL — GET /suggestions/attachments/{aid}/url. Owner or dev.
func (h *SuggestionsHandler) AttachmentURL(w http.ResponseWriter, r *http.Request) {
	aid, err := uuid.Parse(chi.URLParam(r, "aid"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	isDev, uid, err := h.isDev(r.Context())
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	att, err := h.Repo.GetAttachment(r.Context(), aid)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", http.StatusNotFound)
		} else {
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}
	if !isDev {
		sug, gerr := h.Repo.Get(r.Context(), att.SuggestionID)
		if gerr != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if sug.CreatedBy == nil || *sug.CreatedBy != uid {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
	}
	if h.Storage == nil {
		http.Error(w, "storage indisponível", http.StatusInternalServerError)
		return
	}
	const ttl = 5 * time.Minute
	url, expiresAt, err := h.Storage.PresignGet(r.Context(), att.StorageKey, ttl)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"url":        url,
		"expires_at": expiresAt.UTC().Format(time.RFC3339),
	})
}

// MarkRead — POST /suggestions/{id}/read. Per-user read state.
func (h *SuggestionsHandler) MarkRead(w http.ResponseWriter, r *http.Request) {
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
	if err := h.Repo.MarkRead(r.Context(), claims.UserID, id); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Summary — GET /suggestions/summary. Dev-only KPIs.
func (h *SuggestionsHandler) Summary(w http.ResponseWriter, r *http.Request) {
	isDev, _, err := h.isDev(r.Context())
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !isDev {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	sum, err := h.Repo.Summary(r.Context())
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, sum)
}

// UnreadCount — GET /suggestions/unread-count. Scope by persona.
func (h *SuggestionsHandler) UnreadCount(w http.ResponseWriter, r *http.Request) {
	isDev, uid, err := h.isDev(r.Context())
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	n, err := h.Repo.UnreadCount(r.Context(), uid, isDev)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": n})
}
