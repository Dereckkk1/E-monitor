// Package tokens emite, guarda e revoga as credenciais do MCP (spec
// 2026-10-27 §4.1). Só o hash vai ao banco — SHA-256(JWT_SECRET:token),
// igual à Plura e ao hub (contrato do ecossistema §3.1).
package tokens

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
)

const (
	PrefixoAcesso  = "emonitor_mcp_" // token pessoal e access token OAuth
	PrefixoRefresh = "emonitor_mcr_" // refresh OAuth: nunca abre o /mcp
	PrefixoSegredo = "emonitor_mcs_" // client_secret de cliente confidencial (Gemini)
	PrefixoCodigo  = "emonitor_mcc_" // código de autorização OAuth
)

// Gerar devolve prefixo + 64 hex (32 bytes de crypto/rand).
func Gerar(prefixo string) string {
	b := make([]byte, 32)
	// Desde o Go 1.24 crypto/rand.Read não devolve erro (aborta o processo se
	// o SO não tiver entropia), então não há o que tratar aqui.
	_, _ = rand.Read(b)
	return prefixo + hex.EncodeToString(b)
}

// Hash é a única forma em que uma credencial do MCP toca o banco.
func Hash(segredo []byte, token string) string {
	h := sha256.New()
	h.Write(segredo)
	h.Write([]byte{':'})
	h.Write([]byte(token))
	return hex.EncodeToString(h.Sum(nil))
}
