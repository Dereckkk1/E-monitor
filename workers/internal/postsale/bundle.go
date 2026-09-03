package postsale

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"radiocheck/internal/catalog"
	"radiocheck/internal/reportcsv"
)

// zipInput são os 4 arquivos que o cliente baixa por campanha.
type zipInput struct {
	MapPNG          []byte
	InsightsPNG     []byte
	ConsolidatedCSV []byte
	DetailedCSV     []byte
	CampaignSlug    string
}

// buildZip monta o pacote em memória.
//
// Os arquivos ficam numa pasta com o nome da campanha para que baixar dois
// pós-vendas não misture arquivos soltos na pasta de downloads.
func buildZip(in zipInput) ([]byte, error) {
	missing := ""
	switch {
	case len(in.MapPNG) == 0:
		missing = "o mapa ao vivo"
	case len(in.InsightsPNG) == 0:
		missing = "os indicadores"
	case len(in.ConsolidatedCSV) == 0:
		missing = "o relatório consolidado"
	case len(in.DetailedCSV) == 0:
		missing = "o relatório detalhado"
	}
	if missing != "" {
		return nil, fmt.Errorf("postsale: bundle incompleto, faltou %s", missing)
	}

	dir := in.CampaignSlug
	if dir == "" {
		dir = "campanha"
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	files := []struct {
		name string
		data []byte
	}{
		{dir + "/mapa-ao-vivo.png", in.MapPNG},
		{dir + "/indicadores.png", in.InsightsPNG},
		{dir + "/relatorio-consolidado.csv", in.ConsolidatedCSV},
		{dir + "/relatorio-detalhado.csv", in.DetailedCSV},
	}
	for _, f := range files {
		w, err := zw.Create(f.name)
		if err != nil {
			return nil, fmt.Errorf("postsale: zip create %s: %w", f.name, err)
		}
		if _, err := w.Write(f.data); err != nil {
			return nil, fmt.Errorf("postsale: zip write %s: %w", f.name, err)
		}
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("postsale: zip close: %w", err)
	}
	return buf.Bytes(), nil
}

var slugRE = regexp.MustCompile(`[^a-z0-9]+`)

// slug normaliza o nome da campanha pra virar nome de pasta dentro do zip.
func slug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = slugRE.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 60 {
		s = strings.Trim(s[:60], "-")
	}
	if s == "" {
		return "campanha"
	}
	return s
}

// assetKey monta a chave S3. Prefixo próprio (post-sale/) pra não misturar com
// evidência, que tem política de tiering e retenção diferentes.
func assetKey(reportID, campaignID uuid.UUID, name string) string {
	return fmt.Sprintf("post-sale/%s/%s/%s", reportID, campaignID, name)
}

// saoPaulo é o fuso em que o período do pós-venda é escrito. O fallback fixo
// existe porque a imagem pode não trazer tzdata, e errar o fuso aqui entrega
// mês errado ao cliente.
func saoPaulo() *time.Location {
	if loc, err := time.LoadLocation("America/Sao_Paulo"); err == nil {
		return loc
	}
	return time.FixedZone("BRT", -3*3600)
}

// csvWindow converte o período do bloco — DATEs, carregadas como meia-noite UTC
// pela convenção do pacote — nos instantes que delimitam esses dias em São
// Paulo.
//
// A conversão mora aqui, e não no handler que faz o parse, porque os CSVs são o
// ÚNICO consumidor do período que compara contra `detected_at timestamptz`
// (catalog/detections.go). Os outros dois comparam por `::date` — o checking em
// repo.go e os KPIs via `AT TIME ZONE` em insights.go — e já estão corretos:
// mover a correção pro handler os quebraria, empurrando o fim do intervalo pro
// dia seguinte. Fim de dia em microssegundos, a precisão do timestamptz.
func csvWindow(from, to time.Time) (time.Time, time.Time) {
	loc := saoPaulo()
	start := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, loc)
	end := time.Date(to.Year(), to.Month(), to.Day(), 23, 59, 59, 999999000, loc)
	return start, end
}

// buildCSVs gera os dois CSVs do período do bloco, com a MESMA formatação do
// botão "Relatórios" — é o pacote reportcsv que garante isso.
func (s *Service) buildCSVs(ctx context.Context, b BlockRow) (consolidated, detailed []byte, err error) {
	from, to := csvWindow(b.From, b.To)
	f := catalog.AggregateFilter{CampaignIDs: []uuid.UUID{b.CampaignID}, StartDate: &from, EndDate: &to}
	rows, err := s.detections.AggregateByMaterialStation(ctx, f)
	if err != nil {
		return nil, nil, fmt.Errorf("postsale: agregado consolidado: %w", err)
	}
	suffix, err := s.repo.TargetSuffix(ctx, b.CampaignID)
	if err != nil {
		return nil, nil, err
	}

	var cbuf, dbuf bytes.Buffer
	if err := reportcsv.WriteConsolidated(&cbuf, rows, suffix); err != nil {
		return nil, nil, err
	}

	pf := catalog.ListPagedFilter{
		CampaignIDs: []uuid.UUID{b.CampaignID},
		StartDate:   &from,
		EndDate:     &to,
	}
	err = reportcsv.WriteDetailed(&dbuf, func(cb func(catalog.DetectionEnriched) error) error {
		return s.detections.IterateForExport(ctx, pf, cb)
	})
	if err != nil {
		return nil, nil, err
	}
	return cbuf.Bytes(), dbuf.Bytes(), nil
}
