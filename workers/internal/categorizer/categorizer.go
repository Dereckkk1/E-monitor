package categorizer

import "time"

// Campaign é o subset que o categorizer precisa da campanha.
// StartDate/EndDate são date-only (hora 00:00), idealmente em America/Sao_Paulo.
type Campaign struct {
	StartDate time.Time // date-only, hora 00:00 (inclusivo)
	EndDate   time.Time // date-only (inclusivo)
}

// Rule é o subset que o categorizer precisa de uma distribution_rule.
type Rule struct {
	StartDate   time.Time // date-only
	EndDate     time.Time // date-only
	WeekdayMask int16     // bit 0=Dom, ..., 6=Sáb (matching PostgreSQL EXTRACT(DOW))
	TimeStart   time.Time // só componente HH:MM importa
	TimeEnd     time.Time // só componente HH:MM importa
	PlaysPerDay int16
}

// Override é a entrada do distribution_overrides relevante pra célula
// (campaign, type, station, date) sendo categorizada. Quando passado a
// Categorize, substitui as rules pra essa célula+dia: a faixa do override
// vira a única considerada pra in_slot/out_slot. PlaysExpected=0 marca
// uma exclusão explícita — toda detection no dia vira out_slot (faixa
// inerte). Decisões D1/D5/D7 do spec.
type Override struct {
	PlaysExpected int16
	TimeStart     time.Time // só componente HH:MM importa
	TimeEnd       time.Time
}

// Category labels (idênticos aos valores do CHECK constraint em detections.category).
const (
	CatInSlot  = "in_slot"
	CatOutSlot = "out_slot"
	CatOutDate = "out_date"
	CatOrphan  = "orphan"
)

// SlotToleranceSeconds é a folga (15 min) aplicada a cada extremo da faixa de
// horário ao classificar uma detection como in_slot. Cobre o jitter normal
// de stream + broadcaster (latência de buffer, atraso de programação ao
// vivo) que faria uma veiculação às 05:45 cair como "fora da faixa" quando
// o atendente entende que ela tocou 'às 6h'.
const SlotToleranceSeconds = 15 * 60

var spLocation, _ = time.LoadLocation("America/Sao_Paulo")

// dateOnlySP normaliza um time.Time para meia-noite local SP do mesmo dia
// calendário. Necessário porque pgx escaneia colunas DATE como time.Time em
// UTC à meia-noite (00:00Z); compará-las direto contra um `date` em SP
// (00:00-03:00 = 03:00Z) faria o último dia do range cair fora — todo o
// fluxo do categorizer assume datas em SP (ver comentário do tipo Campaign).
// Bug em prod 2026-05-26: 6 detections do dia 26 numa campanha terminando em
// 26 marcadas out_date porque `date.After(cmpEnd)` retornava true.
func dateOnlySP(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, spLocation)
}

// Categorize classifica uma detection.
//
// Regra:
//  1. detectedAt fora de [campaign.StartDate, campaign.EndDate] → out_date
//  2. override != nil:
//     - override.PlaysExpected == 0 → out_slot (faixa inerte; ver D5)
//     - detection ∈ [ts-15min, te+15min] do override → in_slot
//     - caso contrário → out_slot
//     Rules são IGNORADAS quando há override (override REPLACE total — D1).
//  3. override == nil, nenhuma rule aplicável (date+weekday) → orphan
//  4. override == nil, rule aplicável, detection na faixa tolerada → in_slot
//  5. override == nil, rule aplicável, detection fora da faixa → out_slot
//
// Comparações de data são feitas no fuso America/Sao_Paulo. detectedAt pode
// chegar em qualquer fuso (típicamente UTC do worker); o categorizer
// converte internamente pra SP antes de extrair date/weekday/time-of-day.
func Categorize(detectedAt time.Time, cmp Campaign, rules []Rule, override *Override) string {
	local := detectedAt.In(spLocation)
	date := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, spLocation)

	if date.Before(dateOnlySP(cmp.StartDate)) || date.After(dateOnlySP(cmp.EndDate)) {
		return CatOutDate
	}

	hh := local.Hour()
	mm := local.Minute()
	ss := local.Second()
	timeOfDay := hh*3600 + mm*60 + ss

	if override != nil {
		if override.PlaysExpected == 0 {
			return CatOutSlot
		}
		os := override.TimeStart.Hour()*3600 + override.TimeStart.Minute()*60 + override.TimeStart.Second()
		oe := override.TimeEnd.Hour()*3600 + override.TimeEnd.Minute()*60 + override.TimeEnd.Second()
		if timeOfDay >= os-SlotToleranceSeconds && timeOfDay <= oe+SlotToleranceSeconds {
			return CatInSlot
		}
		return CatOutSlot
	}

	dow := int(local.Weekday()) // 0=Sun, 6=Sat — bate com EXTRACT(DOW) do PG
	hasApplicable := false
	for _, r := range rules {
		if date.Before(dateOnlySP(r.StartDate)) || date.After(dateOnlySP(r.EndDate)) {
			continue
		}
		if (1<<dow)&int(r.WeekdayMask) == 0 {
			continue
		}
		hasApplicable = true
		rs := r.TimeStart.Hour()*3600 + r.TimeStart.Minute()*60 + r.TimeStart.Second()
		re := r.TimeEnd.Hour()*3600 + r.TimeEnd.Minute()*60 + r.TimeEnd.Second()
		if timeOfDay >= rs-SlotToleranceSeconds && timeOfDay <= re+SlotToleranceSeconds {
			return CatInSlot
		}
	}
	if hasApplicable {
		return CatOutSlot
	}
	return CatOrphan
}
