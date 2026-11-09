package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"

	"radiocheck/internal/acoes"
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
	Email string `json:"email"`
	// Username é opcional: ausente ou vazio = a conta entra só pelo e-mail.
	Username *string    `json:"username,omitempty"`
	Password string     `json:"password"`
	Name     string     `json:"name"`
	Phone    *string    `json:"phone,omitempty"`
	Role     string     `json:"role"` // "admin" | "client"
	ClientID *uuid.UUID `json:"client_id,omitempty"`
	// ClientIDs é a carteira do usuário Cliente (agências). ClientID continua
	// aceito e equivale a uma lista de um elemento — é o que o frontend antigo
	// manda durante a janela entre o deploy do backend e o do frontend.
	// Quando os dois vêm, ClientIDs vence.
	ClientIDs []uuid.UUID `json:"client_ids,omitempty"`
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

// escreveRecusaUsuario traduz a Recusa de internal/acoes no status e no corpo
// (texto puro = o código) que /admin/users sempre deu. Devolve false quando o
// erro não é uma Recusa — quem chama responde 500.
func escreveRecusaUsuario(w http.ResponseWriter, err error) bool {
	var r *acoes.Recusa
	if !errors.As(err, &r) {
		return false
	}
	switch r.Codigo {
	case acoes.CodigoEmailEmUso, acoes.CodigoUsernameEmUso:
		http.Error(w, r.Mensagem, http.StatusConflict)
	case acoes.CodigoUsuarioNaoEncontrado:
		http.Error(w, r.Mensagem, http.StatusNotFound)
	default:
		// missing_fields, password_too_short, invalid_role, client_id_required,
		// client_id_not_allowed_for_admin, role_client_inconsistent,
		// client_not_found, cannot_reactivate_deleted.
		http.Error(w, r.Mensagem, http.StatusBadRequest)
	}
	return true
}

// servico monta as ações de usuário sobre os colaboradores deste handler. A
// regra de negócio mora em internal/acoes: o MCP chama as mesmas funções.
func (h *UsersHandler) servico() *acoes.Servico {
	return &acoes.Servico{Usuarios: h.repo, BoasVindas: acoes.BoasVindasDoServico(h.welcomeSvc)}
}

func (h *UsersHandler) Create(w http.ResponseWriter, r *http.Request) {
	var p createUserPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	in := acoes.EntradaUsuario{
		Email:            p.Email,
		Senha:            p.Password,
		Nome:             p.Name,
		Username:         p.Username,
		Telefone:         p.Phone,
		Papel:            p.Role,
		ClienteID:        p.ClientID,
		ClienteIDs:       p.ClientIDs,
		EnviarBoasVindas: p.SendWelcome,
		RecebeAlertas:    p.ReceiveAlertEmails,
		RecebePosVenda:   p.ReceivePostSaleEmails,
	}
	if claims, ok := auth.ClaimsFromContext(r.Context()); ok {
		by := claims.UserID
		in.Ator = &by
	}
	u, bv, err := h.servico().CriarUsuario(r.Context(), in)
	if err != nil {
		if escreveRecusaUsuario(w, err) {
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	resp := createUserResponse{User: u}
	if bv != nil {
		resp.Welcome = &welcomeInfo{
			InviteID:    bv.ConviteID,
			Link:        bv.Link,
			EmailStatus: bv.EmailStatus,
			EmailError:  bv.EmailErro,
		}
	}
	writeJSON(w, http.StatusCreated, resp)
}

// ── Patch ────────────────────────────────────────────────────────────────

type updateUserPayload struct {
	Name     *string    `json:"name,omitempty"`
	Phone    *string    `json:"phone,omitempty"`
	Role     *string    `json:"role,omitempty"`
	ClientID *uuid.UUID `json:"client_id,omitempty"`
	// ClientIDs SUBSTITUI a carteira inteira (não é append): quem sai da lista
	// perde o acesso, que é o ponto — a carteira é o escopo de leitura do JWT.
	// Ausente = não mexe na carteira. Quando vem junto com ClientID, vence.
	ClientIDs []uuid.UUID `json:"client_ids,omitempty"`
	IsActive  *bool       `json:"is_active,omitempty"`
	// ReceiveAlertEmails: opt-in/out dos emails diários de alerta (admins).
	ReceiveAlertEmails *bool `json:"receive_alert_emails,omitempty"`
	// ReceivePostSaleEmails: opt-in/out da cópia de todo pós-venda (admins).
	ReceivePostSaleEmails *bool `json:"receive_post_sale_emails,omitempty"`
	// Username: ausente = não mexe; "" = remove; valor = define/troca. Ao
	// contrário do e-mail, é mutável — mas só por aqui (admin), nunca pelo
	// /me: trocar o próprio login não é autoatendimento (decisão do dono).
	Username *string `json:"username,omitempty"`
	Email    *string `json:"email,omitempty"` // só pra detectar e rejeitar
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

	u, err := h.servico().EditarUsuario(r.Context(), id, acoes.EdicaoUsuario{
		Nome:           p.Name,
		Telefone:       p.Phone,
		Papel:          p.Role,
		ClienteID:      p.ClientID,
		ClienteIDs:     &p.ClientIDs,
		Ativo:          p.IsActive,
		RecebeAlertas:  p.ReceiveAlertEmails,
		RecebePosVenda: p.ReceivePostSaleEmails,
		Username:       p.Username,
	})
	if err != nil {
		if escreveRecusaUsuario(w, err) {
			return
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
