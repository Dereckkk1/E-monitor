package catalog

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/google/uuid"
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
	// Zera ANTES, não só depois. Todo teste daqui afirma sobre o conjunto
	// inteiro da tabela ("a busca devolve só a JB", "a lista tem 1 emissora"),
	// e outros testes do pacote criam emissoras sem limpar — rodar o pacote
	// completo fazia esses testes falharem por lixo herdado.
	//
	// TRUNCATE ... CASCADE e não DELETE: emissora herdada costuma vir com
	// veiculação pendurada, e a FK barra o DELETE. Cascatear é seguro aqui —
	// TEST_DATABASE_URL é banco descartável e todo teste do pacote semeia o
	// que precisa no próprio começo.
	//
	// Efeito colateral a conhecer: isso deixa `stations` em ZERO. O guard do
	// pacote internal/api/handlers usa "stations < 50 E clients > 10" como
	// heurística de "isso é DB real, recuso rodar" — então encadear os dois
	// pacotes no MESMO banco (`go test ./internal/catalog/ ./internal/api/...`)
	// faz o handlers abortar. Rode um pacote por banco, ou limpe `clients`
	// entre eles.
	_, err = pool.Exec(ctx, `TRUNCATE stations CASCADE`)
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

// TestStations_List_ByIDs cobre a regressão que deixava o seletor de emissoras
// do /insights VAZIO em produção, sem erro nenhum no console.
//
// O seletor chamava List sem filtro e sem limit. O default é a 1ª página de 20,
// ordenada por monitoring_status/pmm/nome — com 7,5 mil emissoras no catálogo
// de prod, as 20 primeiras praticamente nunca são as da campanha, e o filtro
// client-side por target_stations zerava a lista. Em dev, com poucas emissoras,
// as 20 cobriam tudo e o bug não aparecia.
//
// O teste reproduz a forma da falha: mais emissoras do que a página default, e
// as pedidas ficam FORA dela (paused ordena depois de active). Sem o filtro
// IDs, List devolveria as 20 primeiras e nenhuma das pedidas.
func TestStations_List_ByIDs(t *testing.T) {
	ctx, repo := newTestPool(t)

	var wanted []uuid.UUID
	for i := 0; i < 25; i++ {
		st, err := repo.Create(ctx, CreateStationInput{
			Name:      fmt.Sprintf("Emissora %02d", i),
			Band:      "FM",
			StreamURL: "http://example.com/s",
		})
		require.NoError(t, err)
		// As duas últimas ficam 'paused' (default) e as demais 'active', então
		// as pedidas caem no fim da ordenação — fora da página de 20.
		if i < 23 {
			require.NoError(t, repo.UpdateMonitoringStatus(ctx, st.ID, "active"))
		} else {
			wanted = append(wanted, st.ID)
		}
	}

	// Como era antes: sem filtro, página default de 20 — nenhuma das pedidas.
	def, err := repo.List(ctx, ListInput{})
	require.NoError(t, err)
	require.Len(t, def.Data, 20, "default continua paginando em 20")
	for _, got := range def.Data {
		require.NotContains(t, wanted, got.ID, "as pedidas estão fora da 1ª página")
	}

	// Com IDs: exatamente as pedidas, sem depender de page/limit.
	out, err := repo.List(ctx, ListInput{IDs: wanted})
	require.NoError(t, err)
	require.Len(t, out.Data, len(wanted))
	got := make([]uuid.UUID, 0, len(out.Data))
	for _, s := range out.Data {
		got = append(got, s.ID)
	}
	require.ElementsMatch(t, wanted, got)
}

// IDs ignora a paginação de propósito: o caller pediu um conjunto fechado, e
// truncar em 20 devolveria um subconjunto em silêncio — o mesmo modo de falha
// que o filtro veio corrigir.
func TestStations_List_ByIDs_IgnoraPaginacao(t *testing.T) {
	ctx, repo := newTestPool(t)

	var ids []uuid.UUID
	for i := 0; i < 30; i++ {
		st, err := repo.Create(ctx, CreateStationInput{
			Name:      fmt.Sprintf("Emissora %02d", i),
			Band:      "FM",
			StreamURL: "http://example.com/s",
		})
		require.NoError(t, err)
		ids = append(ids, st.ID)
	}

	out, err := repo.List(ctx, ListInput{IDs: ids, Limit: 5, Page: 3})
	require.NoError(t, err)
	require.Len(t, out.Data, 30, "limit/page não podem cortar o conjunto pedido")
}
