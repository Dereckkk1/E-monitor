package postsale

import (
	"archive/zip"
	"bytes"
	"testing"
	"time"

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

// Regressão do F-129. O período do bloco é DATE — dia cheio no fuso de São
// Paulo — mas os CSVs do zip filtram `detected_at timestamptz`. Enquanto o dia
// virava instante em UTC cru, `to = 31/08` virava 30/08 21:00 BRT e o último
// dia do mês saía inteiro do CSV, embora o KPI da mesma página (timezone-
// correto) contasse esse dia: o documento contradizia o próprio anexo.
//
// Caso real: pós-venda de agosto/2026 de uma campanha 27/08→30/09. O CSV parou
// em 28/08 19:01 (29 e 30 caíram em sábado e domingo) mesmo havendo veiculação
// na segunda 31/08.
func TestCSVWindow_AbrangeODiaInteiroEmSaoPaulo(t *testing.T) {
	loc := saoPaulo()
	start, end := csvWindow(date(2026, time.August, 27), date(2026, time.August, 31))

	dentro := map[string]time.Time{
		"início do primeiro dia": time.Date(2026, time.August, 27, 0, 0, 0, 0, loc),
		"veiculação de 27/08":    time.Date(2026, time.August, 27, 9, 46, 0, 0, loc),
		"veiculação de 31/08":    time.Date(2026, time.August, 31, 19, 1, 0, 0, loc),
		"fim do último dia":      time.Date(2026, time.August, 31, 23, 59, 59, 0, loc),
	}
	for nome, at := range dentro {
		require.Falsef(t, at.Before(start), "%s ficou antes da janela", nome)
		require.Falsef(t, at.After(end), "%s ficou depois da janela", nome)
	}

	fora := map[string]time.Time{
		"véspera do início": time.Date(2026, time.August, 26, 23, 0, 0, 0, loc),
		"dia seguinte ao fim": time.Date(2026, time.September, 1, 0, 0, 0, 0, loc),
	}
	for nome, at := range fora {
		require.Truef(t, at.Before(start) || at.After(end), "%s entrou na janela", nome)
	}
}
