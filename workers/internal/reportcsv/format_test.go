package reportcsv

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func ptrS(s string) *string   { return &s }
func ptrF(f float64) *float64 { return &f }

func TestFormatRadio(t *testing.T) {
	require.Equal(t, "Massa - FM (106.9)", formatRadio("Massa", ptrS("FM"), ptrF(106.9)))
	require.Equal(t, "Massa - FM", formatRadio("Massa", ptrS("FM"), nil))
	require.Equal(t, "Massa (106.9)", formatRadio("Massa", nil, ptrF(106.9)))
	require.Equal(t, "Massa", formatRadio("Massa", nil, nil))
	// Banda vazia é o mesmo que banda ausente — nunca pode sobrar " - " solto.
	require.Equal(t, "Massa", formatRadio("Massa", ptrS(""), nil))
	require.Equal(t, "Massa", formatRadio("  Massa  ", ptrS("   "), nil))
	// Frequência é rótulo de dial: ponto decimal, 1 casa, como no arquivo do
	// fornecedor — e não a vírgula que o resto do CSV usa pra número somável.
	require.Equal(t, "Studio - FM (99.0)", formatRadio("Studio", ptrS("FM"), ptrF(99)))
}

func TestFormatCityUF(t *testing.T) {
	require.Equal(t, "Joinville / SC", formatCityUF(ptrS("Joinville"), ptrS("SC")))
	require.Equal(t, "Joinville", formatCityUF(ptrS("Joinville"), nil))
	require.Equal(t, "SC", formatCityUF(nil, ptrS("SC")))
	require.Equal(t, "", formatCityUF(nil, nil))
	// Separador nunca sai solto quando um dos lados é string vazia.
	require.Equal(t, "Joinville", formatCityUF(ptrS("Joinville"), ptrS("")))
	require.Equal(t, "", formatCityUF(ptrS(""), ptrS("")))
}

func TestFormatBRL(t *testing.T) {
	require.Equal(t, "R$ 0,00", formatBRL(0))
	require.Equal(t, "R$ 6,00", formatBRL(6))
	require.Equal(t, "R$ 53,03", formatBRL(53.03))
	require.Equal(t, "R$ 1.234,50", formatBRL(1234.5))
	require.Equal(t, "R$ 1.234.567,89", formatBRL(1234567.89))
	require.Equal(t, "R$ 999,00", formatBRL(999))
}

func TestSanitizeFilename(t *testing.T) {
	require.Equal(t, "Rogga", SanitizeFilename("Rogga"))
	require.Equal(t, "Acai-Ltda", SanitizeFilename("Açaí Ltda"))
	require.Equal(t, "A-B", SanitizeFilename("A / B"))
	// O ponto sobrevive (está na allowlist — extensões precisam dele); as aspas
	// viram hífen, hífens repetidos colapsam e a ponta é aparada.
	require.Equal(t, "Cliente-S.A", SanitizeFilename(`Cliente "S.A"`))
	require.Equal(t, "", SanitizeFilename(""))
	require.Equal(t, "", SanitizeFilename("   "))
	// Truncado em 60 e sem hífen sobrando na ponta.
	require.LessOrEqual(t, len(SanitizeFilename(repeatA(80))), 60)
}

// repeatA devolve uma string de n caracteres 'a' — só pro teste de truncagem.
func repeatA(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'a'
	}
	return string(b)
}
