// Command cli roda a avaliação do MCP em desenvolvimento. NÃO vai para a
// imagem de prod (não é um cmd/*, e fica fora do workers.Dockerfile de
// propósito — decisão F2-9 do plano da Fatia 2):
//
//	DATABASE_URL=… go run ./internal/mcp/avaliacao/cli semear
//	DATABASE_URL=… go run ./internal/mcp/avaliacao/cli tools
//	DATABASE_URL=… go run ./internal/mcp/avaliacao/cli chamar <tool> '<json>'
//
// tools e chamar falam com o servidor MCP real (as mesmas tools do /mcp),
// em memória, com o admin da semente como principal.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"radiocheck/internal/mcp"
	"radiocheck/internal/mcp/avaliacao"
)

const uso = "uso: cli semear | tools | chamar <tool> '<json>'  (com DATABASE_URL)"

func main() {
	if err := rodar(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func rodar(args []string) error {
	if len(args) == 0 {
		return errors.New(uso)
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return errors.New("DATABASE_URL vazio — " + uso)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return err
	}
	defer pool.Close()
	if args[0] == "semear" {
		_, err := avaliacao.Semear(ctx, pool, time.Now())
		return err
	}
	var p mcp.Principal
	if err := pool.QueryRow(ctx, `SELECT id, email, name FROM users WHERE email = $1`, avaliacao.EmailAdmin).
		Scan(&p.UserID, &p.Email, &p.Nome); err != nil {
		return fmt.Errorf("rode 'semear' antes: %w", err)
	}
	srv := mcp.NovoServidorLocal(mcp.ConfigServidor{Versao: "avaliacao", Portal: "https://e-monitor.online", Leitura: mcp.NovaLeitura(pool)}, &p)
	ct, st := sdk.NewInMemoryTransports()
	if _, err := srv.Connect(ctx, st, nil); err != nil {
		return err
	}
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "avaliacao", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		return err
	}
	defer cs.Close()
	switch args[0] {
	case "tools":
		res, err := cs.ListTools(ctx, nil)
		if err != nil {
			return err
		}
		for _, t := range res.Tools {
			esquema, err := json.Marshal(t.InputSchema)
			if err != nil {
				return err
			}
			fmt.Printf("## %s\n%s\nentrada: %s\n\n", t.Name, t.Description, esquema)
		}
		// As instruções do servidor (o que um cliente MCP injeta no contexto do
		// modelo) vão no fim, para a lista começar pela primeira tool.
		if ins := cs.InitializeResult(); ins != nil && ins.Instructions != "" {
			fmt.Printf("# Instruções do servidor\n%s\n", ins.Instructions)
		}
		return nil
	case "chamar":
		if len(args) < 2 {
			return errors.New("uso: cli chamar <tool> '<json>'")
		}
		argumentos := map[string]any{}
		if len(args) > 2 {
			if err := json.Unmarshal([]byte(args[2]), &argumentos); err != nil {
				return fmt.Errorf("argumentos não são um objeto JSON: %w", err)
			}
		}
		res, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: args[1], Arguments: argumentos})
		if err != nil {
			return err
		}
		if res.IsError {
			fmt.Print("ERRO: ")
		}
		for _, c := range res.Content {
			if tc, ok := c.(*sdk.TextContent); ok {
				fmt.Println(tc.Text)
			}
		}
		return nil
	}
	return fmt.Errorf("comando desconhecido %q — %s", args[0], uso)
}
