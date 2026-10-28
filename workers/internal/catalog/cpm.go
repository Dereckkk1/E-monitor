package catalog

// CPMDaCampanha é o CPM que /campaigns mostra para uma linha de
// CampaignFinancials: o fixed_cpm quando existe; senão o dinâmico,
// (investido + bonificação) ÷ impactos × 1000. A bonificação está nos
// impactos (base in_slot + bonus), então tem de estar no numerador — a mesma
// fórmula de insights.go (perCPM). Até 2026-10 esta conta só existia no
// navegador; o MCP precisa dela pronta (o modelo nunca recalcula).
// nil = sem impactos e sem CPM fixo: não se aplica, não é zero.
func CPMDaCampanha(f CampaignFinancials) *float64 {
	if f.FixedCPM != nil {
		v := *f.FixedCPM
		return &v
	}
	if f.TotalAudience <= 0 {
		return nil
	}
	v := (f.TotalInvested + f.TotalBonusValue) / f.TotalAudience * 1000
	return &v
}
