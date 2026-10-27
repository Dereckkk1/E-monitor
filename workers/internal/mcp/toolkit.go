package mcp

import (
	"encoding/json"
	"fmt"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// resultado põe o MESMO JSON no texto e no structuredContent (contrato §3.3).
// v tem de serializar como objeto JSON.
func resultado(v any) (*sdk.CallToolResult, any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, nil, fmt.Errorf("mcp: serializar resultado: %w", err)
	}
	return &sdk.CallToolResult{
		Content:           []sdk.Content{&sdk.TextContent{Text: string(b)}},
		StructuredContent: json.RawMessage(b),
	}, nil, nil
}

// falha é a falha de negócio: isError com "<mensagem> [<código>]" (contrato §3.4).
func falha(mensagem, codigo string) (*sdk.CallToolResult, any, error) {
	texto := mensagem
	if codigo != "" {
		texto = fmt.Sprintf("%s [%s]", mensagem, codigo)
	}
	return &sdk.CallToolResult{
		IsError: true,
		Content: []sdk.Content{&sdk.TextContent{Text: texto}},
	}, nil, nil
}

// anotacoesLeitura: as quatro dicas explícitas — os defaults do protocolo
// (destructive=true, openWorld=true) são errados para leitura.
func anotacoesLeitura() *sdk.ToolAnnotations {
	nao := false
	return &sdk.ToolAnnotations{
		ReadOnlyHint:    true,
		IdempotentHint:  true,
		DestructiveHint: &nao,
		OpenWorldHint:   &nao,
	}
}
