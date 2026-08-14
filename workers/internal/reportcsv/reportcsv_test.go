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

// writeDetailed roda WriteDetailed sobre um slice e devolve o texto cru e as
// linhas já divididas.
func writeDetailed(t *testing.T, det []catalog.DetectionEnriched) (string, []string) {
	t.Helper()
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
	return out, strings.Split(strings.ReplaceAll(strings.TrimPrefix(out, bomStr), "\r\n", "\n"), "\n")
}

// detFixture monta uma veiculação completa. Os parâmetros são só o que cada
// teste varia; o resto fica fixo.
func detFixture(shortID int32, station, uf, material, category string, price *float64) catalog.DetectionEnriched {
	band, city, typeName := "FM", "Joinville", "Spot 30s"
	freq, pmm, dur := 106.9, 812.0, 30.0
	target := 400
	client := "Rogga"
	return catalog.DetectionEnriched{
		Detection: catalog.Detection{
			StationName:    station,
			CommercialName: material,
			Category:       category,
			// 21:56:20 em São Paulo (UTC-3) = 00:56:20 UTC do dia seguinte.
			DetectedAt: time.Date(2026, 6, 1, 0, 56, 20, 0, time.UTC),
		},
		StationShortID: &shortID, StationBand: &band, StationFrequencyMHz: &freq,
		StationCity: &city, StationState: &uf,
		StationPMM: &pmm, StationPMMTarget: &target,
		MaterialTypeName: &typeName, MaterialDurationSec: &dur,
		ClientName: &client, UnitPrice: price,
	}
}

func TestWriteDetailed_CabecalhoELinha(t *testing.T) {
	price := 6.0
	out, lines := writeDetailed(t, []catalog.DetectionEnriched{
		detFixture(8407, "Massa", "SC", "JINGLE ROGGA VERÃO 30", "in_slot", &price),
	})

	require.True(t, strings.HasPrefix(out, bomStr), "faltou o BOM que o Excel pt-BR precisa")
	require.Equal(t,
		"Identificador;Data;Hora;Rádio;Cidade / UF;Peça;Comercial;Status;PMM;Preço;Cliente;PMM no target;Duração (s)",
		lines[0])
	// Sem linha em branco entre cabeçalho e dados — o fornecedor tem, a gente não.
	require.Equal(t,
		"8407;31/05/2026;21:56:20;Massa - FM (106.9);Joinville / SC;Spot 30s;JINGLE ROGGA VERÃO 30;Dentro da faixa;812;R$ 6,00;Rogga;400;30",
		lines[1])
}

func TestWriteDetailed_PrecoSoNaLinhaDentroDaFaixa(t *testing.T) {
	price := 53.03
	_, lines := writeDetailed(t, []catalog.DetectionEnriched{
		detFixture(1, "A", "SC", "M", "in_slot", &price),
		detFixture(1, "A", "SC", "M", "out_slot", &price),
		detFixture(1, "A", "SC", "M", "out_date", &price),
		detFixture(1, "A", "SC", "M", "orphan", &price),
		detFixture(1, "A", "SC", "M", "in_slot", nil),
	})

	require.Contains(t, lines[1], "Dentro da faixa;812;R$ 53,03")
	// Fora da faixa / fora da data / bônus não faturam: R$ 0,00 mesmo com
	// unit_value cadastrado.
	require.Contains(t, lines[2], "Fora da faixa;812;R$ 0,00")
	require.Contains(t, lines[3], "Fora da data;812;R$ 0,00")
	require.Contains(t, lines[4], "Bônus;812;R$ 0,00")
	// in_slot sem pricing cadastrado também é R$ 0,00.
	require.Contains(t, lines[5], "Dentro da faixa;812;R$ 0,00")
	require.NotContains(t, strings.Join(lines, "\n"), "orphan")
}

func TestWriteDetailed_CamposNulosNaoDeixamSeparadorSolto(t *testing.T) {
	d := catalog.DetectionEnriched{
		Detection: catalog.Detection{
			StationName:    "Radio Sem Nada",
			CommercialName: "Material X",
			Category:       "in_slot",
			DetectedAt:     time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
		},
	}
	_, lines := writeDetailed(t, []catalog.DetectionEnriched{d})

	require.Equal(t,
		";01/06/2026;09:00:00;Radio Sem Nada;;;Material X;Dentro da faixa;;R$ 0,00;;;",
		lines[1])
}

func TestWriteDetailed_RodapeDeTotais(t *testing.T) {
	_, lines := writeDetailed(t, []catalog.DetectionEnriched{
		detFixture(8407, "Massa", "SC", "JINGLE A", "in_slot", nil),
		detFixture(8407, "Massa", "SC", "JINGLE A", "in_slot", nil),
		detFixture(4229, "Itapoá", "SC", "JINGLE B", "in_slot", nil),
		detFixture(9001, "Alfa", "RS", "JINGLE A", "in_slot", nil),
	})

	joined := strings.Join(lines, "\n")
	require.Contains(t, joined, "TOTAL DE RADIOS MONITORADAS;3")
	require.Contains(t, joined, "TOTAL DE VEICULAÇÕES;4")
	require.Contains(t, joined, "RESUMO DE RÁDIOS POR ESTADO COM VEICULAÇÕES")
	require.Contains(t, joined, "RS;1")
	require.Contains(t, joined, "SC;2")
	require.Contains(t, joined, "JINGLE A;3")
	require.Contains(t, joined, "JINGLE B;1")
}

func TestWriteDetailed_SemVeiculacoes(t *testing.T) {
	out, lines := writeDetailed(t, nil)
	require.True(t, strings.HasPrefix(out, bomStr))
	require.Contains(t, lines[0], "Identificador;Data;Hora")
	require.Contains(t, strings.Join(lines, "\n"), "TOTAL DE VEICULAÇÕES;0")
}
