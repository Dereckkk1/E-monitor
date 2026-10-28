package mcp

import "github.com/jackc/pgx/v5/pgxpool"

// Leitura reúne os repositórios que as tools de leitura chamam — os MESMOS
// que os handlers HTTP usam (cmd/api/main.go). Nenhuma regra mora aqui.
type Leitura struct{}

func NovaLeitura(pool *pgxpool.Pool) *Leitura { return &Leitura{} }
