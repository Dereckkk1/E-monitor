package catalog

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// As três queries de campanha reescritas pro filtro de carteira
// realmente EXECUTAM no Postgres (sintaxe + encoding pgx de []uuid.UUID / nil)
// e honram o contrato nil = sem filtro, slice vazio = nenhuma linha.
func TestCampaigns_ClientScopeArrayFilters(t *testing.T) {
	ctx, pool := newTestDB(t)
	c := NewCampaigns(pool)

	// --- ListFiltered ---
	allNil, err := c.ListFiltered(ctx, nil, nil)
	require.NoError(t, err, "ListFiltered(nil, nil) — admin sem filtro")

	withStatus, err := c.ListFiltered(ctx, []string{"ativa"}, nil)
	require.NoError(t, err, "ListFiltered(status, nil)")
	require.LessOrEqual(t, len(withStatus), len(allNil))

	empty, err := c.ListFiltered(ctx, nil, []uuid.UUID{})
	require.NoError(t, err, "ListFiltered(nil, []) — carteira vazia")
	require.Empty(t, empty, "carteira vazia tem que falhar fechada (0 linhas)")

	unknown, err := c.ListFiltered(ctx, nil, []uuid.UUID{uuid.New()})
	require.NoError(t, err, "ListFiltered(nil, [id inexistente])")
	require.Empty(t, unknown)

	// --- ListPaged ---
	_, totalNil, err := c.ListPaged(ctx, "", "", nil, nil, 1, 20)
	require.NoError(t, err, "ListPaged(nil) — admin sem filtro")

	pgEmpty, totalEmpty, err := c.ListPaged(ctx, "", "", []uuid.UUID{}, nil, 1, 20)
	require.NoError(t, err, "ListPaged([]) — carteira vazia")
	require.Empty(t, pgEmpty)
	require.Zero(t, totalEmpty, "COUNT também tem que respeitar a carteira vazia")

	// --- FinancialsByCampaign ---
	finNil, err := c.FinancialsByCampaign(ctx, nil, parseDate("2026-07-15"))
	require.NoError(t, err, "FinancialsByCampaign(nil) — admin sem filtro")

	finEmpty, err := c.FinancialsByCampaign(ctx, []uuid.UUID{}, parseDate("2026-07-15"))
	require.NoError(t, err, "FinancialsByCampaign([]) — carteira vazia")
	require.Empty(t, finEmpty)

	t.Logf("admin: ListFiltered=%d ListPaged.total=%d Financials=%d",
		len(allNil), totalNil, len(finNil))
}
