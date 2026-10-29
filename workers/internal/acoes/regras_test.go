package acoes

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/mcp/mcptest"
	"radiocheck/internal/metrics"
)

func recusaDe(t *testing.T, err error) *Recusa {
	t.Helper()
	var r *Recusa
	require.True(t, errors.As(err, &r), "esperava *Recusa, veio %T: %v", err, err)
	return r
}

func TestSegundosDoHorario(t *testing.T) {
	for entrada, esperado := range map[string]int{
		"00:00": 0, "08:00": 8 * 3600, "8:00": 8 * 3600, "23:59": 23*3600 + 59*60,
		"08:30:15": 8*3600 + 30*60 + 15, "24:00": 24 * 3600, "24:00:00": 24 * 3600,
	} {
		got, ok := segundosDoHorario(entrada)
		require.True(t, ok, entrada)
		require.Equal(t, esperado, got, entrada)
	}
	for _, ruim := range []string{"", "8", "25:00", "24:01", "10:60", "10:00:60", "ab:cd", "-1:00", "10:00:00:00", "0800"} {
		_, ok := segundosDoHorario(ruim)
		require.False(t, ok, ruim)
	}
}

func TestValidarRegra(t *testing.T) {
	ok := EntradaRegra{
		TipoID: uuid.New(), EmissoraIDs: []uuid.UUID{uuid.New()},
		Inicio: dia("2026-06-01"), Fim: dia("2026-06-30"),
		MascaraDias: 127, HoraInicio: "08:00", HoraFim: "12:00", InsercoesPorDia: 3,
	}
	require.NoError(t, validarRegra(ok))

	// O que a rota sempre aceitou continua aceito: máscara 0, 24:00, mesmo dia,
	// lista de emissoras vazia (mas não ausente).
	aceita := ok
	aceita.MascaraDias = 0
	aceita.HoraFim = "24:00"
	aceita.Fim = aceita.Inicio
	aceita.EmissoraIDs = []uuid.UUID{}
	require.NoError(t, validarRegra(aceita))
	// Horário que não é HH:MM fica para o Postgres decidir (H:MM é válido lá).
	aceita.HoraInicio = "8:00"
	require.NoError(t, validarRegra(aceita))

	for nome, mudar := range map[string]func(*EntradaRegra){
		"sem lista de emissoras":      func(e *EntradaRegra) { e.EmissoraIDs = nil },
		"máscara negativa":            func(e *EntradaRegra) { e.MascaraDias = -1 },
		"máscara acima de 127":        func(e *EntradaRegra) { e.MascaraDias = 128 },
		"zero inserções":              func(e *EntradaRegra) { e.InsercoesPorDia = 0 },
		"mais de 100 inserções":       func(e *EntradaRegra) { e.InsercoesPorDia = 101 },
		"data final antes da inicial": func(e *EntradaRegra) { e.Fim = dia("2026-05-31") },
		"hora final igual à inicial":  func(e *EntradaRegra) { e.HoraFim = "08:00" },
		"hora final antes da inicial": func(e *EntradaRegra) { e.HoraFim = "07:59" },
	} {
		t.Run(nome, func(t *testing.T) {
			e := ok
			mudar(&e)
			r := recusaDe(t, validarRegra(e))
			require.Equal(t, CodigoEntradaInvalida, r.Codigo)
			require.NotEmpty(t, r.Mensagem)
		})
	}
}

func TestCriarRegra_GravaERecategorizaOTipoDaRegra(t *testing.T) {
	c := novoCenario(t)

	regra, err := c.srv.CriarRegra(c.ctx, c.campanha, c.entradaRegra(c.tipoA, "08:00", "12:00", 1))

	require.NoError(t, err)
	require.Equal(t, c.campanha, regra.CampaignID)
	require.Equal(t, "08:00", regra.TimeStart)
	require.Equal(t, 1, c.contaRegras(t))
	c.exigeCategoria(t, "in_slot") // a tocada das 10:30 cabe na faixa
}

func TestCriarRegra_SemEmSegundoPlanoEhGoroutine(t *testing.T) {
	c := novoCenario(t)
	c.srv.EmSegundoPlano = nil // produção

	_, err := c.srv.CriarRegra(c.ctx, c.campanha, c.entradaRegra(c.tipoA, "08:00", "12:00", 1))

	require.NoError(t, err)
	require.Eventually(t, func() bool { p, b := c.categoria(t); return p == "in_slot" && b == "in_slot" },
		10*time.Second, 50*time.Millisecond)
}

func TestCriarRegra_EntradaInvalidaNaoGravaNada(t *testing.T) {
	c := novoCenario(t)
	e := c.entradaRegra(c.tipoA, "12:00", "08:00", 1)

	_, err := c.srv.CriarRegra(c.ctx, c.campanha, e)

	require.Equal(t, CodigoEntradaInvalida, recusaDe(t, err).Codigo)
	require.Zero(t, c.contaRegras(t))
	c.exigeCategoria(t, "bonus") // nada recategorizado
}

// Tipo inexistente: antes era "internal error" (500) por causa da FK; agora é
// uma recusa com o motivo.
func TestCriarRegra_TipoInexistenteViraRecusa(t *testing.T) {
	c := novoCenario(t)

	_, err := c.srv.CriarRegra(c.ctx, c.campanha, c.entradaRegra(uuid.New(), "08:00", "12:00", 1))

	r := recusaDe(t, err)
	require.Equal(t, CodigoEntradaInvalida, r.Codigo)
	require.Contains(t, r.Mensagem, "tipo de material")
	require.Zero(t, c.contaRegras(t))
}

func TestCriarRegra_CampanhaInexistenteViraNaoEncontrado(t *testing.T) {
	c := novoCenario(t)

	_, err := c.srv.CriarRegra(c.ctx, uuid.New(), c.entradaRegra(c.tipoA, "08:00", "12:00", 1))

	require.Equal(t, CodigoNaoEncontrado, recusaDe(t, err).Codigo)
}

func TestCriarRegra_HorarioIlegivelViraRecusaDoBanco(t *testing.T) {
	c := novoCenario(t)
	e := c.entradaRegra(c.tipoA, "08:00", "meio-dia", 1)

	_, err := c.srv.CriarRegra(c.ctx, c.campanha, e)

	require.Equal(t, CodigoEntradaInvalida, recusaDe(t, err).Codigo)
	require.Zero(t, c.contaRegras(t))
}

// A falha da recategorização é registrada (métrica rule_create) e NÃO derruba a
// ação: a regra já foi gravada. Provocamos a falha apagando a regra antes de o
// gatilho assíncrono rodar (RecategorizeForRule faz Get da regra).
func TestCriarRegra_FalhaDaRecatEhRegistradaENaoDerrubaAAcao(t *testing.T) {
	c := novoCenario(t)
	var adiado func()
	c.srv.EmSegundoPlano = func(f func()) { adiado = f }
	antes := testutil.ToFloat64(metrics.RecategorizeFailures.WithLabelValues("rule_create"))

	regra, err := c.srv.CriarRegra(c.ctx, c.campanha, c.entradaRegra(c.tipoA, "08:00", "12:00", 1))
	require.NoError(t, err)
	require.NotNil(t, adiado, "a recategorização foi entregue ao EmSegundoPlano")
	_, err = c.pool.Exec(c.ctx, `DELETE FROM distribution_rules WHERE id = $1`, regra.ID)
	require.NoError(t, err)
	adiado()

	require.Equal(t, antes+1, testutil.ToFloat64(metrics.RecategorizeFailures.WithLabelValues("rule_create")))
}

func TestEditarRegra_SubstituiTudoERecategoriza(t *testing.T) {
	c := novoCenario(t)
	regra, err := c.srv.CriarRegra(c.ctx, c.campanha, c.entradaRegra(c.tipoA, "08:00", "12:00", 1))
	require.NoError(t, err)
	c.exigeCategoria(t, "in_slot")

	nova := c.entradaRegra(c.tipoA, "14:00", "18:00", 3)
	nova.Nome = "Tarde"
	require.NoError(t, c.srv.EditarRegra(c.ctx, c.campanha, regra.ID, nova))

	atual, err := c.srv.Regras.Get(c.ctx, regra.ID)
	require.NoError(t, err)
	require.Equal(t, "14:00", atual.TimeStart)
	require.EqualValues(t, 3, atual.PlaysPerDay)
	require.Equal(t, "Tarde", atual.Name)
	c.exigeCategoria(t, "out_slot") // saiu da faixa, cota em aberto
}

func TestEditarRegra_EntradaInvalidaNaoGrava(t *testing.T) {
	c := novoCenario(t)
	regra, err := c.srv.CriarRegra(c.ctx, c.campanha, c.entradaRegra(c.tipoA, "08:00", "12:00", 1))
	require.NoError(t, err)

	ruim := c.entradaRegra(c.tipoA, "08:00", "12:00", 0)
	err = c.srv.EditarRegra(c.ctx, c.campanha, regra.ID, ruim)

	require.Equal(t, CodigoEntradaInvalida, recusaDe(t, err).Codigo)
	atual, gerr := c.srv.Regras.Get(c.ctx, regra.ID)
	require.NoError(t, gerr)
	require.EqualValues(t, 1, atual.PlaysPerDay)
}

func TestApagarRegra_ApagaERecategorizaACampanha(t *testing.T) {
	c := novoCenario(t)
	regra, err := c.srv.CriarRegra(c.ctx, c.campanha, c.entradaRegra(c.tipoA, "08:00", "12:00", 1))
	require.NoError(t, err)
	c.exigeCategoria(t, "in_slot")

	require.NoError(t, c.srv.ApagarRegra(c.ctx, c.campanha, regra.ID))

	require.Zero(t, c.contaRegras(t))
	c.exigeCategoria(t, "bonus")
}

// ───────────────────────────── ajustes do dia ─────────────────────────────

func (c *cenario) entradaAjuste(esperadas int16, ini, fim string) EntradaAjuste {
	return EntradaAjuste{
		CampanhaID: c.campanha, TipoID: c.tipoA, EmissoraID: c.emissora, Dia: dia("2026-06-10"),
		Insercoes: esperadas, HoraInicio: ini, HoraFim: fim,
	}
}

func TestDefinirAjuste_GravaERecategorizaACelulaNaHora(t *testing.T) {
	c := novoCenario(t)
	_, err := c.srv.CriarRegra(c.ctx, c.campanha, c.entradaRegra(c.tipoA, "08:00", "12:00", 1))
	require.NoError(t, err)
	c.exigeCategoria(t, "in_slot")
	motivo := "feriado"
	quem := mcptest.Usuario(t, c.ctx, c.pool, "admin", true)

	e := c.entradaAjuste(0, "00:00", "23:59")
	e.Motivo = &motivo
	e.CriadoPor = &quem
	require.NoError(t, c.srv.DefinirAjuste(c.ctx, e))

	var esperadas int
	var rotulo *string
	var autor *uuid.UUID
	require.NoError(t, c.pool.QueryRow(c.ctx,
		`SELECT plays_expected, reason, created_by FROM distribution_overrides
		  WHERE campaign_id=$1 AND type_id=$2 AND station_id=$3 AND for_date='2026-06-10'`,
		c.campanha, c.tipoA, c.emissora).Scan(&esperadas, &rotulo, &autor))
	require.Equal(t, 0, esperadas)
	require.Equal(t, "feriado", *rotulo)
	require.Equal(t, quem, *autor, "a ação aceita o autor (a rota HTTP não o informa)")
	c.exigeCategoria(t, "bonus") // meta 0 → bonificação, SEM espera: é síncrono
}

func TestDefinirAjuste_ValidaComOsTextosDaRota(t *testing.T) {
	c := novoCenario(t)
	for nome, caso := range map[string]struct {
		e     EntradaAjuste
		texto string
	}{
		"início fora de HH:MM": {c.entradaAjuste(1, "8:00", "12:00"), "time_start must be HH:MM (00:00-23:59)"},
		"fim fora de HH:MM":    {c.entradaAjuste(1, "08:00", "24:00"), "time_end must be HH:MM (00:00-23:59)"},
		"fim igual ao início":  {c.entradaAjuste(1, "10:00", "10:00"), "time_end must be greater than time_start"},
		"fim antes do início":  {c.entradaAjuste(1, "12:00", "08:00"), "time_end must be greater than time_start"},
	} {
		t.Run(nome, func(t *testing.T) {
			r := recusaDe(t, c.srv.DefinirAjuste(c.ctx, caso.e))
			require.Equal(t, CodigoEntradaInvalida, r.Codigo)
			require.Equal(t, caso.texto, r.Mensagem)
		})
	}
	// Novo: meta negativa (antes caía no CHECK do banco como 500).
	r := recusaDe(t, c.srv.DefinirAjuste(c.ctx, c.entradaAjuste(-1, "08:00", "12:00")))
	require.Equal(t, CodigoEntradaInvalida, r.Codigo)
}

func TestDefinirAjuste_TipoInexistenteViraRecusa(t *testing.T) {
	c := novoCenario(t)
	e := c.entradaAjuste(1, "08:00", "12:00")
	e.TipoID = uuid.New()

	r := recusaDe(t, c.srv.DefinirAjuste(c.ctx, e))

	require.Equal(t, CodigoEntradaInvalida, r.Codigo)
	require.Contains(t, r.Mensagem, "tipo de material")
}

func TestDefinirAjuste_SemRecategorizadorSoGrava(t *testing.T) {
	c := novoCenario(t)
	c.srv.RecatCelula = nil

	require.NoError(t, c.srv.DefinirAjuste(c.ctx, c.entradaAjuste(0, "00:00", "23:59")))

	var n int
	require.NoError(t, c.pool.QueryRow(c.ctx, `SELECT count(*) FROM distribution_overrides WHERE campaign_id=$1`, c.campanha).Scan(&n))
	require.Equal(t, 1, n)
	c.exigeCategoria(t, "bonus") // intocada (já era bonus); o ponto é não panicar
}

// A recategorização do ajuste é best-effort: falhar não falha a ação, e o erro
// é registrado com a origem certa.
type recatQueFalha struct{ chamadas int }

func (r *recatQueFalha) RecategorizeForOverride(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, time.Time) error {
	r.chamadas++
	return errors.New("recat caiu")
}

func TestDefinirEApagarAjuste_FalhaDaRecatEhRegistradaENaoFalhaAAcao(t *testing.T) {
	c := novoCenario(t)
	falha := &recatQueFalha{}
	c.srv.RecatCelula = falha
	antesU := testutil.ToFloat64(metrics.RecategorizeFailures.WithLabelValues("override_upsert"))
	antesD := testutil.ToFloat64(metrics.RecategorizeFailures.WithLabelValues("override_delete"))

	require.NoError(t, c.srv.DefinirAjuste(c.ctx, c.entradaAjuste(2, "08:00", "12:00")))
	require.NoError(t, c.srv.ApagarAjuste(c.ctx, c.campanha, c.tipoA, c.emissora, dia("2026-06-10")))

	require.Equal(t, 2, falha.chamadas)
	require.Equal(t, antesU+1, testutil.ToFloat64(metrics.RecategorizeFailures.WithLabelValues("override_upsert")))
	require.Equal(t, antesD+1, testutil.ToFloat64(metrics.RecategorizeFailures.WithLabelValues("override_delete")))
}

func TestApagarAjuste_ApagaERevertePraRegra(t *testing.T) {
	c := novoCenario(t)
	_, err := c.srv.CriarRegra(c.ctx, c.campanha, c.entradaRegra(c.tipoA, "08:00", "12:00", 1))
	require.NoError(t, err)
	require.NoError(t, c.srv.DefinirAjuste(c.ctx, c.entradaAjuste(0, "00:00", "23:59")))
	c.exigeCategoria(t, "bonus")

	require.NoError(t, c.srv.ApagarAjuste(c.ctx, c.campanha, c.tipoA, c.emissora, dia("2026-06-10")))

	c.exigeCategoria(t, "in_slot")
	// Apagar de novo (não existe mais) não é erro.
	require.NoError(t, c.srv.ApagarAjuste(c.ctx, c.campanha, c.tipoA, c.emissora, dia("2026-06-10")))
}
