package catalog

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestDailySummary_EmptyCampaign(t *testing.T) {
	ctx, pool := newTestDB(t)

	cli, _ := NewClients(pool).Create(ctx, CreateClientInput{Name: "T"})
	cmp, _ := NewCampaigns(pool).Create(ctx, CreateCampaignInput{
		Name: "C", ClientID: cli.ID,
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
	})
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM campaigns WHERE id = $1", cmp.ID)
		pool.Exec(ctx, "DELETE FROM clients WHERE id = $1", cli.ID)
	})

	repo := NewDailySummary(pool)
	rows, err := repo.ListByCampaign(ctx, cmp.ID,
		time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	// Empty campaign (no rules, no detections) — view returns nothing for this campaign
	if len(rows) != 0 {
		t.Errorf("empty campaign: got %d rows, want 0", len(rows))
	}
}

// ---------------------------------------------------------- modelo de cota --

// summaryCell é a linha do resumo diário de UMA célula-dia, do jeito que as
// telas financeiras a leem.
type summaryCell struct {
	Expected, InSlot, Deficit, Bonus, OutSlot, OutDate int
}

func (c summaryCell) String() string {
	return fmt.Sprintf("expected=%d in_slot=%d deficit=%d bonus=%d out_slot=%d out_date=%d",
		c.Expected, c.InSlot, c.Deficit, c.Bonus, c.OutSlot, c.OutDate)
}

// readSummaryCell lê a célula-dia pelos DOIS objetos — a view daily_play_summary
// e a função daily_play_summary_for() — e exige que concordem antes de devolver.
//
// Por que os dois: a migration 0065 mudou a MESMA aritmética em dois lugares
// (a view e a cópia com pushdown de filtro). Se só um dos dois for atualizado,
// as telas divergem conforme o caminho de leitura — /detections lê a função,
// scripts e diagnósticos leem a view. scripts/sql/paridade-dps-function.sql faz
// essa checagem contra um clone de prod; aqui ela roda em cada fixture.
func readSummaryCell(t *testing.T, r *parityRun, station, typeID uuid.UUID, day time.Time) summaryCell {
	t.Helper()
	dayStr := day.Format("2006-01-02")

	var fromView, fromFn summaryCell
	scan := func(query string, dst *summaryCell) {
		t.Helper()
		err := r.f.pool.QueryRow(r.f.ctx, query, r.campID, station, typeID, dayStr).Scan(
			&dst.Expected, &dst.InSlot, &dst.Deficit, &dst.Bonus, &dst.OutSlot, &dst.OutDate)
		require.NoError(t, err)
	}
	scan(`SELECT expected, in_slot, deficit, bonus, out_slot, out_date
	      FROM daily_play_summary
	      WHERE campaign_id = $1 AND station_id = $2 AND type_id = $3 AND for_date = $4::date`,
		&fromView)
	scan(`SELECT expected, in_slot, deficit, bonus, out_slot, out_date
	      FROM daily_play_summary_for($4::date, $4::date, ARRAY[$1::uuid])
	      WHERE station_id = $2 AND type_id = $3`,
		&fromFn)

	require.Equalf(t, fromView, fromFn,
		"view e daily_play_summary_for() divergiram na mesma célula-dia:\n  view = %s\n  fn   = %s",
		fromView, fromFn)
	return fromView
}

// TestDailySummary_QuotaModel prova a aritmética que a migration 0065 instalou,
// ponta a ponta: as tocadas entram cruas, o categorizador de produção
// (RecategorizeForCampaign, o mesmo pipeline do reconciler e do backfill)
// assenta a célula-dia, e o resumo diário é lido pelos dois objetos.
//
// As duas regras novas (spec 2026-08-14, D3 + §2):
//
//	deficit = GREATEST(0, expected - in_slot)   — out_slot NÃO abate o contrato
//	bonus   = COUNT(category = 'bonus')          — o categorizador é a fonte única
//
// O primeiro caso é o exemplo 1 do dono e é exatamente o que o modelo ANTIGO
// errava: com `deficit = expected - in_slot - out_slot` ele dava deficit=0 —
// um dia tocado metade fora do horário contratado aparecia como cumprido.
func TestDailySummary_QuotaModel(t *testing.T) {
	f := newParityFixture(t)
	rules := NewDistributionRules(f.pool)

	cases := []struct {
		name      string
		n         int16
		overrides []parityOverride
		times     []time.Time
		want      summaryCell
	}{
		{
			// Exemplo 1 do dono. Faixa 10:00-12:00, N=2: uma tocada dentro
			// preenche uma vaga; a das 03:00 é out_slot (in_slot < N) e não
			// fecha nada — sobra 1 de déficit. Modelo antigo: deficit=0.
			name:  "N=2, 1 dentro e 1 fora -> deficit 1 (out_slot nao abate)",
			n:     2,
			times: []time.Time{f.at(0, 10, 30, 0), f.at(0, 3, 0, 0)},
			want:  summaryCell{Expected: 2, InSlot: 1, Deficit: 1, Bonus: 0, OutSlot: 1},
		},
		{
			// Excedente DENTRO da faixa: a cota fecha nas duas primeiras
			// (ordem cronológica) e a terceira vira bonus pela categoria —
			// não mais pelo GREATEST(in_slot - expected) da view antiga.
			name:  "N=2, 3 dentro -> 2 in_slot + 1 bonus, deficit 0",
			n:     2,
			times: []time.Time{f.at(0, 10, 30, 0), f.at(0, 11, 0, 0), f.at(0, 11, 30, 0)},
			want:  summaryCell{Expected: 2, InSlot: 2, Deficit: 0, Bonus: 1},
		},
		{
			// Override zerado (caso da campanha 270): a regra do dia vale
			// (ppd=3) mas plays_expected=0 supersede → N=0. Sem cota pra
			// ocupar, TODA tocada é bonus — inclusive a das 03:00, que sem a
			// meta não tem contrato pra estar "fora" de.
			name:      "override plays_expected=0 -> tudo bonus, deficit 0",
			n:         3,
			overrides: []parityOverride{{typeID: f.tp1, station: f.stA, day: f.day(0), plays: 0, start: "10:00", end: "12:00"}},
			times:     []time.Time{f.at(0, 3, 0, 0), f.at(0, 10, 30, 0), f.at(0, 14, 0, 0)},
			want:      summaryCell{Expected: 0, InSlot: 0, Deficit: 0, Bonus: 3},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := parityScenario{
				campStart: f.day(-3), campEnd: f.day(3),
				rules: []CreateDistributionRuleInput{
					f.rule(f.tp1, []uuid.UUID{f.stA}, -3, 3, parityAllWeekdays, "10:00", "12:00", tc.n)},
				overrides: tc.overrides,
			}
			for _, at := range tc.times {
				s.plays = append(s.plays, parityPlay{at: at, mat: f.matA})
			}
			run := f.build(t, s)

			// As detections nascem com categoria placeholder (ver o build do
			// fixture); é o categorizador de produção que assenta a célula.
			require.NoError(t, rules.RecategorizeForCampaign(f.ctx, run.campID))

			got := readSummaryCell(t, run, f.stA, f.tp1, f.day(0))
			t.Logf("%s\n  got  %s\n  want %s", tc.name, got, tc.want)
			require.Equal(t, tc.want, got, "resumo diário da célula")
		})
	}
}
