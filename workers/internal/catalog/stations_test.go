package catalog

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"radiocheck/internal/db"
)

func newTestPool(t *testing.T) (context.Context, *Stations) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := db.New(ctx, url, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM stations`)
		pool.Close()
	})
	return ctx, NewStations(pool)
}

func strPtr(s string) *string   { return &s }
func f64Ptr(f float64) *float64 { return &f }

func TestStations_CreateListGet(t *testing.T) {
	ctx, repo := newTestPool(t)

	created, err := repo.Create(ctx, CreateStationInput{
		Name:         "Test FM",
		Band:         "FM",
		FrequencyMHz: f64Ptr(101.5),
		City:         strPtr("São Paulo"),
		State:        strPtr("SP"),
		StreamURL:    "http://example.com/stream",
	})
	require.NoError(t, err)
	require.Equal(t, "Test FM", created.Name)
	require.Equal(t, "paused", created.MonitoringStatus)

	out, err := repo.List(ctx, ListInput{Page: 1, Limit: 10})
	require.NoError(t, err)
	require.Len(t, out.Data, 1)

	fetched, err := repo.Get(ctx, created.ID)
	require.NoError(t, err)
	require.Equal(t, created.ID, fetched.ID)

	require.NoError(t, repo.UpdateMonitoringStatus(ctx, created.ID, "active"))
	fetched, _ = repo.Get(ctx, created.ID)
	require.Equal(t, "active", fetched.MonitoringStatus)
}

// TestStations_Create_SeedsThresholdRow guards the regression that left every
// station uncalibrated forever: before this fix, Stations.Create only inserted
// into `stations` and the worker silently lost noise samples for the new
// station because RecordNoiseSample's UPDATE matched zero rows. Verifies the
// row exists with the expected defaults right after Create returns.
func TestStations_Create_SeedsThresholdRow(t *testing.T) {
	ctx, repo := newTestPool(t)

	created, err := repo.Create(ctx, CreateStationInput{
		Name:      "Threshold Init Test",
		Band:      "FM",
		StreamURL: "http://example.com/stream",
	})
	require.NoError(t, err)

	var (
		calibrationMode bool
		minHashes       int
	)
	err = repo.pool.QueryRow(ctx, `
		SELECT calibration_mode, min_hashes
		FROM station_thresholds
		WHERE station_id = $1`, created.ID,
	).Scan(&calibrationMode, &minHashes)
	require.NoError(t, err, "station_thresholds row must exist after Create")
	require.True(t, calibrationMode, "new station must start in calibration_mode")
	require.Equal(t, 5, minHashes, "default min_hashes from DDL")
}

func TestStations_Create_Geocodes(t *testing.T) {
	ctx, repo := newTestPool(t)

	st, err := repo.Create(ctx, CreateStationInput{
		Name: "Geo FM", Band: "FM",
		City: strPtr("São Paulo"), State: strPtr("SP"),
		StreamURL: "http://example.com/geo",
	})
	require.NoError(t, err)
	require.NotNil(t, st.Latitude)
	require.NotNil(t, st.Longitude)
	require.InDelta(t, -23.55, *st.Latitude, 0.6)
	require.InDelta(t, -46.63, *st.Longitude, 0.6)
}

func TestStations_Create_UnknownCity_NoCoords(t *testing.T) {
	ctx, repo := newTestPool(t)

	st, err := repo.Create(ctx, CreateStationInput{
		Name: "Nowhere FM", Band: "FM",
		City: strPtr("Cidade Inexistente XYZ"), State: strPtr("SP"),
		StreamURL: "http://example.com/nowhere",
	})
	require.NoError(t, err) // cadastro NÃO falha por geocode
	require.Nil(t, st.Latitude)
	require.Nil(t, st.Longitude)
}

func TestStations_Update_Geocoding(t *testing.T) {
	ctx, repo := newTestPool(t)

	// Cria sem coordenada (cidade desconhecida).
	st, err := repo.Create(ctx, CreateStationInput{
		Name: "Upd FM", Band: "FM",
		City: strPtr("Cidade Inexistente XYZ"), State: strPtr("SP"),
		StreamURL: "http://example.com/upd",
	})
	require.NoError(t, err)
	require.Nil(t, st.Latitude)

	// Editar para cidade conhecida → coordenada preenchida.
	st, err = repo.Update(ctx, st.ID, UpdateStationInput{
		Name: "Upd FM", Band: "FM",
		City: strPtr("São Paulo"), State: strPtr("SP"),
		StreamURL: "http://example.com/upd",
	})
	require.NoError(t, err)
	require.NotNil(t, st.Latitude)
	require.InDelta(t, -23.55, *st.Latitude, 0.6)
	savedLat, savedLng := *st.Latitude, *st.Longitude

	// Editar só o nome (mantendo a cidade) → coordenada preservada.
	st, err = repo.Update(ctx, st.ID, UpdateStationInput{
		Name: "Upd FM 2", Band: "FM",
		City: strPtr("São Paulo"), State: strPtr("SP"),
		StreamURL: "http://example.com/upd",
	})
	require.NoError(t, err)
	require.NotNil(t, st.Latitude)
	require.Equal(t, savedLat, *st.Latitude)
	require.Equal(t, savedLng, *st.Longitude)

	// Editar para cidade desconhecida → coordenada anterior preservada (não destrói dado).
	st, err = repo.Update(ctx, st.ID, UpdateStationInput{
		Name: "Upd FM 2", Band: "FM",
		City: strPtr("Outra Cidade Inexistente"), State: strPtr("SP"),
		StreamURL: "http://example.com/upd",
	})
	require.NoError(t, err)
	require.NotNil(t, st.Latitude)
	require.Equal(t, savedLat, *st.Latitude)
}
