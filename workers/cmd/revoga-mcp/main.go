// revoga-mcp revoga credenciais do MCP do E-monitor em massa (spec
// 2026-10-27 §4.1). Sem --aplicar é dry-run. Uso em prod (regra 4.2):
//
//	docker compose ... exec api revoga-mcp --dsn "$DATABASE_URL" --listar
//	docker compose ... exec api revoga-mcp --dsn "$DATABASE_URL" --email pessoa@x --aplicar
//	docker compose ... exec api revoga-mcp --dsn "$DATABASE_URL" --todos --aplicar
//
// Imprime UMA linha JSON por execução, nunca um hash nem um token.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"radiocheck/internal/db"
	"radiocheck/internal/mcp/auditoria"
	"radiocheck/internal/mcp/tokens"
)

type credencial struct {
	Email     string     `json:"email"`
	Tipo      string     `json:"tipo"`
	Nome      string     `json:"nome"`
	CriadoEm  time.Time  `json:"criado_em"`
	UltimoUso *time.Time `json:"ultimo_uso"`
	ExpiraEm  time.Time  `json:"expira_em"`
}

func main() {
	dsn := flag.String("dsn", os.Getenv("DATABASE_URL"), "postgres connection string")
	listar := flag.Bool("listar", false, "lista as credenciais vivas")
	email := flag.String("email", "", "revoga todas as credenciais desta pessoa")
	todos := flag.Bool("todos", false, "revoga todas as credenciais de todo mundo")
	aplicar := flag.Bool("aplicar", false, "sem isto é dry-run")
	flag.Parse()

	modos := 0
	for _, b := range []bool{*listar, *email != "", *todos} {
		if b {
			modos++
		}
	}
	if modos != 1 {
		fmt.Fprintln(os.Stderr, "use exatamente um de --listar, --email X, --todos")
		os.Exit(2)
	}

	ctx := context.Background()
	pool, err := db.New(ctx, *dsn, zap.NewNop())
	if err != nil {
		fmt.Fprintln(os.Stderr, "conectar:", err)
		os.Exit(1)
	}
	defer pool.Close()
	out := json.NewEncoder(os.Stdout)

	if *listar {
		rows, err := pool.Query(ctx,
			`SELECT u.email, t.tipo, t.nome, t.created_at, t.last_used_at, t.expires_at
			   FROM mcp_tokens t JOIN users u ON u.id = t.user_id
			  WHERE t.revoked_at IS NULL AND t.expires_at > NOW() AND t.tipo IN ('pessoal', 'oauth_refresh')
			  ORDER BY u.email, t.created_at`)
		sair(err)
		lista := []credencial{}
		for rows.Next() {
			var c credencial
			sair(rows.Scan(&c.Email, &c.Tipo, &c.Nome, &c.CriadoEm, &c.UltimoUso, &c.ExpiraEm))
			lista = append(lista, c)
		}
		sair(rows.Err())
		sair(out.Encode(map[string]any{"credenciais": lista}))
		return
	}

	filtro, args := `WHERE t.revoked_at IS NULL`, []any{}
	if *email != "" {
		filtro += ` AND LOWER(u.email) = LOWER($1)`
		args = append(args, *email)
	}
	rows, err := pool.Query(ctx,
		`SELECT u.id, u.email, COUNT(*) FROM mcp_tokens t JOIN users u ON u.id = t.user_id `+filtro+`
		  GROUP BY u.id, u.email`, args...)
	sair(err)
	type alvo struct {
		id    uuid.UUID
		email string
		n     int
	}
	var alvos []alvo
	for rows.Next() {
		var a alvo
		sair(rows.Scan(&a.id, &a.email, &a.n))
		alvos = append(alvos, a)
	}
	sair(rows.Err())

	st := tokens.NovoStore(pool, nil)
	revogados := 0
	pessoas := []string{}
	for _, a := range alvos {
		pessoas = append(pessoas, a.email)
		if !*aplicar {
			revogados += a.n
			continue
		}
		n, err := st.RevogarTodos(ctx, a.id)
		sair(err)
		revogados += int(n)
		sair(auditoria.Registrar(ctx, pool, a.id, "mcp.token.revogado", "mcp_token", nil,
			map[string]any{"pelo": "revoga-mcp", "quantidade": n}))
	}
	sair(out.Encode(map[string]any{"aplicado": *aplicar, "pessoas": pessoas, "credenciais": revogados}))
}

func sair(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
