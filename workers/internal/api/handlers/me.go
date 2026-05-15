package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"

	"radiocheck/internal/auth"
	"radiocheck/internal/users"
)

// MeHandler implements the self-service endpoints under /v1/internal/auth/me.
// Any authenticated user (admin/operator/viewer) can call them; the JWT
// claims identify which user is being read or modified.
//
// Distinguish from UsersHandler (admin CRUD): MeHandler operates ONLY on the
// caller's own row, never on other users. Sensitive fields (role, client_id,
// is_active, deleted_at) are NOT mutable via /me.
type MeHandler struct {
	users *users.Repo
}

// minPasswordLen is the floor for any password change accepted by this
// service. Mirrors the bootstrap and admin reset endpoints (Task 7).
const minPasswordLen = 12

// NewMeHandler constructs a MeHandler backed by the given users repo.
func NewMeHandler(repo *users.Repo) *MeHandler {
	return &MeHandler{users: repo}
}

// Get returns the authenticated user's own row. The full users.User is
// serialized — PasswordHash is excluded by its `json:"-"` tag.
func (h *MeHandler) Get(w http.ResponseWriter, r *http.Request) {
	c, ok := auth.ClaimsFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	u, err := h.users.Get(r.Context(), c.UserID)
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

// mePatchPayload accepts only name and phone. Any other field present in
// the body is silently ignored — preventing self-promotion to admin or
// switching client_id is a HARD requirement.
type mePatchPayload struct {
	Name  *string `json:"name,omitempty"`
	Phone *string `json:"phone,omitempty"`
}

// Patch updates the authenticated user's name and/or phone. All other fields
// (role, client_id, is_active, etc.) are silently ignored even if present in
// the request body.
func (h *MeHandler) Patch(w http.ResponseWriter, r *http.Request) {
	c, ok := auth.ClaimsFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var p mePatchPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	u, err := h.users.Update(r.Context(), c.UserID, users.UpdateInput{
		Name:  p.Name,
		Phone: p.Phone,
	})
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

type changePwdPayload struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// ChangePassword requires current_password to prevent abuse via session
// hijacking (an attacker with a stolen JWT can't lock the user out).
func (h *MeHandler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	c, ok := auth.ClaimsFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var p changePwdPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if p.CurrentPassword == "" || p.NewPassword == "" {
		http.Error(w, "missing_fields", http.StatusBadRequest)
		return
	}
	if len(p.NewPassword) < minPasswordLen {
		http.Error(w, "password_too_short", http.StatusBadRequest)
		return
	}
	u, err := h.users.Get(r.Context(), c.UserID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(p.CurrentPassword)) != nil {
		http.Error(w, "invalid_current_password", http.StatusUnauthorized)
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(p.NewPassword), 10)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := h.users.SetPassword(r.Context(), c.UserID, string(hash)); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
