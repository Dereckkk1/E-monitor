// Package calendar implementa cálculo de dias úteis (seg–sex, SEM feriados) e
// a janela de alerta de campanha compartilhada pelos 3 disparos de email.
// Toda comparação é por data (meia-noite), em America/Sao_Paulo.
package calendar

import "time"

// BR é o fuso de referência do negócio. Datas vindas do banco (DATE) são
// comparadas neste fuso.
var BR = mustBR()

func mustBR() *time.Location {
	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		// America/Sao_Paulo está na tzdata padrão; se faltar, UTC é um fallback
		// seguro (o pior caso desloca a fronteira de dia em horas, não quebra).
		return time.UTC
	}
	return loc
}

// DateOnly normaliza t para meia-noite no fuso BR, descartando hora/min/seg.
func DateOnly(t time.Time) time.Time {
	t = t.In(BR)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, BR)
}

// IsBusinessDay retorna true para segunda a sexta.
func IsBusinessDay(t time.Time) bool {
	wd := t.In(BR).Weekday()
	return wd != time.Saturday && wd != time.Sunday
}

// CalendarDaysUntil retorna (target - today) em dias de calendário.
func CalendarDaysUntil(today, target time.Time) int {
	a := DateOnly(today)
	b := DateOnly(target)
	return int(b.Sub(a).Hours() / 24)
}

// BusinessDaysUntil conta dias úteis no intervalo semiaberto (today, target]:
// conta target se for útil, não conta today. Retorna 0 quando target <= today.
func BusinessDaysUntil(today, target time.Time) int {
	a := DateOnly(today)
	b := DateOnly(target)
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
// (target < today) nunca entram.
func InAlertWindow(today, target time.Time) bool {
	if CalendarDaysUntil(today, target) < 0 {
		return false
	}
	return CalendarDaysUntil(today, target) < 3 || BusinessDaysUntil(today, target) <= 2
}
