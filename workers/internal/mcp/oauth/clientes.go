package oauth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"radiocheck/internal/mcp/tokens"
)

var (
	ErrClienteDesconhecido = errors.New("mcp: cliente OAuth desconhecido")
	ErrRedirectInvalido    = errors.New("mcp: redirect_uri inválido (https, ou http só em loopback; sem fragmento, userinfo nem host não-ASCII)")
)

// despejoMaximo limita quantos pendentes um único registro pode despejar.
const despejoMaximo = 10

type Cliente struct {
	ClientID     string
	Nome         string
	RedirectURIs []string
	Confidencial bool
	// Ativado: já chegou ao /token alguma vez (first_token_at preenchido).
	Ativado bool
}

type Clientes struct {
	db      tokens.DB
	segredo []byte
}

// NovosClientes: db é o pool (ou uma tx); segredo é o JWT_SECRET, que salga o
// hash do client_secret.
func NovosClientes(db tokens.DB, segredo []byte) *Clientes {
	return &Clientes{db: db, segredo: segredo}
}

func novoClientID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return "emc_" + hex.EncodeToString(b)
}

// Registrar cria um cliente PÚBLICO pelo DCR (RFC 7591) — confidencial só por
// RegistrarConfidencial. Faz a faxina de quem nunca chegou ao /token (1 dia
// para público, 7 para confidencial) e, com maxPendentes ou mais clientes
// pendentes, despeja os públicos pendentes mais antigos — no máximo
// despejoMaximo por chamada — antes de inserir: o DCR aberto não pode ficar
// travado por quem encheu a fila. Confidencial (do admin) e quem já obteve
// token nunca são despejados. Devolve quantos foram despejados.
func (c *Clientes) Registrar(ctx context.Context, nome string, uris []string, maxPendentes int) (*Cliente, int64, error) {
	if _, err := c.db.Exec(ctx,
		`DELETE FROM mcp_oauth_clients
		  WHERE first_token_at IS NULL
		    AND created_at < NOW() - CASE WHEN client_secret_hash IS NULL
		                                  THEN INTERVAL '1 day' ELSE INTERVAL '7 days' END`); err != nil {
		return nil, 0, err
	}
	despejo, err := c.db.Exec(ctx,
		`DELETE FROM mcp_oauth_clients WHERE id IN (
		   SELECT id FROM mcp_oauth_clients
		    WHERE first_token_at IS NULL AND client_secret_hash IS NULL
		    ORDER BY created_at, id
		    LIMIT LEAST($2::int, GREATEST(0,
		          (SELECT COUNT(*) FROM mcp_oauth_clients WHERE first_token_at IS NULL) - $1::int + 1)))`,
		maxPendentes, despejoMaximo)
	if err != nil {
		return nil, 0, err
	}
	cli := &Cliente{ClientID: novoClientID(), Nome: nome, RedirectURIs: uris}
	if _, err := c.db.Exec(ctx,
		`INSERT INTO mcp_oauth_clients (client_id, nome, redirect_uris) VALUES ($1, $2, $3)`,
		cli.ClientID, nome, uris); err != nil {
		return nil, 0, err
	}
	return cli, despejo.RowsAffected(), nil
}

// RegistrarConfidencial cria um cliente confidencial (ex.: Gemini Enterprise,
// que não faz PKCE). É o caminho do administrador (cmd/registra-cliente-mcp):
// fora do teto de pendentes e sem a lista de destinos do DCR — quem decide o
// destino é o admin —, mas cada URI ainda passa no redirectValido. Devolve o
// client_secret em claro: a única vez em que ele existe.
func (c *Clientes) RegistrarConfidencial(ctx context.Context, nome string, uris []string) (*Cliente, string, error) {
	if len(uris) == 0 {
		return nil, "", fmt.Errorf("%w: informe ao menos um", ErrRedirectInvalido)
	}
	normais := make([]string, len(uris))
	for i, u := range uris {
		n := normalizarOrigem(strings.TrimSpace(u))
		if !redirectValido(n) {
			return nil, "", fmt.Errorf("%w: %s", ErrRedirectInvalido, u)
		}
		normais[i] = n
	}
	segredo := tokens.Gerar(tokens.PrefixoSegredo)
	cli := &Cliente{ClientID: novoClientID(), Nome: nomeCliente(nome), RedirectURIs: normais, Confidencial: true}
	if _, err := c.db.Exec(ctx,
		`INSERT INTO mcp_oauth_clients (client_id, client_secret_hash, nome, redirect_uris) VALUES ($1, $2, $3, $4)`,
		cli.ClientID, tokens.Hash(c.segredo, segredo), cli.Nome, normais); err != nil {
		return nil, "", err
	}
	return cli, segredo, nil
}

func (c *Clientes) Buscar(ctx context.Context, clientID string) (*Cliente, error) {
	cli := Cliente{ClientID: clientID}
	var hash *string
	err := c.db.QueryRow(ctx,
		`SELECT nome, redirect_uris, client_secret_hash, first_token_at IS NOT NULL
		   FROM mcp_oauth_clients WHERE client_id = $1`,
		clientID).Scan(&cli.Nome, &cli.RedirectURIs, &hash, &cli.Ativado)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrClienteDesconhecido
	}
	if err != nil {
		return nil, err
	}
	cli.Confidencial = hash != nil
	return &cli, nil
}

func (c *Clientes) ConferirSegredo(ctx context.Context, clientID, segredo string) (bool, error) {
	var hash *string
	err := c.db.QueryRow(ctx,
		`SELECT client_secret_hash FROM mcp_oauth_clients WHERE client_id = $1`, clientID).Scan(&hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if hash == nil || segredo == "" {
		return false, nil
	}
	return subtle.ConstantTimeCompare([]byte(*hash), []byte(tokens.Hash(c.segredo, segredo))) == 1, nil
}
