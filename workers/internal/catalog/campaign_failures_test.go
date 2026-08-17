package catalog

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestCampaignFailures_Get_ExcludesFutureDays trava o bug reportado no drawer
// de /admin/station-failures ("sidebar que abre"): a view daily_play_summary
// emite uma linha de déficit pra CADA dia agendado até end_date (generate_series
// na migration 0041). Dias >= hoje ainda não chegaram — não podem contar como
// falha. Regra: "de ontem pra trás" (for_date < hoje em America/Sao_Paulo).
//
// Semeia uma regra cuja janela cruza "hoje" [hoje-3, hoje+3], sem detections
// (deficit = expected), e exige que Get NÃO devolva nenhum failure_day >= hoje.
func TestCampaignFailures_Get_ExcludesFutureDays(t *testing.T) {
	ctx, pool := newTestDB(t)

	// Cutoff canônico do jeito que a query calcula (America/Sao_Paulo).
	var todayBR string
	if err := pool.QueryRow(ctx,
		`SELECT (now() AT TIME ZONE 'America/Sao_Paulo')::date::text`).Scan(&todayBR); err != nil {
		t.Fatalf("today BR: %v", err)
	}

	now := time.Now()
	startISO := now.AddDate(0, 0, -3).Format("2006-01-02")
	endISO := now.AddDate(0, 0, 3).Format("2006-01-02")

	client := insSeedClient(t, ctx, pool, "FutureHorizon")
	camp := insSeedCampaign(t, ctx, pool, client, startISO, endISO)
	typeID, _ := insSeedTypeAndMaterial(t, ctx, pool, client, "Spot30")
	st := insSeedStation(t, ctx, pool, "RadioHorizon", 1000, 60, 40, 20, 50, 30, 30, 50, 20)
	// Todos os dias da semana, 2 tocadas/dia, SEM detections → deficit = expected.
	// MaterialIDs explícito ([]) em vez de nil: pgx encoda nil como NULL e a
	// coluna material_ids é NOT NULL. Array vazio = "vale p/ todos os materiais
	// do tipo" (regra sem carve-out).
	if _, err := NewDistributionRules(pool).Create(ctx, CreateDistributionRuleInput{
		CampaignID:  camp,
		TypeID:      typeID,
		StationIDs:  []uuid.UUID{st},
		MaterialIDs: []uuid.UUID{},
		StartDate:   parseDate(startISO),
		EndDate:     parseDate(endISO),
		WeekdayMask: 0b1111111,
		TimeStart:   "08:00",
		TimeEnd:     "20:00",
		PlaysPerDay: 2,
	}); err != nil {
		t.Fatalf("seed distribution_rule: %v", err)
	}

	res, err := NewCampaignFailures(pool).Get(ctx, camp)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	// Sanidade: a regra passada (hoje-3..ontem) tem que gerar déficit — senão o
	// teste passaria vazio à toa.
	var pastDays int
	for _, s := range res.Stations {
		for _, d := range s.FailureDays {
			pastDays++
			if d >= todayBR {
				t.Errorf("failure_day %q é hoje/futuro (>= %q) — dia que não chegou não é falha", d, todayBR)
			}
		}
	}
	if pastDays == 0 {
		t.Fatalf("esperava ao menos 1 dia de falha já encerrado (hoje-3..ontem), veio 0 — fixture não gerou déficit")
	}
	if res.Summary.TotalFailureDays != pastDays {
		t.Errorf("Summary.TotalFailureDays = %d, esperado %d (só dias encerrados)", res.Summary.TotalFailureDays, pastDays)
	}

	// ── ListForDate Q3: o agregado da vida-toda que alimenta o card diário
	// também tem que cortar o futuro. Janela [hoje-3, hoje+3], 2 tocadas/dia,
	// zero plays → o agregado encerrado é (hoje-3,hoje-2,hoje-1) = 3 dias × 2 = 6.
	repo := NewCampaignFailures(pool)
	daily, err := repo.ListForDate(ctx, now.AddDate(0, 0, -1))
	if err != nil {
		t.Fatalf("ListForDate: %v", err)
	}
	var found bool
	for _, c := range daily.Campaigns {
		if c.Campaign.ID != camp {
			continue
		}
		found = true
		for _, s := range c.Stations {
			if s.Deficit > 6 {
				t.Errorf("ListForDate agregado déficit=%d inclui dias futuros (esperado 6 = só hoje-3..ontem)", s.Deficit)
			}
		}
	}
	if !found {
		t.Fatalf("campanha semeada não apareceu em ListForDate(ontem)")
	}

	// ── ListHistorical: exercita as CTEs Q1/Q2 (SQL concatenado com o const).
	// Paginado sobre todas as campanhas do DB de teste, então só exigimos que
	// rode sem erro (valida a concatenação) — a asserção de valor fica no drawer.
	if _, err := repo.ListHistorical(ctx, 1, 200); err != nil {
		t.Fatalf("ListHistorical: %v", err)
	}
}

// ─── Quebra do déficit em off_slot × absent (Task 8 / decisão D7) ──────────
//
// Desde a migration 0065 o déficit é GREATEST(0, expected - in_slot): out_slot
// não abate mais o contrato. Isso faz um dia inteiro tocado FORA da faixa —
// antes "cumprido" — virar falha. Como o painel alimenta o PDF de cobrança
// enviado à emissora, o déficit é quebrado em dois:
//
//	deficit_off_slot = LEAST(deficit, out_slot)          — tocou, hora errada
//	deficit_absent   = GREATEST(0, deficit - out_slot)   — não tocou nada
//
// A quebra é POR CÉLULA-DIA e só depois somada (ver TestCampaignFailures_
// DeficitSplit_MultiDay, que é o contraexemplo de aplicar sobre os totais).

// cfDay descreve uma célula-dia do fixture: quantas tocadas a regra programa
// naquele dia e quantas detections caem em cada categoria.
type cfDay struct {
	daysAgo int // >= 1: o horizonte de falha só conta dias JÁ ENCERRADOS
	plays   int // plays_per_day da regra daquele dia (0 = sem regra)
	inSlot  int
	outSlot int
}

// cfSeedFailureCase monta client+campanha+tipo+material+emissora e, pra cada
// dia, uma regra de 1 dia + as detections nas categorias pedidas. Devolve os
// ids e a linha da emissora em Get(campanha).
//
// As datas saem de "hoje" em America/Sao_Paulo (o frame que failureHorizonClause
// usa), não do TZ da máquina.
func cfSeedFailureCase(t *testing.T, ctx context.Context, pool *pgxpool.Pool,
	label string, days []cfDay) (uuid.UUID, uuid.UUID, CampaignFailureStation) {
	t.Helper()
	if len(days) == 0 {
		t.Fatalf("cfSeedFailureCase(%s): fixture vazio", label)
	}

	var todayBR string
	if err := pool.QueryRow(ctx,
		`SELECT (now() AT TIME ZONE 'America/Sao_Paulo')::date::text`).Scan(&todayBR); err != nil {
		t.Fatalf("today BR: %v", err)
	}
	today := parseDate(todayBR)

	minAgo, maxAgo := days[0].daysAgo, days[0].daysAgo
	for _, d := range days {
		if d.daysAgo < 1 {
			t.Fatalf("cfSeedFailureCase(%s): daysAgo=%d — dia não encerrado não é falha", label, d.daysAgo)
		}
		if d.daysAgo > maxAgo {
			maxAgo = d.daysAgo
		}
		if d.daysAgo < minAgo {
			minAgo = d.daysAgo
		}
	}
	startISO := today.AddDate(0, 0, -maxAgo).Format("2006-01-02")
	endISO := today.AddDate(0, 0, -minAgo).Format("2006-01-02")

	client := insSeedClient(t, ctx, pool, "CF-"+label)
	camp := insSeedCampaign(t, ctx, pool, client, startISO, endISO)
	typeID, matID := insSeedTypeAndMaterial(t, ctx, pool, client, "CF-"+label)
	st := insSeedStation(t, ctx, pool, "CF-"+label, 1000, 60, 40, 20, 50, 30, 30, 50, 20)

	for _, d := range days {
		iso := today.AddDate(0, 0, -d.daysAgo).Format("2006-01-02")
		if d.plays > 0 {
			// Uma regra por dia (start=end): a CTE `expected` soma plays_per_day
			// por (campanha, tipo, emissora, dia), então regras de 1 dia não se
			// misturam entre si.
			insSeedDistributionRule(t, ctx, pool, camp, typeID, st, iso, iso, 0b1111111, "08:00", "20:00", d.plays)
		}
		for i := 0; i < d.inSlot; i++ {
			insSeedDetection(t, ctx, pool, camp, matID, st, "in_slot", iso)
		}
		for i := 0; i < d.outSlot; i++ {
			insSeedDetection(t, ctx, pool, camp, matID, st, "out_slot", iso)
		}
	}

	res, err := NewCampaignFailures(pool).Get(ctx, camp)
	if err != nil {
		t.Fatalf("Get(%s): %v", label, err)
	}
	for _, s := range res.Stations {
		if s.Station.ID == st {
			// O drill-in só devolve emissora com dia de falha — se chegou aqui,
			// há déficit. Invariante global, em todo caso:
			if s.DeficitOffSlot+s.DeficitAbsent != s.Deficit {
				t.Errorf("%s: off_slot(%d) + absent(%d) != deficit(%d)",
					label, s.DeficitOffSlot, s.DeficitAbsent, s.Deficit)
			}
			// Summary é a soma das linhas — a mesma invariante tem que valer lá.
			if res.Summary.TotalDeficitOffSlot+res.Summary.TotalDeficitAbsent != res.Summary.TotalDeficit {
				t.Errorf("%s: summary off_slot(%d) + absent(%d) != total_deficit(%d)",
					label, res.Summary.TotalDeficitOffSlot, res.Summary.TotalDeficitAbsent, res.Summary.TotalDeficit)
			}
			return camp, st, s
		}
	}
	t.Fatalf("%s: emissora semeada não apareceu em Get — fixture não gerou déficit?", label)
	return camp, st, CampaignFailureStation{}
}

// cfAssert confere o trio (deficit, off_slot, absent) de uma linha.
func cfAssert(t *testing.T, label string, got CampaignFailureStation, deficit, offSlot, absent int) {
	t.Helper()
	if got.Deficit != deficit || got.DeficitOffSlot != offSlot || got.DeficitAbsent != absent {
		t.Errorf("%s: deficit=%d off_slot=%d absent=%d; esperado %d/%d/%d",
			label, got.Deficit, got.DeficitOffSlot, got.DeficitAbsent, deficit, offSlot, absent)
	}
}

// Caso canônico: programado 3, 1 no horário, 1 fora → déficit 2, metade com
// veiculação por trás (off_slot) e metade silêncio puro (absent).
func TestCampaignFailures_DeficitSplit_PartialOffSlot(t *testing.T) {
	ctx, pool := newTestDB(t)
	camp, st, row := cfSeedFailureCase(t, ctx, pool, "partial",
		[]cfDay{{daysAgo: 2, plays: 3, inSlot: 1, outSlot: 1}})
	cfAssert(t, "Get", row, 2, 1, 1)

	// ── Q3 do ListForDate: SQL separado, mesma quebra. Fixture de 1 dia, então
	// o agregado do período inteiro coincide com o do dia.
	var todayBR string
	if err := pool.QueryRow(ctx,
		`SELECT (now() AT TIME ZONE 'America/Sao_Paulo')::date::text`).Scan(&todayBR); err != nil {
		t.Fatalf("today BR: %v", err)
	}
	day := parseDate(todayBR).AddDate(0, 0, -2)
	daily, err := NewCampaignFailures(pool).ListForDate(ctx, day)
	if err != nil {
		t.Fatalf("ListForDate: %v", err)
	}
	var found bool
	for _, c := range daily.Campaigns {
		if c.Campaign.ID != camp {
			continue
		}
		for _, s := range c.Stations {
			if s.Station.ID != st {
				continue
			}
			found = true
			cfAssert(t, "ListForDate Q3", s, 2, 1, 1)
		}
	}
	if !found {
		t.Fatalf("campanha/emissora semeada não apareceu em ListForDate")
	}
	// O summary do dia soma as linhas — invariante tem que sobreviver à soma
	// sobre TODAS as campanhas do DB de teste, não só a nossa.
	if daily.Summary.TotalDeficitOffSlot+daily.Summary.TotalDeficitAbsent != daily.Summary.TotalDeficit {
		t.Errorf("ListForDate summary: off_slot(%d) + absent(%d) != total_deficit(%d)",
			daily.Summary.TotalDeficitOffSlot, daily.Summary.TotalDeficitAbsent, daily.Summary.TotalDeficit)
	}

	// ── ListHistorical: terceiro SQL. Paginado sobre o DB inteiro e ordenado
	// por impacto, então varre as páginas até achar a campanha semeada.
	repo := NewCampaignFailures(pool)
	var hist *CampaignHistoricalRow
	for page := 1; ; page++ {
		res, err := repo.ListHistorical(ctx, page, 200)
		if err != nil {
			t.Fatalf("ListHistorical p%d: %v", page, err)
		}
		for i := range res.Campaigns {
			r := res.Campaigns[i]
			if r.TotalDeficitOffSlot+r.TotalDeficitAbsent != r.TotalDeficit {
				t.Errorf("ListHistorical %q: off_slot(%d) + absent(%d) != total_deficit(%d)",
					r.Campaign.Name, r.TotalDeficitOffSlot, r.TotalDeficitAbsent, r.TotalDeficit)
			}
			if r.Campaign.ID == camp {
				hist = &res.Campaigns[i]
			}
		}
		if hist != nil || page*200 >= res.Total || len(res.Campaigns) == 0 {
			break
		}
	}
	if hist == nil {
		t.Fatalf("campanha semeada não apareceu em ListHistorical")
	}
	if hist.TotalDeficit != 2 || hist.TotalDeficitOffSlot != 1 || hist.TotalDeficitAbsent != 1 {
		t.Errorf("ListHistorical: deficit=%d off_slot=%d absent=%d; esperado 2/1/1",
			hist.TotalDeficit, hist.TotalDeficitOffSlot, hist.TotalDeficitAbsent)
	}
}

// Sem nenhuma tocada fora da faixa, o déficit inteiro é ausência — é o único
// caso em que a emissora pode ser cobrada por não ter veiculado.
func TestCampaignFailures_DeficitSplit_NoOutSlot(t *testing.T) {
	ctx, pool := newTestDB(t)
	_, _, row := cfSeedFailureCase(t, ctx, pool, "absent",
		[]cfDay{{daysAgo: 2, plays: 3, inSlot: 1, outSlot: 0}})
	cfAssert(t, "out_slot=0", row, 2, 0, 2)
}

// out_slot maior que o déficit: off_slot satura no déficit (não pode passar) e
// absent zera. O excedente de out_slot não vira crédito negativo.
func TestCampaignFailures_DeficitSplit_OutSlotExceedsDeficit(t *testing.T) {
	ctx, pool := newTestDB(t)
	_, _, row := cfSeedFailureCase(t, ctx, pool, "capped",
		[]cfDay{{daysAgo: 2, plays: 3, inSlot: 1, outSlot: 5}})
	cfAssert(t, "out_slot > deficit", row, 2, 2, 0)
}

// MUDANÇA DE COMPORTAMENTO que motivou a Task 8: a emissora tocou a cota
// INTEIRA, só que toda fora da faixa contratada.
//
//	antes de 0065: deficit = GREATEST(0, 2 - 0 - 2) = 0 → não era falha
//	depois de 0065: deficit = GREATEST(0, 2 - 0)     = 2 → é falha
//
// O dia agora é falha (correto: o contrato de horário não foi cumprido), mas o
// déficit é 100% off_slot — nunca pode entrar no PDF como "não veiculou".
func TestCampaignFailures_DeficitSplit_FullQuotaOutsideWindow(t *testing.T) {
	ctx, pool := newTestDB(t)
	camp, st, row := cfSeedFailureCase(t, ctx, pool, "fullout",
		[]cfDay{{daysAgo: 2, plays: 2, inSlot: 0, outSlot: 2}})
	cfAssert(t, "cota inteira fora da faixa", row, 2, 2, 0)
	if row.DeficitAbsent != 0 {
		t.Errorf("emissora veiculou o dia todo — absent tem que ser 0, veio %d", row.DeficitAbsent)
	}

	// Prova direta do "antes": a fórmula velha (expected - in_slot - out_slot)
	// zerava neste mesmo fixture.
	var oldDeficit int
	if err := pool.QueryRow(ctx, `
SELECT COALESCE(SUM(GREATEST(0, dps.expected - dps.in_slot - dps.out_slot)), 0)::int
FROM daily_play_summary dps
WHERE dps.campaign_id = $1 AND dps.station_id = $2`, camp, st).Scan(&oldDeficit); err != nil {
		t.Fatalf("old-formula probe: %v", err)
	}
	if oldDeficit != 0 {
		t.Errorf("fórmula pré-0065 devia dar 0 neste fixture (cota tocada, só que fora), veio %d", oldDeficit)
	}
}

// Fixture multi-dia: prova a invariante somada E que a quebra é POR CÉLULA-DIA.
//
// Dias (plays/in_slot/out_slot) e a quebra correta por dia:
//
//	D5: 2/0/0 → deficit 2, off 0, absent 2   (silêncio total)
//	D4: 2/2/5 → deficit 0, off 0, absent 0   (cumpriu; 5 fora sobrando)
//	D3: 3/1/1 → deficit 2, off 1, absent 1
//	D2: 4/0/9 → deficit 4, off 4, absent 0   (satura)
//	                total: deficit 8, off 5, absent 3
//
// Se LEAST/GREATEST fossem aplicados sobre os TOTAIS (deficit 8, out_slot 15)
// daria off=8 / absent=0 — as 5 tocadas fora do D4 desculpariam o silêncio do
// D5, e a emissora sairia absolvida de um dia em que não foi ao ar. É
// exatamente esse resultado que este teste rejeita.
func TestCampaignFailures_DeficitSplit_MultiDay(t *testing.T) {
	ctx, pool := newTestDB(t)
	_, _, row := cfSeedFailureCase(t, ctx, pool, "multiday", []cfDay{
		{daysAgo: 5, plays: 2, inSlot: 0, outSlot: 0},
		{daysAgo: 4, plays: 2, inSlot: 2, outSlot: 5},
		{daysAgo: 3, plays: 3, inSlot: 1, outSlot: 1},
		{daysAgo: 2, plays: 4, inSlot: 0, outSlot: 9},
	})
	cfAssert(t, "multi-dia", row, 8, 5, 3)
	if row.DeficitOffSlot == row.Deficit {
		t.Errorf("off_slot(%d) == deficit(%d): LEAST/GREATEST parecem ter sido aplicados sobre os TOTAIS, "+
			"não por célula-dia — out_slot de um dia está desculpando o silêncio de outro",
			row.DeficitOffSlot, row.Deficit)
	}
}

func TestIsBonified_NoExtras(t *testing.T) {
	if IsBonified(5, 0) {
		t.Errorf("zero extras should never be bonified")
	}
}

func TestIsBonified_ExtrasCoverDeficit(t *testing.T) {
	if !IsBonified(3, 3) {
		t.Errorf("extras == deficit should bonify")
	}
	if !IsBonified(2, 5) {
		t.Errorf("extras > deficit should bonify")
	}
}

func TestIsBonified_ExtrasInsufficient(t *testing.T) {
	if IsBonified(5, 3) {
		t.Errorf("extras < deficit should NOT bonify")
	}
}

func TestIsBonified_ZeroDeficit(t *testing.T) {
	// No deficit, irrelevant; explicit choice: not bonified because nothing
	// to compensate. UI only shows bonificada when something failed.
	if IsBonified(0, 5) {
		t.Errorf("zero deficit should not register as bonified")
	}
}
