package handlers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"radiocheck/internal/auth"
	"radiocheck/internal/hub"
	"radiocheck/internal/users"
)

// HubSSOHandler atende a entrada pela Central de Clientes — RFC-001 §8.1.
//
// A página /sso do frontend lê o `?code=` da URL e chama este handler. O código
// é trocado com o hub, o usuário local é achado ou criado, e o token emitido é o
// MESMO JWT de 8h do login normal, no MESMO envelope. Depois deste ponto o hub
// está fora do caminho: zero dependência em runtime.
//
// Não substitui o login local — os dois coexistem para sempre (decisão D5).
type HubSSOHandler struct {
	db    *pgxpool.Pool
	users *users.Repo
	hub   *hub.Client
}

func NewHubSSOHandler(db *pgxpool.Pool, repo *users.Repo, hc *hub.Client) *HubSSOHandler {
	return &HubSSOHandler{db: db, users: repo, hub: hc}
}

// papelDoUsuario decide o role local a partir do payload do hub (§8, tabela).
//
// `provisionProfile` é deliberadamente IGNORADO aqui, e isso não é descuido.
//
// No E-rádios ele importa porque lá um cliente pode ser `advertiser` ou
// `agency` — dois papéis legítimos, e o hub sabe qual. No E-monitor não existe
// essa escolha: todo usuário de cliente é `viewer`, ponto. Então não há nada
// para o perfil configurar, e ler o campo só criaria uma porta — a configuração
// de um cliente no hub virando role aqui dentro, que é exatamente o que a
// decisão D9 proíbe. Quem promove alguém no E-monitor é o E-monitor.
//
// Se um dia houver um segundo papel de cliente aqui, o lugar de tratá-lo é este
// — com allowlist fechada, como o E-rádios faz.
func papelDoUsuario(p *hub.UserPayload) string {
	if p.Level == "internal" {
		return "admin"
	}
	return "viewer"
}

// Login é o POST /v1/internal/auth/sso.
func (h *HubSSOHandler) Login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if body.Code == "" {
		http.Error(w, "missing_code", http.StatusBadRequest)
		return
	}
	if h.users == nil || h.hub == nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	payload, err := h.hub.Exchange(r.Context(), body.Code)
	if err != nil {
		if he, ok := hub.AsError(err); ok {
			// A mensagem do hub é segura para o usuário: ela nunca cita o
			// código nem a chave (ver internal/hub).
			http.Error(w, he.Message, he.Status)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	u, err := h.acharOuCriar(r.Context(), payload)
	if err != nil {
		if he, ok := hub.AsError(err); ok {
			http.Error(w, he.Message, he.Status)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Gates LOCAIS continuam mandando. O hub é dono da identidade, não da
	// permissão de entrar aqui: conta desativada no E-monitor segue desativada,
	// venha a pessoa por onde vier. Conta criada agora nasce ativa, então isto
	// só alcança quem já existia.
	if !u.IsActive {
		http.Error(w, "account_disabled", http.StatusForbidden)
		return
	}
	if u.ClientID != nil {
		ativo, err := h.clienteAtivo(r.Context(), *u.ClientID)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if !ativo {
			http.Error(w, "client_disabled", http.StatusForbidden)
			return
		}
	}

	tok, err := auth.IssueTokenForUser(u)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	expiresAt := time.Now().Add(8 * time.Hour)
	if claims, err := auth.ParseToken(tok); err == nil && claims.RegisteredClaims.ExpiresAt != nil {
		expiresAt = claims.RegisteredClaims.ExpiresAt.Time
	}

	_ = h.users.TouchLastLogin(r.Context(), u.ID)

	// Envelope IDÊNTICO ao do /auth/login: o frontend reusa o mesmo caminho de
	// sessão, e qualquer campo a menos aqui quebraria o guard de role.
	writeJSON(w, http.StatusOK, loginResponse{
		Token: tok, ExpiresAt: expiresAt,
		User: loginUser{
			ID: u.ID, Email: u.Email, Role: u.Role, Name: u.Name,
			ClientID: u.ClientID, ClientIDs: u.ClientIDs,
		},
	})
}

// acharOuCriar resolve o usuário local: por vínculo, por e-mail, ou criando.
func (h *HubSSOHandler) acharOuCriar(ctx context.Context, p *hub.UserPayload) (*users.User, error) {
	// 1) Já vinculado.
	u, err := h.users.GetByHubID(ctx, p.HubUserID)
	if err == nil {
		return u, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}

	// 2) Mesmo e-mail, ainda sem vínculo → VINCULA. Criar uma segunda conta
	// para a mesma pessoa deixaria as duas vivas e divergindo.
	u, err = h.users.GetByEmail(ctx, p.Email)
	if err == nil {
		if err := h.users.SetHubID(ctx, u.ID, p.HubUserID); err != nil {
			return nil, err
		}
		return u, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}

	// 3) Ninguém: JIT provisioning (§8).
	return h.criarPorJit(ctx, p)
}

func (h *HubSSOHandler) criarPorJit(ctx context.Context, p *hub.UserPayload) (*users.User, error) {
	var clientID *uuid.UUID
	if p.Level != "internal" {
		if p.Client == nil {
			return nil, &hub.Error{
				Status:  http.StatusForbidden,
				Message: "client_not_provisioned",
			}
		}
		id, err := h.clientePorHubID(ctx, p.Client.HubClientID)
		if err != nil {
			return nil, err
		}
		if id == nil {
			// O tenant local não existe. NÃO criamos um por conta própria: o
			// `clients` do E-monitor carrega cadastro de verdade (contrato,
			// PMM alvo, regras de distribuição), e adivinhar isso a partir de
			// um nome geraria um cliente fantasma que alguém teria que limpar.
			// O sync `client.upsert` da Fase 3 é quem preenche `clients.hub_id`;
			// até lá, o vínculo é manual e este erro diz exatamente isso.
			return nil, &hub.Error{
				Status:  http.StatusForbidden,
				Message: "client_not_provisioned",
			}
		}
		clientID = id
	}

	// Senha aleatória forte: a conta existe, mas o login LOCAL dela só passa a
	// funcionar depois de um reset daqui ou do sync de senha da Fase 3.
	// Ninguém, nem nós, conhece este valor.
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return nil, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(hex.EncodeToString(buf)), 10)
	if err != nil {
		return nil, err
	}

	hubID := p.HubUserID
	return h.users.Create(ctx, users.CreateInput{
		Email:        p.Email,
		PasswordHash: string(hash),
		Role:         papelDoUsuario(p),
		ClientID:     clientID,
		Name:         p.Name,
		Phone:        p.Phone,
		HubID:        &hubID,
	})
}

// clientePorHubID resolve o tenant local pelo id do cliente no hub.
// Devolve (nil, nil) quando não há vínculo — não é erro, é "ainda não mapeado".
func (h *HubSSOHandler) clientePorHubID(ctx context.Context, hubClientID string) (*uuid.UUID, error) {
	var id uuid.UUID
	err := h.db.QueryRow(ctx,
		`SELECT id FROM clients WHERE hub_id = $1`, hubClientID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &id, nil
}

func (h *HubSSOHandler) clienteAtivo(ctx context.Context, id uuid.UUID) (bool, error) {
	var ativo bool
	err := h.db.QueryRow(ctx,
		`SELECT is_active FROM clients WHERE id = $1`, id).Scan(&ativo)
	if errors.Is(err, pgx.ErrNoRows) {
		// Vínculo órfão (a FK é RESTRICT, então não deveria acontecer).
		// Fail-closed: sem cliente, não há escopo, e sem escopo o viewer veria
		// o quê? Melhor recusar do que abrir uma sessão sem fronteira.
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return ativo, nil
}
