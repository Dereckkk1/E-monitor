package catalog

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// hvQuando monta um instante em America/Sao_Paulo (dia AAAA-MM-DD, hora HH:MM).
func hvQuando(diaISO, hora string) time.Time {
	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		loc = time.FixedZone("BRT", -3*3600)
	}
	t, err := time.ParseInLocation("2006-01-02 15:04", diaISO+" "+hora, loc)
	if err != nil {
		panic(err)
	}
	return t
}

// hvTocada insere uma tocada com a projeção canônica. estado: "" (aprovada),
// "retracted", "ignored" ou "audit_rejected".
func hvTocada(t *testing.T, ctx context.Context, pool *pgxpool.Pool,
	campanha, material, emissora uuid.UUID, quando time.Time, estado string) uuid.UUID {
	t.Helper()
	var retr, ign *time.Time
	evid := "available"
	switch estado {
	case "":
	case "retracted":
		retr = &quando
	case "ignored":
		ign = &quando
	case "audit_rejected":
		evid = "audit_rejected"
	default:
		t.Fatalf("hvTocada: estado desconhecido %q", estado)
	}
	var id uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO detections (station_id, commercial_id, campaign_id, detected_at,
		                        match_start_offset_ms, match_end_offset_ms, confidence,
		                        hash_count, temporal_coverage, variant_used, rate_used,
		                        category, retracted_at, ignored_at, evidence_status)
		VALUES ($1, $2, $3, $4, 0, 30000, 0.95, 100, 0.85, 0, 0, 'in_slot', $5, $6, $7)
		RETURNING id`, emissora, material, campanha, quando, retr, ign, evid).Scan(&id))
	_, err := pool.Exec(ctx, `
		INSERT INTO detection_campaigns (detection_id, detected_at, campaign_id, commercial_id, category)
		VALUES ($1, $2, $3, $4, 'in_slot')`, id, quando, campanha, material)
	require.NoError(t, err)
	return id
}

func hvDia(s string) *time.Time { d := parseDate(s); return &d }

func TestHaVeiculacao_SemAncoraEhErro(t *testing.T) {
	ctx, pool := newTestDB(t)
	_, err := NewDetections(pool).HaVeiculacao(ctx, EscopoVeiculacao{TipoIDs: []uuid.UUID{uuid.New()}})
	require.Error(t, err, "só tipo/emissora/dia não ancoram o recorte")
}

func TestHaVeiculacao_CampanhaSemTocadaFalseComTocadaTrue(t *testing.T) {
	ctx, pool := newTestDB(t)
	cli := insSeedClient(t, ctx, pool, "HV camp "+uuid.NewString()[:6])
	camp := insSeedCampaign(t, ctx, pool, cli, "2026-10-01", "2026-10-31")
	st := insSeedStationNoProfile(t, ctx, pool, "HV est "+uuid.NewString()[:6])
	_, mat := insSeedTypeAndMaterial(t, ctx, pool, cli, "hv-mat")
	repo := NewDetections(pool)

	ha, err := repo.HaVeiculacao(ctx, EscopoVeiculacao{CampanhaIDs: []uuid.UUID{camp}})
	require.NoError(t, err)
	require.False(t, ha, "campanha sem tocada")

	hvTocada(t, ctx, pool, camp, mat, st, hvQuando("2026-10-05", "10:00"), "")
	ha, err = repo.HaVeiculacao(ctx, EscopoVeiculacao{CampanhaIDs: []uuid.UUID{camp}})
	require.NoError(t, err)
	require.True(t, ha, "campanha com tocada")

	ha, err = repo.HaVeiculacao(ctx, EscopoVeiculacao{CampanhaIDs: []uuid.UUID{uuid.New(), camp}})
	require.NoError(t, err)
	require.True(t, ha, "lista de campanhas: basta uma")
}

func TestHaVeiculacao_FiltraPorTipoEmissoraMaterialECliente(t *testing.T) {
	ctx, pool := newTestDB(t)
	cli := insSeedClient(t, ctx, pool, "HV filtros "+uuid.NewString()[:6])
	outroCli := insSeedClient(t, ctx, pool, "HV outro "+uuid.NewString()[:6])
	camp := insSeedCampaign(t, ctx, pool, cli, "2026-10-01", "2026-10-31")
	st := insSeedStationNoProfile(t, ctx, pool, "HV est A "+uuid.NewString()[:6])
	outraSt := insSeedStationNoProfile(t, ctx, pool, "HV est B "+uuid.NewString()[:6])
	tipo, mat := insSeedTypeAndMaterial(t, ctx, pool, cli, "hv-a")
	outroTipo, outroMat := insSeedTypeAndMaterial(t, ctx, pool, cli, "hv-b")
	hvTocada(t, ctx, pool, camp, mat, st, hvQuando("2026-10-05", "10:00"), "")
	repo := NewDetections(pool)

	casos := []struct {
		nome   string
		escopo EscopoVeiculacao
		quer   bool
	}{
		{"tipo certo", EscopoVeiculacao{CampanhaIDs: []uuid.UUID{camp}, TipoIDs: []uuid.UUID{tipo}}, true},
		{"tipo de outro material", EscopoVeiculacao{CampanhaIDs: []uuid.UUID{camp}, TipoIDs: []uuid.UUID{outroTipo}}, false},
		{"dois tipos, um casa", EscopoVeiculacao{CampanhaIDs: []uuid.UUID{camp}, TipoIDs: []uuid.UUID{outroTipo, tipo}}, true},
		{"emissora certa", EscopoVeiculacao{CampanhaIDs: []uuid.UUID{camp}, EmissoraIDs: []uuid.UUID{st}}, true},
		{"emissora sem tocada", EscopoVeiculacao{CampanhaIDs: []uuid.UUID{camp}, EmissoraIDs: []uuid.UUID{outraSt}}, false},
		{"material sem campanha: todas as do material", EscopoVeiculacao{MaterialID: &mat}, true},
		{"outro material", EscopoVeiculacao{MaterialID: &outroMat}, false},
		{"material + campanha que não o tem", EscopoVeiculacao{CampanhaIDs: []uuid.UUID{camp}, MaterialID: &outroMat}, false},
		{"cliente da campanha", EscopoVeiculacao{ClienteID: &cli}, true},
		{"outro cliente", EscopoVeiculacao{ClienteID: &outroCli}, false},
		{"cliente + emissora sem tocada", EscopoVeiculacao{ClienteID: &cli, EmissoraIDs: []uuid.UUID{outraSt}}, false},
		{"cliente + tipo + emissora", EscopoVeiculacao{ClienteID: &cli, TipoIDs: []uuid.UUID{tipo}, EmissoraIDs: []uuid.UUID{st}}, true},
	}
	for _, c := range casos {
		ha, err := repo.HaVeiculacao(ctx, c.escopo)
		require.NoError(t, err, c.nome)
		require.Equal(t, c.quer, ha, c.nome)
	}
}

func TestHaVeiculacao_DiaEmSaoPauloENaoEmUTC(t *testing.T) {
	ctx, pool := newTestDB(t)
	cli := insSeedClient(t, ctx, pool, "HV dia "+uuid.NewString()[:6])
	camp := insSeedCampaign(t, ctx, pool, cli, "2026-10-01", "2026-10-31")
	st := insSeedStationNoProfile(t, ctx, pool, "HV est "+uuid.NewString()[:6])
	_, mat := insSeedTypeAndMaterial(t, ctx, pool, cli, "hv-dia")
	// 23:30 SP do dia 10 = 02:30 UTC do dia 11.
	hvTocada(t, ctx, pool, camp, mat, st, hvQuando("2026-10-10", "23:30"), "")
	repo := NewDetections(pool)

	pergunta := func(de, ate string) bool {
		e := EscopoVeiculacao{CampanhaIDs: []uuid.UUID{camp}}
		if de != "" {
			e.DiaDe = hvDia(de)
		}
		if ate != "" {
			e.DiaAte = hvDia(ate)
		}
		ha, err := repo.HaVeiculacao(ctx, e)
		require.NoError(t, err)
		return ha
	}
	require.True(t, pergunta("2026-10-10", "2026-10-10"), "23:30 SP conta no dia 10")
	require.False(t, pergunta("2026-10-11", "2026-10-11"), "e não no dia 11, mesmo sendo 11 em UTC")
	require.False(t, pergunta("2026-10-09", "2026-10-09"))
	require.True(t, pergunta("2026-10-10", ""), "só DiaDe")
	require.False(t, pergunta("2026-10-11", ""), "DiaDe depois da tocada")
	require.True(t, pergunta("", "2026-10-10"), "só DiaAte")
	require.False(t, pergunta("", "2026-10-09"), "DiaAte antes da tocada")
	require.True(t, pergunta("2026-10-01", "2026-10-31"), "intervalo que a contém")

	// 00:10 SP do dia 12 = 03:10 UTC do dia 12: o outro lado da fronteira.
	hvTocada(t, ctx, pool, camp, mat, st, hvQuando("2026-10-12", "00:10"), "")
	require.True(t, pergunta("2026-10-12", "2026-10-12"))
	require.False(t, pergunta("2026-10-11", "2026-10-11"))
}

func TestHaVeiculacao_TocadaForaDoConjuntoAprovadoNaoConta(t *testing.T) {
	ctx, pool := newTestDB(t)
	cli := insSeedClient(t, ctx, pool, "HV aprov "+uuid.NewString()[:6])
	camp := insSeedCampaign(t, ctx, pool, cli, "2026-10-01", "2026-10-31")
	st := insSeedStationNoProfile(t, ctx, pool, "HV est "+uuid.NewString()[:6])
	_, mat := insSeedTypeAndMaterial(t, ctx, pool, cli, "hv-aprov")
	repo := NewDetections(pool)
	escopo := EscopoVeiculacao{CampanhaIDs: []uuid.UUID{camp}}

	for _, estado := range []string{"retracted", "ignored", "audit_rejected"} {
		hvTocada(t, ctx, pool, camp, mat, st, hvQuando("2026-10-05", "10:00"), estado)
	}
	ha, err := repo.HaVeiculacao(ctx, escopo)
	require.NoError(t, err)
	require.False(t, ha, "retratada, ignorada e audit_rejected não são veiculação aprovada: o recat não as reclassifica")

	hvTocada(t, ctx, pool, camp, mat, st, hvQuando("2026-10-05", "11:00"), "")
	ha, err = repo.HaVeiculacao(ctx, escopo)
	require.NoError(t, err)
	require.True(t, ha, "uma aprovada basta")
}

func TestHaVeiculacao_ProjecaoSecundariaContaNaCampanhaDela(t *testing.T) {
	ctx, pool := newTestDB(t)
	cli := insSeedClient(t, ctx, pool, "HV fan-out "+uuid.NewString()[:6])
	base := insSeedCampaign(t, ctx, pool, cli, "2026-10-01", "2026-10-31")
	secundaria := insSeedCampaign(t, ctx, pool, cli, "2026-10-01", "2026-10-31")
	st := insSeedStationNoProfile(t, ctx, pool, "HV est "+uuid.NewString()[:6])
	_, mat := insSeedTypeAndMaterial(t, ctx, pool, cli, "hv-fan")
	quando := hvQuando("2026-10-05", "10:00")
	id := hvTocada(t, ctx, pool, base, mat, st, quando, "")
	// Multi-atribuição (F-119): a MESMA tocada também é projeção da secundária.
	_, err := pool.Exec(ctx, `INSERT INTO detection_campaigns (detection_id, detected_at, campaign_id, commercial_id, category)
		VALUES ($1, $2, $3, $4, 'bonus')`, id, quando, secundaria, mat)
	require.NoError(t, err)

	ha, err := NewDetections(pool).HaVeiculacao(ctx, EscopoVeiculacao{CampanhaIDs: []uuid.UUID{secundaria}})
	require.NoError(t, err)
	require.True(t, ha, "o recat escopa pela projeção, não pela tocada-base")
}
