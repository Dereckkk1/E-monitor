package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

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
// **Os quatro eventos do §9.3 estão implementados**: `user.deactivate` (fatia 1),
// `client.upsert` e `user.upsert` (fatia 2), `user.password_changed` (fatia 3).
// O ramo `default` continua existindo para eventos que uma versão futura do hub
// invente — ele aceita e ignora, o que permite os dois lados subirem em ordens
// diferentes sem encher a DLQ.
//
// ⚠️ A regra que o `default` carrega: aceitar em silêncio é certo para evento que
// não se conhece e ERRADO para evento que se conhece e não se implementou. Um
// evento conhecido caindo ali faz o hub marcar como sincronizado algo que não
// foi. Ao acrescentar um evento novo ao §9.3, ou se implementa o `case`, ou se
// aceita conscientemente que o hub vai mentir sobre ele.
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
	case "client.upsert":
		h.upsertCliente(w, r, env)
	case "user.upsert":
		h.upsertUsuario(w, r, env)
	case "user.password_changed":
		h.trocarSenha(w, r, env)
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

// ── client.upsert ───────────────────────────────────────────────────────────

type dadosCliente struct {
	HubClientID string  `json:"hubClientId"`
	Name        string  `json:"name"`
	CNPJ        *string `json:"cnpj"`
	LogoURL     *string `json:"logoUrl"`
	ContactName *string `json:"contactName"`
	Phone       *string `json:"phone"`
	City        *string `json:"city"`
	State       *string `json:"state"`
	Active      *bool   `json:"active"`
}

// upsertCliente materializa o tenant que o hub acabou de criar ou editar.
//
// **Aqui o E-monitor CRIA, e no SSO ele não cria — e a diferença é deliberada.**
// O `criarPorJit` recusa inventar um cliente porque lá a informação chega no meio
// do login de alguém, como efeito colateral de um clique: adivinhar um tenant ali
// geraria cliente fantasma que ninguém pediu. Este evento é o oposto — alguém
// habilitou o produto para aquele cliente no admin do hub, deliberadamente. O
// §9.3 manda "criar com defaults mínimos se não existir", e é barato: `clients`
// só exige `name`. Contrato, PMM alvo e regras de distribuição continuam vazios
// e continuam sendo preenchidos por aqui, como sempre foram.
//
// É isto que destrava o `client_not_provisioned` do §8.1 — o erro que hoje barra
// todo usuário de cliente cuja empresa ainda não tem `hub_id` carimbado.
func (h *HubSyncHandler) upsertCliente(w http.ResponseWriter, r *http.Request, env syncEnvelope) {
	var d dadosCliente
	if err := json.Unmarshal(env.Data, &d); err != nil || d.HubClientID == "" || d.Name == "" {
		http.Error(w, "missing_client_fields", http.StatusBadRequest)
		return
	}
	ativo := d.Active == nil || *d.Active

	// 1) Já vinculado: atualiza pelo `hub_id`, que é a chave forte.
	var id string
	err := h.db.QueryRow(r.Context(), `
		UPDATE clients SET name=$2, cnpj=COALESCE($3,cnpj), logo_url=COALESCE($4,logo_url),
		       contact_name=COALESCE($5,contact_name), phone=COALESCE($6,phone),
		       city=COALESCE($7,city), state=COALESCE($8,state), is_active=$9, updated_at=NOW()
		 WHERE hub_id=$1 RETURNING id`,
		d.HubClientID, d.Name, d.CNPJ, d.LogoURL, d.ContactName, d.Phone, d.City, d.State, ativo,
	).Scan(&id)
	if err == nil {
		h.responde(w, &id)
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// 2) Sem vínculo, mas com CNPJ IGUAL: carimba o `hub_id` no que já existe.
	//
	// Só por CNPJ, e só quando ele não é vazio. Casar por NOME seria repetir o
	// defeito que a §9.5 registrou no importador: um cliente renomeado no
	// E-monitor virava um cliente novo no hub, em silêncio. CNPJ é identidade;
	// nome é rótulo.
	if d.CNPJ != nil && *d.CNPJ != "" {
		err = h.db.QueryRow(r.Context(), `
			UPDATE clients SET hub_id=$1, name=$2, is_active=$3, updated_at=NOW()
			 WHERE cnpj=$4 AND hub_id IS NULL RETURNING id`,
			d.HubClientID, d.Name, ativo, *d.CNPJ).Scan(&id)
		if err == nil {
			h.responde(w, &id)
			return
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}

	// 3) Ninguém: cria com o mínimo.
	err = h.db.QueryRow(r.Context(), `
		INSERT INTO clients (name, cnpj, logo_url, contact_name, phone, city, state, is_active, hub_id)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`,
		d.Name, d.CNPJ, d.LogoURL, d.ContactName, d.Phone, d.City, d.State, ativo, d.HubClientID,
	).Scan(&id)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.responde(w, &id)
}

// ── user.upsert ─────────────────────────────────────────────────────────────

type dadosUsuario struct {
	HubUserID string  `json:"hubUserId"`
	Email     string  `json:"email"`
	Name      *string `json:"name"`
	Phone     *string `json:"phone"`
	Active    *bool   `json:"active"`
}

// upsertUsuario atualiza a identidade de quem JÁ existe aqui, e vincula quem
// existe pelo e-mail. **Não cria conta nova.**
//
// Essa é a única decisão desta fatia que diverge da leitura literal do §9.3
// ("criar/atualizar"), e ela é conservadora de propósito:
//
//   - A conta na plataforma nasce no PRIMEIRO CLIQUE (JIT), e isso não é detalhe
//     de implementação — é o modelo mental que o RFC §8.1 e o handoff descrevem.
//     O `criarPorJit` já cria com este mesmo payload, no momento em que a pessoa
//     de fato aparece.
//   - Criar aqui povoaria o `users` do E-monitor com dezenas de contas de gente
//     que talvez nunca clique. As telas de operação listam usuários.
//   - Acrescentar a criação depois é uma linha. Apagar contas criadas por engano
//     em produção não é.
//
// O casamento por e-mail EXISTE e é o que evita a duplicata que a §9.5 chama de
// pior caso: alguém que já tem conta local ganha o `hub_id` em vez de uma segunda
// conta. É a mesma escada do `acharOuCriar`, sem o degrau de criação.
func (h *HubSyncHandler) upsertUsuario(w http.ResponseWriter, r *http.Request, env syncEnvelope) {
	var d dadosUsuario
	if err := json.Unmarshal(env.Data, &d); err != nil || d.HubUserID == "" {
		http.Error(w, "missing_hub_user_id", http.StatusBadRequest)
		return
	}
	ativo := d.Active == nil || *d.Active

	// 1) Já vinculado.
	var id string
	err := h.db.QueryRow(r.Context(), `
		UPDATE users SET name=COALESCE($2,name), phone=COALESCE($3,phone),
		       is_active=$4, updated_at=NOW()
		 WHERE hub_id=$1 AND deleted_at IS NULL RETURNING id`,
		d.HubUserID, d.Name, d.Phone, ativo).Scan(&id)
	if err == nil {
		h.responde(w, &id)
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// 2) Mesmo e-mail, ainda sem vínculo → vincula em vez de duplicar.
	if d.Email != "" {
		err = h.db.QueryRow(r.Context(), `
			UPDATE users SET hub_id=$1, name=COALESCE($3,name), phone=COALESCE($4,phone),
			       is_active=$5, updated_at=NOW()
			 WHERE LOWER(email)=LOWER($2) AND hub_id IS NULL AND deleted_at IS NULL
			 RETURNING id`,
			d.HubUserID, d.Email, d.Name, d.Phone, ativo).Scan(&id)
		if err == nil {
			h.responde(w, &id)
			return
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}

	// 3) Ninguém. Não é erro: a pessoa ainda não clicou, e o JIT a criará com
	// este mesmo payload quando clicar. `externalId` nulo encerra o evento.
	h.responde(w, nil)
}

// ── user.password_changed ───────────────────────────────────────────────────

type dadosSenha struct {
	HubUserID string `json:"hubUserId"`
	Email     string `json:"email"`
	Password  string `json:"password"`
}

// trocarSenha aplica a senha que o hub propagou — RFC-001 §9.4, decisão D8.
//
// # Por que a senha chega em texto claro
//
// Os hashes são incompatíveis entre as três plataformas: bcrypt aqui e no
// E-rádios, Argon2id na Plura. Sincronizar hash exigiria rebaixar a Plura ao
// algoritmo mais fraco. A D8 escolheu o modelo do SCIM (RFC 7644): a senha viaja
// em claro no envelope, sobre HTTPS e autenticada pela chave de plataforma, e
// cada lado faz o hash com o algoritmo nativo e DESCARTA o claro.
//
// No hub ela fica cifrada (AES-256-GCM) enquanto espera na fila e é decifrada só
// no instante da entrega. Aqui ela existe apenas dentro desta função.
//
// # A senha não pode aparecer em lugar nenhum
//
// Nem em log, nem em erro, nem em telemetria — §9.4, mitigação 2. Por isso
// nenhuma mensagem daqui ecoa o corpo, e o `dadosSenha` nunca é impresso. O
// `reqmetrics` já registra só rota, método e duração, sem payload.
//
// # Só quem é vinculado
//
// O casamento é por `hub_id`, como nos outros eventos. Uma conta local que nunca
// veio do hub não tem a senha trocada por ele: a coexistência (D5) diz que o
// login local dela é dela.
func (h *HubSyncHandler) trocarSenha(w http.ResponseWriter, r *http.Request, env syncEnvelope) {
	var d dadosSenha
	if err := json.Unmarshal(env.Data, &d); err != nil || d.HubUserID == "" || d.Password == "" {
		// A mensagem não diz qual campo faltou quando o que falta é a senha —
		// "missing_password" num log de acesso já é mais do que se precisa saber.
		http.Error(w, "missing_fields", http.StatusBadRequest)
		return
	}

	// Custo 10, o mesmo do `me.go`, do `users.go` e do JIT do SSO. Divergir aqui
	// criaria contas com força de hash diferente conforme o caminho pelo qual a
	// senha foi definida — e ninguém saberia disso olhando a tabela.
	hash, err := bcrypt.GenerateFromPassword([]byte(d.Password), 10)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	var id string
	err = h.db.QueryRow(r.Context(),
		`UPDATE users SET password_hash = $2, updated_at = NOW()
		  WHERE hub_id = $1 AND deleted_at IS NULL
		  RETURNING id`, d.HubUserID, string(hash)).Scan(&id)

	if errors.Is(err, pgx.ErrNoRows) {
		// Ninguém vinculado: a pessoa ainda não clicou, e o JIT vai criá-la com
		// uma senha aleatória no primeiro acesso. Não há o que trocar — 200 com
		// externalId nulo encerra o evento em vez de mandá-lo para a DLQ.
		h.responde(w, nil)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	h.responde(w, &id)
}
