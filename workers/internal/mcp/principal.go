// Package mcp é o servidor MCP do E-monitor (spec docs/superpowers/specs/
// 2026-10-27-mcp-server-design.md). Só admin interno; tools chamam os mesmos
// serviços das telas — nenhuma regra de negócio mora aqui.
package mcp

import (
	"context"

	"github.com/google/uuid"
)

// Principal é o dono da credencial que abriu o pedido, relido do banco.
type Principal struct {
	UserID  uuid.UUID
	Email   string
	Nome    string
	TokenID uuid.UUID
}

type chavePrincipal struct{}

func ComPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, chavePrincipal{}, p)
}

func PrincipalDe(ctx context.Context) (*Principal, bool) {
	p, ok := ctx.Value(chavePrincipal{}).(*Principal)
	return p, ok && p != nil
}
