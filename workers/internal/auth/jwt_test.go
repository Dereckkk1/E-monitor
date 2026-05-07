package auth_test

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"radiocheck/internal/auth"
)

func TestJWT_RoundTrip(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret-32-chars-minimum!!!!")
	id := uuid.New()
	tok, err := auth.IssueToken(id, "operator")
	assert.NoError(t, err)
	claims, err := auth.ParseToken(tok)
	assert.NoError(t, err)
	assert.Equal(t, id, claims.UserID)
	assert.Equal(t, "operator", claims.Role)
}

func TestJWT_InvalidToken(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret-32-chars-minimum!!!!")
	_, err := auth.ParseToken("not-a-valid-token")
	assert.Error(t, err)
}

func TestJWT_ExpiredToken(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret-32-chars-minimum!!!!")
	id := uuid.New()
	claims := auth.Claims{
		UserID: id,
		Role:   "operator",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-1 * time.Hour)),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString([]byte("test-secret-32-chars-minimum!!!!"))
	assert.NoError(t, err)
	_, err = auth.ParseToken(signed)
	assert.Error(t, err)
}
