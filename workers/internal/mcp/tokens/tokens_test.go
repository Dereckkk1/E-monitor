package tokens

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGerar_PrefixoE64Hex(t *testing.T) {
	tok := Gerar(PrefixoAcesso)
	require.True(t, strings.HasPrefix(tok, "emonitor_mcp_"))
	corpo := strings.TrimPrefix(tok, PrefixoAcesso)
	require.Len(t, corpo, 64)
	_, err := hex.DecodeString(corpo)
	require.NoError(t, err)
	require.NotEqual(t, tok, Gerar(PrefixoAcesso))
}

func TestHash_SegueOContrato(t *testing.T) {
	// Contrato do ecossistema §3.1: SHA-256(JWT_SECRET:token), em hex.
	soma := sha256.Sum256([]byte("segredo:emonitor_mcp_abc"))
	require.Equal(t, hex.EncodeToString(soma[:]), Hash([]byte("segredo"), "emonitor_mcp_abc"))
	require.NotEqual(t, Hash([]byte("outro"), "emonitor_mcp_abc"), Hash([]byte("segredo"), "emonitor_mcp_abc"))
}
