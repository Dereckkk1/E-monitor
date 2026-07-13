package categorizer

import (
	"time"

	"github.com/google/uuid"
)

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
	// MaterialIDs vazio = regra vale pra todos os materiais do tipo (migration
	// 0019). Não-vazio = regra "carve-out": vale só pra esses materiais, e eles
	// passam a ser julgados SÓ por regras que os nomeiam (migration 0043).
	MaterialIDs []uuid.UUID
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

// Categorize classifica uma detection do material materialID.
//
// Carve-out (migration 0043): se materialID é nomeado em alguma regra com
// MaterialIDs não-vazio, ele é julgado SÓ por essas regras (regras gerais do
// tipo — MaterialIDs vazio — deixam de valer pra ele). Tocar fora do período/
// dia da regra dele vira out_date; fora da faixa, out_slot; nunca orphan.
// Material sem regra específica usa as regras gerais, exatamente como antes.
//
// Comparações de data no fuso America/Sao_Paulo (ver Categorize original).
func Categorize(detectedAt time.Time, cmp Campaign, materialID uuid.UUID, rules []Rule, override *Override) string {
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

	dow := int(local.Weekday())

	// inWindow casa data+dia da rule e (opcionalmente) a faixa horária tolerada.
	matchesDateWeekday := func(r Rule) bool {
		if date.Before(dateOnlySP(r.StartDate)) || date.After(dateOnlySP(r.EndDate)) {
			return false
		}
		return (1<<dow)&int(r.WeekdayMask) != 0
	}
	matchesTime := func(r Rule) bool {
		rs := r.TimeStart.Hour()*3600 + r.TimeStart.Minute()*60 + r.TimeStart.Second()
		re := r.TimeEnd.Hour()*3600 + r.TimeEnd.Minute()*60 + r.TimeEnd.Second()
		return timeOfDay >= rs-SlotToleranceSeconds && timeOfDay <= re+SlotToleranceSeconds
	}

	// Carve-out: materialID é nomeado em ALGUMA regra específica? (independente de data)
	carved := false
	for _, r := range rules {
		if len(r.MaterialIDs) > 0 && containsUUID(r.MaterialIDs, materialID) {
			carved = true
			break
		}
	}

	if carved {
		hasDateWeekday := false
		inRulePeriod := false
		for _, r := range rules {
			if len(r.MaterialIDs) == 0 || !containsUUID(r.MaterialIDs, materialID) {
				continue
			}
			// Dentro do range de datas da regra dele (ignorando dia/faixa)?
			if !date.Before(dateOnlySP(r.StartDate)) && !date.After(dateOnlySP(r.EndDate)) {
				inRulePeriod = true
			}
			if !matchesDateWeekday(r) {
				continue
			}
			hasDateWeekday = true
			if matchesTime(r) {
				return CatInSlot
			}
		}
		if hasDateWeekday {
			return CatOutSlot
		}
		// Dentro do período do material mas em dia/faixa sem meta → dia extra
		// dentro do período contratado → orphan (a view credita bônus). out_date
		// fica reservado a tocadas FORA do período das regras do material.
		if inRulePeriod {
			return CatOrphan
		}
		return CatOutDate
	}

	// Material comum — só regras gerais (MaterialIDs vazio), lógica original.
	hasApplicable := false
	for _, r := range rules {
		if len(r.MaterialIDs) > 0 {
			continue
		}
		if !matchesDateWeekday(r) {
			continue
		}
		hasApplicable = true
		if matchesTime(r) {
			return CatInSlot
		}
	}
	if hasApplicable {
		return CatOutSlot
	}
	return CatOrphan
}

func containsUUID(ids []uuid.UUID, id uuid.UUID) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}
