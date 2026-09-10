package postsale

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/catalog"
)

// Cenário misto sobre o seedScenario canônico (campanha 01–30/06, regra de
// 01 a 05/06 com 2 tocadas/dia):
//
//	Radio Acima   → per_insertion, unit 10 → 10 in_slot + 3 bonus
//	Radio Devendo → per_insertion, unit 10 →  7 in_slot
//	Radio Exata   → consolidated, R$ 400/mês
//
// Daí saem os números que os testes abaixo conferem:
//
//	pacote        = 400 × 1 ciclo mensal           = 400
//	pago          = (10 + 7) × 10                  = 170
//	bonificação   = 3 × 10                         =  30
//	total         = 400 + 170 + 30                 = 600
const (
	mixUnitValue         = 10.0
	mixConsolidatedValue = 400.0

	wantValorEntregue = 570.0 // pacote + pago, SEM o bônus
	wantBonificacao   = 30.0  // só a parcela por-inserção
	wantTotal         = 600.0 // a soma, que é o que o CPM usa
	wantBonusCount    = int64(3)
)

// mixToday fixa o "hoje" do serviço. O valor consolidado é MENSAL e acumula por
// ciclo iniciado até hoje: sem fixar, o teste passaria em junho/2026 e
// multiplicaria o pacote por 1, 2, 3… conforme a data em que rodasse.
var mixToday = date(2026, 6, 30)

// seedMixedPricing dá pricing MISTO à campanha do seedScenario: duas emissoras
// por-inserção e uma consolidada. É a combinação que o /insights chama de "modo
// fornecedor" e onde mora o bug que este arquivo trava.
func seedMixedPricing(t *testing.T, ctx context.Context, pool *pgxpool.Pool, s scenario) {
	t.Helper()
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM campaign_station_type_pricing WHERE campaign_id = $1", s.CampaignID)
		pool.Exec(ctx, "DELETE FROM campaign_station_pricing WHERE campaign_id = $1", s.CampaignID)
	})

	for _, st := range []uuid.UUID{s.Acima, s.Devendo} {
		_, err := pool.Exec(ctx, `
			INSERT INTO campaign_station_pricing(campaign_id, station_id, mode, consolidated_value)
			VALUES ($1, $2, 'per_insertion', NULL)
			ON CONFLICT (campaign_id, station_id) DO UPDATE
			    SET mode = EXCLUDED.mode, consolidated_value = EXCLUDED.consolidated_value`,
			s.CampaignID, st)
		require.NoError(t, err, "seed pricing per_insertion")

		_, err = pool.Exec(ctx, `
			INSERT INTO campaign_station_type_pricing(campaign_id, station_id, type_id, unit_value)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (campaign_id, station_id, type_id) DO UPDATE
			    SET unit_value = EXCLUDED.unit_value`,
			s.CampaignID, st, s.TypeID, mixUnitValue)
		require.NoError(t, err, "seed unit_value")
	}

	_, err := pool.Exec(ctx, `
		INSERT INTO campaign_station_pricing(campaign_id, station_id, mode, consolidated_value)
		VALUES ($1, $2, 'consolidated', $3)
		ON CONFLICT (campaign_id, station_id) DO UPDATE
		    SET mode = EXCLUDED.mode, consolidated_value = EXCLUDED.consolidated_value`,
		s.CampaignID, s.Exata, mixConsolidatedValue)
	require.NoError(t, err, "seed pricing consolidated")
}

// buildMixedBlock semeia o cenário misto e devolve os KPIs do bloco.
func buildMixedBlock(t *testing.T, ctx context.Context, pool *pgxpool.Pool, s scenario) BlockKPIs {
	t.Helper()
	svc := newTestService(t, pool, nil, &fakeStore{})
	svc.todayFn = func() time.Time { return mixToday }

	p, err := svc.Build(ctx, SnapshotInput{
		ClientID: s.ClientID,
		Blocks: []BlockInput{{
			CampaignID: s.CampaignID,
			From:       date(2026, 6, 1),
			To:         date(2026, 6, 30),
		}},
	})
	require.NoError(t, err)
	require.Len(t, p.Campaigns, 1)
	return p.Campaigns[0].KPIs
}

// Em campanha MISTA o bônus das emissoras por-inserção precisa APARECER, e não
// ficar embutido no valor entregue.
//
// Antes de 2026-09-10 o pós-venda herdava um payload em que uma única emissora
// consolidada zerava a bonificação da campanha inteira, e o card sumia do
// documento do cliente. Medido no restore de prod, isso escondia R$ 15.781 numa
// campanha da TINTAS RENNER e R$ 9.895 numa da VERISURE.
func TestBuildBlock_MixedPricing_BonusIsSplitOutNotHidden(t *testing.T) {
	ctx, pool := newTestDB(t)
	seed := seedScenario(t, ctx, pool)
	seedMixedPricing(t, ctx, pool, seed)

	k := buildMixedBlock(t, ctx, pool, seed)

	require.True(t, k.Consolidated,
		"campanha mista tem emissora consolidada → Consolidated true (o flag sozinho NÃO decide o card)")
	require.InDelta(t, wantBonificacao, k.Bonificacao, 0.01,
		"bonificação = só a parcela por-inserção (3 bônus × R$ 10); a consolidada não tem preço por inserção")
	require.Equal(t, wantBonusCount, k.BonificacaoCount,
		"a contagem viaja no payload porque o gate de exibição do /insights depende dela")
	require.InDelta(t, wantValorEntregue, k.ValorEntregue, 0.01,
		"valor entregue = pacote + o que foi PAGO; o bônus saiu daqui pro card próprio")
}

// A repartição não pode inventar nem sumir com dinheiro: as duas parcelas
// somadas continuam sendo o total de antes, e é por isso que o CPM não se move.
func TestBuildBlock_MixedPricing_SplitPreservesTotalAndCPM(t *testing.T) {
	ctx, pool := newTestDB(t)
	seed := seedScenario(t, ctx, pool)
	seedMixedPricing(t, ctx, pool, seed)

	k := buildMixedBlock(t, ctx, pool, seed)

	require.InDelta(t, wantTotal, k.ValorEntregue+k.Bonificacao, 0.01,
		"as duas parcelas somadas são o total de sempre — repartição de exibição, não número novo")
	require.Positive(t, k.Impactos, "sem impactos o CPM não é verificável")
	require.InDelta(t, wantTotal/float64(k.Impactos)*1000, k.CPM, 0.01,
		"CPM = (valor entregue + bonificação) ÷ impactos × 1000")
}

// O CRITÉRIO DE ACEITE: o bloco do pós-venda tem que bater EXATAMENTE com o
// /insights filtrado no mesmo período.
//
// Vale por construção — o Build copia o insights.Compute sem recalcular nada —,
// e este teste existe pra que continue valendo: qualquer conta própria que
// alguém acrescente ao buildBlock abre uma segunda fonte de verdade pro mesmo
// número, e o cliente passa a ler no documento algo diferente do que o admin
// aprovou no dashboard. Compara campo a campo, com o MESMO `today` dos dois
// lados (o consolidado acumula por ciclo mensal iniciado até hoje).
func TestBuildBlock_MatchesInsightsForSamePeriod(t *testing.T) {
	ctx, pool := newTestDB(t)
	seed := seedScenario(t, ctx, pool)
	seedMixedPricing(t, ctx, pool, seed)

	k := buildMixedBlock(t, ctx, pool, seed)

	ins, err := catalog.NewInsights(pool).Compute(ctx, catalog.InsightsParams{
		ClientID:    seed.ClientID,
		CampaignIDs: []uuid.UUID{seed.CampaignID},
		From:        date(2026, 6, 1),
		To:          date(2026, 6, 30),
		StationIDs:  []uuid.UUID{},
		Today:       mixToday,
	})
	require.NoError(t, err)

	require.InDelta(t, ins.KPIs.Investido.Executado, k.ValorEntregue, 0.001, "valor entregue")
	require.InDelta(t, ins.KPIs.Bonificacao.Valor, k.Bonificacao, 0.001, "bonificação")
	require.Equal(t, ins.KPIs.Bonificacao.Count, k.BonificacaoCount, "contagem de bonificação")
	require.Equal(t, ins.KPIs.Impactos, k.Impactos, "impactos")
	require.InDelta(t, ins.KPIs.CPM, k.CPM, 0.001, "cpm")
	require.Equal(t, ins.KPIs.StationsCount, k.StationsCount, "emissoras")
	require.Equal(t, ins.Consolidated, k.Consolidated, "flag de consolidado")
}

// Campanha 100% consolidada continua sem bonificação — e o zero ali é ausência
// de PREÇO, não ausência de bônus: o pacote é pela emissora, não por inserção.
// É o caso em que o card deve mesmo sumir, e é o que separa "não há o que
// precificar" de "não houve bonificação".
func TestBuildBlock_AllConsolidated_HasNoPriceableBonus(t *testing.T) {
	ctx, pool := newTestDB(t)
	seed := seedScenario(t, ctx, pool)
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM campaign_station_pricing WHERE campaign_id = $1", seed.CampaignID)
	})
	for _, st := range []uuid.UUID{seed.Acima, seed.Devendo, seed.Exata} {
		_, err := pool.Exec(ctx, `
			INSERT INTO campaign_station_pricing(campaign_id, station_id, mode, consolidated_value)
			VALUES ($1, $2, 'consolidated', $3)
			ON CONFLICT (campaign_id, station_id) DO UPDATE
			    SET mode = EXCLUDED.mode, consolidated_value = EXCLUDED.consolidated_value`,
			seed.CampaignID, st, mixConsolidatedValue)
		require.NoError(t, err, "seed pricing consolidated")
	}

	k := buildMixedBlock(t, ctx, pool, seed)

	require.True(t, k.Consolidated)
	require.Zero(t, k.Bonificacao, "sem emissora por-inserção não há bônus precificável")
	require.Zero(t, k.BonificacaoCount,
		"a contagem também fica zerada — é ela que impede o card de aparecer com R$ 0,00 "+
			"afirmando ao cliente que não houve bonificação (as 3 tocadas de bônus seguem nos impactos)")
}
