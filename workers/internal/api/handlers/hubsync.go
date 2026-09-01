package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"radiocheck/internal/hub"
)

// HubSyncHandler recebe as mutações de identidade que o hub empurra — RFC-001
// §9.2, o sentido OPOSTO do HubSSOHandler.
//
// No SSO é o E-monitor que pergunta ao hub quem é a pessoa. Aqui é o hub que
// avisa o E-monitor que algo mudou. A autenticação é a mesma chave de
// plataforma, nos dois sentidos — e reusá-la é decisão: chave nova exigiria
// variável de ambiente nova, e variável nova aqui significa lembrar do bloco
// `environment:` explícito do compose, que é justamente o que foi esquecido e
// fez o SSO responder 503 em produção com o `.env` aparentemente certo (PR #8).
//
// A fatia 1 da Fase 3 entrega só `user.deactivate`. Os outros eventos do §9.3
// respondem `{"ok":true}` e não fazem nada — o que permite o hub subir antes
// deste lado sem encher a DLQ com eventos que aqui ainda não existem.
type HubSyncHandler struct {
	db  *pgxpool.Pool
	hub *hub.Client
}

func NewHubSyncHandler(db *pgxpool.Pool, hc *hub.Client) *HubSyncHandler {
	return &HubSyncHandler{db: db, hub: hc}
}

// envelope do §9.3.
type syncEnvelope struct {
	EventID    string          `json:"eventId"`
	Event      string          `json:"event"`
	OccurredAt string          `json:"occurredAt"`
	Data       json.RawMessage `json:"data"`
}

type syncResposta struct {
	OK bool `json:"ok"`
	// O id local, que o hub grava em `userPlatformIdentities` (§5.6). Nulo
	// quando não há usuário local — e isso não é erro: o provisionamento é JIT,
	// a pessoa pode simplesmente ainda não ter clicado no card.
	ExternalID *string `json:"externalId"`
}

func (h *HubSyncHandler) responde(w http.ResponseWriter, externalID *string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(syncResposta{OK: true, ExternalID: externalID})
}

// Receive atende `POST /v1/internal/hub/sync`.
func (h *HubSyncHandler) Receive(w http.ResponseWriter, r *http.Request) {
	// A chave é conferida ANTES de ler o corpo. Um corpo mal formado vindo de
	// quem não se autenticou não merece nem parsing, e responder 400 antes de
	// 401 contaria a quem sonda que o endpoint existe e o que ele espera.
	if h.hub == nil || !h.hub.ChaveConfere(r.Header.Get("X-Hub-Platform-Key")) {
		http.Error(w, "invalid_platform_key", http.StatusUnauthorized)
		return
	}

	var env syncEnvelope
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&env); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if env.EventID == "" || env.Event == "" {
		http.Error(w, "missing_event", http.StatusBadRequest)
		return
	}

	switch env.Event {
	case "user.deactivate":
		h.desativar(w, r, env)
	default:
		// Evento que esta versão não implementa é ACEITO e ignorado.
		//
		// Responder erro faria o hub tentar oito vezes e enterrar na DLQ um
		// evento que não tem defeito nenhum — só chegou antes do código que o
		// entende. Aceitar deixa hub e plataforma subirem em ordens diferentes,
		// que é a única forma realista de evoluir quatro repositórios.
		h.responde(w, nil)
	}
}

func (h *HubSyncHandler) desativar(w http.ResponseWriter, r *http.Request, env syncEnvelope) {
	var d struct {
		HubUserID string `json:"hubUserId"`
		Email     string `json:"email"`
	}
	if err := json.Unmarshal(env.Data, &d); err != nil || d.HubUserID == "" {
		http.Error(w, "missing_hub_user_id", http.StatusBadRequest)
		return
	}

	// O casamento é por `hub_id` e SÓ por `hub_id`. Cair para o e-mail aqui
	// seria perigoso de um jeito que não aparece em teste: dois sistemas com o
	// mesmo e-mail em pessoas diferentes existem, e desativar a errada por
	// heurística é pior que não desativar ninguém. O e-mail vem no payload para
	// diagnóstico, não para busca.
	//
	// `RETURNING id` é o que distingue "desativei" de "não havia ninguém": sem
	// ele, um `hub_id` inexistente e uma desativação bem-sucedida ficam
	// indistinguíveis, e o hub gravaria `externalId` de alguém que não existe.
	//
	// A operação é idempotente por natureza — repetir o UPDATE não muda nada. É
	// o que o §9.2 aceita ("ignorar repetido é aceitável"). Quando chegar o
	// primeiro evento que NÃO é idempotente sozinho, aí entra a dedup por
	// `eventId` numa tabela própria; hoje ela seria uma migration sem uso.
	var id string
	err := h.db.QueryRow(r.Context(),
		`UPDATE users SET is_active = FALSE
		  WHERE hub_id = $1 AND deleted_at IS NULL
		  RETURNING id`, d.HubUserID).Scan(&id)

	if errors.Is(err, pgx.ErrNoRows) {
		// Ninguém vinculado a esse `hub_id`: a pessoa nunca entrou por aqui, ou
		// já foi apagada. Não há o que desativar, e insistir não faria aparecer.
		// 200 com `externalId` nulo — o evento está resolvido.
		h.responde(w, nil)
		return
	}
	if err != nil {
		// 500 é o único caso em que queremos o retry do hub: banco fora do ar
		// melhora sozinho, `hub_id` inexistente não.
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	h.responde(w, &id)
}
