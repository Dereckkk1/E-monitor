package mcp

import (
	"context"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type usuarioResumo struct {
	ID    string `json:"id"`
	Nome  string `json:"nome"`
	Email string `json:"email"`
}

type saidaQuemSouEu struct {
	Usuario usuarioResumo `json:"usuario"`
	Papel   string        `json:"papel"`
	Sistema string        `json:"sistema"`
}

func registrarSistema(s *sdk.Server, p *Principal) {
	sdk.AddTool(s, &sdk.Tool{
		Name: "quem_sou_eu",
		Description: "Devolve o usuário dono da credencial com que o MCP do E-monitor está a correr: id, nome, e-mail e papel. " +
			"Use para confirmar que a conexão funciona e com que identidade as outras tools vão agir. Não recebe argumentos.",
		Annotations: anotacoesLeitura(),
	}, func(_ context.Context, _ *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, any, error) {
		if p == nil {
			return falha("sem identidade: o pedido não passou pela autenticação do MCP", "sem_identidade")
		}
		return resultado(saidaQuemSouEu{
			Usuario: usuarioResumo{ID: p.UserID.String(), Nome: p.Nome, Email: p.Email},
			Papel:   "admin",
			Sistema: "E-monitor",
		})
	})
}
