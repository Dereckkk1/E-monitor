// Package reportcsv escreve os CSVs de relatório em qualquer io.Writer.
//
// Existe porque os mesmos bytes são servidos por dois caminhos: o download
// direto (handlers HTTP de /reports e /detections/export) e o bundle .zip do
// pós-venda. Duplicar a formatação nos dois faria o anexo do pós-venda
// divergir do botão "Relatórios" no primeiro ajuste de coluna.
package reportcsv

import (
	"encoding/csv"
	"fmt"
	"io"
	"strings"
	"time"

	"radiocheck/internal/catalog"
)

// bom é o marcador que faz o Excel pt-BR reconhecer UTF-8.
var bom = []byte{0xEF, 0xBB, 0xBF}

func saoPaulo() *time.Location {
	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		return time.FixedZone("BRT", -3*3600)
	}
	return loc
}

// WriteConsolidated escreve o CSV consolidado: uma linha por material ×
// emissora, com o breakdown por status.
//
// targetSuffix entra no cabeçalho das colunas "no target" (ex.:
// " (Mulheres 25-49)"); passe "" quando o cliente não tem rótulo de
// público-alvo — nunca monte "()" vazio.
func WriteConsolidated(out io.Writer, rows []catalog.MaterialStationRow, targetSuffix string) error {
	if _, err := out.Write(bom); err != nil {
		return err
	}
	cw := csv.NewWriter(out)
	cw.Comma = ';'
	if err := cw.Write([]string{
		"ID Material", "Material", "Tipo", "Duração (s)",
		"Emissora", "Frequência", "Banda", "Cidade", "UF",
		"Total Veiculações",
		// Breakdown por status — útil pra fechamento (saber quanto foi bônus,
		// quanto foi fora-faixa, dentro de cada combinação).
		"Dentro da faixa", "Fora da faixa", "Fora da data", "Bônus",
		// Impactos = Total Veiculações × PMM da emissora. A coluna "no target"
		// usa o PMM no target do cliente dono da campanha; fica vazia quando não
		// há cadastro (que não é a mesma coisa que zero).
		"PMM", "Impactos", "PMM no target" + targetSuffix, "Impactos no target" + targetSuffix,
		"Primeira", "Última",
	}); err != nil {
		return err
	}

	loc := saoPaulo()
	for _, row := range rows {
		idLabel := ""
		if row.MaterialShortID != nil {
			idLabel = fmt.Sprintf("%d", *row.MaterialShortID)
		}
		dur := ""
		if row.MaterialDurationSec != nil {
			dur = strings.ReplaceAll(fmt.Sprintf("%.0f", *row.MaterialDurationSec), ".", ",")
		}
		freq := ""
		if row.StationFrequencyMHz != nil {
			freq = strings.ReplaceAll(fmt.Sprintf("%.1f", *row.StationFrequencyMHz), ".", ",")
		}
		pmmStr, impactosStr := "", ""
		if row.StationPMM != nil {
			pmmStr = strings.ReplaceAll(fmt.Sprintf("%.0f", *row.StationPMM), ".", ",")
			impactosStr = fmt.Sprintf("%.0f", *row.StationPMM*float64(row.Count))
		}
		pmmTargetStr, impactosTargetStr := "", ""
		if row.StationPMMTarget != nil {
			pmmTargetStr = fmt.Sprintf("%d", *row.StationPMMTarget)
			impactosTargetStr = fmt.Sprintf("%d", *row.StationPMMTarget*row.Count)
		}
		if err := cw.Write([]string{
			idLabel,
			row.MaterialTitle,
			strOrEmpty(row.MaterialTypeName),
			dur,
			row.StationName,
			freq,
			strOrEmpty(row.StationBand),
			strOrEmpty(row.StationCity),
			strOrEmpty(row.StationState),
			fmt.Sprintf("%d", row.Count),
			fmt.Sprintf("%d", row.InSlotCount),
			fmt.Sprintf("%d", row.OutSlotCount),
			fmt.Sprintf("%d", row.OutDateCount),
			fmt.Sprintf("%d", row.OrphanCount),
			pmmStr,
			impactosStr,
			pmmTargetStr,
			impactosTargetStr,
			row.FirstDetectedAt.In(loc).Format("02/01/2006 15:04"),
			row.LastDetectedAt.In(loc).Format("02/01/2006 15:04"),
		}); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// WriteDetailed escreve o CSV detalhado: uma linha por veiculação.
//
// O iterador é injetado para que o chamador escolha a fonte — streaming HTTP
// (Detections.IterateForExport direto no ResponseWriter) ou buffer em memória
// (bundle do pós-venda) — sem que o formato mude entre os dois.
func WriteDetailed(out io.Writer, iterate func(cb func(catalog.DetectionEnriched) error) error) error {
	if _, err := out.Write(bom); err != nil {
		return err
	}
	cw := csv.NewWriter(out)
	cw.Comma = ';'
	// "PMM no target" fica SEM o rótulo de público-alvo do cliente aqui, ao
	// contrário do CSV consolidado: campaign_id é opcional neste export, então
	// as linhas podem cobrir várias campanhas de CLIENTES DIFERENTES — cada uma
	// com o seu target. Um rótulo único no cabeçalho estaria errado para parte
	// das linhas, e rótulo errado é pior que rótulo nenhum.
	if err := cw.Write([]string{
		"Data", "Hora", "Emissora", "Frequência", "Banda", "Cidade", "UF",
		"Material", "Duração (s)", "Tipo", "Cliente", "PMM", "PMM no target", "Status",
	}); err != nil {
		return err
	}

	loc := saoPaulo()
	if err := iterate(func(d catalog.DetectionEnriched) error {
		t := d.DetectedAt.In(loc)
		freq := ""
		if d.StationFrequencyMHz != nil {
			freq = strings.ReplaceAll(fmt.Sprintf("%.1f", *d.StationFrequencyMHz), ".", ",")
		}
		pmm := ""
		if d.StationPMM != nil {
			pmm = strings.ReplaceAll(fmt.Sprintf("%.0f", *d.StationPMM), ".", ",")
		}
		pmmTarget := ""
		if d.StationPMMTarget != nil {
			pmmTarget = fmt.Sprintf("%d", *d.StationPMMTarget)
		}
		dur := ""
		if d.MaterialDurationSec != nil {
			dur = strings.ReplaceAll(fmt.Sprintf("%.0f", *d.MaterialDurationSec), ".", ",")
		}
		return cw.Write([]string{
			t.Format("02/01/2006"),
			t.Format("15:04:05"),
			d.StationName,
			freq,
			strOrEmpty(d.StationBand),
			strOrEmpty(d.StationCity),
			strOrEmpty(d.StationState),
			d.CommercialName,
			dur,
			strOrEmpty(d.MaterialTypeName),
			strOrEmpty(d.ClientName),
			pmm,
			pmmTarget,
			CategoryLabelPT(d.Category),
		})
	}); err != nil {
		return err
	}
	cw.Flush()
	return cw.Error()
}

// CategoryLabelPT mapeia o enum da coluna `category` (in_slot|out_slot|
// out_date|orphan) pro rótulo PT-BR usado nos relatórios exportados. Mesmo
// vocabulário do DayDetailModal.jsx — "Bônus" pra orphan (veiculação sem regra
// correspondente, que conta como bônus comercial pro cliente).
func CategoryLabelPT(c string) string {
	switch c {
	case "in_slot":
		return "Dentro da faixa"
	case "out_slot":
		return "Fora da faixa"
	case "out_date":
		return "Fora da data"
	case "orphan":
		return "Bônus"
	default:
		return c // fallback defensivo se aparecer um valor novo
	}
}

func strOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
