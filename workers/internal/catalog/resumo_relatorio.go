package catalog

import "math"

// ResumoTotais são os totais do relatório de campanha (PDF e MCP).
type ResumoTotais struct {
	Detections        int
	DistinctMaterials int
	DistinctStations  int
	// Impactos = Σ round(PMM × impact_count) por emissora, impact_count =
	// in_slot + bonus (base canônica — docs/features/client-target-pmm.md).
	// NÃO é Detections/Count: out_slot não vale nada comercialmente e
	// out_date está fora do período contratado. ImpactosTarget usa o PMM no
	// target; StationsWithTarget > 0 é o gate de exibição no PDF.
	Impactos           int64
	ImpactosTarget     int64
	StationsWithTarget int
}

// ImpactosDaEmissora: round(PMM × (in_slot + bonus)). math.Round, não
// truncamento: a coluna "Impactos" da tabela do PDF é calculada no frontend
// com Math.round(pmm × impact_count) (utils/pdfReport.js). Truncar aqui faria
// o KPI do topo ficar ABAIXO da soma da própria coluna no mesmo documento —
// stations.pmm é numeric(10,2), então o produto é fracionário e a diferença
// chega a 1 por emissora. nil = emissora sem PMM (fora do total, não zero).
func ImpactosDaEmissora(s StationAggregateRow) *int64 {
	if s.StationPMM == nil {
		return nil
	}
	v := int64(math.Round(*s.StationPMM * float64(s.ImpactCount)))
	return &v
}

// TotaisDoResumo soma os totais do resumo de campanha. Os impactos vêm de
// porEmissora (uma linha por emissora), não de material × emissora — senão a
// mesma emissora entraria uma vez por material.
func TotaisDoResumo(porMaterial *MaterialAggregateResult, porEmissora []StationAggregateRow) ResumoTotais {
	t := ResumoTotais{
		Detections:        porMaterial.TotalDetections,
		DistinctMaterials: porMaterial.DistinctMaterials,
		DistinctStations:  len(porEmissora),
	}
	for _, s := range porEmissora {
		if v := ImpactosDaEmissora(s); v != nil {
			t.Impactos += *v
		}
		if s.StationPMMTarget != nil {
			t.ImpactosTarget += int64(*s.StationPMMTarget * s.ImpactCount)
			t.StationsWithTarget++
		}
	}
	return t
}
