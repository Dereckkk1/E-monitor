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
// Convention: nil = "no scope" = can see everything.
func ClientScopeFromContext(ctx context.Context) *uuid.UUID {
	c, ok := ClaimsFromContext(ctx)
	if !ok {
		return nil
	}
	if c.Role != "viewer" {
		return nil
	}
	return c.ClientID
}
