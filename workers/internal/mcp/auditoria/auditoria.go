// Package auditoria grava no audit_log (migration 0009, sem escritor até o
// MCP) as ações feitas pelo MCP ou sobre ele, marcadas com origem "mcp" —
// é assim que uma escrita da IA se distingue de uma feita à mão (contrato §5).
package auditoria

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// Execer é o mínimo que Registrar precisa: *pgxpool.Pool e pgx.Tx servem.
type Execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

func Registrar(ctx context.Context, db Execer, ator uuid.UUID, acao, recurso string, recursoID *uuid.UUID, meta map[string]any) error {
	m := make(map[string]any, len(meta)+1)
	for k, v := range meta {
		m[k] = v
	}
	m["origem"] = "mcp" // depois da cópia: quem chama não sobrescreve
	_, err := db.Exec(ctx,
		`INSERT INTO audit_log (actor_id, actor_type, action, resource, resource_id, metadata)
		 VALUES ($1, 'user', $2, $3, $4, $5)`,
		ator, acao, recurso, recursoID, m)
	return err
}
