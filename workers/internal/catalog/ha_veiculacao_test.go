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
// "retracted", "ignored", "audit_rejected" ou "ambiguous".
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
	case "audit_rejected", "ambiguous":
		evid = estado
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

	for _, estado := range []string{"retracted", "ignored", "audit_rejected", "ambiguous"} {
		hvTocada(t, ctx, pool, camp, mat, st, hvQuando("2026-10-05", "10:00"), estado)
	}
	ha, err := repo.HaVeiculacao(ctx, escopo)
	require.NoError(t, err)
	require.False(t, ha, "retratada, ignorada, audit_rejected e ambígua não são veiculação aprovada: o recat não as reclassifica")

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

// O predicado de dia só entra na consulta quando o dia foi pedido: com
// `$6::date IS NULL OR ...` um plano genérico não poda partições de
// detections/detection_campaigns (particionadas por detected_at).
func TestHaVeiculacaoSQL_SoTemOPredicadoDeDiaQuandoPedido(t *testing.T) {
	sem := montarHaVeiculacaoSQL(false, false)
	require.NotContains(t, sem, "$6")
	require.NotContains(t, sem, "$7")
	require.NotContains(t, sem, "IS NULL OR dc.detected_at", "nada de OR com parâmetro nulo sobre a coluna particionada")

	so1 := montarHaVeiculacaoSQL(true, false)
	require.Contains(t, so1, "dc.detected_at >= ($6::date")
	require.Contains(t, so1, "d.detected_at >= ($6::date")
	require.NotContains(t, so1, "$7")

	ambos := montarHaVeiculacaoSQL(true, true)
	require.Contains(t, ambos, "dc.detected_at <  (($7::date + 1)")
	require.Contains(t, ambos, "d.detected_at <  (($7::date + 1)")

	soAte := montarHaVeiculacaoSQL(false, true)
	require.Contains(t, soAte, "(($6::date + 1)", "sem DiaDe, o DiaAte é o $6")
	require.NotContains(t, soAte, "$7")

	for _, q := range []string{sem, so1, ambos, soAte} {
		require.Contains(t, q, ApprovedDetectionsFilter, "o filtro de conjunto aprovado entra sempre")
	}
}

// Material legado sem type_id: sem TipoIDs a tocada conta (o recorte não filtra
// por tipo); com TipoIDs não casa tipo nenhum.
func TestHaVeiculacao_MaterialSemTipo(t *testing.T) {
	ctx, pool := newTestDB(t)
	cli := insSeedClient(t, ctx, pool, "HV semtipo "+uuid.NewString()[:6])
	camp := insSeedCampaign(t, ctx, pool, cli, "2026-10-01", "2026-10-31")
	st := insSeedStationNoProfile(t, ctx, pool, "HV est "+uuid.NewString()[:6])
	tipo, mat := insSeedTypeAndMaterial(t, ctx, pool, cli, "hv-legado")
	_, err := pool.Exec(ctx, `UPDATE materials SET type_id = NULL WHERE id = $1`, mat)
	require.NoError(t, err)
	hvTocada(t, ctx, pool, camp, mat, st, hvQuando("2026-10-05", "10:00"), "")
	repo := NewDetections(pool)

	ha, err := repo.HaVeiculacao(ctx, EscopoVeiculacao{CampanhaIDs: []uuid.UUID{camp}})
	require.NoError(t, err)
	require.True(t, ha, "sem filtro de tipo, o material sem tipo conta")

	ha, err = repo.HaVeiculacao(ctx, EscopoVeiculacao{CampanhaIDs: []uuid.UUID{camp}, TipoIDs: []uuid.UUID{tipo}})
	require.NoError(t, err)
	require.False(t, ha, "com tipo pedido, material sem tipo não casa")
}

// PARIDADE com o recat real: HaVeiculacao é o corte que o recat usa. Mesma
// campanha, mesma mudança de regra, mesmo RecategorizeForCampaign:
//   - só tocadas fora do conjunto aprovado → HaVeiculacao false E o recat não
//     muda nenhuma categoria;
//   - uma tocada aprovada → HaVeiculacao true E o recat a reclassifica.
func TestHaVeiculacao_ParidadeComORecatReal(t *testing.T) {
	categorias := func(ctx context.Context, pool *pgxpool.Pool, camp uuid.UUID) map[uuid.UUID]string {
		rows, err := pool.Query(ctx, `SELECT detection_id, category FROM detection_campaigns WHERE campaign_id = $1`, camp)
		require.NoError(t, err)
		defer rows.Close()
		out := map[uuid.UUID]string{}
		for rows.Next() {
			var id uuid.UUID
			var c string
			require.NoError(t, rows.Scan(&id, &c))
			out[id] = c
		}
		require.NoError(t, rows.Err())
		return out
	}

	for _, caso := range []struct {
		nome        string
		estados     []string // um estado por tocada ("" = aprovada)
		querHa      bool
		querMudanca bool
	}{
		{"só retratadas e ignoradas", []string{"retracted", "ignored"}, false, false},
		{"só audit_rejected e ambíguas", []string{"audit_rejected", "ambiguous"}, false, false},
		{"uma aprovada entre as retratadas", []string{"retracted", ""}, true, true},
		{"só aprovada", []string{""}, true, true},
	} {
		t.Run(caso.nome, func(t *testing.T) {
			ctx, pool := newTestDB(t)
			cli := insSeedClient(t, ctx, pool, "HV paridade "+uuid.NewString()[:6])
			camp := insSeedCampaign(t, ctx, pool, cli, "2026-10-01", "2026-10-31")
			st := insSeedStationNoProfile(t, ctx, pool, "HV est "+uuid.NewString()[:6])
			tipo, mat := insSeedTypeAndMaterial(t, ctx, pool, cli, "hv-par")
			regras := NewDistributionRules(pool)
			regra := CreateDistributionRuleInput{
				CampaignID: camp, TypeID: tipo, StationIDs: []uuid.UUID{st}, MaterialIDs: []uuid.UUID{},
				Name: "Paridade", StartDate: parseDate("2026-10-01"), EndDate: parseDate("2026-10-31"),
				WeekdayMask: 127, TimeStart: "09:00", TimeEnd: "11:00", PlaysPerDay: 1,
			}
			criada, err := regras.Create(ctx, regra)
			require.NoError(t, err)

			// As tocadas entram com category 'in_slot' (dentro da faixa 09–11).
			for _, estado := range caso.estados {
				hvTocada(t, ctx, pool, camp, mat, st, hvQuando("2026-10-05", "10:00"), estado)
			}
			ha, err := NewDetections(pool).HaVeiculacao(ctx, EscopoVeiculacao{CampanhaIDs: []uuid.UUID{camp}})
			require.NoError(t, err)
			require.Equal(t, caso.querHa, ha)

			// Mudança de regra: a faixa passa a 14–18, e a tocada das 10h (aprovada)
			// deixa de estar dentro dela → vira out_slot no recat.
			regra.TimeStart, regra.TimeEnd = "14:00", "18:00"
			require.NoError(t, regras.Update(ctx, criada.ID, regra))
			antes := categorias(ctx, pool, camp)
			require.NoError(t, regras.RecategorizeForCampaign(ctx, camp))
			depois := categorias(ctx, pool, camp)

			if caso.querMudanca {
				require.NotEqual(t, antes, depois, "HaVeiculacao=true: o recat mexe em categoria")
			} else {
				require.Equal(t, antes, depois, "HaVeiculacao=false: o recat não mexe em nenhuma categoria")
			}
		})
	}
}
