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

// TestJWT_RejectsHS512 ensures ParseToken refuses tokens signed with an HMAC
// algorithm OTHER than HS256, even though they share the same key family.
// This is defense in depth: an attacker who can issue a token (e.g. via a
// compromised tool) and chooses a less-vetted algorithm should not bypass
// the parser.
func TestJWT_RejectsHS512(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret-32-chars-minimum!!!!")
	id := uuid.New()
	claims := auth.Claims{
		UserID: id,
		Role:   "admin",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS512, claims)
	signed, err := tok.SignedString([]byte("test-secret-32-chars-minimum!!!!"))
	assert.NoError(t, err)
	_, err = auth.ParseToken(signed)
	assert.Error(t, err, "HS512 token must be rejected; only HS256 is accepted")
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
