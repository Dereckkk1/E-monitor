package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
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

// ClientIDs é a carteira ATIVA (o que o token carrega), não a carteira crua do
// banco: cliente desativado não aparece. omitempty mantém a resposta do
// admin/operator byte a byte igual à de antes da feature.
type loginUser struct {
	ID        uuid.UUID   `json:"id"`
	Email     string      `json:"email"`
	Role      string      `json:"role"`
	Name      string      `json:"name"`
	ClientID  *uuid.UUID  `json:"client_id,omitempty"`
	ClientIDs []uuid.UUID `json:"client_ids,omitempty"`
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
	// foi suspensa, não cada conta individualmente). Com carteira multi-cliente
	// (agências), a regra é "pelo menos um cliente ativo": desativar um cliente
	// da carteira não pode derrubar a conta inteira, só some com aquele cliente
	// da visão. Com 1 cliente o comportamento é idêntico ao anterior.
	//
	// A lista ativa é a que vai no token, então isto é ao mesmo tempo o gate de
	// login e o escopo da sessão. Fail-closed em dois pontos:
	//
	//  1. vínculo órfão (cliente deletado — não deveria, a FK é RESTRICT)
	//     simplesmente não entra na lista ativa, porque a query só devolve
	//     clientes que existem E estão ativos;
	//  2. carteira vazia com principal preenchido (não deveria — o trigger da
	//     0062 mantém o principal dentro dela) cai no fallback abaixo, senão o
	//     gate seria pulado e um cliente desativado passaria batido.
	//
	// Reusa o pool cru porque o gating é de auth, não pertence ao users.Repo.
	wallet := u.ClientIDs
	if len(wallet) == 0 && u.ClientID != nil {
		wallet = []uuid.UUID{*u.ClientID}
	}
	if len(wallet) > 0 {
		activeSet, err := h.activeClients(r.Context(), wallet)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		// Filtra preservando a ordem canônica da carteira (users.userColumns
		// ordena por user_clients.created_at, client_id) em vez de confiar na
		// ordem das linhas devolvidas pelo Postgres, que não é garantida sem
		// ORDER BY. Isso torna o principal reposicionado abaixo determinístico:
		// é sempre o vínculo mais antigo ainda ativo.
		active := make([]uuid.UUID, 0, len(wallet))
		for _, id := range wallet {
			if _, ok := activeSet[id]; ok {
				active = append(active, id)
			}
		}
		if len(active) == 0 {
			http.Error(w, "client_disabled", http.StatusForbidden)
			return
		}
		u.ClientIDs = active
		// O principal não pode ser um cliente que o usuário não enxerga mais:
		// a sessão ficaria rotulada com ele (cabeçalho, defaults de tela) e
		// qualquer check pontual contra o escopo daria 403.
		if u.ClientID == nil || !slices.Contains(active, *u.ClientID) {
			u.ClientID = &active[0]
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
		ID: u.ID, Email: u.Email, Role: u.Role, Name: u.Name,
		ClientID: u.ClientID, ClientIDs: u.ClientIDs,
	}}

	writeJSON(w, http.StatusOK, resp)
}

// activeClients devolve, como conjunto, quais dos ids informados são de
// clientes existentes E ativos. Conjunto (não slice) porque a ordem de saída
// do Postgres sem ORDER BY não é garantida — quem chama reordena pela carteira.
func (h *AuthHandler) activeClients(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]struct{}, error) {
	rows, err := h.db.Query(ctx,
		`SELECT id FROM clients WHERE id = ANY($1) AND is_active = TRUE`, ids)
	if err != nil {
		return nil, err
	}
	// Close é idempotente e liberar a conexão é obrigatório em TODO caminho de
	// saída, inclusive no erro de Scan — daí o defer em vez de Close manual.
	defer rows.Close()

	out := make(map[uuid.UUID]struct{}, len(ids))
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
