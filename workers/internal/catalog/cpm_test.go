package catalog

import "testing"

func TestCPMDaCampanha(t *testing.T) {
	fixo := 42.0
	cases := []struct {
		nome string
		f    CampaignFinancials
		want *float64
	}{
		// A bonificação está nos impactos, então TEM de estar no numerador
		// (docs/features/campaign-fixed-cpm.md). (200 + 100) / 3000 × 1000 = 100.
		{"dinâmico com bonificação", CampaignFinancials{TotalInvested: 200, TotalBonusValue: 100, TotalAudience: 3000}, cpmPtr(100.0)},
		{"fixo vence", CampaignFinancials{TotalInvested: 200, TotalAudience: 3000, FixedCPM: &fixo}, &fixo},
		{"sem impactos e sem fixo = não se aplica", CampaignFinancials{TotalInvested: 200}, nil},
	}
	for _, c := range cases {
		got := CPMDaCampanha(c.f)
		if (got == nil) != (c.want == nil) || (got != nil && *got != *c.want) {
			t.Fatalf("%s: quer %v, veio %v", c.nome, cpmDeref(c.want), cpmDeref(got))
		}
	}
}

// cpmPtr/cpmDeref: o pacote já tem ptr(string) e deref(*string) noutros testes.
func cpmPtr(f float64) *float64 { return &f }

func cpmDeref(f *float64) any {
	if f == nil {
		return nil
	}
	return *f
}
