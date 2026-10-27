// Package tokens emite, guarda e revoga as credenciais do MCP (spec
// 2026-10-27 §4.1). Só o hash vai ao banco — SHA-256(JWT_SECRET:token),
// igual à Plura e ao hub (contrato do ecossistema §3.1).
package tokens

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	PrefixoAcesso  = "emonitor_mcp_" // token pessoal e access token OAuth
	PrefixoRefresh = "emonitor_mcr_" // refresh OAuth: nunca abre o /mcp
	PrefixoSegredo = "emonitor_mcs_" // client_secret de cliente confidencial (Gemini)
	PrefixoCodigo  = "emonitor_mcc_" // código de autorização OAuth
)

// Gerar devolve prefixo + 64 hex (32 bytes de crypto/rand).
func Gerar(prefixo string) string {
	b := make([]byte, 32)
	// Desde o Go 1.24 crypto/rand.Read não devolve erro (aborta o processo se
	// o SO não tiver entropia), então não há o que tratar aqui.
	_, _ = rand.Read(b)
	return prefixo + hex.EncodeToString(b)
}

// Hash é a única forma em que uma credencial do MCP toca o banco.
func Hash(segredo []byte, token string) string {
	h := sha256.New()
	h.Write(segredo)
	h.Write([]byte{':'})
	h.Write([]byte(token))
	return hex.EncodeToString(h.Sum(nil))
}

type Tipo string

const (
	TipoPessoal      Tipo = "pessoal"
	TipoOAuthAcesso  Tipo = "oauth_access"
	TipoOAuthRefresh Tipo = "oauth_refresh"
)

// ErrNaoEncontrado cobre "não existe", "revogado", "vencido" e, em
// DonoAdminAtivo, "não é admin ativo": quem chama não distingue de propósito.
var ErrNaoEncontrado = errors.New("mcp: credencial inexistente, revogada ou vencida")

// DB é o que *pgxpool.Pool e pgx.Tx têm em comum.
type DB interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type Store struct {
	db      DB
	segredo []byte
}

func NovoStore(db DB, segredo []byte) *Store { return &Store{db: db, segredo: segredo} }

// NaTransacao devolve um Store que escreve dentro de tx.
func (s *Store) NaTransacao(tx pgx.Tx) *Store { return &Store{db: tx, segredo: s.segredo} }

// Hash aplica o segredo do Store (usado também como chave do rate limit).
func (s *Store) Hash(token string) string { return Hash(s.segredo, token) }

type Token struct {
	ID         uuid.UUID  `json:"id"`
	UserID     uuid.UUID  `json:"-"`
	Tipo       Tipo       `json:"tipo"`
	Nome       string     `json:"nome"`
	ClientID   *string    `json:"client_id"`
	ExpiresAt  time.Time  `json:"expira_em"`
	LastUsedAt *time.Time `json:"ultimo_uso"`
	CreatedAt  time.Time  `json:"criado_em"`
}

type Novo struct {
	UserID   uuid.UUID
	Tipo     Tipo
	Nome     string
	ClientID *string
	Validade time.Duration
}

const colunas = `id, user_id, tipo, nome, client_id, expires_at, last_used_at, created_at`

func scan(row pgx.Row) (*Token, error) {
	var t Token
	var tipo string
	err := row.Scan(&t.ID, &t.UserID, &tipo, &t.Nome, &t.ClientID, &t.ExpiresAt, &t.LastUsedAt, &t.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNaoEncontrado
	}
	if err != nil {
		return nil, err
	}
	t.Tipo = Tipo(tipo)
	return &t, nil
}

// Emitir cria a credencial e devolve o texto claro — a ÚNICA vez em que ele existe.
func (s *Store) Emitir(ctx context.Context, n Novo) (string, *Token, error) {
	prefixo := PrefixoAcesso
	if n.Tipo == TipoOAuthRefresh {
		prefixo = PrefixoRefresh
	}
	claro := Gerar(prefixo)
	t, err := scan(s.db.QueryRow(ctx,
		`INSERT INTO mcp_tokens (user_id, tipo, token_hash, nome, client_id, expires_at)
		 VALUES ($1, $2, $3, $4, $5, NOW() + make_interval(secs => $6))
		 RETURNING `+colunas,
		n.UserID, string(n.Tipo), s.Hash(claro), n.Nome, n.ClientID, n.Validade.Seconds()))
	if err != nil {
		return "", nil, fmt.Errorf("mcp: emitir token: %w", err)
	}
	return claro, t, nil
}

// Buscar acha uma credencial viva de um dos tipos pedidos.
func (s *Store) Buscar(ctx context.Context, claro string, tipos ...Tipo) (*Token, error) {
	nomes := make([]string, len(tipos))
	for i, tp := range tipos {
		nomes[i] = string(tp)
	}
	return scan(s.db.QueryRow(ctx,
		`SELECT `+colunas+` FROM mcp_tokens
		  WHERE token_hash = $1 AND tipo = ANY($2)
		    AND revoked_at IS NULL AND expires_at > NOW()`,
		s.Hash(claro), nomes))
}

// ConsumirRefresh revoga o refresh e o devolve, atomicamente: o segundo uso
// do mesmo refresh já não acha nada (rotação, spec §4.1).
func (s *Store) ConsumirRefresh(ctx context.Context, claro string) (*Token, error) {
	return scan(s.db.QueryRow(ctx,
		`UPDATE mcp_tokens SET revoked_at = NOW()
		  WHERE token_hash = $1 AND tipo = 'oauth_refresh'
		    AND revoked_at IS NULL AND expires_at > NOW()
		  RETURNING `+colunas,
		s.Hash(claro)))
}

// Tocar registra o uso, no máximo uma escrita por minuto por token.
func (s *Store) Tocar(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`UPDATE mcp_tokens SET last_used_at = NOW()
		  WHERE id = $1 AND (last_used_at IS NULL OR last_used_at < NOW() - INTERVAL '1 minute')`, id)
	return err
}

// ListarConexoes devolve o que a tela "Integração com IA" mostra: tokens
// pessoais e uma linha por conexão OAuth (o refresh vivo dela).
func (s *Store) ListarConexoes(ctx context.Context, userID uuid.UUID) ([]Token, error) {
	rows, err := s.db.Query(ctx,
		`SELECT `+colunas+` FROM mcp_tokens
		  WHERE user_id = $1 AND tipo IN ('pessoal', 'oauth_refresh')
		    AND revoked_at IS NULL AND expires_at > NOW()
		  ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Token{}
	for rows.Next() {
		t, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// Revogar mata a credencial `id` se ela for de `userID`. Numa conexão OAuth
// (client_id preenchido) derruba todos os tokens daquele cliente do usuário.
func (s *Store) Revogar(ctx context.Context, id, userID uuid.UUID) (bool, error) {
	tag, err := s.db.Exec(ctx,
		`UPDATE mcp_tokens t SET revoked_at = NOW()
		   FROM mcp_tokens alvo
		  WHERE alvo.id = $1 AND alvo.user_id = $2
		    AND t.user_id = alvo.user_id AND t.revoked_at IS NULL
		    AND (t.id = alvo.id OR (alvo.client_id IS NOT NULL AND t.client_id = alvo.client_id))`,
		id, userID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// RevogarTodos mata toda credencial viva do usuário e os códigos pendentes.
func (s *Store) RevogarTodos(ctx context.Context, userID uuid.UUID) (int64, error) {
	var n int64
	err := s.db.QueryRow(ctx,
		`WITH t AS (UPDATE mcp_tokens SET revoked_at = NOW()
		             WHERE user_id = $1 AND revoked_at IS NULL RETURNING 1),
		      c AS (DELETE FROM mcp_oauth_codes WHERE user_id = $1)
		 SELECT COUNT(*) FROM t`, userID).Scan(&n)
	return n, err
}

type Dono struct {
	ID    uuid.UUID
	Email string
	Nome  string
}

// DonoAdminAtivo relê o usuário: só admin ativo e não apagado (spec D4).
func (s *Store) DonoAdminAtivo(ctx context.Context, userID uuid.UUID) (*Dono, error) {
	var d Dono
	err := s.db.QueryRow(ctx,
		`SELECT id, email, name FROM users
		  WHERE id = $1 AND role = 'admin' AND is_active AND deleted_at IS NULL`,
		userID).Scan(&d.ID, &d.Email, &d.Nome)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNaoEncontrado
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}
