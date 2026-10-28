package mcp

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/mcp/mcptest"
	"radiocheck/internal/postsale"
)

func TestNovaLeitura_TodosOsRepositorios(t *testing.T) {
	L := NovaLeitura(nil)
	require.NotNil(t, L.Clientes)
	require.NotNil(t, L.PMMTarget)
	require.NotNil(t, L.Campanhas)
	require.NotNil(t, L.MateriaisCampanha)
	require.NotNil(t, L.Regras)
	require.NotNil(t, L.Ajustes)
	require.NotNil(t, L.Precos)
	require.NotNil(t, L.Materiais)
	require.NotNil(t, L.TiposMaterial)
	require.NotNil(t, L.Emissoras)
	require.NotNil(t, L.Veiculacoes)
	require.NotNil(t, L.Grade)
	require.NotNil(t, L.Notificacoes)
	require.NotNil(t, L.Usuarios)
	require.NotNil(t, L.BoasVindas)
}

func TestFixturesDeCatalogo(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente Fixture")
	est := mcptest.Emissora(t, ctx, pool, "Rádio Fixture", 1000)
	tipo := mcptest.TipoMaterial(t, ctx, pool, "Spot fixture")
	mat := mcptest.Material(t, ctx, pool, cli, tipo, "Spot A")
	camp := mcptest.Campanha(t, ctx, pool, cli, "Campanha Fixture", "2026-10-01", "2026-10-31", est)
	mcptest.Vincular(t, ctx, pool, camp, mat, est)
	mcptest.Regra(t, ctx, pool, camp, tipo, est, "2026-10-01", "2026-10-31", 127, "06:00", "22:00", 2)
	mcptest.Preco(t, ctx, pool, camp, est, tipo, 100)
	mcptest.Veiculacao(t, ctx, pool, camp, mat, est, "in_slot", mcptest.Quando("2026-10-10", "10:00"))

	L := NovaLeitura(pool)
	rows, err := L.Grade.ListByCampaign(ctx, camp, mcptest.Dia("2026-10-10"), mcptest.Dia("2026-10-10"))
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.EqualValues(t, 2, rows[0].Expected)
	require.EqualValues(t, 1, rows[0].InSlot)
}

func TestNomesEmissoras_IdsRepetidosNaoQuebram(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	e1 := mcptest.Emissora(t, ctx, pool, "Rádio Um", 0)
	e2 := mcptest.Emissora(t, ctx, pool, "Rádio Dois", 0)
	var ids []uuid.UUID
	for i := 0; i < 600; i++ {
		ids = append(ids, e1, e2)
	}
	nomes, err := nomesEmissoras(ctx, NovaLeitura(pool), ids)
	require.NoError(t, err)
	require.Equal(t, map[uuid.UUID]string{e1: "Rádio Um", e2: "Rádio Dois"}, nomes)
}

func TestNovaLeitura_RepositoriosDeRelatorio(t *testing.T) {
	L := NovaLeitura(nil)
	require.NotNil(t, L.Insights)
	require.NotNil(t, L.Gerencial)
	require.NotNil(t, L.Assertividade)
	require.NotNil(t, L.MapaAoVivo)
	require.NotNil(t, L.FalhasEmissoras)
	require.NotNil(t, L.FalhasCampanhas)
	require.NotNil(t, L.FalhasPorDia)
	require.NotNil(t, L.PosVenda)
	require.Nil(t, L.Workers, "sem supervisor: emissoras_ao_vivo sai null")
	fixo := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	L.Agora = func() time.Time { return fixo }
	require.Equal(t, fixo, L.agora())
	require.WithinDuration(t, time.Now(), (&Leitura{}).agora(), time.Minute)
}

// TestFixturesDeRelatorio prova as fixtures de relatório contra o schema real
// (as tools das próximas tasks dependem delas).
func TestFixturesDeRelatorio(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente Relatório")
	est := mcptest.Emissora(t, ctx, pool, "Rádio Relatório", 1000)
	tipo := mcptest.TipoMaterial(t, ctx, pool, "Spot relatório")
	mat := mcptest.Material(t, ctx, pool, cli, tipo, "Spot R")
	camp := mcptest.Campanha(t, ctx, pool, cli, "Campanha Relatório", "2026-09-01", "2026-09-30", est)

	mcptest.PMMTarget(t, ctx, pool, cli, est, 250)
	mcptest.Queda(t, ctx, pool, est, mcptest.Quando("2026-09-10", "08:00"), 3600)
	mcptest.Cobertura(t, ctx, pool, est, "A1", 50, 75,
		mcptest.CidadeAlcance{IBGE: 3550308, Nome: "São Paulo", Km: 0},
		mcptest.CidadeAlcance{IBGE: 3518800, Nome: "Guarulhos", Km: 17.5})
	mcptest.Assertividade(t, ctx, pool, mcptest.Dia("2026-09-10"), camp, est, mat, 10, 1, 2)
	rascunho := mcptest.PosVenda(t, ctx, pool, cli, camp, "Rascunho", "2026-09-01", "2026-09-30", false, nil)
	enviado := mcptest.PosVenda(t, ctx, pool, cli, camp, "Enviado", "2026-09-01", "2026-09-30", true,
		&postsale.Payload{Version: 1, Title: "Enviado"},
		postsale.RecipientInput{Email: "a@cliente.teste", Name: "A"},
		postsale.RecipientInput{Email: "b@cliente.teste", Name: "B"})

	var pmm, quedas, cidades, auto int
	require.NoError(t, pool.QueryRow(ctx, `SELECT
		(SELECT pmm_target FROM client_station_pmm WHERE station_id = $1),
		(SELECT count(*) FROM stream_health_events WHERE station_id = $1 AND event_type = 'down'),
		(SELECT count(*) FROM station_coverage_cities WHERE station_id = $1),
		(SELECT sum(auto) FROM assertiveness_daily WHERE station_id = $1)`, est).Scan(&pmm, &quedas, &cidades, &auto))
	require.Equal(t, []int{250, 1, 2, 10}, []int{pmm, quedas, cidades, auto})

	L := NovaLeitura(pool)
	r, err := L.PosVenda.Get(ctx, rascunho)
	require.NoError(t, err)
	require.Equal(t, "draft", r.Status)
	r, err = L.PosVenda.Get(ctx, enviado)
	require.NoError(t, err)
	require.Equal(t, "sent", r.Status)
	dest, err := L.PosVenda.Recipients(ctx, enviado)
	require.NoError(t, err)
	require.Len(t, dest, 2) // ORDER BY email: a@ (1ª pessoa) e b@
	require.Equal(t, "sent", dest[0].EmailStatus)
	require.Equal(t, "sent", dest[1].EmailStatus, "enviado = email enviado para todos")
	require.NotNil(t, dest[0].OpenedAt, "só a 1ª pessoa abriu")
	require.Nil(t, dest[1].OpenedAt)
	require.Zero(t, dest[1].OpenCount)
}

// TestDBCatalogo_ZeraQuedas: stream_health_events não tem FK para stations,
// então o DBCatalogo a trunca explicitamente (senão as quedas vazam entre testes).
func TestDBCatalogo_ZeraQuedas(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	est := mcptest.Emissora(t, ctx, pool, "Rádio Queda", 0)
	mcptest.Queda(t, ctx, pool, est, mcptest.Quando("2026-09-10", "08:00"), 600)
	ctx, pool = mcptest.DBCatalogo(t)
	var n int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM stream_health_events`).Scan(&n))
	require.Zero(t, n)
}
