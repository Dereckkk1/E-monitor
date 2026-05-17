package auth

import (
	"context"

	"github.com/google/uuid"
)

// ContextWithClaims is exposed for tests (and any caller that wants to
// mount a context with pre-defined claims, e.g. integrated handler tests).
// In production, the RequireJWT middleware is what populates this.
func ContextWithClaims(ctx context.Context, c *Claims) context.Context {
	return context.WithValue(ctx, claimsKey, c)
}

// ClientScopeFromContext returns the client_id of the JWT if the requester is
// a viewer (customer), or nil if they are admin/operator/anonymous. Use in
// handlers that need to filter by client when the requester is a customer.
//
// Convention: nil = "no scope" = can see everything (admin/operator only).
//
// Defense-in-depth: a viewer without a client_id claim is a malformed token
// (pre-migration 0027 or future bug). Instead of treating it as unscoped
// (which would grant full access), we return uuid.Nil so every scoped query
// produces an empty result set.
func ClientScopeFromContext(ctx context.Context) *uuid.UUID {
	c, ok := ClaimsFromContext(ctx)
	if !ok {
		return nil
	}
	if c.Role != "viewer" {
		return nil
	}
	if c.ClientID == nil {
		// Viewer sem client_id claim é token mal-formado (anterior à
		// migration 0027 ou bug futuro). Defense-in-depth: retorna UUID
		// zero pra forçar todas as queries a não retornarem nada, em vez
		// de tratar como unscoped (que dá acesso total).
		empty := uuid.Nil
		return &empty
	}
	return c.ClientID
}
