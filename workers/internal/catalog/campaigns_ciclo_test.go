package catalog

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// Casos fixos da função pura, sem banco: o contrato das quatro regras.
func TestEstadoPeloCiclo_Regras(t *testing.T) {
	hoje := time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)
	d := func(offset int) time.Time { return hoje.AddDate(0, 0, offset) }
	for nome, c := range map[string]struct {
		estado      string
		inicio, fim int
		esperado    string
	}{
		"programada que ainda não começou fica":           {"programada", 1, 5, "programada"},
		"programada que começa hoje ativa":                {"programada", 0, 5, "ativa"},
		"programada com a janela toda no passado conclui": {"programada", -5, -1, "concluida"},
		"ativa dentro da janela fica":                     {"ativa", -3, 3, "ativa"},
		"ativa que termina hoje fica":                     {"ativa", -3, 0, "ativa"},
		"ativa que terminou ontem conclui":                {"ativa", -3, -1, "concluida"},
		"ativa com início futuro volta (DEMOTE)":          {"ativa", 1, 5, "programada"},
		"concluida com fim futuro volta a ativa":          {"concluida", -3, 3, "ativa"},
		"concluida que termina hoje volta a ativa":        {"concluida", -3, 0, "ativa"},
		"concluida com início futuro volta a programada":  {"concluida", 1, 5, "programada"},
		"concluida de verdade fica":                       {"concluida", -5, -1, "concluida"},
		"cancelada nunca sai do lugar":                    {"cancelada", -5, 5, "cancelada"},
	} {
		require.Equal(t, c.esperado, EstadoPeloCiclo(c.estado, d(c.inicio), d(c.fim), hoje), nome)
	}
}

// A hora do instante não conta: só a data de calendário.
func TestEstadoPeloCiclo_IgnoraAHora(t *testing.T) {
	hoje := time.Date(2026, 10, 29, 23, 59, 0, 0, time.UTC)
	inicio := time.Date(2026, 10, 29, 18, 0, 0, 0, time.UTC)
	fim := time.Date(2026, 10, 29, 1, 0, 0, 0, time.UTC)
	require.Equal(t, "ativa", EstadoPeloCiclo("programada", inicio, fim, hoje))
}

// Convergência: uma segunda rodada do agendador não muda mais nada.
func TestEstadoPeloCiclo_ConvergeEmUmaRodada(t *testing.T) {
	hoje := time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)
	for _, estado := range []string{"programada", "ativa", "concluida", "cancelada"} {
		for i := -4; i <= 4; i++ {
			for f := i; f <= 4; f++ {
				ini, fim := hoje.AddDate(0, 0, i), hoje.AddDate(0, 0, f)
				uma := EstadoPeloCiclo(estado, ini, fim, hoje)
				require.Equal(t, uma, EstadoPeloCiclo(uma, ini, fim, hoje), "%s %d..%d", estado, i, f)
			}
		}
	}
}

// A função pura reproduz o SQL: semeia uma campanha para cada combinação de
// estado gravado × início × fim (em relação a hoje, America/Sao_Paulo), roda o
// PromoteScheduledLifecycle REAL e compara o estado que ele deixou com o que a
// função previu. Se alguém mexer nas regras de data do SQL, este teste quebra.
func TestEstadoPeloCiclo_BateComOSQL(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewCampaigns(pool)

	cli, err := NewClients(pool).Create(ctx, CreateClientInput{Name: "ciclo-sql-" + uuid.NewString()})
	require.NoError(t, err)
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM campaigns WHERE client_id=$1`, cli.ID)
		pool.Exec(ctx, `DELETE FROM clients WHERE id=$1`, cli.ID)
	})

	// "hoje" é o do banco, no mesmo fuso do SQL — não o relógio do processo.
	hojeBanco := func() time.Time {
		var h time.Time
		require.NoError(t, pool.QueryRow(ctx, `SELECT (now() AT TIME ZONE 'America/Sao_Paulo')::date`).Scan(&h))
		return h
	}
	hoje := hojeBanco()

	offsets := []int{-5, -1, 0, 1, 5}
	type caso struct {
		id          uuid.UUID
		estado      string
		inicio, fim time.Time
		descricao   string
	}
	var casos []caso
	for _, estado := range []string{"programada", "ativa", "concluida", "cancelada"} {
		for _, i := range offsets {
			for _, f := range offsets {
				if f < i { // a constraint campaign_dates_valid exige fim ≥ início
					continue
				}
				ini, fim := hoje.AddDate(0, 0, i), hoje.AddDate(0, 0, f)
				casos = append(casos, caso{
					id: seedCampaignRaw(t, ctx, pool, cli.ID, estado, ini, fim), estado: estado, inicio: ini, fim: fim,
					descricao: fmt.Sprintf("%s início%+d fim%+d", estado, i, f),
				})
			}
		}
	}

	_, _, _, err = repo.PromoteScheduledLifecycle(ctx)
	require.NoError(t, err)
	if !hojeBanco().Equal(hoje) {
		t.Skip("o dia virou (America/Sao_Paulo) no meio do teste")
	}

	for _, c := range casos {
		var obtido string
		require.NoError(t, pool.QueryRow(ctx, `SELECT status FROM campaigns WHERE id=$1`, c.id).Scan(&obtido))
		require.Equal(t, EstadoPeloCiclo(c.estado, c.inicio, c.fim, hoje), obtido, c.descricao)
	}
	require.Greater(t, len(casos), 50, "a matriz não pode encolher em silêncio")
}
