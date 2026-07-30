package postsale

import (
	"archive/zip"
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildZip_ContemOsQuatroArquivos(t *testing.T) {
	blob, err := buildZip(zipInput{
		MapPNG:          []byte("fake-png-map"),
		InsightsPNG:     []byte("fake-png-insights"),
		ConsolidatedCSV: []byte("a;b;c"),
		DetailedCSV:     []byte("d;e;f"),
		CampaignSlug:    "249-engie",
	})
	require.NoError(t, err)

	zr, err := zip.NewReader(bytes.NewReader(blob), int64(len(blob)))
	require.NoError(t, err)

	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	require.True(t, names["249-engie/mapa-ao-vivo.png"], "faltou o mapa")
	require.True(t, names["249-engie/indicadores.png"], "faltou o insights")
	require.True(t, names["249-engie/relatorio-consolidado.csv"], "faltou o consolidado")
	require.True(t, names["249-engie/relatorio-detalhado.csv"], "faltou o detalhado")
	require.Len(t, zr.File, 4)
}

// Asset faltando é erro: melhor abortar o publish do que entregar ao cliente um
// zip com um "mapa" vazio dentro.
func TestBuildZip_FalhaSeFaltaAsset(t *testing.T) {
	_, err := buildZip(zipInput{ConsolidatedCSV: []byte("x"), CampaignSlug: "s"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "mapa")

	_, err = buildZip(zipInput{
		MapPNG: []byte("m"), InsightsPNG: []byte("i"), ConsolidatedCSV: []byte("c"),
		CampaignSlug: "s",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "detalhado")
}

func TestSlug(t *testing.T) {
	require.Equal(t, "249-engie-plano-pae", slug("249 ENGIE | PLANO PAE"))
	require.Equal(t, "campanha", slug("   "))
	require.Equal(t, "campanha", slug("!!!"))
	require.LessOrEqual(t, len(slug(string(make([]byte, 200)))), 60)
}
