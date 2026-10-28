package catalog

import "github.com/google/uuid"

// PrecoDeTabela é o valor unitário de tabela de uma veiculação: o mesmo
// lookup que o relatório Data e Hora faz no navegador
// (frontend/src/components/AirtimeDetectionRow.jsx, resolveCost). Consolidado
// não tem valor por inserção (nil + modo PricingModeConsolidated); por
// inserção é o unit_value do tipo do material na emissora. NÃO é o valor
// faturado da linha: out_slot e out_date não valem nada e bonus é
// bonificação. modo "" = a emissora não tem linha de preço na campanha.
func PrecoDeTabela(precos []StationPricing, campanha, emissora uuid.UUID, tipo *uuid.UUID) (*float64, string) {
	for _, p := range precos {
		if p.CampaignID != campanha || p.StationID != emissora {
			continue
		}
		if p.Mode != PricingModePerInsertion || tipo == nil {
			return nil, p.Mode
		}
		for _, tp := range p.PerType {
			if tp.TypeID == *tipo {
				v := tp.UnitValue
				return &v, p.Mode
			}
		}
		return nil, p.Mode
	}
	return nil, ""
}
