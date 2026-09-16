// Package hub fala com o E-Hub (Central de Clientes) — RFC-001 §7.
//
// O hub é dono da IDENTIDADE (quem é, e-mail, nome, ativo/inativo, a que
// cliente pertence). O E-monitor continua dono da AUTORIZAÇÃO interna — role e
// escopo de cliente —, que o hub nunca toca (decisão D9).
//
// Este pacote faz uma coisa só: trocar o código de uso único que chegou na URL
// pelos dados do usuário, numa chamada server-to-server autenticada pela chave
// desta plataforma.
package hub

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// UserPayload espelha a resposta do exchange (RFC §7.2), campo a campo.
type UserPayload struct {
	HubUserID        string          `json:"hubUserId"`
	Email            string          `json:"email"`
	Name             string          `json:"name"`
	Phone            *string         `json:"phone"`
	Level            string          `json:"level"` // "internal" | "client"
	Client           *ClientPayload  `json:"client"`
	ProvisionProfile json.RawMessage `json:"provisionProfile"`
	ExternalID       *string         `json:"externalId"`
}

// ClientPayload é o cliente dono do usuário. nil quando level == "internal".
type ClientPayload struct {
	HubClientID string  `json:"hubClientId"`
	Name        string  `json:"name"`
	CNPJ        *string `json:"cnpj"`
}

// Error carrega o status HTTP que o handler deve devolver ao navegador.
//
// Sem isto, toda falha do hub viraria 500 e a pessoa leria "erro no servidor"
// quando o problema era um código expirado — que se resolve clicando de novo.
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string { return e.Message }

// ErrNotConfigured é o caso "esta instalação não tem SSO ligado".
var ErrNotConfigured = &Error{
	Status:  http.StatusServiceUnavailable,
	Message: "integração com a Central de Clientes não está configurada",
}

// Client troca códigos com o hub. Zero value não serve — use New.
type Client struct {
	baseURL     string
	platformKey string
	http        *http.Client
}

// New constrói o cliente. `baseURL` e `platformKey` vazios são permitidos: o
// Exchange devolve ErrNotConfigured, e é o handler que traduz isso em 503. A
// API sobe normalmente sem a integração.
func New(baseURL, platformKey string) *Client {
	return &Client{
		baseURL:     baseURL,
		platformKey: platformKey,
		// Timeout porque isto acontece DENTRO do login de alguém: sem ele, um
		// hub pendurado deixaria a pessoa olhando um spinner até o navegador
		// desistir sozinho.
		http: &http.Client{Timeout: 10 * time.Second},
	}
}

// Configured diz se esta instalação tem a integração ligada.
func (c *Client) Configured() bool {
	return c != nil && c.baseURL != "" && c.platformKey != ""
}

// ChaveConfere autentica uma requisição que o HUB fez para cá — o sentido
// oposto do Exchange (RFC-001 §9.2).
//
// A MESMA chave serve nas duas direções, e isso é decisão, não acaso: uma chave
// nova exigiria variável de ambiente nova, e variável nova neste repositório
// significa lembrar do bloco `environment:` explícito do compose. Foi
// exatamente o esquecimento que fez o SSO responder 503 em produção com tudo
// aparentemente certo no `.env` (PR #8). Reusar `HUB_PLATFORM_KEY` remove a
// classe inteira do problema.
//
// Devolve um acessor booleano em vez de expor a chave: quem chama precisa saber
// se confere, não qual é.
//
// `subtle.ConstantTimeCompare` porque a comparação acontece antes de qualquer
// autenticação — é a própria autenticação — e `==` em string vaza o prefixo
// comum pelo tempo de resposta.
func (c *Client) ChaveConfere(apresentada string) bool {
	if !c.Configured() || apresentada == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(c.platformKey), []byte(apresentada)) == 1
}

// Exchange troca o código pelos dados do usuário.
//
// O corpo da resposta é lido com limite: o hub é um serviço confiável, mas
// "confiável" não é "incapaz de responder um gigabyte por engano", e isto roda
// no caminho de um login.
const maxBody = 1 << 20 // 1 MiB

func (c *Client) Exchange(ctx context.Context, code string) (*UserPayload, error) {
	if !c.Configured() {
		return nil, ErrNotConfigured
	}

	body, err := json.Marshal(map[string]string{"code": code})
	if err != nil {
		return nil, &Error{Status: http.StatusInternalServerError, Message: "erro interno"}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/api/sso/exchange", bytes.NewReader(body))
	if err != nil {
		return nil, &Error{Status: http.StatusInternalServerError, Message: "erro interno"}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hub-Platform-Key", c.platformKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, &Error{
			Status:  http.StatusBadGateway,
			Message: "não foi possível falar com a Central de Clientes",
		}
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// segue abaixo
	case http.StatusGone:
		return nil, &Error{
			Status:  http.StatusGone,
			Message: "este link de acesso expirou ou já foi usado",
		}
	case http.StatusUnauthorized:
		// A chave DESTA plataforma foi recusada. Não é culpa de quem clicou, e
		// devolver 401 numa rota de login convidaria o frontend a tentar
		// renovar uma sessão que não existe.
		return nil, &Error{
			Status:  http.StatusServiceUnavailable,
			Message: "integração com a Central de Clientes indisponível",
		}
	case http.StatusForbidden:
		return nil, &Error{
			Status:  http.StatusForbidden,
			Message: "seu acesso a este produto não está liberado na Central de Clientes",
		}
	default:
		return nil, &Error{
			Status:  http.StatusBadGateway,
			Message: "a Central de Clientes respondeu com erro",
		}
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, &Error{Status: http.StatusBadGateway, Message: "resposta ilegível da Central de Clientes"}
	}
	var out UserPayload
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, &Error{Status: http.StatusBadGateway, Message: "a Central de Clientes respondeu de forma inesperada"}
	}
	if out.HubUserID == "" || out.Email == "" {
		return nil, &Error{Status: http.StatusBadGateway, Message: "a Central de Clientes respondeu de forma inesperada"}
	}
	return &out, nil
}

// AsError extrai o *Error de um erro do pacote. Devolve ok=false para qualquer
// outra coisa, que o handler deve tratar como 500.
func AsError(err error) (*Error, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}

// String é usado só em log/diagnóstico; nunca inclui o código nem a chave.
func (e *Error) String() string { return fmt.Sprintf("hub: %d %s", e.Status, e.Message) }

// ── A porta de eventos do hub (desenho de 2026-09-16) ───────────────────────

// ClienteUpsert são os campos que a porta do hub lê.
//
// ⚠️ Todo campo opcional é ponteiro COM `omitempty`, e isso é o contrato, não
// estilo: do outro lado, campo ausente significa "não falei dele" e campo
// presente significa "é este o valor de hoje". Um `string` comum viajaria como
// `""` e diria ao hub que o cliente não tem cidade.
type ClienteUpsert struct {
	IDNaPlataforma  string  `json:"idNaPlataforma"`
	Nome            string  `json:"nome"`
	CNPJ            *string `json:"cnpj,omitempty"`
	LogoURL         *string `json:"logoUrl,omitempty"`
	ContatoNome     *string `json:"contatoNome,omitempty"`
	ContatoEmail    *string `json:"contatoEmail,omitempty"`
	ContatoTelefone *string `json:"contatoTelefone,omitempty"`
	Cidade          *string `json:"cidade,omitempty"`
	UF              *string `json:"uf,omitempty"`
}

type envelopeEvento struct {
	Tipo       string `json:"tipo"`
	OcorridoEm string `json:"ocorridoEm"`
	Dados      any    `json:"dados"`
}

// Emitir avisa o hub de uma mutação que aconteceu AQUI — o sentido oposto do
// que o `hubsync.go` recebe.
//
// `ocorridoEm` é carimbado aqui, em UTC, e é a guarda de ORDEM do hub: sem ele
// um retrato antigo sobrescreveria um dado novo. Não é auditoria.
//
// ⚠️ O hub recusa `ocorridoEm` mais de 5 minutos no futuro. Relógio da VM
// adiantado faz TODA emissão voltar 400 — e o sintoma (nada chega ao hub) não
// aponta para o relógio.
func (c *Client) Emitir(ctx context.Context, tipo string, dados any) error {
	if !c.Configured() {
		return ErrNotConfigured
	}

	body, err := json.Marshal(envelopeEvento{
		Tipo:       tipo,
		OcorridoEm: time.Now().UTC().Format(time.RFC3339),
		Dados:      dados,
	})
	if err != nil {
		return &Error{Status: http.StatusInternalServerError, Message: "erro interno"}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/api/platform/events", bytes.NewReader(body))
	if err != nil {
		return &Error{Status: http.StatusInternalServerError, Message: "erro interno"}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hub-Platform-Key", c.platformKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return &Error{
			Status:  http.StatusBadGateway,
			Message: "não foi possível falar com a Central de Clientes",
		}
	}
	defer resp.Body.Close()
	// Drena o corpo antes de fechar: sem isto a conexão não volta para o pool
	// keep-alive, e cada cliente criado abre um socket novo.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBody))

	if resp.StatusCode != http.StatusOK {
		return &Error{
			Status:  resp.StatusCode,
			Message: fmt.Sprintf("a Central de Clientes recusou o evento %s", tipo),
		}
	}
	return nil
}
