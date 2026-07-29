package postsale

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPeriodLabel(t *testing.T) {
	// Intervalo inclusivo nas duas pontas: 01/06 a 31/07 são 61 dias, não 60.
	require.Equal(t, "01/06/2026 a 31/07/2026 · 61 dias",
		periodLabel(date(2026, 6, 1), date(2026, 7, 31)))

	// Um dia só: "1 dias" seria feio e "61 dias" seria mentira.
	d := date(2026, 6, 10)
	require.Equal(t, "10/06/2026 · 1 dia", periodLabel(d, d))
}

func TestMonthsLabel(t *testing.T) {
	require.Equal(t, "Junho e Julho de 2026", monthsLabel(date(2026, 6, 1), date(2026, 7, 31)))
	require.Equal(t, "Junho de 2026", monthsLabel(date(2026, 6, 1), date(2026, 6, 30)))
	require.Equal(t, "Maio, Junho e Julho de 2026", monthsLabel(date(2026, 5, 1), date(2026, 7, 31)))
	// Cruzando o ano, cada mês carrega o próprio ano.
	require.Equal(t, "Dezembro de 2026 e Janeiro de 2027",
		monthsLabel(date(2026, 12, 1), date(2027, 1, 31)))
}

func TestConformingCount_ContaAsRemovidas(t *testing.T) {
	all := []StationRow{
		{Name: "A", Kind: KindAbove},
		{Name: "B", Kind: KindCompensation},
		{Name: "C", Kind: KindConforming},
		{Name: "D", Kind: KindConforming},
	}
	// Nada removido: as 2 conformes.
	require.Equal(t, 2, conformingCount(len(all), []StationRow{all[0], all[1]}))
	// Admin removeu a "A" das listas → ela vira conforme. O total continua 4.
	require.Equal(t, 3, conformingCount(len(all), []StationRow{all[1]}))
	// Nunca negativo, mesmo se a lista editada trouxer linha a mais.
	require.Equal(t, 0, conformingCount(1, all))
}
