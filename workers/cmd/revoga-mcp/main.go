// revoga-mcp revoga credenciais do MCP do E-monitor em massa (spec
// 2026-10-27 §4.1). Sem --aplicar é dry-run. Uso em prod (regra 4.2):
//
//	docker compose ... exec api revoga-mcp --dsn "$DATABASE_URL" --listar
//	docker compose ... exec api revoga-mcp --dsn "$DATABASE_URL" --email pessoa@x --aplicar
//	docker compose ... exec api revoga-mcp --dsn "$DATABASE_URL" --todos --aplicar
//
// Imprime UMA linha JSON por execução, nunca um hash nem um token.
//
// Contagem: "credenciais" é o número de credenciais VIVAS (não revogadas e não
// vencidas) de TODOS os tipos — pessoal, access e refresh do OAuth —, e o
// dry-run conta exatamente o que o --aplicar revoga. Por incluir os access
// tokens do OAuth, o número é maior que o de linhas do --listar, que mostra
// uma linha por token pessoal e por conexão OAuth. "pessoas" são as que têm
// ao menos uma credencial viva ou um código de autorização pendente;
// "codigos_apagados" conta esses códigos (--todos apaga todos).
//
// Auditoria: uma linha por pessoa com credencial revogada, sem ator (é o
// operador na VM): actor_type 'cli', metadata.pelo = "revoga-mcp".
//
// Saída: 0 ok (inclusive --email sem ninguém, com aviso no stderr); 2 uso
// errado; 1 erro de banco.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"radiocheck/internal/db"
	"radiocheck/internal/mcp/auditoria"
	"radiocheck/internal/mcp/tokens"
)

type opcoes struct {
	listar  bool
	email   string
	todos   bool
	aplicar bool
}

func validar(o opcoes) error {
	modos := 0
	for _, b := range []bool{o.listar, o.email != "", o.todos} {
		if b {
			modos++
		}
	}
	if modos != 1 {
		return errors.New("use exatamente um de --listar, --email X, --todos")
	}
	if o.listar && o.aplicar {
		return errors.New("--listar só lê: não combina com --aplicar")
	}
	return nil
}

func main() {
	dsn := flag.String("dsn", os.Getenv("DATABASE_URL"), "postgres connection string")
	var o opcoes
	flag.BoolVar(&o.listar, "listar", false, "lista as credenciais vivas")
	flag.StringVar(&o.email, "email", "", "revoga todas as credenciais desta pessoa")
	flag.BoolVar(&o.todos, "todos", false, "revoga todas as credenciais de todo mundo")
	flag.BoolVar(&o.aplicar, "aplicar", false, "sem isto é dry-run")
	flag.Parse()
	o.email = strings.TrimSpace(o.email)

	if err := validar(o); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	ctx := context.Background()
	pool, err := db.New(ctx, *dsn, zap.NewNop())
	if err != nil {
		fmt.Fprintln(os.Stderr, "conectar:", err)
		os.Exit(1)
	}
	defer pool.Close()
	if err := executar(ctx, pool, o, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type credencial struct {
	Email     string     `json:"email"`
	Tipo      string     `json:"tipo"`
	Nome      string     `json:"nome"`
	CriadoEm  time.Time  `json:"criado_em"`
	UltimoUso *time.Time `json:"ultimo_uso"`
	ExpiraEm  time.Time  `json:"expira_em"`
}

func executar(ctx context.Context, pool *pgxpool.Pool, o opcoes, saida, avisos io.Writer) error {
	o.email = strings.TrimSpace(o.email)
	out := json.NewEncoder(saida)

	if o.listar {
		rows, err := pool.Query(ctx,
			`SELECT u.email, t.tipo, t.nome, t.created_at, t.last_used_at, t.expires_at
			   FROM mcp_tokens t JOIN users u ON u.id = t.user_id
			  WHERE t.revoked_at IS NULL AND t.expires_at > NOW() AND t.tipo IN ('pessoal', 'oauth_refresh')
			  ORDER BY u.email, t.created_at`)
		if err != nil {
			return err
		}
		defer rows.Close()
		lista := []credencial{}
		for rows.Next() {
			var c credencial
			if err := rows.Scan(&c.Email, &c.Tipo, &c.Nome, &c.CriadoEm, &c.UltimoUso, &c.ExpiraEm); err != nil {
				return err
			}
			lista = append(lista, c)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		return out.Encode(map[string]any{"credenciais": lista})
	}

	// Alvos: quem tem credencial viva (o que RevogarTodos conta) ou código
	// pendente (o que RevogarTodos apaga). Fora do --todos o filtro por email
	// vale SEMPRE: um email vazio não pode virar "todo mundo".
	filtro, args := ``, []any{}
	if !o.todos {
		filtro = ` AND LOWER(u.email) = LOWER($1)`
		args = append(args, o.email)
	}
	rows, err := pool.Query(ctx,
		`WITH vivas AS (
		   SELECT user_id, COUNT(*) AS n FROM mcp_tokens
		    WHERE revoked_at IS NULL AND expires_at > NOW() GROUP BY user_id
		 ), codigos AS (
		   SELECT user_id, COUNT(*) AS n FROM mcp_oauth_codes GROUP BY user_id
		 )
		 SELECT u.id, u.email, COALESCE(v.n, 0), COALESCE(c.n, 0)
		   FROM users u
		   LEFT JOIN vivas v ON v.user_id = u.id
		   LEFT JOIN codigos c ON c.user_id = u.id
		  WHERE (v.n > 0 OR c.n > 0)`+filtro+`
		  ORDER BY u.email`, args...)
	if err != nil {
		return err
	}
	type alvo struct {
		id         uuid.UUID
		email      string
		vivas, cod int
	}
	var alvos []alvo
	for rows.Next() {
		var a alvo
		if err := rows.Scan(&a.id, &a.email, &a.vivas, &a.cod); err != nil {
			rows.Close()
			return err
		}
		alvos = append(alvos, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	if !o.todos && len(alvos) == 0 {
		var existe bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE LOWER(email) = LOWER($1))`, o.email).Scan(&existe); err != nil {
			return err
		}
		if !existe {
			fmt.Fprintf(avisos, "revoga-mcp: aviso: nenhum usuário com o email %q\n", o.email)
		}
	}

	revogadas, codigos := 0, 0
	pessoas := []string{}
	for _, a := range alvos {
		pessoas = append(pessoas, a.email)
		codigos += a.cod
	}
	if !o.aplicar {
		for _, a := range alvos {
			revogadas += a.vivas
		}
		return out.Encode(map[string]any{"aplicado": false, "pessoas": pessoas, "credenciais": revogadas, "codigos_apagados": codigos})
	}

	if o.todos {
		// Todos os códigos pendentes, de uma vez; RevogarTodos abaixo apagaria
		// os mesmos pessoa a pessoa, então a contagem fica aqui.
		tag, err := pool.Exec(ctx, `DELETE FROM mcp_oauth_codes`)
		if err != nil {
			return err
		}
		codigos = int(tag.RowsAffected())
	}
	st := tokens.NovoStore(pool, nil)
	for _, a := range alvos {
		n, err := st.RevogarTodos(ctx, a.id)
		if err != nil {
			return err
		}
		revogadas += int(n)
		if n == 0 {
			continue
		}
		if err := auditoria.RegistrarSistema(ctx, pool, "revoga-mcp", "mcp.token.revogado", "mcp_token", nil,
			map[string]any{"alvo_user_id": a.id, "alvo_email": a.email, "quantidade": n}); err != nil {
			return err
		}
	}
	return out.Encode(map[string]any{"aplicado": true, "pessoas": pessoas, "credenciais": revogadas, "codigos_apagados": codigos})
}
