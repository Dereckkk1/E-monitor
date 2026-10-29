package catalog

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

// UpdateCadastro grava só o cadastro: stream_url, banda, logo e o metadata
// INTEIRO (inclusive chave que o struct StationMeta não conhece) sobrevivem —
// é o que o Update (PUT total) não garante. E a coordenada é re-derivada da
// cidade+UF, como no Update.
func TestStations_UpdateCadastro_NaoTocaStreamBandaLogoNemMetadata(t *testing.T) {
	ctx, repo := newTestPool(t)
	st, err := repo.Create(ctx, CreateStationInput{
		Name: "Cad FM", Band: "FM", StreamURL: "http://example.com/cad",
		City: strPtr("Cidade Inexistente XYZ"), State: strPtr("SP"),
	})
	require.NoError(t, err)
	_, err = repo.pool.Exec(ctx, `
		UPDATE stations SET logo_url = 'http://logo/x.png',
		       metadata = '{"categories":["jovem"],"chave_desconhecida":{"a":1}}'::jsonb
		WHERE id = $1`, st.ID)
	require.NoError(t, err)

	got, err := repo.UpdateCadastro(ctx, st.ID, UpdateCadastroInput{
		Name: "Cad FM 2", FrequencyMHz: fptr(101.5), City: strPtr("São Paulo"), State: strPtr("SP"), PMM: fptr(12345),
	})

	require.NoError(t, err)
	require.Equal(t, "Cad FM 2", got.Name)
	require.InDelta(t, 101.5, *got.FrequencyMHz, 0.001)
	require.Equal(t, "São Paulo", *got.City)
	require.InDelta(t, 12345, *got.PMM, 0.001)
	require.Equal(t, "http://example.com/cad", got.StreamURL, "stream_url intocado")
	require.Equal(t, "FM", got.Band)
	require.Equal(t, "http://logo/x.png", *got.LogoURL)
	// Mudou a cidade para uma conhecida: a coordenada é derivada.
	require.NotNil(t, got.Latitude)
	require.InDelta(t, -23.55, *got.Latitude, 0.6)
	// O metadata ficou como estava, inclusive a chave desconhecida.
	var meta string
	require.NoError(t, repo.pool.QueryRow(ctx, `SELECT metadata::text FROM stations WHERE id = $1`, st.ID).Scan(&meta))
	require.JSONEq(t, `{"categories":["jovem"],"chave_desconhecida":{"a":1}}`, meta)
}

func TestStations_UpdateCadastro_CidadeDesconhecidaPreservaACoordenada(t *testing.T) {
	ctx, repo := newTestPool(t)
	st, err := repo.Create(ctx, CreateStationInput{
		Name: "Cad2 FM", Band: "FM", StreamURL: "http://example.com/cad2",
		City: strPtr("São Paulo"), State: strPtr("SP"),
	})
	require.NoError(t, err)
	require.NotNil(t, st.Latitude)
	lat := *st.Latitude

	got, err := repo.UpdateCadastro(ctx, st.ID, UpdateCadastroInput{
		Name: "Cad2 FM", City: strPtr("Outra Cidade Inexistente"), State: strPtr("SP"),
	})

	require.NoError(t, err)
	require.Equal(t, lat, *got.Latitude, "lookup que falha não destrói a coordenada")
}

func TestStations_UpdateCadastro_Inexistente(t *testing.T) {
	ctx, repo := newTestPool(t)
	_, err := repo.UpdateCadastro(ctx, uuid.New(), UpdateCadastroInput{Name: "x"})
	require.ErrorIs(t, err, pgx.ErrNoRows)
}

func fptr(f float64) *float64 { return &f }
