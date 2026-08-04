package auth_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/auth"
)

func TestClientScopes_Viewer_MultipleClients(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	ctx := auth.ContextWithClaims(context.Background(), &auth.Claims{
		Role: "viewer", ClientID: &a, ClientIDs: []uuid.UUID{a, b},
	})
	require.Equal(t, []uuid.UUID{a, b}, auth.ClientScopesFromContext(ctx))
	require.True(t, auth.ScopeAllows(ctx, a))
	require.True(t, auth.ScopeAllows(ctx, b))
	require.False(t, auth.ScopeAllows(ctx, uuid.New()))
}

func TestClientScopes_LegacyToken_OnlyClientID(t *testing.T) {
	// JWT emitido antes da migração multi-cliente (validade de 8h): traz só
	// client_id. Sem essa tradução, todo cliente logado seria deslogado no
	// deploy do backend.
	cid := uuid.New()
	ctx := auth.ContextWithClaims(context.Background(), &auth.Claims{
		Role: "viewer", ClientID: &cid,
	})
	require.Equal(t, []uuid.UUID{cid}, auth.ClientScopesFromContext(ctx))
	require.True(t, auth.ScopeAllows(ctx, cid))
	require.False(t, auth.ScopeAllows(ctx, uuid.New()))
}

func TestClientScopes_Admin_ReturnsNil(t *testing.T) {
	ctx := auth.ContextWithClaims(context.Background(), &auth.Claims{Role: "admin"})
	require.Nil(t, auth.ClientScopesFromContext(ctx))
	require.True(t, auth.ScopeAllows(ctx, uuid.New()))
}

func TestClientScopes_Operator_ReturnsNil(t *testing.T) {
	cid := uuid.New()
	ctx := auth.ContextWithClaims(context.Background(), &auth.Claims{
		Role: "operator", ClientID: &cid, ClientIDs: []uuid.UUID{cid},
	})
	require.Nil(t, auth.ClientScopesFromContext(ctx))
}

func TestClientScopes_NoClaims(t *testing.T) {
	require.Nil(t, auth.ClientScopesFromContext(context.Background()))
	require.True(t, auth.ScopeAllows(context.Background(), uuid.New()))
}

func TestClientScopes_ViewerNoClient_FailsClosed(t *testing.T) {
	// Viewer sem nenhum cliente é token mal-formado. Devolve [uuid.Nil] pra
	// toda query escopada dar vazio, em vez de virar unscoped (acesso total).
	ctx := auth.ContextWithClaims(context.Background(), &auth.Claims{Role: "viewer"})
	require.Equal(t, []uuid.UUID{uuid.Nil}, auth.ClientScopesFromContext(ctx))
	require.False(t, auth.ScopeAllows(ctx, uuid.New()))
}
