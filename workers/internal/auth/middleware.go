package auth

import (
	"context"
	"net/http"
	"strings"

	"radiocheck/internal/reqmetrics"
)

type ctxKey string

const claimsKey ctxKey = "claims"

// ClaimsFromContext extracts JWT claims from the request context.
func ClaimsFromContext(ctx context.Context) (*Claims, bool) {
	c, ok := ctx.Value(claimsKey).(*Claims)
	return c, ok
}

func RequireJWT(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		claims, err := ParseToken(strings.TrimPrefix(header, "Bearer "))
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), claimsKey, claims)
		// Propaga o user_id pro reqmetrics.Middleware (outer) via holder
		// compartilhado — single source of truth pra telemetria por usuário.
		reqmetrics.SetUserID(ctx, claims.UserID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireJWTAtivo é o RequireJWT mais a checagem de `is_active` (ver
// VerificadorAtivo). Um middleware novo em vez de mudar a assinatura do
// RequireJWT: os testes de rota montam o router sem banco, e forçá-los a ter um
// pgxpool só para exercitar autorização seria pagar caro por nada.
//
// `v` nil devolve o RequireJWT puro — é o mesmo "nil desliga a peça" que o
// router já usa para HubSSO, Metrics e BlockList.
func RequireJWTAtivo(v *VerificadorAtivo) func(http.Handler) http.Handler {
	if v == nil {
		return RequireJWT
	}
	return func(next http.Handler) http.Handler {
		return RequireJWT(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c, ok := ClaimsFromContext(r.Context())
			if ok && !v.Ativo(r.Context(), c.UserID) {
				// 401 e não 403: para o cliente HTTP a sessão deixou de valer, e
				// é isso que faz o frontend mandar a pessoa para o /login em vez
				// de mostrar "sem permissão" numa tela que ela não pode mais ver.
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		}))
	}
}

// WithClaims é a contraparte exportada de ClaimsFromContext, usada por
// tests de handler que precisam injetar claims sem passar pelo middleware
// JWT real. Em código de produção, RequireJWT é o único setter.
func WithClaims(ctx context.Context, c *Claims) context.Context {
	return context.WithValue(ctx, claimsKey, c)
}

func RequireRole(roles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := ClaimsFromContext(r.Context())
			if !ok {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			for _, role := range roles {
				if claims.Role == role {
					next.ServeHTTP(w, r)
					return
				}
			}
			http.Error(w, "forbidden", http.StatusForbidden)
		})
	}
}
