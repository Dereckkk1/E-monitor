package acoes

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/mcp/mcptest"
)

// comCota põe uma regra de 1 inserção/dia, faixa 10:00-12:00, na campanha toda.
// As tocadas destes testes caem em 11/06, longe da tocada-base do cenário (10/06).
func (c *cenario) comCota(t *testing.T) {
	t.Helper()
	mcptest.Regra(t, c.ctx, c.pool, c.campanha, c.tipoA, c.emissora, "2026-06-01", "2026-06-30", 127, "10:00", "12:00", 1)
}

func (c *cenario) entradaManual(t *testing.T, quando time.Time) EntradaManual {
	t.Helper()
	return EntradaManual{Campanha: c.campanha, Emissora: c.emissora, Material: c.material,
		Quando: quando, Nota: " censura ", Ator: mcptest.Usuario(t, c.ctx, c.pool, "admin", true)}
}

func (c *cenario) categoriaDe(t *testing.T, id uuid.UUID) string {
	t.Helper()
	var cat string
	require.NoError(t, c.pool.QueryRow(c.ctx,
		`SELECT category FROM detection_campaigns WHERE detection_id = $1`, id).Scan(&cat))
	return cat
}

func TestCriarVeiculacaoManual_GravaComAtorNotaECategoria(t *testing.T) {
	c := novoCenario(t)
	c.comCota(t)
	in := c.entradaManual(t, mcptest.Quando("2026-06-11", "10:30"))

	det, err := c.srv.CriarVeiculacaoManual(c.ctx, in)

	require.NoError(t, err)
	require.Equal(t, "in_slot", det.Category)
	var por *uuid.UUID
	var nota *string
	require.NoError(t, c.pool.QueryRow(c.ctx,
		`SELECT manual_by, manual_note FROM detections WHERE id = $1`, det.ID).Scan(&por, &nota))
	require.Equal(t, in.Ator, *por)
	require.Equal(t, "censura", *nota)
	require.Equal(t, "in_slot", c.categoriaDe(t, det.ID))
}

func TestCriarVeiculacaoManual_Recusas(t *testing.T) {
	c := novoCenario(t)
	c.srv.Agora = func() time.Time { return mcptest.Quando("2026-06-11", "12:00") }
	base := c.entradaManual(t, mcptest.Quando("2026-06-11", "10:30"))

	semCampanha := base
	semCampanha.Campanha = uuid.Nil
	semQuando := base
	semQuando.Quando = time.Time{}
	futuro := base
	futuro.Quando = c.srv.Agora().Add(6 * time.Minute)
	naoVinculado := base
	naoVinculado.Material = uuid.New()

	casos := []struct {
		nome   string
		in     EntradaManual
		codigo string
		msg    string
	}{
		{"id ausente", semCampanha, CodigoEntradaInvalida, "campaign_id, station_id and commercial_id are required"},
		{"sem quando", semQuando, CodigoEntradaInvalida, "detected_at is required"},
		{"futuro", futuro, CodigoEntradaInvalida, "detected_at cannot be in the future"},
		{"material nao vinculado", naoVinculado, CodigoMaterialNaoVinculado, "material is not linked to this station in this campaign"},
	}
	for _, tc := range casos {
		t.Run(tc.nome, func(t *testing.T) {
			_, err := c.srv.CriarVeiculacaoManual(c.ctx, tc.in)
			var r *Recusa
			require.ErrorAs(t, err, &r)
			require.Equal(t, tc.codigo, r.Codigo)
			require.Equal(t, tc.msg, r.Mensagem)
		})
	}
}

func TestCriarVeiculacaoManual_ToleranciaDeCincoMinutosUsaORelogioInjetado(t *testing.T) {
	c := novoCenario(t)
	c.comCota(t)
	agora := mcptest.Quando("2026-06-11", "10:00")
	c.srv.Agora = func() time.Time { return agora }

	_, err := c.srv.CriarVeiculacaoManual(c.ctx, c.entradaManual(t, agora.Add(4*time.Minute)))
	require.NoError(t, err)
	_, err = c.srv.CriarVeiculacaoManual(c.ctx, c.entradaManual(t, agora.Add(5*time.Minute+time.Second)))
	r := recusaDe(t, err)
	require.Equal(t, CodigoEntradaInvalida, r.Codigo)
	require.Equal(t, "detected_at cannot be in the future", r.Mensagem)
}

// NoFuturo é a régua que CriarVeiculacaoManual usa e que o MCP consulta antes
// de aceitar o pedido: o relógio do serviço mais a tolerância de 5 minutos, com a
// borda exata ainda permitida. Não precisa de banco.
func TestNoFuturo_ToleranciaEBordaExata(t *testing.T) {
	agora := mcptest.Quando("2026-06-11", "10:00")
	s := &Servico{Agora: func() time.Time { return agora }}

	require.False(t, s.NoFuturo(agora.Add(-time.Hour)), "o passado nunca é futuro")
	require.False(t, s.NoFuturo(agora.Add(5*time.Minute)), "a borda exata ainda passa")
	require.True(t, s.NoFuturo(agora.Add(5*time.Minute+time.Nanosecond)))
	require.True(t, s.NoFuturo(agora.Add(time.Hour)))

	// Sem relógio injetado vale o relógio de verdade.
	relogioReal := &Servico{}
	require.False(t, relogioReal.NoFuturo(time.Now()))
	require.True(t, relogioReal.NoFuturo(time.Now().Add(time.Hour)))
}

// Data sem partição em detections (23514 "no partition of relation"): é entrada
// inválida, não erro interno.
func TestCriarVeiculacaoManual_DataSemParticaoEhEntradaInvalida(t *testing.T) {
	c := novoCenario(t)
	c.comCota(t)

	_, err := c.srv.CriarVeiculacaoManual(c.ctx, c.entradaManual(t, mcptest.Quando("1999-03-10", "10:30")))

	r := recusaDe(t, err)
	require.Equal(t, CodigoEntradaInvalida, r.Codigo)
	require.Equal(t, "detected_at fora do período suportado", r.Mensagem)
}

func TestIgnorarERestaurarVeiculacao_MexemNaCotaDoDia(t *testing.T) {
	c := novoCenario(t)
	c.comCota(t)
	ator := mcptest.Usuario(t, c.ctx, c.pool, "admin", true)
	primeira, err := c.srv.CriarVeiculacaoManual(c.ctx, c.entradaManual(t, mcptest.Quando("2026-06-11", "10:30")))
	require.NoError(t, err)
	segunda, err := c.srv.CriarVeiculacaoManual(c.ctx, c.entradaManual(t, mcptest.Quando("2026-06-11", "11:00")))
	require.NoError(t, err)
	require.Equal(t, "in_slot", c.categoriaDe(t, primeira.ID))
	require.Equal(t, "bonus", c.categoriaDe(t, segunda.ID))

	ign, err := c.srv.IgnorarVeiculacao(c.ctx, primeira.ID, ator)
	require.NoError(t, err)
	require.NotNil(t, ign.IgnoredAt)
	require.Equal(t, ator, *ign.IgnoredBy)
	require.Equal(t, "in_slot", c.categoriaDe(t, segunda.ID), "ignorar a primeira promove a segunda")

	res, err := c.srv.RestaurarVeiculacao(c.ctx, primeira.ID)
	require.NoError(t, err)
	require.Nil(t, res.IgnoredAt)
	require.Nil(t, res.IgnoredBy)
	require.Equal(t, "in_slot", c.categoriaDe(t, primeira.ID))
	require.Equal(t, "bonus", c.categoriaDe(t, segunda.ID), "restaurar devolve a vaga à primeira")
}

func TestIgnorarERestaurarVeiculacao_InexistenteEhNaoEncontrado(t *testing.T) {
	c := novoCenario(t)

	for nome, f := range map[string]func() error{
		"ignorar":   func() error { _, err := c.srv.IgnorarVeiculacao(c.ctx, uuid.New(), uuid.New()); return err },
		"restaurar": func() error { _, err := c.srv.RestaurarVeiculacao(c.ctx, uuid.New()); return err },
	} {
		t.Run(nome, func(t *testing.T) {
			var r *Recusa
			require.ErrorAs(t, f(), &r)
			require.Equal(t, CodigoNaoEncontrado, r.Codigo)
			require.Equal(t, "not found", r.Mensagem)
		})
	}
}

// tocadaSomeDepoisDeIgnorada instala um gatilho que apaga a tocada logo depois do UPDATE de
// ignored_at: a escrita dá certo e a releitura final não acha mais a linha — o caso em que "a
// ação gravou e o que vem depois falha". O gatilho sai no fim do teste.
func (c *cenario) tocadaSomeDepoisDeIgnorada(t *testing.T) {
	t.Helper()
	for _, q := range []string{
		`CREATE OR REPLACE FUNCTION tg_teste_some_tocada() RETURNS trigger LANGUAGE plpgsql AS $$
		 BEGIN DELETE FROM detections WHERE id = NEW.id AND detected_at = NEW.detected_at; RETURN NULL; END $$`,
		`CREATE TRIGGER tg_teste_some_tocada AFTER UPDATE OF ignored_at ON detections
		 FOR EACH ROW EXECUTE FUNCTION tg_teste_some_tocada()`,
	} {
		_, err := c.pool.Exec(c.ctx, q)
		require.NoError(t, err)
	}
	t.Cleanup(func() {
		c.pool.Exec(c.ctx, `DROP TRIGGER IF EXISTS tg_teste_some_tocada ON detections`)
		c.pool.Exec(c.ctx, `DROP FUNCTION IF EXISTS tg_teste_some_tocada()`)
	})
}

// A escrita gravou e só a releitura final falha (aqui: a tocada sumiu logo depois do UPDATE): não é
// Recusa nem erro — o nil sem erro diz "gravado", como no CancelarCampanha — nem o nao_encontrado
// que diria "nada aconteceu" de uma tocada que FOI ignorada/restaurada.
func TestIgnorarERestaurarVeiculacao_ReleituraQueFalhaDepoisDeGravarNaoEhErro(t *testing.T) {
	c := novoCenario(t)
	ator := mcptest.Usuario(t, c.ctx, c.pool, "admin", true)

	t.Run("ignorar", func(t *testing.T) {
		det, err := c.srv.CriarVeiculacaoManual(c.ctx, c.entradaManual(t, mcptest.Quando("2026-06-11", "10:30")))
		require.NoError(t, err)
		c.tocadaSomeDepoisDeIgnorada(t)

		out, err := c.srv.IgnorarVeiculacao(c.ctx, det.ID, ator)

		require.NoError(t, err, "a tocada foi ignorada: depois de gravar não existe erro nem Recusa")
		require.Nil(t, out)
		var existe bool
		require.NoError(t, c.pool.QueryRow(c.ctx, `SELECT EXISTS(SELECT 1 FROM detections WHERE id = $1)`, det.ID).Scan(&existe))
		require.False(t, existe, "o gatilho apagou a tocada depois do UPDATE: a releitura não a acha")
	})
	t.Run("restaurar", func(t *testing.T) {
		det, err := c.srv.CriarVeiculacaoManual(c.ctx, c.entradaManual(t, mcptest.Quando("2026-06-12", "10:30")))
		require.NoError(t, err)
		_, err = c.srv.IgnorarVeiculacao(c.ctx, det.ID, ator)
		require.NoError(t, err)
		// o gatilho vai depois do ignorar: só o restaurar (o UPDATE de ignored_at para NULL) o dispara
		c.tocadaSomeDepoisDeIgnorada(t)

		out, err := c.srv.RestaurarVeiculacao(c.ctx, det.ID)

		require.NoError(t, err)
		require.Nil(t, out)
	})
}
