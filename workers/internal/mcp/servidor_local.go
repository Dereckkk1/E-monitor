//go:build avaliacao

package mcp

import sdk "github.com/modelcontextprotocol/go-sdk/mcp"

// NovoServidorLocal expõe o servidor (sem HTTP nem autenticação) ao CLI de
// avaliação (internal/mcp/avaliacao/cli), que roda em desenvolvimento contra
// um banco semeado. Só existe com a tag de build "avaliacao": o binário de
// prod (cmd/api, sem a tag) não o inclui, e ele nunca é montado numa rota.
func NovoServidorLocal(cfg ConfigServidor, p *Principal) *sdk.Server { return novoServidor(cfg, p) }
