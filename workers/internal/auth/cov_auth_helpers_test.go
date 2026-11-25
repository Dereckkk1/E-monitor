package auth

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"radiocheck/internal/db"
)

const covZSecret = "segredo-de-teste-com-mais-de-32-bytes-ok"

// covZPool abre um pool contra TEST_DATABASE_URL (skip se ausente).
func covZPool(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := db.New(ctx, url, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return ctx, pool
}

// covZSign assina um JWT HS256 com a chave e claims dados.
func covZSign(t *testing.T, secret string, claims Claims) string {
	t.Helper()
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
	require.NoError(t, err)
	return s
}

// covZClaims monta claims validos por 1h para um papel.
func covZClaims(role string) Claims {
	return Claims{
		UserID: uuid.New(),
		Role:   role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
}

// covZUser insere um usuario e agenda a remocao. active=false desativa.
func covZUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool, email string, active, deleted bool) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, role, is_active, deleted_at)
		 VALUES ($1, 'hash-de-teste', 'admin', $2, CASE WHEN $3 THEN now() END) RETURNING id`,
		email, active, deleted).Scan(&id))
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, id) })
	return id
}
