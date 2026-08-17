package categorizer

// AVISO — os testes de Categorize deste arquivo (e SÓ eles) ainda esperam o
// veredito "orphan". Não é sinal de que a categoria continua viva: Categorize é
// código morto em produção desde que o insert-path migrou pra Settle, e é o
// último escritor de CatOrphan que resta. O veredito atual em todo o resto do
// sistema é CatBonus. Estes testes existem só pra documentar o comportamento
// antigo enquanto a função não for deletada; não os copie para código novo, e
// não conclua deles que algum consumidor deve procurar por 'orphan'.
//
// Os testes de Settle, no mesmo pacote, são os que valem pra produção.

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// América/São Paulo é onde a campanha "vive" — fixo no plano.
var saoPaulo, _ = time.LoadLocation("America/Sao_Paulo")

func mkRule(startDay, endDay int, mask int16, ts, te string, plays int16) Rule {
	parseTime := func(hhmm string) time.Time {
		t, _ := time.Parse("15:04", hhmm)
		return t
	}
	return Rule{
		StartDate:   time.Date(2026, 6, startDay, 0, 0, 0, 0, saoPaulo),
		EndDate:     time.Date(2026, 6, endDay, 0, 0, 0, 0, saoPaulo),
		WeekdayMask: mask,
		TimeStart:   parseTime(ts),
		TimeEnd:     parseTime(te),
		PlaysPerDay: plays,
	}
}

func mkOverride(plays int16, ts, te string) *Override {
	parseTime := func(hhmm string) time.Time {
		t, _ := time.Parse("15:04", hhmm)
		return t
	}
	return &Override{
		PlaysExpected: plays,
		TimeStart:     parseTime(ts),
		TimeEnd:       parseTime(te),
	}
}

func TestCategorize_OutDate(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	// Detection em 1º de julho — fora da campanha
	got := Categorize(time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC), cmp, uuid.Nil, nil, nil)
	if got != "out_date" {
		t.Errorf("got %q, want out_date", got)
	}
}

func TestCategorize_Orphan_NoRules(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	got := Categorize(time.Date(2026, 6, 15, 10, 0, 0, 0, saoPaulo), cmp, uuid.Nil, nil, nil)
	if got != "orphan" {
		t.Errorf("got %q, want orphan", got)
	}
}

func TestCategorize_InSlot(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	// 10/06/2026 é quarta-feira (DOW=3 → bit 3 → 8)
	// Mask 62 (0111110) = seg-sex
	rules := []Rule{mkRule(1, 30, 62, "08:00", "10:00", 3)}
	// Detection às 09:00 BRT (UTC-3 → 12:00 UTC) numa quarta
	det := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	got := Categorize(det, cmp, uuid.Nil, rules, nil)
	if got != "in_slot" {
		t.Errorf("got %q, want in_slot", got)
	}
}

func TestCategorize_OutSlot(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	rules := []Rule{mkRule(1, 30, 62, "08:00", "10:00", 3)}
	// Detection às 14:00 BRT (17:00 UTC) — fora da faixa (>>15min de folga)
	det := time.Date(2026, 6, 10, 17, 0, 0, 0, time.UTC)
	got := Categorize(det, cmp, uuid.Nil, rules, nil)
	if got != "out_slot" {
		t.Errorf("got %q, want out_slot", got)
	}
}

func TestCategorize_InSlot_ToleranceBefore(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	rules := []Rule{mkRule(1, 30, 62, "06:00", "19:00", 3)}
	// 05:45 BRT — exatamente no limite da tolerância → in_slot
	det := time.Date(2026, 6, 10, 5, 45, 0, 0, saoPaulo)
	if got := Categorize(det, cmp, uuid.Nil, rules, nil); got != "in_slot" {
		t.Errorf("05:45 com rule 06:00-19:00: got %q, want in_slot", got)
	}
	// 05:44 BRT — 16 min antes, fora da tolerância → out_slot
	det = time.Date(2026, 6, 10, 5, 44, 0, 0, saoPaulo)
	if got := Categorize(det, cmp, uuid.Nil, rules, nil); got != "out_slot" {
		t.Errorf("05:44 com rule 06:00-19:00: got %q, want out_slot", got)
	}
}

func TestCategorize_InSlot_ToleranceAfter(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	rules := []Rule{mkRule(1, 30, 62, "06:00", "19:00", 3)}
	// 19:15 BRT — exatamente no limite → in_slot
	det := time.Date(2026, 6, 10, 19, 15, 0, 0, saoPaulo)
	if got := Categorize(det, cmp, uuid.Nil, rules, nil); got != "in_slot" {
		t.Errorf("19:15 com rule 06:00-19:00: got %q, want in_slot", got)
	}
	// 19:16 BRT — 16 min depois → out_slot
	det = time.Date(2026, 6, 10, 19, 16, 0, 0, saoPaulo)
	if got := Categorize(det, cmp, uuid.Nil, rules, nil); got != "out_slot" {
		t.Errorf("19:16 com rule 06:00-19:00: got %q, want out_slot", got)
	}
}

func TestCategorize_Orphan_WrongWeekday(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	// Mask 62 = seg-sex. Detection num sábado (06/06/2026, DOW=6 → bit 6 = 64)
	rules := []Rule{mkRule(1, 30, 62, "08:00", "10:00", 3)}
	det := time.Date(2026, 6, 6, 12, 0, 0, 0, saoPaulo) // sáb 12:00 BRT
	got := Categorize(det, cmp, uuid.Nil, rules, nil)
	if got != "orphan" {
		t.Errorf("got %q, want orphan (sábado fora da regra)", got)
	}
}

func TestCategorize_InSlot_BoundaryInclusive(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	rules := []Rule{mkRule(1, 30, 62, "08:00", "10:00", 3)}
	// Exatamente 08:00 BRT (11:00 UTC) — deve ser in_slot (boundary inclusive)
	det := time.Date(2026, 6, 10, 11, 0, 0, 0, time.UTC)
	got := Categorize(det, cmp, uuid.Nil, rules, nil)
	if got != "in_slot" {
		t.Errorf("got %q, want in_slot (08:00 é início da faixa)", got)
	}
	// 10:00:00 BRT — também in_slot (boundary inclusive)
	det2 := time.Date(2026, 6, 10, 13, 0, 0, 0, time.UTC)
	got2 := Categorize(det2, cmp, uuid.Nil, rules, nil)
	if got2 != "in_slot" {
		t.Errorf("got %q, want in_slot (10:00 é fim da faixa)", got2)
	}
}

// ─── Override tests (migration 0031) ──────────────────────────────────────

func TestCategorize_Override_InSlot(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	ov := mkOverride(2, "14:00", "16:00")
	det := time.Date(2026, 6, 10, 14, 30, 0, 0, saoPaulo)
	if got := Categorize(det, cmp, uuid.Nil, nil, ov); got != "in_slot" {
		t.Errorf("got %q, want in_slot (detection na faixa do override)", got)
	}
}

func TestCategorize_Override_OutSlot_OutsideWindow(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	ov := mkOverride(2, "14:00", "16:00")
	// 09:00 — fora da faixa do override (>>15min folga)
	det := time.Date(2026, 6, 10, 9, 0, 0, 0, saoPaulo)
	if got := Categorize(det, cmp, uuid.Nil, nil, ov); got != "out_slot" {
		t.Errorf("got %q, want out_slot", got)
	}
}

func TestCategorize_Override_IgnoresRules(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	// Rule 08:00-10:00 cobriria a detection às 09:00, mas o override
	// (14:00-16:00) deve mandar — rule é IGNORADA.
	rules := []Rule{mkRule(1, 30, 62, "08:00", "10:00", 3)}
	ov := mkOverride(2, "14:00", "16:00")
	det := time.Date(2026, 6, 10, 9, 0, 0, 0, saoPaulo)
	if got := Categorize(det, cmp, uuid.Nil, rules, ov); got != "out_slot" {
		t.Errorf("got %q, want out_slot (override deve sobrepor rule)", got)
	}
}

func TestCategorize_Override_CountZero_AlwaysOutSlot(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	// count=0 + faixa "14:00-16:00" — faixa é inerte. Qualquer detection
	// no dia → out_slot, mesmo dentro da faixa.
	ov := mkOverride(0, "14:00", "16:00")
	det := time.Date(2026, 6, 10, 15, 0, 0, 0, saoPaulo) // dentro da faixa
	if got := Categorize(det, cmp, uuid.Nil, nil, ov); got != "out_slot" {
		t.Errorf("count=0 dentro da faixa: got %q, want out_slot", got)
	}
}

func TestCategorize_Override_ToleranceBoundary(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	ov := mkOverride(1, "10:00", "12:00")
	// 09:45 — exatamente 15min antes → in_slot
	det := time.Date(2026, 6, 10, 9, 45, 0, 0, saoPaulo)
	if got := Categorize(det, cmp, uuid.Nil, nil, ov); got != "in_slot" {
		t.Errorf("09:45 override 10:00-12:00: got %q, want in_slot", got)
	}
	// 09:44 — 16min antes → out_slot
	det = time.Date(2026, 6, 10, 9, 44, 0, 0, saoPaulo)
	if got := Categorize(det, cmp, uuid.Nil, nil, ov); got != "out_slot" {
		t.Errorf("09:44 override 10:00-12:00: got %q, want out_slot", got)
	}
}

func TestCategorize_Override_OutOfDate_StillOutDate(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	ov := mkOverride(1, "10:00", "12:00")
	// Detection em 1º de julho — fora da campanha. out_date manda sobre override.
	det := time.Date(2026, 7, 1, 11, 0, 0, 0, saoPaulo)
	if got := Categorize(det, cmp, uuid.Nil, nil, ov); got != "out_date" {
		t.Errorf("got %q, want out_date (range da campanha tem prioridade)", got)
	}
}

// Regressão: pgx escaneia colunas DATE como time.Time em UTC à meia-noite.
// Antes do fix, o último dia da campanha caía como out_date porque o `date`
// (SP-midnight, 03:00Z) era considerado After do cmp.EndDate (UTC-midnight,
// 00:00Z). Bug confirmado em prod 2026-05-26 — 6 detections do dia 26 numa
// campanha terminando em 26 marcadas out_date.
func TestCategorize_LastDay_CampaignEndDateScannedAsUTC(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),  // como o pgx devolve
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC), // como o pgx devolve
	}
	// 30/06/2026 às 10:00 BRT — DENTRO da campanha (último dia, inclusivo)
	det := time.Date(2026, 6, 30, 10, 0, 0, 0, saoPaulo)
	got := Categorize(det, cmp, uuid.Nil, nil, nil)
	if got == "out_date" {
		t.Errorf("detection em 30/06 10:00 BRT (= end_date) marcada %q; o end_date é inclusivo, esperava != out_date", got)
	}
}

// Regressão: mesmo problema, mas no loop de rules — r.EndDate também vem
// em UTC do scan. Antes do fix, o último dia da regra caía em orphan/out_slot
// em vez de in_slot porque a regra era pulada (date.After(r.EndDate) = true).
func TestCategorize_LastDay_RuleEndDateScannedAsUTC(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
	}
	parseTime := func(hhmm string) time.Time {
		t, _ := time.Parse("15:04", hhmm)
		return t
	}
	// 30/06/2026 é terça-feira (DOW=2 → bit 2 → 4). Mask 62 = seg-sex inclui.
	rule := Rule{
		StartDate:   time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		EndDate:     time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
		WeekdayMask: 62,
		TimeStart:   parseTime("08:00"),
		TimeEnd:     parseTime("10:00"),
		PlaysPerDay: 3,
	}
	// Detection 09:00 BRT no último dia da rule — deve ser in_slot
	det := time.Date(2026, 6, 30, 9, 0, 0, 0, saoPaulo)
	got := Categorize(det, cmp, uuid.Nil, []Rule{rule}, nil)
	if got != "in_slot" {
		t.Errorf("detection no último dia da rule (30/06 09:00 BRT, dentro da faixa 08-10): got %q, want in_slot", got)
	}
}

// mkMatRule é como mkRule mas com material_ids preenchido (regra específica).
func mkMatRule(startDay, endDay int, mask int16, ts, te string, plays int16, mats ...uuid.UUID) Rule {
	r := mkRule(startDay, endDay, mask, ts, te, plays)
	r.MaterialIDs = mats
	return r
}

func TestCategorize_CarveOut_InSlot(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	m := uuid.New()
	// Regra específica de m: 1ª semana (1-7), seg-sex, 18-19h.
	rules := []Rule{mkMatRule(1, 7, 62, "18:00", "19:00", 1, m)}
	// m toca 02/06 (ter) 18:30 BRT (21:30 UTC) → dentro → in_slot
	det := time.Date(2026, 6, 2, 21, 30, 0, 0, time.UTC)
	if got := Categorize(det, cmp, m, rules, nil); got != "in_slot" {
		t.Errorf("got %q, want in_slot", got)
	}
}

func TestCategorize_CarveOut_OutSlot_WrongTime(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	m := uuid.New()
	rules := []Rule{mkMatRule(1, 7, 62, "18:00", "19:00", 1, m)}
	// m toca 02/06 (ter) 10:00 BRT — dentro da data/dia, fora da faixa → out_slot
	det := time.Date(2026, 6, 2, 13, 0, 0, 0, time.UTC)
	if got := Categorize(det, cmp, m, rules, nil); got != "out_slot" {
		t.Errorf("got %q, want out_slot", got)
	}
}

func TestCategorize_CarveOut_OutDate_OutsideRulePeriod(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	m := uuid.New()
	rules := []Rule{mkMatRule(1, 7, 62, "18:00", "19:00", 1, m)}
	// m toca 16/06 (semana 3) 18:30 BRT — fora do período da regra dele,
	// mas dentro da campanha → out_date (não orphan, não in_slot).
	det := time.Date(2026, 6, 16, 21, 30, 0, 0, time.UTC)
	if got := Categorize(det, cmp, m, rules, nil); got != "out_date" {
		t.Errorf("got %q, want out_date", got)
	}
}

func TestCategorize_CarveOut_IgnoresGeneralTypeRule(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	m := uuid.New()
	// Regra GERAL do tipo (07-19h o mês todo) + regra específica de m (1ª sem 18-19h).
	general := mkRule(1, 30, 62, "07:00", "19:00", 3) // material_ids vazio
	specific := mkMatRule(1, 7, 62, "18:00", "19:00", 1, m)
	rules := []Rule{general, specific}
	// m toca 16/06 (semana 3) 10:00 BRT. A regra geral cobriria (07-19h), MAS
	// m está carved-out → só a regra dele vale → fora do período → out_date.
	det := time.Date(2026, 6, 16, 13, 0, 0, 0, time.UTC)
	if got := Categorize(det, cmp, m, rules, nil); got != "out_date" {
		t.Errorf("got %q, want out_date (carve-out ignora regra geral)", got)
	}
}

func TestCategorize_NonCarvedMaterial_UsesGeneralRule(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	other := uuid.New() // material SEM regra específica
	specificForSomeoneElse := mkMatRule(1, 7, 62, "18:00", "19:00", 1, uuid.New())
	general := mkRule(1, 30, 62, "07:00", "19:00", 3)
	rules := []Rule{general, specificForSomeoneElse}
	// other toca 16/06 10:00 BRT — não está carved-out → regra geral vale → in_slot.
	det := time.Date(2026, 6, 16, 13, 0, 0, 0, time.UTC)
	if got := Categorize(det, cmp, other, rules, nil); got != "in_slot" {
		t.Errorf("got %q, want in_slot (material comum usa regra geral)", got)
	}
}

// Caso novo (spec 2026-07-13): material carved toca DENTRO do range da regra
// dele, mas num dia-da-semana sem meta (sábado, regra seg-sex). Está dentro do
// período contratado → dia extra → orphan (credita bônus), NÃO out_date.
func TestCategorize_CarveOut_InPeriodWrongWeekday_Orphan(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	m := uuid.New()
	// Regra específica de m: mês todo (1-30), seg-sex (mask 62), 08-22h.
	rules := []Rule{mkMatRule(1, 30, 62, "08:00", "22:00", 1, m)}
	// 06/06/2026 é SÁBADO (DOW=6). Dentro do range 1-30, mas fora do mask 62.
	det := time.Date(2026, 6, 6, 12, 0, 0, 0, saoPaulo)
	if got := Categorize(det, cmp, m, rules, nil); got != "orphan" {
		t.Errorf("got %q, want orphan (sábado dentro do período do material)", got)
	}
}

// Guarda-corpo: fora do range da regra do material continua out_date (não vira
// orphan). Distingue "dia extra dentro do período" de "fora do período".
func TestCategorize_CarveOut_OutsidePeriod_StaysOutDate(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	m := uuid.New()
	// Regra só na 1ª semana (1-7). Detecção no sábado 20/06 (semana 3) → fora
	// do range da regra → out_date.
	rules := []Rule{mkMatRule(1, 7, 62, "08:00", "22:00", 1, m)}
	det := time.Date(2026, 6, 20, 12, 0, 0, 0, saoPaulo) // sáb, fora do range 1-7
	if got := Categorize(det, cmp, m, rules, nil); got != "out_date" {
		t.Errorf("got %q, want out_date (fora do período do material)", got)
	}
}

// Limite do range (start_date/end_date são inclusivos): sábado (fora do mask
// seg-sex) caindo EXATAMENTE no start_date e no end_date do range da regra
// continua dentro do período → orphan, não out_date.
func TestCategorize_CarveOut_InPeriodBoundary_Orphan(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	m := uuid.New()
	// 06/06/2026 é SÁBADO (fora do mask 62 seg-sex).
	// Caso A: sábado == end_date do range da regra ([1,6]) → dentro (inclusivo) → orphan.
	rulesEnd := []Rule{mkMatRule(1, 6, 62, "08:00", "22:00", 1, m)}
	det := time.Date(2026, 6, 6, 12, 0, 0, 0, saoPaulo)
	if got := Categorize(det, cmp, m, rulesEnd, nil); got != "orphan" {
		t.Errorf("end_date boundary: got %q, want orphan", got)
	}
	// Caso B: sábado == start_date do range da regra ([6,20]) → dentro (inclusivo) → orphan.
	rulesStart := []Rule{mkMatRule(6, 20, 62, "08:00", "22:00", 1, m)}
	if got := Categorize(det, cmp, m, rulesStart, nil); got != "orphan" {
		t.Errorf("start_date boundary: got %q, want orphan", got)
	}
}
