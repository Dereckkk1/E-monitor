package catalog

// CPMDaCampanha é o CPM que /campaigns mostra para uma linha de
// CampaignFinancials, espelhando frontend/src/pages/CampaignsPage.jsx
// (linhas 1017-1026): entregue = investido + bonificação; sem entregue
// (> 0) não há CPM nenhum, nem o fixo; com entregue, vale o fixed_cpm
// quando existe, senão o dinâmico entregue ÷ impactos × 1000. A bonificação
// está nos impactos (base in_slot + bonus), então tem de estar no numerador —
// a mesma fórmula de insights.go (perCPM). Até 2026-10 esta conta só existia
// no navegador; o MCP precisa dela pronta (o modelo nunca recalcula).
// nil = não se aplica, não é zero.
func CPMDaCampanha(f CampaignFinancials) *float64 {
	entregue := f.TotalInvested + f.TotalBonusValue
	if !(entregue > 0) {
		return nil
	}
	if f.FixedCPM != nil {
		v := *f.FixedCPM
		return &v
	}
	if f.TotalAudience <= 0 {
		return nil
	}
	v := entregue / f.TotalAudience * 1000
	return &v
}
