package mcp

import (
	"encoding/json"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIndexarEscritas_Trinta(t *testing.T) {
	m, err := indexarEscritas(escritasDe(ConfigServidor{}))
	require.NoError(t, err)
	require.Len(t, m, 30)
}

func TestIndexarEscritas_NomeRepetidoFalha(t *testing.T) {
	e := escritaCriarTipoMaterial(ConfigServidor{})
	_, err := indexarEscritas([]escritaRegistravel{e, e})
	require.Error(t, err)
	require.Contains(t, err.Error(), "criar_tipo_material")
}

func TestEscritasDe_NomesIguaisAoInventario(t *testing.T) {
	var nomes, esperados []string
	for _, e := range escritasDe(ConfigServidor{}) {
		nomes = append(nomes, e.nomeEscrita())
	}
	for n := range escritasEsperadas {
		esperados = append(esperados, n)
	}
	sort.Strings(nomes)
	sort.Strings(esperados)
	require.Equal(t, esperados, nomes)
}

func TestDecodificarArgs_TipoMaterial(t *testing.T) {
	in, err := decodificarArgs[entradaCriarTipoMaterial](json.RawMessage(`{"nome":"Vinheta 5s","cor":"#10b981"}`))
	require.NoError(t, err)
	require.Equal(t, "Vinheta 5s", in.Nome)
	require.Equal(t, "#10b981", in.Cor)

	_, err = decodificarArgs[entradaCriarTipoMaterial](json.RawMessage(`{"nome":"x","inventado":1}`))
	require.Error(t, err, "campo desconhecido")
	_, err = decodificarArgs[entradaCriarTipoMaterial](json.RawMessage(`{"nome":1}`))
	require.Error(t, err, "tipo errado")
	_, err = decodificarArgs[entradaCriarTipoMaterial](json.RawMessage(`{"cor":"#10b981"}`))
	require.Error(t, err, "campo obrigatório ausente")
}

// O que o SDK aceita numa chamada real a decodificador aceita, e o que ele
// recusa o decodificador recusa.
func TestDecodificarArgs_ConcordaComOSDK(t *testing.T) {
	b := novaBancadaTool(t)
	validos := []map[string]any{
		{"nome": "Tipo T3 A"},
		{"nome": "Tipo T3 B", "cor": "#10b981", "descricao": "d"},
	}
	for _, args := range validos {
		b.chamar(t, "criar_tipo_material", args) // preview: o SDK aceitou
		raw, _ := json.Marshal(args)
		_, err := decodificarArgs[entradaCriarTipoMaterial](raw)
		require.NoError(t, err, string(raw))
	}
	invalidos := []map[string]any{
		{"nome": "x", "inventado": 1},
		{"nome": 1},
		{"cor": "#10b981"},
	}
	for _, args := range invalidos {
		b.chamarErro(t, "criar_tipo_material", args)
		raw, _ := json.Marshal(args)
		_, err := decodificarArgs[entradaCriarTipoMaterial](raw)
		require.Error(t, err, string(raw))
	}
}
