package catalog

import (
	"testing"
	"time"

	"github.com/google/uuid"
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
