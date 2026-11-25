package catalog

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// CategorizeFor responde "qual categoria esta tocada teria?" SEM gravá-la, com a
// regra de cota de docs/features/quota-aware-categorization.md: as N primeiras
// tocadas dentro da faixa (±15min) são in_slot e o resto do dia é bonus; fora da
// faixa só vale out_slot enquanto a meta não fechou; fora do período é out_date.
func TestCovC_Detections_CategorizeFor_RegraDeCota(t *testing.T) {
	ctx, pool := newTestDB(t)
	f := newCellFixture(t, ctx, pool, "covc-catfor", 1) // regra 10:00–12:00, N=1, junho/2026
	sp, err := time.LoadLocation("America/Sao_Paulo")
	require.NoError(t, err)
	at := func(day, h, m int) time.Time { return time.Date(2026, 6, day, h, m, 0, 0, sp) }
	cat := func(when time.Time) string {
		got, err := f.dets.CategorizeFor(f.ctx, f.camp, f.mat, f.station, when)
		require.NoError(t, err)
		return got
	}

	// Dia vazio (nada gravado).
	assert.Equal(t, "in_slot", cat(at(10, 11, 0)), "dentro da faixa, cota aberta")
	assert.Equal(t, "in_slot", cat(at(10, 12, 10)), "tolerância de 15min depois do fim da faixa")
	assert.Equal(t, "in_slot", cat(at(10, 9, 50)), "tolerância de 15min antes do início da faixa")
	assert.Equal(t, "out_slot", cat(at(10, 14, 0)), "fora da faixa enquanto a meta está aberta")
	assert.Equal(t, "out_date", cat(time.Date(2026, 7, 15, 11, 0, 0, 0, sp)), "fora do período da campanha")

	// A cota fecha com UMA tocada dentro da faixa: tudo o que vier depois é excedente.
	first := f.play(t, 10, 30)
	require.Equal(t, "in_slot", f.cat(t, first))
	assert.Equal(t, "bonus", cat(at(10, 11, 0)), "meta cumprida: o excedente dentro da faixa é bonus")
	assert.Equal(t, "bonus", cat(at(10, 14, 0)), "meta cumprida: fora da faixa não vale out_slot, vira bonus")
	// Outro dia não é afetado pela cota deste.
	assert.Equal(t, "in_slot", cat(at(11, 11, 0)))
	// Categorizar não grava nada: a tocada hipotética não ocupou vaga.
	var n int
	require.NoError(t, f.pool.QueryRow(f.ctx, `SELECT count(*) FROM detections WHERE station_id=$1`, f.station).Scan(&n))
	assert.Equal(t, 1, n)

	_, err = f.dets.CategorizeFor(covCCancelled(), f.camp, f.mat, f.station, at(10, 11, 0))
	require.Error(t, err)
}

// Uma tocada anterior à faixa que chega primeiro NÃO ocupa a vaga do dia: quando
// a tocada dentro da faixa chega, a cota fecha para ela (in_slot) e a de fora
// continua out_slot só enquanto a meta não fechou — depois do fechamento ela
// vira excedente (bonus), pois out_slot não vale nada.
func TestCovC_Detections_Create_ForaDaFaixaRebaixaQuandoACotaFecha(t *testing.T) {
	ctx, pool := newTestDB(t)
	f := newCellFixture(t, ctx, pool, "covc-quota-order", 1)

	fora := f.play(t, 14, 0)
	assert.Equal(t, "out_slot", f.cat(t, fora), "meta ainda aberta: fora da faixa é out_slot")
	dentro := f.play(t, 10, 30)
	assert.Equal(t, "in_slot", f.cat(t, dentro))
	assert.Equal(t, "bonus", f.cat(t, fora), "com a meta cumprida a tocada de fora da faixa não vale nada e vira bonus")
	assert.Equal(t, "bonus", f.projCat(t, fora), "a projeção canônica acompanha a base")

	// ResettleDetectionCells (caminho em lote) converge o mesmo veredito depois de
	// um UPDATE cru que desalinhou a categoria.
	_, err := f.pool.Exec(f.ctx, `UPDATE detections SET category='in_slot' WHERE id=$1`, fora.ID)
	require.NoError(t, err)
	require.NoError(t, f.dets.ResettleDetectionCells(f.ctx, fora.ID, &fora.DetectedAt))
	assert.Equal(t, "bonus", f.cat(t, fora))
	assert.Equal(t, "in_slot", f.cat(t, dentro))
}
