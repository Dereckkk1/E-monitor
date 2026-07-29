package postsale

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"radiocheck/internal/catalog"
)

var mesesPT = [...]string{"", "Janeiro", "Fevereiro", "Março", "Abril", "Maio",
	"Junho", "Julho", "Agosto", "Setembro", "Outubro", "Novembro", "Dezembro"}

// periodLabel é o período por extenso que aparece embaixo do nome da campanha.
// Intervalo inclusivo nas duas pontas — é assim que o cliente conta os dias
// contratados.
func periodLabel(from, to time.Time) string {
	days := int(to.Sub(from).Hours()/24) + 1
	if days <= 1 {
		return fmt.Sprintf("%s · 1 dia", from.Format("02/01/2006"))
	}
	return fmt.Sprintf("%s a %s · %d dias",
		from.Format("02/01/2006"), to.Format("02/01/2006"), days)
}

// monthsLabel monta "Junho e Julho de 2026" pro cabeçalho do documento.
// Quando o intervalo cruza o ano, cada mês carrega o próprio ano — senão
// "Dezembro e Janeiro de 2027" dataria dezembro errado.
func monthsLabel(from, to time.Time) string {
	type ym struct {
		y int
		m time.Month
	}
	var seq []ym
	cur := time.Date(from.Year(), from.Month(), 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(to.Year(), to.Month(), 1, 0, 0, 0, 0, time.UTC)
	for !cur.After(end) {
		seq = append(seq, ym{cur.Year(), cur.Month()})
		cur = cur.AddDate(0, 1, 0)
	}
	if len(seq) == 0 {
		return ""
	}
	sameYear := seq[0].y == seq[len(seq)-1].y
	parts := make([]string, 0, len(seq))
	for _, s := range seq {
		if sameYear {
			parts = append(parts, mesesPT[s.m])
		} else {
			parts = append(parts, fmt.Sprintf("%s de %d", mesesPT[s.m], s.y))
		}
	}
	joined := parts[0]
	switch {
	case len(parts) == 2:
		joined = parts[0] + " e " + parts[1]
	case len(parts) > 2:
		joined = strings.Join(parts[:len(parts)-1], ", ") + " e " + parts[len(parts)-1]
	}
	if sameYear {
		return fmt.Sprintf("%s de %d", joined, seq[0].y)
	}
	return joined
}

// conformingCount = total de emissoras do período menos as que ficaram nas
// listas. Emissora que o admin removeu migra pra cá: o total sempre fecha, e o
// cliente nunca vê uma emissora "sumir" do relatório.
func conformingCount(total int, shown []StationRow) int {
	n := total - len(shown)
	if n < 0 {
		return 0
	}
	return n
}

// BlockInput é o que o chamador (preview ou publish) fornece por campanha.
type BlockInput struct {
	CampaignID   uuid.UUID
	From, To     time.Time
	CheckingText string
	// CheckingRows é o que o admin editou. nil (primeira montagem) → derivamos
	// de StationRows; não-nil (inclusive vazio) → respeitamos a edição dele.
	CheckingRows []StationRow
	HasBundle    bool
}

// SnapshotInput agrupa o pós-venda inteiro.
type SnapshotInput struct {
	ClientID     uuid.UUID
	Title        string
	IntroMessage string
	Blocks       []BlockInput
}

// Build monta o Payload completo.
//
// MESMA função no preview e no publish. Se divergirem, o admin aprova um
// número e o cliente vê outro — que é exatamente o que o pós-venda promete
// nunca acontecer.
func (s *Service) Build(ctx context.Context, in SnapshotInput) (*Payload, error) {
	client, err := s.repo.ClientBrief(ctx, in.ClientID)
	if err != nil {
		return nil, err
	}
	p := &Payload{
		Version:      PayloadVersion,
		GeneratedAt:  s.now(),
		Client:       *client,
		Title:        in.Title,
		IntroMessage: in.IntroMessage,
		Footer:       s.footer,
		Campaigns:    []CampaignBlock{},
	}
	var minFrom, maxTo time.Time
	for _, b := range in.Blocks {
		block, err := s.buildBlock(ctx, in.ClientID, b)
		if err != nil {
			return nil, err
		}
		p.Campaigns = append(p.Campaigns, *block)
		if minFrom.IsZero() || b.From.Before(minFrom) {
			minFrom = b.From
		}
		if maxTo.IsZero() || b.To.After(maxTo) {
			maxTo = b.To
		}
	}
	if !minFrom.IsZero() {
		p.PeriodLabel = monthsLabel(minFrom, maxTo)
	}
	return p, nil
}

func (s *Service) buildBlock(ctx context.Context, clientID uuid.UUID, b BlockInput) (*CampaignBlock, error) {
	meta, err := s.repo.CampaignBrief(ctx, b.CampaignID)
	if err != nil {
		return nil, err
	}
	if meta.ClientID != clientID {
		// Última linha de defesa: montar bloco de campanha de outro cliente
		// vazaria dado entre anunciantes. O handler já valida, mas um bug lá
		// não pode virar vazamento aqui.
		return nil, fmt.Errorf("postsale: campanha %s não é do cliente %s", b.CampaignID, clientID)
	}

	ins, err := s.insights.Compute(ctx, catalog.InsightsParams{
		ClientID:    clientID,
		CampaignIDs: []uuid.UUID{b.CampaignID},
		From:        b.From,
		To:          b.To,
		Today:       s.today(),
	})
	if err != nil {
		return nil, fmt.Errorf("postsale: insights da campanha %s: %w", b.CampaignID, err)
	}

	// O total de emissoras do período é SEMPRE a verdade do banco, mesmo
	// quando o admin editou a lista: é o que faz o "as outras N" fechar.
	all, err := s.repo.StationRows(ctx, b.CampaignID, b.From, b.To)
	if err != nil {
		return nil, err
	}
	rows := b.CheckingRows
	if rows == nil {
		rows = make([]StationRow, 0, len(all))
		for _, r := range all {
			if r.Kind != KindConforming {
				rows = append(rows, r)
			}
		}
	}

	return &CampaignBlock{
		CampaignID:      b.CampaignID,
		Name:            meta.Name,
		Status:          meta.Status,
		PeriodFrom:      b.From.Format("2006-01-02"),
		PeriodTo:        b.To.Format("2006-01-02"),
		PeriodLabel:     periodLabel(b.From, b.To),
		CheckingText:    b.CheckingText,
		CheckingRows:    rows,
		ConformingCount: conformingCount(len(all), rows),
		HasBundle:       b.HasBundle,
		KPIs: BlockKPIs{
			ValorEntregue:      ins.KPIs.Investido.Executado,
			Impactos:           ins.KPIs.Impactos,
			CPM:                ins.KPIs.CPM,
			Bonificacao:        ins.KPIs.Bonificacao.Valor,
			StationsCount:      ins.KPIs.StationsCount,
			ImpactosTarget:     ins.KPIs.ImpactosTarget,
			CPMTarget:          ins.KPIs.CPMTarget,
			StationsWithTarget: ins.KPIs.StationsWithTarget,
			TargetLabel:        ins.TargetLabel,
			Consolidated:       ins.Consolidated,
		},
	}, nil
}

// DefaultCheckingText é o texto que o passo 3 sugere. O admin edita à vontade.
func DefaultCheckingText(deliveryPct int) string {
	return fmt.Sprintf("Mídia entregue com excelência! Toda a veiculação foi realizada "+
		"conforme planejada, atingindo %d%% de entrega no período determinado.", deliveryPct)
}

// DefaultIntroMessage é a mensagem de abertura sugerida.
func DefaultIntroMessage(clientName string) string {
	return strings.TrimSpace(fmt.Sprintf(
		"É um prazer ter a %s com a gente. Reunimos aqui o resultado da sua "+
			"veiculação no rádio — cada inserção monitorada, conferida e comprovada. "+
			"Qualquer dúvida, é só chamar: estamos por perto.", clientName))
}

