package postsale

import (
	"fmt"
	"strings"
	"time"
)

var mesesPT = [...]string{"", "Janeiro", "Fevereiro", "Março", "Abril", "Maio",
	"Junho", "Julho", "Agosto", "Setembro", "Outubro", "Novembro", "Dezembro"}

// periodLabel é o período por extenso que aparece embaixo do nome da campanha.
// Intervalo inclusivo nas duas pontas — é assim que o cliente conta os dias
// contratados.
func periodLabel(from, to time.Time) string {
	days := int(to.Sub(from).Hours()/24) + 1
	if days <= 1 {
		return fmt.Sprintf("%s · 1 dia", from.Format("02/01/2006"))
	}
	return fmt.Sprintf("%s a %s · %d dias",
		from.Format("02/01/2006"), to.Format("02/01/2006"), days)
}

// monthsLabel monta "Junho e Julho de 2026" pro cabeçalho do documento.
// Quando o intervalo cruza o ano, cada mês carrega o próprio ano — senão
// "Dezembro e Janeiro de 2027" dataria dezembro errado.
func monthsLabel(from, to time.Time) string {
	type ym struct {
		y int
		m time.Month
	}
	var seq []ym
	cur := time.Date(from.Year(), from.Month(), 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(to.Year(), to.Month(), 1, 0, 0, 0, 0, time.UTC)
	for !cur.After(end) {
		seq = append(seq, ym{cur.Year(), cur.Month()})
		cur = cur.AddDate(0, 1, 0)
	}
	if len(seq) == 0 {
		return ""
	}
	sameYear := seq[0].y == seq[len(seq)-1].y
	parts := make([]string, 0, len(seq))
	for _, s := range seq {
		if sameYear {
			parts = append(parts, mesesPT[s.m])
		} else {
			parts = append(parts, fmt.Sprintf("%s de %d", mesesPT[s.m], s.y))
		}
	}
	joined := parts[0]
	switch {
	case len(parts) == 2:
		joined = parts[0] + " e " + parts[1]
	case len(parts) > 2:
		joined = strings.Join(parts[:len(parts)-1], ", ") + " e " + parts[len(parts)-1]
	}
	if sameYear {
		return fmt.Sprintf("%s de %d", joined, seq[0].y)
	}
	return joined
}

// conformingCount = total de emissoras do período menos as que ficaram nas
// listas. Emissora que o admin removeu migra pra cá: o total sempre fecha, e o
// cliente nunca vê uma emissora "sumir" do relatório.
func conformingCount(total int, shown []StationRow) int {
	n := total - len(shown)
	if n < 0 {
		return 0
	}
	return n
}
