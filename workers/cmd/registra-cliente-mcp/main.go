// registra-cliente-mcp cadastra um cliente OAuth CONFIDENCIAL do MCP do
// E-monitor — o Gemini Enterprise, que não faz PKCE e se autentica no /token
// com client_secret. O registro dinâmico aberto (/mcp/oauth/register) só
// aceita cliente público; o confidencial passa por aqui, pelo administrador.
// Uso em prod (regra 4.2):
//
//	docker compose ... exec api registra-cliente-mcp --dsn "$DATABASE_URL" \
//	    --nome "Gemini Enterprise" \
//	    --redirect https://vertexaisearch.cloud.google.com/oauth-redirect
//
// --redirect pode ser repetida ou separada por vírgula. O hash do segredo é
// salgado com o JWT_SECRET do ambiente — o mesmo da API, senão o /token não
// reconhece o segredo. Imprime UMA linha JSON com o client_secret em claro: a
// única vez em que ele existe. Um cliente que não chegar ao /token em 7 dias
// é apagado pela faxina do registro.
//
// Saída: 0 ok; 2 uso errado (flag faltando, JWT_SECRET curto, redirect
// inválido); 1 erro de banco.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"go.uber.org/zap"

	"radiocheck/internal/db"
	"radiocheck/internal/mcp/auditoria"
	"radiocheck/internal/mcp/oauth"
)

// listaRedirect aceita --redirect repetida e/ou separada por vírgula.
type listaRedirect []string

func (l *listaRedirect) String() string { return strings.Join(*l, ",") }

func (l *listaRedirect) Set(v string) error {
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			*l = append(*l, p)
		}
	}
	return nil
}

type saida struct {
	ClientID     string   `json:"client_id"`
	ClientSecret string   `json:"client_secret"`
	Nome         string   `json:"nome"`
	RedirectURIs []string `json:"redirect_uris"`
	Aviso        string   `json:"aviso"`
}

func main() {
	dsn := flag.String("dsn", os.Getenv("DATABASE_URL"), "postgres connection string")
	nome := flag.String("nome", "", "nome do cliente, como aparece na tela de consentimento (obrigatório)")
	var redirects listaRedirect
	flag.Var(&redirects, "redirect", "redirect_uri do cliente; repita a flag ou separe por vírgula (obrigatório)")
	flag.Parse()

	if strings.TrimSpace(*nome) == "" || len(redirects) == 0 {
		uso("informe --nome e ao menos um --redirect")
	}
	if *dsn == "" {
		uso("informe --dsn ou DATABASE_URL")
	}
	segredo := []byte(os.Getenv("JWT_SECRET"))
	if len(segredo) < 32 {
		uso("JWT_SECRET ausente ou com menos de 32 bytes (precisa ser o mesmo da API)")
	}

	ctx := context.Background()
	pool, err := db.New(ctx, *dsn, zap.NewNop())
	if err != nil {
		falhar("conectar", err)
	}
	defer pool.Close()

	// Cliente e auditoria juntos: ou os dois ficam, ou nenhum.
	tx, err := pool.Begin(ctx)
	if err != nil {
		falhar("abrir transação", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	cli, claro, err := oauth.NovosClientes(tx, segredo).RegistrarConfidencial(ctx, *nome, redirects)
	if errors.Is(err, oauth.ErrRedirectInvalido) {
		uso(err.Error())
	}
	if err != nil {
		falhar("registrar cliente", err)
	}
	// Quem age é o operador na VM, sem usuário do portal: ator NULL, tipo
	// 'cli', e o "pelo" diz de onde veio.
	if err := auditoria.RegistrarSistema(ctx, tx, "registra-cliente-mcp", "mcp.oauth.cliente_confidencial_criado", "mcp_oauth_client", nil,
		map[string]any{"client_id": cli.ClientID, "cliente": cli.Nome}); err != nil {
		falhar("auditoria", err)
	}
	if err := tx.Commit(ctx); err != nil {
		falhar("commit", err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(saida{
		ClientID:     cli.ClientID,
		ClientSecret: claro,
		Nome:         cli.Nome,
		RedirectURIs: cli.RedirectURIs,
		Aviso:        "guarde o client_secret agora: ele não será mostrado de novo",
	}); err != nil {
		falhar("escrever saída", err)
	}
}

func uso(msg string) {
	fmt.Fprintln(os.Stderr, "registra-cliente-mcp:", msg)
	flag.Usage()
	os.Exit(2)
}

func falhar(etapa string, err error) {
	fmt.Fprintf(os.Stderr, "registra-cliente-mcp: %s: %v\n", etapa, err)
	os.Exit(1)
}
