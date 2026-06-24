package evidence

import (
	"testing"

	"radiocheck/internal/catalog"
)

func TestDecideRejectRecovery(t *testing.T) {
	cases := []struct {
		name     string
		existing *catalog.SiblingDetectionRow
		want     RejectRecoveryAction
	}{
		{"sem row do vencedor -> reatribui", nil, RecoveryReattribute},
		{"row retraida+available -> restaura",
			&catalog.SiblingDetectionRow{Retracted: true, EvidenceStatus: "available"}, RecoveryRestore},
		{"row retraida mas audit_rejected -> pula (15s tambem reprovou)",
			&catalog.SiblingDetectionRow{Retracted: true, EvidenceStatus: "audit_rejected"}, RecoverySkip},
		{"row aprovada nao-retraida -> pula (ja contada)",
			&catalog.SiblingDetectionRow{Retracted: false, EvidenceStatus: "available"}, RecoverySkip},
		{"row missing nao-retraida -> pula (ja contada)",
			&catalog.SiblingDetectionRow{Retracted: false, EvidenceStatus: "missing"}, RecoverySkip},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := decideRejectRecovery(c.existing); got != c.want {
				t.Errorf("decideRejectRecovery = %v, queria %v", got, c.want)
			}
		})
	}
}
