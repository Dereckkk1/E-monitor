package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEstado_SobreviveEntreRodadas(t *testing.T) {
	caminho := filepath.Join(t.TempDir(), "estado.json")

	e, err := carregaEstado(caminho)
	require.NoError(t, err, "arquivo inexistente é estado vazio, não erro")
	require.False(t, e.ja("u1"))

	require.NoError(t, e.marca("u1", "6650aaaaaaaaaaaaaaaaaaaa"))

	deNovo, err := carregaEstado(caminho)
	require.NoError(t, err)
	require.True(t, deNovo.ja("u1"))
	require.False(t, deNovo.ja("u2"))
}

func TestEstado_ArquivoCorrompidoEErro(t *testing.T) {
	caminho := filepath.Join(t.TempDir(), "estado.json")
	require.NoError(t, os.WriteFile(caminho, []byte("{nao é json"), 0o600))
	_, err := carregaEstado(caminho)
	require.Error(t, err, "estado ilegível não pode virar 'nada importado' e reimportar tudo")
}
