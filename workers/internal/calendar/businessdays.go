// Package calendar implementa cálculo de dias úteis (seg–sex, SEM feriados) e
// a janela de alerta de campanha compartilhada pelos 3 disparos de email.
//
// Convenção de datas (importante):
//   - Datas DATE do Postgres voltam como meia-noite UTC (ex.: 2026-06-15T00:00Z).
//     Elas são "datas civis" sem fuso — NÃO devem ser convertidas de timezone,
//     senão UTC-3 joga o dia pra trás (15/06 → 14/06).
//   - Um instante (time.Now()) precisa ser convertido para America/Sao_Paulo
//     para sabermos qual é "o dia de hoje no Brasil".
//
// Por isso há duas portas de entrada: Today(now) para instantes e CivilDate(t)
// para datas vindas do banco. Ambas retornam meia-noite UTC do dia civil, que é
// a base canônica usada por toda a aritmética abaixo.
package calendar

import "time"

// BR é o fuso de referência do negócio.
var BR = mustBR()

func mustBR() *time.Location {
	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		// America/Sao_Paulo está na tzdata padrão; UTC é fallback seguro.
		return time.UTC
	}
	return loc
}

// CivilDate extrai o dia civil (Y/M/D na própria location de t) e o devolve como
// meia-noite UTC. Para datas DATE do banco (já em UTC) é efetivamente um
// truncamento; não desloca o dia.
func CivilDate(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// Today converte um instante para o dia civil no Brasil e o devolve como
// meia-noite UTC. Use para "hoje".
func Today(now time.Time) time.Time {
	n := now.In(BR)
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC)
}

// IsBusinessDay retorna true para segunda a sexta (dia civil de t).
func IsBusinessDay(t time.Time) bool {
	wd := CivilDate(t).Weekday()
	return wd != time.Saturday && wd != time.Sunday
}

// CalendarDaysUntil retorna (target - today) em dias de calendário.
func CalendarDaysUntil(today, target time.Time) int {
	return int(CivilDate(target).Sub(CivilDate(today)).Hours() / 24)
}

// BusinessDaysUntil conta dias úteis no intervalo semiaberto (today, target]:
// conta target se for útil, não conta today. Retorna 0 quando target <= today.
func BusinessDaysUntil(today, target time.Time) int {
	a := CivilDate(today)
	b := CivilDate(target)
	if !b.After(a) {
		return 0
	}
	count := 0
	for cur := a.AddDate(0, 0, 1); !cur.After(b); cur = cur.AddDate(0, 0, 1) {
		if IsBusinessDay(cur) {
			count++
		}
	}
	return count
}

// InAlertWindow é o predicado compartilhado: dispara alerta quando faltam menos
// de 3 dias corridos OU até 2 dias úteis para a data-alvo. Datas no passado
// (target < today) nunca entram. Os argumentos são normalizados internamente
// (CivilDate), então a location dos inputs não altera o dia que eles nomeiam.
func InAlertWindow(today, target time.Time) bool {
	if CalendarDaysUntil(today, target) < 0 {
		return false
	}
	return CalendarDaysUntil(today, target) < 3 || BusinessDaysUntil(today, target) <= 2
}
