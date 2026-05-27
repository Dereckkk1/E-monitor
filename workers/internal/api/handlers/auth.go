package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"radiocheck/internal/auth"
	"radiocheck/internal/users"
)

type AuthHandler struct {
	db    *pgxpool.Pool
	users *users.Repo
}

// NewAuthHandler constructs the login handler. The users repo handles all
// user-table reads/writes; the raw pool is kept for transactional operations
// outside the repo (currently none — kept for symmetry with other handlers).
func NewAuthHandler(db *pgxpool.Pool, repo *users.Repo) *AuthHandler {
	return &AuthHandler{db: db, users: repo}
}

// loginResponse is the JSON envelope returned to the frontend on a
// successful login. token is the HS256 JWT; expires_at is RFC3339; user is
// the minimum projection the UI needs to render header + role guards.
type loginResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	User      loginUser `json:"user"`
}

type loginUser struct {
	ID       uuid.UUID  `json:"id"`
	Email    string     `json:"email"`
	Role     string     `json:"role"`
	Name     string     `json:"name"`
	ClientID *uuid.UUID `json:"client_id,omitempty"`
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
	if h.users == nil {
		// Defensive: tests that construct AuthHandler without a repo
		// (e.g. TestAuth_Login_BadJSON, TestNewAuthHandler_Defaults) will
		// short-circuit on the body decode above. Anything else hitting a
		// nil repo is a wiring bug — fail loudly.
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	u, err := h.users.GetByEmail(r.Context(), body.Email)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "invalid credentials", http.StatusUnauthorized)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(body.Password)) != nil {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}
	if !u.IsActive {
		// Conta desativada (soft) — distinguimos de credencial inválida porque
		// o usuário precisa saber que existe mas está bloqueada (procurar
		// admin). Contas deletadas (deleted_at NOT NULL) caem em
		// pgx.ErrNoRows acima porque GetByEmail filtra; tratadas como
		// "invalid credentials" sem revelar a existência prévia.
		http.Error(w, "account_disabled", http.StatusForbidden)
		return
	}

	// Cliente desativado bloqueia o login de TODOS os seus usuários (a empresa
	// foi suspensa, não cada conta individualmente). Fail-closed: se a linha
	// do cliente sumiu (não deveria — FK users.client_id é RESTRICT), também
	// bloqueia. Reusa o pool cru porque o gating é de auth, não pertence ao
	// users.Repo.
	if u.ClientID != nil {
		var clientActive bool
		switch err := h.db.QueryRow(r.Context(),
			`SELECT is_active FROM clients WHERE id = $1`, *u.ClientID,
		).Scan(&clientActive); {
		case errors.Is(err, pgx.ErrNoRows):
			http.Error(w, "client_disabled", http.StatusForbidden)
			return
		case err != nil:
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		case !clientActive:
			http.Error(w, "client_disabled", http.StatusForbidden)
			return
		}
	}

	tok, err := auth.IssueTokenForUser(u)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	claims, err := auth.ParseToken(tok)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	expiresAt := time.Now().Add(8 * time.Hour)
	if claims.RegisteredClaims.ExpiresAt != nil {
		expiresAt = claims.RegisteredClaims.ExpiresAt.Time
	}

	// Telemetria: TouchLastLogin best-effort. Falha não bloqueia o login —
	// é só um carimbo pra "última atividade".
	_ = h.users.TouchLastLogin(r.Context(), u.ID)

	resp := loginResponse{Token: tok, ExpiresAt: expiresAt, User: loginUser{
		ID: u.ID, Email: u.Email, Role: u.Role, Name: u.Name, ClientID: u.ClientID,
	}}

	writeJSON(w, http.StatusOK, resp)
}
