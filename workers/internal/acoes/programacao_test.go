package acoes

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/catalog"
	"radiocheck/internal/mcp/mcptest"
)

func (c *cenario) entradaProgramacao() ProgramarCampanhaEntrada {
	return ProgramarCampanhaEntrada{
		Campanha: c.entradaCampanha(""),
		Regras: []EntradaRegra{{
			TipoID: c.tipoA, EmissoraIDs: []uuid.UUID{c.emissora}, Nome: "Janeiro",
			Inicio: dia("2027-01-11"), Fim: dia("2027-01-15"), MascaraDias: 62,
			HoraInicio: "07:00", HoraFim: "19:00", InsercoesPorDia: 2,
		}},
		Precos: []catalog.EntradaPreco{{
			StationID: c.emissora, Mode: catalog.PricingModePerInsertion,
			PerType: []catalog.TypePricing{{TypeID: c.tipoA, UnitValue: 56.25}},
		}},
	}
}

func (c *cenario) contarProgramacao(t *testing.T) [3]int {
	t.Helper()
	var n [3]int
	require.NoError(t, c.pool.QueryRow(c.ctx, `SELECT
		(SELECT count(*) FROM campaigns WHERE name = 'Verão 2027'),
		(SELECT count(*) FROM distribution_rules r JOIN campaigns k ON k.id = r.campaign_id WHERE k.name = 'Verão 2027'),
		(SELECT count(*) FROM campaign_station_pricing p JOIN campaigns k ON k.id = p.campaign_id WHERE k.name = 'Verão 2027')`,
	).Scan(&n[0], &n[1], &n[2]))
	return n
}

func TestProgramarCampanha_GravaTudoEAvisaOHub(t *testing.T) {
	c, hf, _ := novoCenarioCampanha(t)
	feita, err := c.srv.ProgramarCampanha(c.ctx, c.entradaProgramacao())
	require.NoError(t, err)
	require.Equal(t, [3]int{1, 1, 1}, c.contarProgramacao(t))
	require.Len(t, feita.Regras, 1)
	require.Equal(t, feita.Campanha.ID, feita.Regras[0].CampaignID)
	require.Equal(t, 1, feita.Precos)
	require.Len(t, hf.EventosDoTipo("campanha.upsert"), 1)
}

// A ação confere o que o banco não confere (station_ids sem FK; o preço só tem FK para stations):
// nenhum chamador grava regra ou preço de emissora fora da campanha.
func TestProgramarCampanha_RecusasDaAcaoNaoGravamNada(t *testing.T) {
	c, hf, _ := novoCenarioCampanha(t)
	fora := mcptest.Emissora(t, c.ctx, c.pool, "Fora da campanha", 100)
	casos := map[string]func(e *ProgramarCampanhaEntrada){
		"regra com emissora fora": func(e *ProgramarCampanhaEntrada) { e.Regras[0].EmissoraIDs = []uuid.UUID{c.emissora, fora} },
		"preço de emissora fora":  func(e *ProgramarCampanhaEntrada) { e.Precos[0].StationID = fora },
		"regra fora do período":   func(e *ProgramarCampanhaEntrada) { e.Regras[0].Fim = dia("2027-03-01") },
		"fim antes do início":     func(e *ProgramarCampanhaEntrada) { e.Campanha.Fim = dia("2027-01-01") },
		"sem emissoras":           func(e *ProgramarCampanhaEntrada) { e.Campanha.EmissoraIDs = nil },
		"regra inválida":          func(e *ProgramarCampanhaEntrada) { e.Regras[0].InsercoesPorDia = 0 },
	}
	for nome, mexe := range casos {
		e := c.entradaProgramacao()
		mexe(&e)
		_, err := c.srv.ProgramarCampanha(c.ctx, e)
		require.Equal(t, CodigoEntradaInvalida, recusaDe(t, err).Codigo, nome)
	}
	require.Equal(t, [3]int{0, 0, 0}, c.contarProgramacao(t))
	require.Empty(t, hf.Eventos(), "nada gravado, nada avisado")
}
