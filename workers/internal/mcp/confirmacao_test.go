package mcp

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestArgsCanonicos_OrdemDeChaveNaoImportaEConfirmacaoSai(t *testing.T) {
	type in struct {
		B           int    `json:"b"`
		A           string `json:"a"`
		Confirmacao string `json:"confirmacao,omitempty"`
	}
	x, err := argsCanonicos(in{B: 1, A: "z", Confirmacao: "tok"})
	require.NoError(t, err)
	require.Equal(t, `{"a":"z","b":1}`, string(x))
	y, _ := argsCanonicos(map[string]any{"b": 1, "a": "z"})
	require.Equal(t, string(x), string(y))
}

func TestConfirmacao_VerificaSoComMesmosArgsUsuarioEPrazo(t *testing.T) {
	seg := bytes.Repeat([]byte("s"), 32)
	u := uuid.New()
	agora := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	tok := assinarConfirmacao(seg, "editar_material", []byte(`{"a":1}`), u, agora.Add(ttlConfirmacao))
	require.Regexp(t, `^[1-9][0-9]{0,15}\.[A-Za-z0-9_-]{43}$`, tok)
	require.True(t, verificarConfirmacao(seg, tok, "editar_material", []byte(`{"a":1}`), u, agora))
	require.False(t, verificarConfirmacao(seg, tok, "editar_material", []byte(`{"a":2}`), u, agora), "args mudados")
	require.False(t, verificarConfirmacao(seg, tok, "editar_cliente", []byte(`{"a":1}`), u, agora), "outra tool")
	require.False(t, verificarConfirmacao(seg, tok, "editar_material", []byte(`{"a":1}`), uuid.New(), agora), "outro usuário")
	require.False(t, verificarConfirmacao(seg, tok, "editar_material", []byte(`{"a":1}`), u, agora.Add(11*time.Minute)), "vencido")
	require.False(t, verificarConfirmacao(bytes.Repeat([]byte("x"), 32), tok, "editar_material", []byte(`{"a":1}`), u, agora), "outro segredo")
	for _, ruim := range []string{"", "x", "0" + tok, "+" + tok, " " + tok, tok + "=", strings.Replace(tok, ".", "", 1)} {
		require.False(t, verificarConfirmacao(seg, ruim, "editar_material", []byte(`{"a":1}`), u, agora), ruim)
	}
	longe := assinarConfirmacao(seg, "editar_material", []byte(`{"a":1}`), u, agora.Add(time.Hour))
	require.False(t, verificarConfirmacao(seg, longe, "editar_material", []byte(`{"a":1}`), u, agora), "exp além do TTL+folga")
}
