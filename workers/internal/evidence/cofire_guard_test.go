package evidence

import (
	"testing"

	"radiocheck/internal/catalog"
)

func TestDecideCofireAction(t *testing.T) {
	cases := []struct {
		name     string
		existing *catalog.SiblingDetectionRow
		want     cofireAction
	}{
		{"sem row do vencedor -> reatribui (15s-as-30s legítimo)",
			nil, cofireReattribute},
		{"vencedor presente e contado (available) -> retrata self (duplicata)",
			&catalog.SiblingDetectionRow{Retracted: false, EvidenceStatus: "available"}, cofireRetractSelf},
		{"vencedor presente e contado (missing conta) -> retrata self",
			&catalog.SiblingDetectionRow{Retracted: false, EvidenceStatus: "missing"}, cofireRetractSelf},
		{"vencedor retraído+available -> restaura vencedor + retrata self",
			&catalog.SiblingDetectionRow{Retracted: true, EvidenceStatus: "available"}, cofireRestoreThenRetractSelf},
		// M-1: vencedor presente mas NÃO contado (audit_rejected) -> reatribuir, NÃO
		// retratar self (senão a veiculação fica contada zero vezes).
		{"vencedor presente audit_rejected (não conta) -> reatribui self",
			&catalog.SiblingDetectionRow{Retracted: false, EvidenceStatus: "audit_rejected"}, cofireReattribute},
		{"vencedor retraído+audit_rejected (não conta, não restaura) -> reatribui self",
			&catalog.SiblingDetectionRow{Retracted: true, EvidenceStatus: "audit_rejected"}, cofireReattribute},
		{"vencedor retraído+missing (não restaurável) -> reatribui self",
			&catalog.SiblingDetectionRow{Retracted: true, EvidenceStatus: "missing"}, cofireReattribute},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := decideCofireAction(c.existing); got != c.want {
				t.Errorf("decideCofireAction = %v, queria %v", got, c.want)
			}
		})
	}
}
