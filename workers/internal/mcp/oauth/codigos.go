package oauth

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"radiocheck/internal/mcp/tokens"
)

var ErrCodigoInvalido = errors.New("mcp: código de autorização inválido, vencido ou já usado")

type Codigo struct {
	ClientID    string
	UserID      uuid.UUID
	RedirectURI string
	Desafio     *string
}

type Codigos struct {
	db      tokens.DB
	segredo []byte
}

func (c *Codigos) NaTransacao(tx pgx.Tx) *Codigos { return &Codigos{db: tx, segredo: c.segredo} }

// Criar grava o código (10 minutos) e devolve o texto claro.
func (c *Codigos) Criar(ctx context.Context, cod Codigo) (string, error) {
	if _, err := c.db.Exec(ctx, `DELETE FROM mcp_oauth_codes WHERE expires_at < NOW()`); err != nil {
		return "", err
	}
	claro := tokens.Gerar(tokens.PrefixoCodigo)
	_, err := c.db.Exec(ctx,
		`INSERT INTO mcp_oauth_codes (code_hash, client_id, user_id, redirect_uri, code_challenge, expires_at)
		 VALUES ($1, $2, $3, $4, $5, NOW() + INTERVAL '10 minutes')`,
		tokens.Hash(c.segredo, claro), cod.ClientID, cod.UserID, cod.RedirectURI, cod.Desafio)
	return claro, err
}

// Consumir apaga e devolve: uso único por construção.
func (c *Codigos) Consumir(ctx context.Context, claro string) (*Codigo, error) {
	var cod Codigo
	err := c.db.QueryRow(ctx,
		`DELETE FROM mcp_oauth_codes WHERE code_hash = $1 AND expires_at > NOW()
		 RETURNING client_id, user_id, redirect_uri, code_challenge`,
		tokens.Hash(c.segredo, claro)).Scan(&cod.ClientID, &cod.UserID, &cod.RedirectURI, &cod.Desafio)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrCodigoInvalido
	}
	if err != nil {
		return nil, err
	}
	return &cod, nil
}
