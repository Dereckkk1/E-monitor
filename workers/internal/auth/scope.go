package auth

import (
	"context"
	"slices"

	"github.com/google/uuid"
)

// ContextWithClaims is exposed for tests (and any caller that wants to
// mount a context with pre-defined claims, e.g. integrated handler tests).
// In production, the RequireJWT middleware is what populates this.
func ContextWithClaims(ctx context.Context, c *Claims) context.Context {
	return context.WithValue(ctx, claimsKey, c)
}

// ClientScopesFromContext devolve a carteira de clientes do requester quando ele
// é viewer (cliente), ou nil quando é admin/operator/anônimo. Use em handlers que
// filtram listas por cliente.
//
// Convenção: nil = "sem escopo" = enxerga tudo (só admin/operator).
//
// Defense-in-depth: viewer sem nenhum cliente é token mal-formado (anterior à
// migration 0027 ou bug futuro). Devolve []uuid.UUID{uuid.Nil} pra toda query
// escopada dar resultado vazio, em vez de tratar como unscoped (acesso total).
//
// Compatibilidade: token emitido antes da migration 0062 traz apenas client_id
// e vale por até 8h. Traduzimos pra lista de um elemento — sem isso, todo
// cliente logado seria deslogado no deploy do backend.
func ClientScopesFromContext(ctx context.Context) []uuid.UUID {
	c, ok := ClaimsFromContext(ctx)
	if !ok {
		return nil
	}
	if c.Role != "viewer" {
		return nil
	}
	if len(c.ClientIDs) > 0 {
		return c.ClientIDs
	}
	if c.ClientID != nil {
		return []uuid.UUID{*c.ClientID}
	}
	return []uuid.UUID{uuid.Nil}
}

// ScopeAllows diz se o requester pode enxergar dados do cliente informado.
// Devolve true quando não há escopo (admin/operator). É o helper dos checks
// pontuais que respondem 404 anti-oracle.
func ScopeAllows(ctx context.Context, clientID uuid.UUID) bool {
	scopes := ClientScopesFromContext(ctx)
	if scopes == nil {
		return true
	}
	return slices.Contains(scopes, clientID)
}
