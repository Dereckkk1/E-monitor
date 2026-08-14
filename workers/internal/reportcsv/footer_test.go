package reportcsv

import (
	"bytes"
	"encoding/csv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// renderFooter roda o acumulador através de um csv.Writer e devolve as linhas
// já divididas, pra o teste asseverar linha a linha.
func renderFooter(t *testing.T, tot *detailedTotals) []string {
	t.Helper()
	var buf bytes.Buffer
	cw := csv.NewWriter(&buf)
	cw.Comma = ';'
	require.NoError(t, tot.write(cw))
	cw.Flush()
	require.NoError(t, cw.Error())
	return strings.Split(strings.ReplaceAll(buf.String(), "\r\n", "\n"), "\n")
}

func TestDetailedTotals_ContaEmissorasDistintasNaoVeiculacoes(t *testing.T) {
	tot := newDetailedTotals()
	// 3 veiculações, 2 emissoras, ambas em SC.
	tot.add("id:1", "SC", "JINGLE A")
	tot.add("id:1", "SC", "JINGLE A")
	tot.add("id:2", "SC", "JINGLE B")

	lines := renderFooter(t, tot)
	require.Contains(t, lines, "TOTAL DE RADIOS MONITORADAS;2")
	require.Contains(t, lines, "TOTAL DE RÁDIOS POR ESTADO COM VEICULAÇÕES;2")
	require.Contains(t, lines, "TOTAL DE VEICULAÇÕES;3")
	// O bloco por UF conta EMISSORAS distintas, não veiculações.
	require.Contains(t, lines, "SC;2")
}

func TestDetailedTotals_ResumoPorComercialOrdenadoDescComDesempate(t *testing.T) {
	tot := newDetailedTotals()
	tot.add("id:1", "SC", "POPULAR")
	tot.add("id:1", "SC", "POPULAR")
	tot.add("id:1", "SC", "POPULAR")
	tot.add("id:1", "SC", "ZEBRA")
	tot.add("id:1", "SC", "ALFA")

	lines := renderFooter(t, tot)
	start := indexOf(lines, "Comercial;Total")
	require.GreaterOrEqual(t, start, 0, "faltou o cabeçalho do resumo por comercial")
	// Maior total primeiro; empate desempata por título asc (ALFA antes de ZEBRA).
	require.Equal(t, "POPULAR;3", lines[start+1])
	require.Equal(t, "ALFA;1", lines[start+2])
	require.Equal(t, "ZEBRA;1", lines[start+3])
}

func TestDetailedTotals_UFsOrdenadasEVaziaPorUltimo(t *testing.T) {
	tot := newDetailedTotals()
	tot.add("id:1", "SC", "A")
	tot.add("id:2", "RS", "A")
	tot.add("id:3", "", "A") // emissora sem estado cadastrado

	lines := renderFooter(t, tot)
	start := indexOf(lines, "UF;TOTAL")
	require.GreaterOrEqual(t, start, 0)
	require.Equal(t, "RS;1", lines[start+1])
	require.Equal(t, "SC;1", lines[start+2])
	require.Equal(t, ";1", lines[start+3], "UF vazia entra no resumo, por último")
	// A emissora sem UF não pode sumir do total geral.
	require.Contains(t, lines, "TOTAL DE RADIOS MONITORADAS;3")
}

func TestDetailedTotals_Vazio(t *testing.T) {
	lines := renderFooter(t, newDetailedTotals())
	require.Contains(t, lines, "TOTAL DE VEICULAÇÕES;0")
	require.Contains(t, lines, "TOTAL DE RADIOS MONITORADAS;0")
	require.Contains(t, lines, "UF;TOTAL")
	require.Contains(t, lines, "Comercial;Total")
}

func indexOf(lines []string, want string) int {
	for i, l := range lines {
		if l == want {
			return i
		}
	}
	return -1
}
