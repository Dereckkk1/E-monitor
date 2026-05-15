package auth

import (
	"errors"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"radiocheck/internal/users"
)

type Claims struct {
	UserID   uuid.UUID  `json:"user_id"`
	Role     string     `json:"role"`
	ClientID *uuid.UUID `json:"client_id,omitempty"`
	jwt.RegisteredClaims
}

func getSecret() ([]byte, error) {
	s := os.Getenv("JWT_SECRET")
	if len(s) < 32 {
		return nil, errors.New("JWT_SECRET must be at least 32 bytes")
	}
	return []byte(s), nil
}

// IssueTokenForUser gera o JWT a partir de uma struct users.User. Inclui
// o claim client_id quando o user é viewer; nil caso contrário.
func IssueTokenForUser(u *users.User) (string, error) {
	secret, err := getSecret()
	if err != nil {
		return "", err
	}
	claims := Claims{
		UserID:   u.ID,
		Role:     u.Role,
		ClientID: u.ClientID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(8 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return tok.SignedString(secret)
}

// IssueToken é mantido para compatibilidade com bootstrap.go e testes
// legados. Não inclui client_id — admin/operator nunca tem.
//
// Deprecated: use IssueTokenForUser.
func IssueToken(userID uuid.UUID, role string) (string, error) {
	return IssueTokenForUser(&users.User{ID: userID, Role: role})
}

func ParseToken(tokenStr string) (*Claims, error) {
	secret, err := getSecret()
	if err != nil {
		return nil, err
	}
	tok, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		// Fix the algorithm to HS256 explicitly (defense in depth against
		// "alg confusion" attacks). Accepting *jwt.SigningMethodHMAC would
		// also allow HS384/HS512 — asymmetric with IssueToken which signs
		// with HS256, so a token signed with a different HMAC variant is
		// not legitimately produced by us anyway.
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok || t.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, errors.New("unexpected signing method")
		}
		return secret, nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := tok.Claims.(*Claims)
	if !ok || !tok.Valid {
		return nil, errors.New("invalid token")
	}
	return claims, nil
}
