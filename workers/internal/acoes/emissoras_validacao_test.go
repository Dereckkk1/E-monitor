package acoes

// Validação pura do cadastro da emissora (sem banco): a frequência confere com a
// faixa da banda, a UF é uma das 27, e nome/cidade têm teto.

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidarCadastroEmissora_FrequenciaConfereComABanda(t *testing.T) {
	casos := []struct {
		banda string
		freq  float64
		ok    bool
	}{
		{"FM", 76, true}, {"FM", 99.9, true}, {"FM", 108, true},
		{"FM", 75.9, false}, {"FM", 108.1, false}, {"FM", 1180, false}, {"FM", 20, false},
		{"AM", 530, true}, {"AM", 1180, true}, {"AM", 1700, true},
		{"AM", 529, false}, {"AM", 1701, false}, {"AM", 99.9, false},
		{"", 50, true}, // sem banda só vale o limite do NUMERIC (comportamento anterior)
		{"", 5000, true},
	}
	for _, c := range casos {
		_, err := ValidarCadastroEmissora(CadastroEmissora{FrequenciaMHz: fl(c.freq), Banda: c.banda})
		if c.ok {
			require.NoError(t, err, "%s %v", c.banda, c.freq)
			continue
		}
		r := recusaDe(t, err)
		require.Equal(t, CodigoEntradaInvalida, r.Codigo, "%s %v", c.banda, c.freq)
		require.Contains(t, r.Mensagem, c.banda, "a mensagem diz de que banda é a faixa")
	}

	// A mensagem diz a unidade da banda (MHz para FM, kHz para AM).
	_, err := ValidarCadastroEmissora(CadastroEmissora{FrequenciaMHz: fl(10), Banda: "FM"})
	require.Contains(t, recusaDe(t, err).Mensagem, "MHz")
	_, err = ValidarCadastroEmissora(CadastroEmissora{FrequenciaMHz: fl(10), Banda: "AM"})
	require.Contains(t, recusaDe(t, err).Mensagem, "kHz")
}

func TestValidarCadastroEmissora_UFEhUmaDasVinteESete(t *testing.T) {
	for _, uf := range []string{"XX", "ZZ", "AA", "M", "MGX", "  "} {
		_, err := ValidarCadastroEmissora(CadastroEmissora{UF: sp(uf)})
		require.Equal(t, CodigoEntradaInvalida, recusaDe(t, err).Codigo, "UF %q", uf)
	}
	for _, uf := range []string{"MG", " mg ", "sp", "DF", "TO", "AC"} {
		up, err := ValidarCadastroEmissora(CadastroEmissora{UF: sp(uf)})
		require.NoError(t, err, "UF %q", uf)
		require.Equal(t, strings.ToUpper(strings.TrimSpace(uf)), *up.State)
	}
	require.True(t, UFValida("RJ"))
	require.False(t, UFValida("rj"), "UFValida espera a sigla já em maiúsculas")
	require.False(t, UFValida("XX"))
}

func TestValidarCadastroEmissora_NomeECidadeTemTeto(t *testing.T) {
	_, err := ValidarCadastroEmissora(CadastroEmissora{Nome: sp(strings.Repeat("á", 200)), Cidade: sp(strings.Repeat("ã", 200))})
	require.NoError(t, err, "200 runas (não bytes) passam")

	_, err = ValidarCadastroEmissora(CadastroEmissora{Nome: sp(strings.Repeat("á", 201))})
	r := recusaDe(t, err)
	require.Equal(t, CodigoEntradaInvalida, r.Codigo)
	require.Contains(t, r.Mensagem, "nome")
	_, err = ValidarCadastroEmissora(CadastroEmissora{Cidade: sp(strings.Repeat("ã", 201))})
	r = recusaDe(t, err)
	require.Equal(t, CodigoEntradaInvalida, r.Codigo)
	require.Contains(t, r.Mensagem, "cidade")
}
