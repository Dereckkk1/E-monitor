package oauth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"

	"github.com/jackc/pgx/v5"

	"radiocheck/internal/mcp/tokens"
)

var ErrClienteDesconhecido = errors.New("mcp: cliente OAuth desconhecido")

type Cliente struct {
	ClientID     string
	Nome         string
	RedirectURIs []string
	Confidencial bool
}

type Clientes struct {
	db      tokens.DB
	segredo []byte
}

// Registrar cria o cliente (RFC 7591). Devolve o client_secret em claro só
// para confidencial, e só agora. Faz a faxina de quem nunca chegou ao
// /token: 1 dia para público, 7 para confidencial (o Gemini é configurado à
// mão e pode demorar).
func (c *Clientes) Registrar(ctx context.Context, nome string, uris []string, confidencial bool) (*Cliente, string, error) {
	if _, err := c.db.Exec(ctx,
		`DELETE FROM mcp_oauth_clients
		  WHERE first_token_at IS NULL
		    AND created_at < NOW() - CASE WHEN client_secret_hash IS NULL
		                                  THEN INTERVAL '1 day' ELSE INTERVAL '7 days' END`); err != nil {
		return nil, "", err
	}
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	cli := &Cliente{ClientID: "emc_" + hex.EncodeToString(b), Nome: nome, RedirectURIs: uris, Confidencial: confidencial}
	var segredo string
	var hash *string
	if confidencial {
		segredo = tokens.Gerar(tokens.PrefixoSegredo)
		h := tokens.Hash(c.segredo, segredo)
		hash = &h
	}
	_, err := c.db.Exec(ctx,
		`INSERT INTO mcp_oauth_clients (client_id, client_secret_hash, nome, redirect_uris)
		 VALUES ($1, $2, $3, $4)`, cli.ClientID, hash, nome, uris)
	if err != nil {
		return nil, "", err
	}
	return cli, segredo, nil
}

func (c *Clientes) Buscar(ctx context.Context, clientID string) (*Cliente, error) {
	cli := Cliente{ClientID: clientID}
	var hash *string
	err := c.db.QueryRow(ctx,
		`SELECT nome, redirect_uris, client_secret_hash FROM mcp_oauth_clients WHERE client_id = $1`,
		clientID).Scan(&cli.Nome, &cli.RedirectURIs, &hash)
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
