package categorizer

import (
	"sort"
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

// Category labels. Todos são aceitos pelo CHECK constraint de
// detections.category e detection_campaigns.category desde a migration 0063,
// que alargou os dois pra 'bonus' SEM tirar 'orphan' (ver CatOrphan).
const (
	CatInSlot  = "in_slot"
	CatOutSlot = "out_slot"
	CatOutDate = "out_date"
	// CatOrphan é o nome ANTIGO de CatBonus, aposentado como veredito pela spec
	// 2026-08-14 (D4). Nenhum produtor de produção grava mais 'orphan': o
	// insert-path usa Settle e a recategorização usa recatClassifiedCTE, e os
	// dois emitem CatBonus. O único escritor que resta é Categorize, que já não
	// tem caller de produção (só os testes dela).
	//
	// Continua existindo por dois motivos, os dois de LEITURA:
	//   1. janela de deploy — o binário antigo segue gravando 'orphan' entre o
	//      `migrate up` e o recreate do container;
	//   2. banco onde o backfill (cmd/backfill-recategorize) ainda não rodou.
	//
	// Consumidor que soma bonificação deve usar BonusCategoriesSQL, não este
	// valor sozinho.
	CatOrphan = "orphan"
	// CatBonus é a veiculação que excede a meta do dia — bonificação. É o
	// veredito atual: substituiu CatOrphan na spec 2026-08-14 (D4), as linhas
	// existentes foram renomeadas pela migration 0064 e a view
	// daily_play_summary passou a contá-lo direto na 0065.
	CatBonus = "bonus"
)

// BonusCategoriesSQL é a lista de valores que contam como bonificação numa
// cláusula SQL `IN`. Existe pra que nenhum consumidor precise repetir (nem
// esquecer) o sinônimo legado: 'bonus' é o veredito atual, 'orphan' é o mesmo
// conceito escrito pelo binário antigo durante a janela de deploy ou por um
// banco onde o backfill da Task 12 ainda não rodou. Descartar 'orphan' num
// total financeiro sumiria com veiculação real do relatório do cliente — o
// erro caro é o oposto do de contá-la duas vezes (não há como: a mesma linha
// só tem uma categoria).
//
// Escrita: nunca. Só CatBonus é gravado. Ver o godoc de CatOrphan.
const BonusCategoriesSQL = `('bonus','orphan')`

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
// MORTA EM PRODUÇÃO. Superada por Settle (spec 2026-08-14): classifica uma
// tocada isolada, sem noção de cota do dia, e por isso diverge do modelo novo em
// célula-dia. O insert-path em internal/catalog/detections.go já migrou pra
// settleCellDay e não sobrou nenhum caller fora dos testes desta própria função
// — que continuam aqui só documentando o comportamento antigo. É também o único
// lugar do código que ainda devolve CatOrphan. Código novo chama Settle;
// deletar esta função (com categorizer_test.go) não afeta produção.
//
// Carve-out (migration 0043): se materialID é nomeado em alguma regra com
// MaterialIDs não-vazio, ele é julgado SÓ por essas regras (regras gerais do
// tipo — MaterialIDs vazio — deixam de valer pra ele). Tocar fora do período/
// dia da regra dele vira out_date; fora da faixa, out_slot; nunca CatOrphan.
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
		// dentro do período contratado → bonificação (no vocabulário antigo
		// desta função, CatOrphan). out_date fica reservado a tocadas FORA do
		// período das regras do material.
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

// Play é uma tocada da célula-dia a ser fechada.
type Play struct {
	DetectedAt time.Time
	MaterialID uuid.UUID
}

// Settle fecha uma célula-dia (campanha, tipo, emissora, dia) inteira e devolve
// a categoria de cada tocada NA MESMA ORDEM do slice de entrada.
//
// Substitui a Categorize (uma tocada por vez) como regra canônica; as duas
// coexistem de propósito até a Task 3 trocar o insert-path.
//
// Regra canônica (spec 2026-08-14 §2) — o "passo 0:" evita que o gofmt leia o
// bloco como lista ordenada e reflue as continuações:
//
//	passo 0: out_date — fora do período da campanha, ou (carve-out) fora do
//	               período das regras que nomeiam o material. Não consome cota.
//	1. N         — override.PlaysExpected, senão Σ plays_per_day das regras do dia.
//	2. "dentro da faixa" — casa ALGUMA faixa válida hoje (±SlotToleranceSeconds).
//	                       Sem cota por faixa: a meta é do dia.
//	3. dentro da faixa, em ordem cronológica: as N primeiras → in_slot, resto → bonus.
//	4. fora da faixa: in_slot < N → out_slot, senão → bonus.
//
// `day` é a data local SP da célula à meia-noite — passada explicitamente pra que
// N seja bem definido mesmo quando todas as tocadas são out_date.
//
// Pré-condições (contrato do caller — NÃO são validadas, violar dá resultado
// silenciosamente errado):
//
//  1. TODAS as tocadas de `plays` pertencem à MESMA célula-dia `day`. N é
//     calculado uma vez a partir de `day`, mas o teste de faixa usa a data local
//     de cada tocada: misturar dias aplica a meta de um dia às faixas de outro.
//     Quem monta o slice tem que filtrar por [day, day+1) em SP.
//  2. `plays` vem ordenado por (detected_at, id) — a mesma ordem do
//     ROW_NUMBER() do SQL. A ordenação interna é por detected_at apenas e
//     estável, ou seja, empates no mesmo segundo preservam a ordem do slice:
//     é o caller que decide quem leva a vaga da cota num empate, e ele só
//     concorda com o SQL se tiver desempatado por id.
//
// PARIDADE: esta função e a CTE `classified` de
// internal/catalog/distribution_rules.go (recatClassifiedCTE) DEVEM concordar.
// O settle_parity_test.go provará isso — ainda não existe, é a Task 5.
func Settle(day time.Time, plays []Play, cmp Campaign, rules []Rule, override *Override) []string {
	out := make([]string, len(plays))
	dayLocal := dateOnlySP(day)

	// Meta do dia. Override supersede as regras (D1/D7 do spec 2026-05-19).
	n := 0
	if override != nil {
		n = int(override.PlaysExpected)
	} else {
		for _, r := range rules {
			if ruleCoversDay(r, dayLocal) {
				n += int(r.PlaysPerDay)
			}
		}
	}

	// Índices que entram na cota, ordenados por detected_at com desempate estável
	// pela posição no slice. Isso equivale ao ORDER BY (detected_at, id) do
	// ROW_NUMBER no SQL apenas sob a pré-condição 2 — é o caller que traz o
	// desempate por id, aqui só o preservamos.
	type entry struct {
		i        int
		inWindow bool
	}
	entries := make([]entry, 0, len(plays))
	for i, p := range plays {
		local := p.DetectedAt.In(spLocation)
		date := dateOnlySP(local)

		if date.Before(dateOnlySP(cmp.StartDate)) || date.After(dateOnlySP(cmp.EndDate)) {
			out[i] = CatOutDate
			continue
		}
		// out_date do carve-out vale INDEPENDENTE de override — sem isso, uma
		// célula zerada mascarava o material fora do período dele (bug 2026-08).
		if carvedOutsidePeriod(date, p.MaterialID, rules) {
			out[i] = CatOutDate
			continue
		}
		entries = append(entries, entry{i: i, inWindow: inAnyWindow(local, date, p.MaterialID, rules, override)})
	}
	sort.SliceStable(entries, func(a, b int) bool {
		return plays[entries[a].i].DetectedAt.Before(plays[entries[b].i].DetectedAt)
	})

	// Passo 3 — dentro da faixa preenche a meta.
	inSlot := 0
	for _, e := range entries {
		if !e.inWindow {
			continue
		}
		if inSlot < n {
			out[e.i] = CatInSlot
			inSlot++
		} else {
			out[e.i] = CatBonus
		}
	}
	// Passo 4 — fora da faixa: segura o saldo enquanto a meta não fechou DENTRO
	// da faixa; depois disso é excedente (D2).
	for _, e := range entries {
		if e.inWindow {
			continue
		}
		if inSlot < n {
			out[e.i] = CatOutSlot
		} else {
			out[e.i] = CatBonus
		}
	}
	return out
}

// ruleCoversDay casa data+dia-da-semana de uma regra contra o dia da célula.
// (No SQL: `for_date BETWEEN r.start_date AND r.end_date` + máscara do DOW.)
func ruleCoversDay(r Rule, day time.Time) bool {
	if day.Before(dateOnlySP(r.StartDate)) || day.After(dateOnlySP(r.EndDate)) {
		return false
	}
	return (1<<int(day.Weekday()))&int(r.WeekdayMask) != 0
}

// carvedOutsidePeriod: o material é nomeado em alguma regra específica (carve-out)
// e o dia está fora do range de datas de TODAS elas.
func carvedOutsidePeriod(date time.Time, materialID uuid.UUID, rules []Rule) bool {
	carved, inPeriod := false, false
	for _, r := range rules {
		if len(r.MaterialIDs) == 0 || !containsUUID(r.MaterialIDs, materialID) {
			continue
		}
		carved = true
		if !date.Before(dateOnlySP(r.StartDate)) && !date.After(dateOnlySP(r.EndDate)) {
			inPeriod = true
		}
	}
	return carved && !inPeriod
}

// inAnyWindow: a tocada cai em alguma faixa que vale hoje, com tolerância.
// Com override, a faixa do override é a ÚNICA considerada. Sem override,
// material carve-out é julgado só pelas regras que o nomeiam; material comum,
// só pelas regras gerais.
func inAnyWindow(local, date time.Time, materialID uuid.UUID, rules []Rule, override *Override) bool {
	tod := local.Hour()*3600 + local.Minute()*60 + local.Second()
	within := func(ts, te time.Time) bool {
		s := ts.Hour()*3600 + ts.Minute()*60 + ts.Second()
		e := te.Hour()*3600 + te.Minute()*60 + te.Second()
		return tod >= s-SlotToleranceSeconds && tod <= e+SlotToleranceSeconds
	}
	if override != nil {
		return within(override.TimeStart, override.TimeEnd)
	}
	carved := false
	for _, r := range rules {
		if len(r.MaterialIDs) > 0 && containsUUID(r.MaterialIDs, materialID) {
			carved = true
			break
		}
	}
	for _, r := range rules {
		specific := len(r.MaterialIDs) > 0
		if carved != specific {
			continue // carved usa só específicas; comum usa só gerais
		}
		if carved && !containsUUID(r.MaterialIDs, materialID) {
			continue
		}
		if !ruleCoversDay(r, date) {
			continue
		}
		if within(r.TimeStart, r.TimeEnd) {
			return true
		}
	}
	return false
}

func containsUUID(ids []uuid.UUID, id uuid.UUID) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}
