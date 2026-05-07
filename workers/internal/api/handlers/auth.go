package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
	"radiocheck/internal/auth"
)

type AuthHandler struct {
	db *pgxpool.Pool
}

func NewAuthHandler(db *pgxpool.Pool) *AuthHandler {
	return &AuthHandler{db: db}
}

// loginResponse is the JSON envelope returned to the frontend on a
// successful login. token is the HS256 JWT; expires_at is the unix epoch
// (RFC3339) at which the token stops being accepted; user is the minimum
// projection the UI needs to render the header / route guards.
type loginResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	User      struct {
		ID    uuid.UUID `json:"id"`
		Email string    `json:"email"`
		Role  string    `json:"role"`
	} `json:"user"`
}

func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	var id uuid.UUID
	var hash, role string
	err := h.db.QueryRow(r.Context(),
		`SELECT id, password_hash, role FROM users WHERE email = $1`, body.Email,
	).Scan(&id, &hash, &role)
	if err != nil {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(body.Password)) != nil {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}
	tok, err := auth.IssueToken(id, role)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Re-parse the token to surface the exp claim back to the UI without
	// duplicating the 8h constant. ParseToken validates HS256 signature so
	// this also doubles as a self-consistency check.
	claims, err := auth.ParseToken(tok)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	var expiresAt time.Time
	if claims.RegisteredClaims.ExpiresAt != nil {
		expiresAt = claims.RegisteredClaims.ExpiresAt.Time
	} else {
		// Should never happen: IssueToken always sets ExpiresAt.
		expiresAt = time.Now().Add(8 * time.Hour)
	}
	resp := loginResponse{Token: tok, ExpiresAt: expiresAt}
	resp.User.ID = id
	resp.User.Email = body.Email
	resp.User.Role = role

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
