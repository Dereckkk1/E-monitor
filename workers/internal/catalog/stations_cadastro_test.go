package catalog

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

func fptr(f float64) *float64 { return &f }

// UpdateCadastro grava só o cadastro: stream_url, banda, logo e o metadata
// INTEIRO (inclusive chave que o struct StationMeta não conhece) sobrevivem —
// é o que o Update (PUT total) não garante. E a coordenada é re-derivada da
// cidade+UF quando uma delas é enviada, como no Update.
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

	got, falhou, err := repo.UpdateCadastro(ctx, st.ID, UpdateCadastroInput{
		Name: strPtr("Cad FM 2"), FrequencyMHz: fptr(101.5), City: strPtr("São Paulo"), State: strPtr("SP"), PMM: fptr(12345),
	})

	require.NoError(t, err)
	require.False(t, falhou)
	require.Equal(t, "Cad FM 2", got.Name)
	require.InDelta(t, 101.5, *got.FrequencyMHz, 0.001)
	require.Equal(t, "São Paulo", *got.City)
	require.InDelta(t, 12345, *got.PMM, 0.001)
	require.Equal(t, "http://example.com/cad", got.StreamURL, "stream_url intocado")
	require.Equal(t, "FM", got.Band)
	require.Equal(t, "http://logo/x.png", *got.LogoURL)
	require.NotNil(t, got.Latitude, "cidade conhecida: a coordenada é derivada")
	require.InDelta(t, -23.55, *got.Latitude, 0.6)
	var meta string
	require.NoError(t, repo.pool.QueryRow(ctx, `SELECT metadata::text FROM stations WHERE id = $1`, st.ID).Scan(&meta))
	require.JSONEq(t, `{"categories":["jovem"],"chave_desconhecida":{"a":1}}`, meta)
}

// A edição é PARCIAL no SQL (COALESCE): campo nil NÃO é gravado, então uma
// mudança concorrente num campo que o pedido não enviou não é revertida.
func TestStations_UpdateCadastro_ParcialNoSQLNaoReverteCampoNaoEnviado(t *testing.T) {
	ctx, repo := newTestPool(t)
	st, err := repo.Create(ctx, CreateStationInput{
		Name: "Parcial FM", Band: "FM", StreamURL: "http://example.com/parcial",
		City: strPtr("São Paulo"), State: strPtr("SP"), FrequencyMHz: fptr(99.9),
	})
	require.NoError(t, err)
	// Alguém (outra sessão, o wizard) mexe no PMM e na frequência DEPOIS de o pedido ter lido a emissora.
	_, err = repo.pool.Exec(ctx, `UPDATE stations SET pmm = 777, frequency_mhz = 88.5 WHERE id = $1`, st.ID)
	require.NoError(t, err)

	got, falhou, err := repo.UpdateCadastro(ctx, st.ID, UpdateCadastroInput{Name: strPtr("Só o nome")})

	require.NoError(t, err)
	require.False(t, falhou, "cidade/UF não enviadas: não há geocodificação")
	require.Equal(t, "Só o nome", got.Name)
	require.InDelta(t, 777, *got.PMM, 0.001, "PMM não enviado: o valor concorrente fica")
	require.InDelta(t, 88.5, *got.FrequencyMHz, 0.001)
	require.Equal(t, "São Paulo", *got.City)
	require.Equal(t, "SP", *got.State)
}

// Geocodificação: roda quando cidade OU UF são enviadas (com o par já gravado
// completando o que faltou) e a falha é devolvida — a coordenada fica velha e
// quem chama pode avisar.
func TestStations_UpdateCadastro_GeocodificacaoEFalha(t *testing.T) {
	ctx, repo := newTestPool(t)
	st, err := repo.Create(ctx, CreateStationInput{
		Name: "Geo2 FM", Band: "FM", StreamURL: "http://example.com/geo2",
		City: strPtr("Cidade Inexistente XYZ"), State: strPtr("SP"),
	})
	require.NoError(t, err)
	require.Nil(t, st.Latitude)

	// Só a cidade vem no pedido; a UF é a gravada (SP).
	got, falhou, err := repo.UpdateCadastro(ctx, st.ID, UpdateCadastroInput{City: strPtr("São Paulo")})
	require.NoError(t, err)
	require.False(t, falhou)
	require.NotNil(t, got.Latitude)
	lat := *got.Latitude

	// Cidade que não existe: falhou=true e a coordenada anterior fica.
	got, falhou, err = repo.UpdateCadastro(ctx, st.ID, UpdateCadastroInput{City: strPtr("Outra Cidade Inexistente")})
	require.NoError(t, err)
	require.True(t, falhou, "o lookup falhou: a coordenada está velha")
	require.Equal(t, "Outra Cidade Inexistente", *got.City)
	require.Equal(t, lat, *got.Latitude, "um lookup que falha não destrói a coordenada")

	// Sem cidade/UF no pedido, não há tentativa (falhou=false).
	_, falhou, err = repo.UpdateCadastro(ctx, st.ID, UpdateCadastroInput{PMM: fptr(1)})
	require.NoError(t, err)
	require.False(t, falhou)

	// Emissora sem cidade gravada e só a UF no pedido: não há par para geocodificar.
	_, err = repo.pool.Exec(ctx, `UPDATE stations SET city = NULL WHERE id = $1`, st.ID)
	require.NoError(t, err)
	_, falhou, err = repo.UpdateCadastro(ctx, st.ID, UpdateCadastroInput{State: strPtr("MG")})
	require.NoError(t, err)
	require.True(t, falhou)
}

func TestStations_UpdateCadastro_Inexistente(t *testing.T) {
	ctx, repo := newTestPool(t)
	_, _, err := repo.UpdateCadastro(ctx, uuid.New(), UpdateCadastroInput{Name: strPtr("x")})
	require.ErrorIs(t, err, pgx.ErrNoRows)
	// Também no caminho que lê o par cidade/UF (tx com FOR UPDATE).
	_, _, err = repo.UpdateCadastro(ctx, uuid.New(), UpdateCadastroInput{City: strPtr("São Paulo")})
	require.ErrorIs(t, err, pgx.ErrNoRows)
}
