package postsale

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// PayloadVersion é a versão do formato congelado. Suba quando mudar o layout de
// forma incompatível — a página pública lê `version` e escolhe o renderer, para
// que pós-vendas antigos continuem abrindo iguais ao dia do envio.
const PayloadVersion = 1

// Payload é o snapshot completo do pós-venda. Depois do publish ele é imutável:
// nenhuma recategorização, reatribuição ou mudança de PMM altera o que o
// cliente vê. É a promessa central da feature.
type Payload struct {
	Version      int             `json:"version"`
	GeneratedAt  time.Time       `json:"generated_at"`
	Client       ClientBrief     `json:"client"`
	Title        string          `json:"title"`
	IntroMessage string          `json:"intro_message"`
	PeriodLabel  string          `json:"period_label"`
	Campaigns    []CampaignBlock `json:"campaigns"`
	Footer       Footer          `json:"footer"`
}

type ClientBrief struct {
	Name    string  `json:"name"`
	LogoURL *string `json:"logo_url"`
}

// CampaignBlock é um bloco independente. Os números de blocos diferentes NUNCA
// somam — é regra de produto, não limitação técnica: cada campanha tem seu
// próprio período e seu próprio fechamento.
type CampaignBlock struct {
	CampaignID  uuid.UUID `json:"campaign_id"`
	Name        string    `json:"name"`
	Status      string    `json:"status"` // inclusive "cancelada"
	PeriodFrom  string    `json:"period_from"`
	PeriodTo    string    `json:"period_to"`
	PeriodLabel string    `json:"period_label"`

	KPIs         BlockKPIs    `json:"kpis"`
	CheckingText string       `json:"checking_text"`
	CheckingRows []StationRow `json:"checking_rows"`
	// ConformingCount é o "as outras N entregaram conforme o planejado".
	// Inclui as emissoras que o admin removeu das listas — o total fecha sempre.
	ConformingCount int `json:"conforming_count"`
	// HasBundle diz se o .zip existe. Falso no preview: os relatórios só são
	// gerados no publish.
	HasBundle bool `json:"has_bundle"`
}

// MarshalJSON garante `checking_rows: []` em vez de `null`. O frontend faz
// .filter() em cima da lista, e um null viraria TypeError na página do cliente
// — que é justamente a página que não tem ninguém pra socorrer.
func (b CampaignBlock) MarshalJSON() ([]byte, error) {
	type alias CampaignBlock // evita recursão infinita no Marshal
	a := alias(b)
	if a.CheckingRows == nil {
		a.CheckingRows = []StationRow{}
	}
	return json.Marshal(a)
}

type BlockKPIs struct {
	ValorEntregue float64 `json:"valor_entregue"`
	Impactos      int64   `json:"impactos"`
	CPM           float64 `json:"cpm"`
	Bonificacao   float64 `json:"bonificacao"`
	StationsCount int     `json:"stations_count"`

	// Bloco "no target": a UI só renderiza quando StationsWithTarget > 0.
	// Ausência de cadastro NÃO é zero — ver docs/features/client-target-pmm.md.
	ImpactosTarget     int64   `json:"impactos_target"`
	CPMTarget          float64 `json:"cpm_target"`
	StationsWithTarget int     `json:"stations_with_target"`
	TargetLabel        *string `json:"target_label"`

	// Consolidated vem do InsightsPayload: em pricing consolidado a bonificação
	// fica zerada e o card some, mesma regra do /insights.
	Consolidated bool `json:"consolidated"`

	// Overridden marca que algum número deste bloco foi ajustado à mão pelo
	// admin. Serve ao painel admin (que mostra o valor do sistema ao lado);
	// a página do cliente NÃO exibe isso — pra ele o número é o número.
	Overridden bool `json:"overridden"`
}

// Footer é o rodapé institucional, injetado por configuração para não ficar
// hardcoded no componente do frontend.
type Footer struct {
	Email     string `json:"email"`
	City      string `json:"city"`
	Instagram string `json:"instagram"`
	LinkedIn  string `json:"linkedin"`
}
