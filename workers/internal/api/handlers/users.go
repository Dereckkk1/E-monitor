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
	"radiocheck/internal/welcome"
)

// UsersHandler implements admin CRUD for /v1/internal/admin/users/*.
//
// Vocabulário externo ↔ DB:
//
//	API "admin"  → DB "admin"
//	API "client" → DB "viewer"
//
// 'operator' permanece no DB por compat mas não é exposto na API de
// criação (cai em invalid_role).
type UsersHandler struct {
	repo *users.Repo
	// welcomeSvc emite o convite de boas-vindas quando send_welcome vem true.
	// Pode ser nil/desabilitado (sem WELCOME_ENC_KEY): nesse caso a criação do
	// usuário segue normal e a resposta traz welcome.email_status='unavailable'.
	welcomeSvc *welcome.Service
}

func NewUsersHandler(repo *users.Repo, welcomeSvc *welcome.Service) *UsersHandler {
	return &UsersHandler{repo: repo, welcomeSvc: welcomeSvc}
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
	body := map[string]any{
		"data":        list,
		"total":       total,
		"total_pages": totalPages,
		"page":        in.Page,
		"page_size":   in.PageSize,
	}
	// Convite mais recente por usuário, num único SELECT (sem N+1). Alimenta o
	// selo "boas-vindas enviado" e a ação de revogar na lista do /admin/users.
	if h.welcomeSvc != nil && h.welcomeSvc.Enabled() && len(list) > 0 {
		ids := make([]uuid.UUID, 0, len(list))
		for i := range list {
			ids = append(ids, list[i].ID)
		}
		if invites, err := h.welcomeSvc.Repo().Latest(r.Context(), ids); err == nil {
			byUser := make(map[string]any, len(invites))
			for uid, inv := range invites {
				byUser[uid.String()] = map[string]any{
					"invite_id":    inv.ID,
					"link":         h.welcomeSvc.Link(inv.Token),
					"email_status": inv.EmailStatus,
					"opened_at":    inv.OpenedAt,
					"open_count":   inv.OpenCount,
					"revoked_at":   inv.RevokedAt,
					"created_at":   inv.CreatedAt,
				}
			}
			body["welcome_invites"] = byUser
		}
	}
	writeJSON(w, http.StatusOK, body)
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
	Role     string     `json:"role"` // "admin" | "client"
	ClientID *uuid.UUID `json:"client_id,omitempty"`
	// SendWelcome emite o convite e dispara o email de boas-vindas.
	SendWelcome bool `json:"send_welcome,omitempty"`
	// Preferências de email (só fazem sentido pra admin — as queries de
	// destinatário filtram por role). Ausentes = default da coluna.
	ReceiveAlertEmails    *bool `json:"receive_alert_emails,omitempty"`
	ReceivePostSaleEmails *bool `json:"receive_post_sale_emails,omitempty"`
}

// createUserResponse é o usuário criado mais, quando pedido, o resultado do
// convite. O link vem SEMPRE que o convite foi emitido — inclusive quando o
// email falhou ou o SMTP está desligado — pra que o admin consiga copiá-lo e
// mandar por fora, que é o fluxo que a equipe já usa hoje.
type createUserResponse struct {
	*users.User
	Welcome *welcomeInfo `json:"welcome,omitempty"`
}

type welcomeInfo struct {
	InviteID    *uuid.UUID `json:"invite_id,omitempty"`
	Link        string     `json:"link,omitempty"`
	EmailStatus string     `json:"email_status"` // sent | failed | disabled | unavailable
	EmailError  string     `json:"email_error,omitempty"`
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
		Email:                 p.Email,
		PasswordHash:          string(hash),
		Role:                  dbRole,
		ClientID:              p.ClientID,
		Name:                  p.Name,
		Phone:                 p.Phone,
		ReceiveAlertEmails:    p.ReceiveAlertEmails,
		ReceivePostSaleEmails: p.ReceivePostSaleEmails,
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

	resp := createUserResponse{User: u}
	if p.SendWelcome {
		resp.Welcome = h.issueWelcome(r, u, p.Password)
	}
	writeJSON(w, http.StatusCreated, resp)
}

// issueWelcome emite o convite pro usuário recém-criado.
//
// Nunca devolve erro: o usuário JÁ está gravado, e falhar a resposta inteira
// porque o SMTP recusou a conexão faria o admin achar que a criação não
// funcionou (e tentar de novo, colidindo em email_taken). O desfecho vai no
// campo email_status pra UI mostrar honestamente o que aconteceu.
func (h *UsersHandler) issueWelcome(r *http.Request, u *users.User, plainPassword string) *welcomeInfo {
	if h.welcomeSvc == nil || !h.welcomeSvc.Enabled() {
		return &welcomeInfo{EmailStatus: "unavailable"}
	}
	in := welcome.SendInput{
		UserID:   u.ID,
		Name:     u.Name,
		Email:    u.Email,
		Password: plainPassword,
		Role:     u.Role,
	}
	if claims, ok := auth.ClaimsFromContext(r.Context()); ok {
		by := claims.UserID
		in.CreatedBy = &by
	}
	if u.ClientID != nil {
		in.ClientName = h.welcomeSvc.Repo().ClientName(r.Context(), *u.ClientID)
	}
	res, err := h.welcomeSvc.Issue(r.Context(), in)
	if err != nil {
		return &welcomeInfo{EmailStatus: "unavailable", EmailError: err.Error()}
	}
	return &welcomeInfo{
		InviteID:    &res.InviteID,
		Link:        res.Link,
		EmailStatus: res.EmailStatus,
		EmailError:  res.EmailError,
	}
}

// ── Patch ────────────────────────────────────────────────────────────────

type updateUserPayload struct {
	Name     *string    `json:"name,omitempty"`
	Phone    *string    `json:"phone,omitempty"`
	Role     *string    `json:"role,omitempty"`
	ClientID *uuid.UUID `json:"client_id,omitempty"`
	IsActive *bool      `json:"is_active,omitempty"`
	// ReceiveAlertEmails: opt-in/out dos emails diários de alerta (admins).
	ReceiveAlertEmails *bool `json:"receive_alert_emails,omitempty"`
	// ReceivePostSaleEmails: opt-in/out da cópia de todo pós-venda (admins).
	ReceivePostSaleEmails *bool   `json:"receive_post_sale_emails,omitempty"`
	Email                 *string `json:"email,omitempty"` // só pra detectar e rejeitar
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
		Name:                  p.Name,
		Phone:                 p.Phone,
		IsActive:              p.IsActive,
		ReceiveAlertEmails:    p.ReceiveAlertEmails,
		ReceivePostSaleEmails: p.ReceivePostSaleEmails,
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
