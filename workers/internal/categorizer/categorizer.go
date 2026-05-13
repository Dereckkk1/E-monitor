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

// Category labels (idênticos aos valores do CHECK constraint em detections.category).
const (
	CatInSlot  = "in_slot"
	CatOutSlot = "out_slot"
	CatOutDate = "out_date"
	CatOrphan  = "orphan"
)

// SlotToleranceSeconds é a folga (15 min) aplicada a cada extremo da faixa de
// horário de uma rule ao classificar uma detection como in_slot. Cobre o
// jitter normal de stream + broadcaster (latência de buffer, atraso de
// programação ao vivo, etc.) que faria uma veiculação tocada às 05:45 cair
// como "fora da faixa" quando o atendente entende que ela tocou 'às 6h'.
const SlotToleranceSeconds = 15 * 60

var spLocation, _ = time.LoadLocation("America/Sao_Paulo")

// Categorize classifica uma detection. A campanha é assumida existente
// (o detection é insert pela matching engine, sempre tem campaign_id).
//
// Regra (spec §6.1):
//  1. detectedAt fora de [campaign.StartDate, campaign.EndDate] → out_date
//  2. nenhuma rule aplicável (mesmo material/station/data) → orphan
//  3. rule existe e time ∈ [time_start - 15min, time_end + 15min] → in_slot
//  4. rule existe mas time fora da faixa tolerada → out_slot
//
// Comparações de data são feitas no fuso America/Sao_Paulo. detectedAt pode
// chegar em qualquer fuso (típicamente UTC do worker); o categorizer
// converte internamente pra SP antes de extrair date/weekday/time-of-day.
//
// Tempo de faixa tem tolerância de SlotToleranceSeconds (15min) em cada extremo
// e é boundary-inclusive — uma rule 08:00-10:00 aceita detections entre
// 07:45:00 e 10:15:00 como in_slot.
func Categorize(detectedAt time.Time, cmp Campaign, rules []Rule) string {
	local := detectedAt.In(spLocation)
	date := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, spLocation)

	if date.Before(cmp.StartDate) || date.After(cmp.EndDate) {
		return CatOutDate
	}

	dow := int(local.Weekday()) // 0=Sun, 6=Sat — bate com EXTRACT(DOW) do PG
	hh := local.Hour()
	mm := local.Minute()
	ss := local.Second()
	timeOfDay := hh*3600 + mm*60 + ss

	hasApplicable := false
	for _, r := range rules {
		// Date range
		if date.Before(r.StartDate) || date.After(r.EndDate) {
			continue
		}
		// Weekday mask
		if (1<<dow)&int(r.WeekdayMask) == 0 {
			continue
		}
		hasApplicable = true
		// Time window — boundary inclusive, com folga de SlotToleranceSeconds em
		// cada extremo. Não há wraparound: se rs < tolerância (rule perto da
		// meia-noite), o limite inferior é efetivamente 00:00 do mesmo dia.
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
