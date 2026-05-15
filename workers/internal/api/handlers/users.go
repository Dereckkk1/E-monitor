package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/crypto/bcrypt"

	"radiocheck/internal/auth"
	"radiocheck/internal/users"
)

// UsersHandler implements admin CRUD for /v1/internal/admin/users/*.
//
// Vocabulário externo ↔ DB:
//   API "admin"  → DB "admin"
//   API "client" → DB "viewer"
// 'operator' permanece no DB por compat mas não é exposto na API de
// criação (cai em invalid_role).
type UsersHandler struct {
	repo *users.Repo
}

func NewUsersHandler(repo *users.Repo) *UsersHandler {
	return &UsersHandler{repo: repo}
}

// roleAlias converte vocabulário API → DB. operator não é exposto.
func roleAlias(in string) (string, bool) {
	switch in {
	case "admin":
		return "admin", true
	case "client":
		return "viewer", true
	}
	return "", false
}

// parseRoleFilter converte vocabulário API → DB para filtros do query string.
// Aceita também "operator" e "viewer" diretamente pra retrocompat.
func parseRoleFilter(in string) (dbRole string, ok bool) {
	switch in {
	case "admin":
		return "admin", true
	case "client":
		return "viewer", true
	case "operator":
		return "operator", true
	case "viewer":
		return "viewer", true
	}
	return "", false
}

func atoiOr(s string, def int) int {
	if s == "" {
		return def
	}
	if v, err := strconv.Atoi(s); err == nil {
		return v
	}
	return def
}

// ── List ─────────────────────────────────────────────────────────────────

func (h *UsersHandler) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	in := users.ListInput{
		Q:        q.Get("q"),
		Status:   q.Get("status"),
		Page:     atoiOr(q.Get("page"), 1),
		PageSize: atoiOr(q.Get("page_size"), 20),
	}
	if rawRole := q.Get("role"); rawRole != "" {
		dbRole, ok := parseRoleFilter(rawRole)
		if !ok {
			http.Error(w, "invalid_role_filter", http.StatusBadRequest)
			return
		}
		in.Role = dbRole
	}
	if rawCID := q.Get("client_id"); rawCID != "" {
		cid, err := uuid.Parse(rawCID)
		if err != nil {
			http.Error(w, "invalid_client_id", http.StatusBadRequest)
			return
		}
		in.ClientID = &cid
	}
	list, total, err := h.repo.List(r.Context(), in)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if list == nil {
		list = []users.User{}
	}
	totalPages := 0
	if in.PageSize > 0 {
		totalPages = (total + in.PageSize - 1) / in.PageSize
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data":        list,
		"total":       total,
		"total_pages": totalPages,
		"page":        in.Page,
		"page_size":   in.PageSize,
	})
}

// ── Get ──────────────────────────────────────────────────────────────────

func (h *UsersHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid_id", http.StatusBadRequest)
		return
	}
	u, err := h.repo.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not_found", http.StatusNotFound)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

// ── Create ───────────────────────────────────────────────────────────────

type createUserPayload struct {
	Email    string     `json:"email"`
	Password string     `json:"password"`
	Name     string     `json:"name"`
	Phone    *string    `json:"phone,omitempty"`
	Role     string     `json:"role"`     // "admin" | "client"
	ClientID *uuid.UUID `json:"client_id,omitempty"`
}

func (h *UsersHandler) Create(w http.ResponseWriter, r *http.Request) {
	var p createUserPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if p.Email == "" || p.Password == "" || p.Name == "" {
		http.Error(w, "missing_fields", http.StatusBadRequest)
		return
	}
	if len(p.Password) < minPasswordLen {
		http.Error(w, "password_too_short", http.StatusBadRequest)
		return
	}
	dbRole, ok := roleAlias(p.Role)
	if !ok {
		http.Error(w, "invalid_role", http.StatusBadRequest)
		return
	}
	if dbRole == "viewer" && p.ClientID == nil {
		http.Error(w, "client_id_required", http.StatusBadRequest)
		return
	}
	if dbRole != "viewer" && p.ClientID != nil {
		http.Error(w, "client_id_not_allowed_for_admin", http.StatusBadRequest)
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(p.Password), 10)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	u, err := h.repo.Create(r.Context(), users.CreateInput{
		Email:        p.Email,
		PasswordHash: string(hash),
		Role:         dbRole,
		ClientID:     p.ClientID,
		Name:         p.Name,
		Phone:        p.Phone,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch pgErr.Code {
			case "23505": // unique_violation (email)
				http.Error(w, "email_taken", http.StatusConflict)
				return
			case "23514": // check_violation
				http.Error(w, "role_client_inconsistent", http.StatusBadRequest)
				return
			case "23503": // foreign_key_violation (client_id inválido)
				http.Error(w, "client_not_found", http.StatusBadRequest)
				return
			}
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, u)
}

// ── Patch ────────────────────────────────────────────────────────────────

type updateUserPayload struct {
	Name     *string    `json:"name,omitempty"`
	Phone    *string    `json:"phone,omitempty"`
	Role     *string    `json:"role,omitempty"`
	ClientID *uuid.UUID `json:"client_id,omitempty"`
	IsActive *bool      `json:"is_active,omitempty"`
	Email    *string    `json:"email,omitempty"` // só pra detectar e rejeitar
}

func (h *UsersHandler) Patch(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid_id", http.StatusBadRequest)
		return
	}
	var p updateUserPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if p.Email != nil {
		http.Error(w, "email_immutable", http.StatusBadRequest)
		return
	}

	current, err := h.repo.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not_found", http.StatusNotFound)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Reativar deletado é proibido (decisão da P4 do spec).
	if p.IsActive != nil && *p.IsActive && current.DeletedAt != nil {
		http.Error(w, "cannot_reactivate_deleted", http.StatusBadRequest)
		return
	}

	in := users.UpdateInput{
		Name:     p.Name,
		Phone:    p.Phone,
		IsActive: p.IsActive,
	}
	if p.Role != nil {
		dbRole, ok := roleAlias(*p.Role)
		if !ok {
			http.Error(w, "invalid_role", http.StatusBadRequest)
			return
		}
		in.Role = &dbRole
		// Se vai virar admin, força client_id a nulo (consistência);
		// se vai virar client, exige p.ClientID OU já estar com um.
		if dbRole == "viewer" && p.ClientID == nil && current.ClientID == nil {
			http.Error(w, "client_id_required", http.StatusBadRequest)
			return
		}
		if dbRole != "viewer" {
			in.ClearClient = true
		}
	}
	if p.ClientID != nil {
		// ClientID só faz sentido se o usuário é (ou está virando) viewer.
		// Se Role não veio mas current já é viewer, OK: trocar de cliente.
		// Se Role veio como admin, ClearClient já está true acima e ignoramos.
		if !in.ClearClient {
			in.ClientID = p.ClientID
		}
	}

	u, err := h.repo.Update(r.Context(), id, in)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch pgErr.Code {
			case "23514":
				http.Error(w, "role_client_inconsistent", http.StatusBadRequest)
				return
			case "23503":
				http.Error(w, "client_not_found", http.StatusBadRequest)
				return
			}
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

// ── ResetPassword ────────────────────────────────────────────────────────

type resetPwdPayload struct {
	Password string `json:"password"`
}

func (h *UsersHandler) ResetPassword(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid_id", http.StatusBadRequest)
		return
	}
	var p resetPwdPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if len(p.Password) < minPasswordLen {
		http.Error(w, "password_too_short", http.StatusBadRequest)
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(p.Password), 10)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := h.repo.SetPassword(r.Context(), id, string(hash)); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not_found", http.StatusNotFound)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── Delete ───────────────────────────────────────────────────────────────

func (h *UsersHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid_id", http.StatusBadRequest)
		return
	}
	caller, ok := auth.ClaimsFromContext(r.Context())
	if ok && caller.UserID == id {
		http.Error(w, "cannot_delete_self", http.StatusBadRequest)
		return
	}
	if err := h.repo.SoftDelete(r.Context(), id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Idempotente: já deletado ou não existe → 204.
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
