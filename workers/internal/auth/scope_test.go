package auth_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/auth"
)

func TestClientScopeFromContext_Viewer(t *testing.T) {
	cid := uuid.New()
	ctx := auth.ContextWithClaims(context.Background(), &auth.Claims{
		Role: "viewer", ClientID: &cid,
	})
	got := auth.ClientScopeFromContext(ctx)
	require.NotNil(t, got)
	require.Equal(t, cid, *got)
}

func TestClientScopeFromContext_Admin_ReturnsNil(t *testing.T) {
	ctx := auth.ContextWithClaims(context.Background(), &auth.Claims{Role: "admin"})
	require.Nil(t, auth.ClientScopeFromContext(ctx))
}

func TestClientScopeFromContext_Operator_ReturnsNil(t *testing.T) {
	cid := uuid.New()
	// Even if operator somehow has a client_id, scope is nil because
	// scope is "viewer-only".
	ctx := auth.ContextWithClaims(context.Background(), &auth.Claims{
		Role: "operator", ClientID: &cid,
	})
	require.Nil(t, auth.ClientScopeFromContext(ctx))
}

func TestClientScopeFromContext_NoClaims(t *testing.T) {
	require.Nil(t, auth.ClientScopeFromContext(context.Background()))
}

func TestClientScopeFromContext_ViewerNoClient_ReturnsNil(t *testing.T) {
	// Defensive: a viewer without client_id (shouldn't happen — DB CHECK
	// constraint blocks it) returns nil rather than crashing.
	ctx := auth.ContextWithClaims(context.Background(), &auth.Claims{Role: "viewer"})
	require.Nil(t, auth.ClientScopeFromContext(ctx))
}
