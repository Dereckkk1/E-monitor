package evidence

import "radiocheck/internal/catalog"

// cofireAction é a decisão do co-fire guard do §18.2.2-v2 PASS-path, a partir da
// row existente do cut vencedor na janela (nil = não existe).
//
// Diferente do reject-path (decideRejectRecovery), aqui `self` — a detecção em
// auditoria — está APROVADA e contando. Então o guard só pode retratar `self`
// quando a veiculação CONTINUA contada por outra via (o vencedor presente e
// contado, ou um vencedor retraído que dá pra restaurar). Se o vencedor existe
// mas NÃO conta (audit_rejected / retraído sem evidência), retratar `self`
// zeraria a tocada — então reatribui `self` pro vencedor em vez disso (M-1).
type cofireAction int

const (
	// cofireReattribute — vencedor sem row (15s-contado-como-30s legítimo) OU
	// presente mas não contado: reatribui `self` pro vencedor (fluxo normal).
	cofireReattribute cofireAction = iota
	// cofireRetractSelf — vencedor já conta a veiculação: `self` é duplicata, retrata.
	cofireRetractSelf
	// cofireRestoreThenRetractSelf — vencedor retraído porém com evidência
	// available (v1 o deslocou): restaura o vencedor e retrata `self`.
	cofireRestoreThenRetractSelf
)

// detectionCounts reflete o ApprovedDetectionsFilter (catalog.ApprovedDetectionsFilter)
// no nível de uma row: conta quando NÃO retraída e o evidence_status não é
// 'audit_rejected'. (ignored_at não chega aqui — é ação manual de admin.)
func detectionCounts(r *catalog.SiblingDetectionRow) bool {
	return !r.Retracted && r.EvidenceStatus != "audit_rejected"
}

// decideCofireAction escolhe o ramo do pass-path. Pura/testável.
func decideCofireAction(existing *catalog.SiblingDetectionRow) cofireAction {
	if existing == nil {
		return cofireReattribute
	}
	if detectionCounts(existing) {
		return cofireRetractSelf // vencedor já conta → self é duplicata
	}
	if existing.Retracted && existing.EvidenceStatus == "available" {
		return cofireRestoreThenRetractSelf // vencedor restaurável → restaura e retrata self
	}
	// Vencedor presente mas não contado e não restaurável (audit_rejected, ou
	// retraído sem evidência): reatribuir self preserva a tocada (não zera).
	return cofireReattribute
}
