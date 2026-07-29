package reportcsv

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/catalog"
)

// bomStr é o BOM UTF-8 escrito como escape: um BOM literal no fonte Go é erro
// de compilação ("illegal byte order mark").
const bomStr = "\ufeff"

func TestWriteConsolidated_CabecalhoELinha(t *testing.T) {
	pmm := 1200.0
	target := 300
	freq := 98.5
	city, state, band := "Passo Fundo", "RS", "FM"
	typeName := "Spot"
	rows := []catalog.MaterialStationRow{{
		MaterialID: uuid.New(), MaterialTitle: "Spot 30s", MaterialTypeName: &typeName,
		StationID: uuid.New(), StationName: "Radio X",
		StationCity: &city, StationState: &state, StationBand: &band,
		StationFrequencyMHz: &freq,
		StationPMM:          &pmm, StationPMMTarget: &target,
		Count: 10, InSlotCount: 8, OutSlotCount: 1, OutDateCount: 0, OrphanCount: 1,
		FirstDetectedAt: time.Date(2026, 6, 1, 15, 0, 0, 0, time.UTC),
		LastDetectedAt:  time.Date(2026, 6, 30, 18, 0, 0, 0, time.UTC),
	}}

	var buf bytes.Buffer
	require.NoError(t, WriteConsolidated(&buf, rows, " (Mulheres 25-49)"))

	out := buf.String()
	require.True(t, strings.HasPrefix(out, bomStr), "faltou o BOM que o Excel pt-BR precisa")
	require.Contains(t, out, "PMM no target (Mulheres 25-49)")
	require.Contains(t, out, "Radio X")
	require.Contains(t, out, ";", "separador tem que ser ponto-e-vírgula")
	require.Contains(t, out, "12000", "impactos = 10 × 1200")
	require.Contains(t, out, "3000", "impactos no target = 10 × 300")
	// Decimal em pt-BR usa vírgula.
	require.Contains(t, out, "98,5")
}

// Sem rótulo de público-alvo o cabeçalho fica limpo — não pode sobrar "()".
func TestWriteConsolidated_SemTargetSuffix(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, WriteConsolidated(&buf, nil, ""))
	require.Contains(t, buf.String(), "PMM no target;")
	require.NotContains(t, buf.String(), "()")
}

func TestWriteDetailed_TraduzCategoria(t *testing.T) {
	freq := 98.5
	band := "FM"
	det := []catalog.DetectionEnriched{
		{
			Detection: catalog.Detection{
				StationName: "Radio X", CommercialName: "Spot 30s", Category: "orphan",
				DetectedAt: time.Date(2026, 6, 1, 15, 30, 0, 0, time.UTC),
			},
			StationBand: &band, StationFrequencyMHz: &freq,
		},
		{
			Detection: catalog.Detection{
				StationName: "Radio Y", CommercialName: "Spot 30s", Category: "in_slot",
				DetectedAt: time.Date(2026, 6, 2, 15, 30, 0, 0, time.UTC),
			},
		},
	}

	var buf bytes.Buffer
	err := WriteDetailed(&buf, func(cb func(catalog.DetectionEnriched) error) error {
		for _, d := range det {
			if err := cb(d); err != nil {
				return err
			}
		}
		return nil
	})
	require.NoError(t, err)

	out := buf.String()
	require.True(t, strings.HasPrefix(out, bomStr))
	// Vocabulário PT-BR do DayDetailModal, não o enum do banco.
	require.Contains(t, out, "Bônus")
	require.Contains(t, out, "Dentro da faixa")
	require.NotContains(t, out, "orphan")
}
