package mcp

import (
	"context"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"radiocheck/internal/catalog"
)

type entradaInsights struct {
	ClienteID   string   `json:"cliente_id" jsonschema:"Id do cliente (de listar_clientes). Todas as campanhas têm de ser dele. Nunca invente: pergunte."`
	CampanhaIDs []string `json:"campanha_ids" jsonschema:"De 1 a 50 campanhas do cliente (de listar_campanhas). Nunca invente: pergunte."`
	De          string   `json:"de,omitempty" jsonschema:"Primeiro dia (AAAA-MM-DD). Padrão: 1º dia do mês corrente em America/Sao_Paulo."`
	Ate         string   `json:"ate,omitempty" jsonschema:"Último dia (AAAA-MM-DD). Padrão: último dia do mês corrente."`
	EmissoraIDs []string `json:"emissora_ids,omitempty" jsonschema:"Só estas emissoras (até 200, ids de buscar_emissoras). Vazio = todas as da campanha."`
	MaterialIDs []string `json:"material_ids,omitempty" jsonschema:"Só estes materiais (até 200). Com filtro, os valores em R$ viram rateio."`
}

type periodoDTO struct {
	De            string `json:"de"`
	Ate           string `json:"ate"`
	Granularidade string `json:"granularidade"`
}

// pontoInsights: um ponto da série do gráfico. bonus é a categoria bonus da
// tocada (BucketRow.Extras conta só ela — catalog/insights.go aggregateBuckets).
type pontoInsights struct {
	Periodo    string `json:"periodo"`
	Programado int    `json:"programado"`
	InSlot     int    `json:"in_slot"`
	OutSlot    int    `json:"out_slot"`
	OutDate    int    `json:"out_date"`
	Deficit    int    `json:"deficit"`
	Bonus      int    `json:"bonus"`
}

type bonificacaoDTO struct {
	Valor     float64 `json:"valor"`
	Insercoes int64   `json:"insercoes"`
}

type insightsDTO struct {
	Periodo            periodoDTO         `json:"periodo"`
	Campanhas          []campanhaResumida `json:"campanhas"`
	Impactos           int64              `json:"impactos"`
	ImpactosTarget     *int64             `json:"impactos_target"`
	EmissorasComTarget int                `json:"emissoras_com_target"`
	RotuloTarget       *string            `json:"rotulo_target"`
	Veiculacoes        int64              `json:"veiculacoes"`
	PorCategoria       struct {
		InSlot  int64 `json:"in_slot"`
		Bonus   int64 `json:"bonus"`
		OutSlot int64 `json:"out_slot"`
		OutDate int64 `json:"out_date"`
	} `json:"por_categoria"`
	Emissoras       int `json:"emissoras"`
	EmissorasComPMM int `json:"emissoras_com_pmm"`
	// Investido: o card "Investido" da tela (KPIs.Investido.Executado). O
	// Contratado do payload não aparece em card nenhum e não sai.
	Investido float64 `json:"investido"`
	// Bonificacao: null quando a tela esconde o card (showBonificacaoDe em
	// frontend/src/utils/insightsCards.js).
	Bonificacao          *bonificacaoDTO `json:"bonificacao"`
	CPM                  *float64        `json:"cpm"`
	CPMTarget            *float64        `json:"cpm_target"`
	Consolidado          bool            `json:"consolidado"`
	RateadoPorMaterial   bool            `json:"rateado_por_material"`
	ParticipacaoMaterial *float64        `json:"participacao_material"`
	Genero               struct {
		M int64 `json:"m"`
		F int64 `json:"f"`
	} `json:"genero"`
	Classe struct {
		AB int64 `json:"ab"`
		C  int64 `json:"c"`
		DE int64 `json:"de"`
	} `json:"classe"`
	Idade struct {
		De18a24 int64 `json:"18_24"`
		De25a49 int64 `json:"25_49"`
		De50    int64 `json:"50_mais"`
	} `json:"idade"`
	Serie     []pontoInsights `json:"serie"`
	Links     links           `json:"links"`
	Ressalvas []string        `json:"ressalvas"`
}

const ressalvaInsights = "Impactos = PMM × (in_slot + bonus); out_slot não vale nada e out_date está fora do período. veiculacoes soma as 4 categorias. CPM = (investido + bonificação) ÷ impactos × 1000 (ou o CPM fixo da campanha); null = sem impactos no período (não é zero); cpm_target é sempre o dinâmico, mesmo com CPM fixo. impactos_target null = nenhuma emissora com PMM no target do cliente. Demografia só soma emissoras com PMM. Na série, bonus é a contagem da categoria bonus e deficit = programado − in_slot (out_slot não abate)."

const ressalvaConsolidado = "Há emissora com preço consolidado (modo fornecedor): investido soma, por emissora consolidada, um pacote mensal INTEIRO para cada mês de calendário da vigência que cruza a janela [de, ate] e já começou até hoje (America/Sao_Paulo) — mês parcial conta inteiro, não é proporcional aos dias —, mais o in_slot das emissoras por inserção a preço de tabela."

const ressalvaBonificacaoMista = "Seleção mista: bonificacao precifica só as emissoras por inserção. O bônus das consolidadas está em por_categoria.bonus e em impactos, sem valor (o pacote não tem preço por inserção)."

const ressalvaBonificacaoSemPreco = "bonificacao null: há emissora consolidada e nenhuma bonificação precificável nas emissoras por inserção. O pacote consolidado não tem preço por inserção, então o bônus dele fica sem valor; null NÃO quer dizer que não houve bônus (veja por_categoria.bonus)."

const ressalvaRateio = "Com filtro de material, investido, bonificação, CPM e o programado da série são RATEIO pela participação do material nas veiculações (participacao_material; o contrato não separa valor por material); impactos, veiculações e demografia são exatos."

const ressalvaMudancaDeRegra = "Os números caíram em 2026-08-17 por mudança de regra (o executado deixou de somar out_slot e acabou a dupla contagem do excedente): relatório anterior a essa data não bate com este, e é o antigo que estava errado."

// mudancaDeRegra: o dia em que a categorização por cota entrou (docs/features/insights-dashboard.md).
var mudancaDeRegra = time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC)

// mostraBonificacao: a regra da tela (showBonificacaoDe, insightsCards.js).
// Fora do modo fornecedor o card aparece sempre (zero é "não houve bônus"); com
// consolidada, só quando há parcela por inserção — ali o zero é "sem preço".
func mostraBonificacao(p *catalog.InsightsPayload) bool {
	if !p.Consolidated {
		return true
	}
	return p.KPIs.Bonificacao.Valor > 0 || p.KPIs.Bonificacao.Count > 0
}

func registrarInsights(s *sdk.Server, cfg ConfigServidor) {
	ferramentaLeitura(s, cfg, "insights_campanha",
		"Devolve o painel /insights de UM cliente: impactos (e no target), veiculações por categoria, investido, "+
			"bonificação, CPM, demografia e a série por dia (ou por mês acima de 31 dias), "+
			"mais o link da tela já filtrada. Pede cliente_id e 1 a 50 campanhas dele; período padrão = mês corrente. "+
			"A armadilha: com material_ids, os valores em R$ viram rateio. Nunca some nem recalcule: os números vêm prontos.",
		func(ctx context.Context, L *Leitura, in entradaInsights) (*sdk.CallToolResult, error) {
			cliente, err := uuid.Parse(in.ClienteID)
			if err != nil {
				return recusaEntrada("cliente_id")
			}
			camps, rec := lerIDsObrigatorios("campanha_ids", in.CampanhaIDs, 50)
			if rec != nil {
				return rec, nil
			}
			emissoras, rec := lerIDs("emissora_ids", in.EmissoraIDs, 200)
			if rec != nil {
				return rec, nil
			}
			materiais, rec := lerIDs("material_ids", in.MaterialIDs, 200)
			if rec != nil {
				return rec, nil
			}
			agora := L.agora()
			pIni, pFim := mesCorrenteSP(agora)
			de, ate, rec := lerDias(in.De, in.Ate, pIni, pFim)
			if rec != nil {
				return rec, nil
			}
			pay, err := L.Insights.Compute(ctx, catalog.InsightsParams{
				ClientID: cliente, CampaignIDs: camps, From: de, To: ate,
				// hojeSP = catalog.TodaySaoPaulo (meia-noite UTC do dia de SP), no relógio injetável.
				StationIDs: emissoras, MaterialIDs: materiais, Today: hojeSP(agora),
			})
			if err != nil {
				// O mesmo teste do handler: o repo marca campanha de outro cliente com "cross-client".
				if strings.Contains(err.Error(), "cross-client") {
					return recusa("há campanha_ids que não são do cliente informado (ou não existem): use listar_campanhas com cliente_id", "entrada_invalida")
				}
				return nil, err
			}
			out := insightsDe(pay)
			q := url.Values{}
			q.Set("client_id", cliente.String())
			q.Set("campaigns", csvIDs(camps))
			q.Set("from", dia(de))
			q.Set("to", dia(ate))
			if len(emissoras) > 0 {
				q.Set("stations", csvIDs(emissoras))
			}
			if len(materiais) > 0 {
				q.Set("materials", csvIDs(materiais))
			}
			out.Links = links{Tela: cfg.linkTela("/insights", q)}
			out.Ressalvas = []string{ressalvaInsights}
			if pay.Consolidated {
				out.Ressalvas = append(out.Ressalvas, ressalvaConsolidado)
				if out.Bonificacao == nil {
					out.Ressalvas = append(out.Ressalvas, ressalvaBonificacaoSemPreco)
				} else {
					out.Ressalvas = append(out.Ressalvas, ressalvaBonificacaoMista)
				}
			}
			if pay.MaterialProrated {
				out.Ressalvas = append(out.Ressalvas, ressalvaRateio)
			}
			if de.Before(mudancaDeRegra) {
				out.Ressalvas = append(out.Ressalvas, ressalvaMudancaDeRegra)
			}
			if slices.ContainsFunc(pay.Campaigns, func(c catalog.CampaignBrief) bool { return c.Status == "cancelada" }) {
				out.Ressalvas = append(out.Ressalvas, ressalvaCanceladaRelatorio)
			}
			return ok(out)
		})
}

func insightsDe(p *catalog.InsightsPayload) insightsDTO {
	k := p.KPIs
	var o insightsDTO
	o.Periodo = periodoDTO{De: p.Period.From, Ate: p.Period.To, Granularidade: map[string]string{"day": "dia", "month": "mes"}[p.Period.Granularity]}
	o.Campanhas = make([]campanhaResumida, len(p.Campaigns))
	for i, c := range p.Campaigns {
		o.Campanhas[i] = campanhaResumida{Campanha: refDe(c.ID, c.Name), Inicio: c.StartDate, Fim: c.EndDate, Estado: c.Status}
	}
	o.Impactos = k.Impactos
	o.EmissorasComTarget = k.StationsWithTarget
	if k.StationsWithTarget > 0 {
		v := k.ImpactosTarget
		o.ImpactosTarget = &v
	}
	o.RotuloTarget = p.TargetLabel
	o.Veiculacoes = k.VeiculacoesTotal
	o.PorCategoria.InSlot = p.VeiculacoesBreakdown.InSlot
	o.PorCategoria.Bonus = p.VeiculacoesBreakdown.ExtrasOrphan // o nome antigo da categoria bonus
	o.PorCategoria.OutSlot = p.VeiculacoesBreakdown.OutSlot
	o.PorCategoria.OutDate = p.VeiculacoesBreakdown.OutDate
	o.Emissoras = k.StationsCount
	o.EmissorasComPMM = k.StationsWithPMM
	o.Investido = k.Investido.Executado
	if mostraBonificacao(p) {
		o.Bonificacao = &bonificacaoDTO{Valor: k.Bonificacao.Valor, Insercoes: k.Bonificacao.Count}
	}
	if k.Impactos > 0 {
		v := k.CPM
		o.CPM = &v
	}
	if k.StationsWithTarget > 0 && k.ImpactosTarget > 0 {
		v := k.CPMTarget
		o.CPMTarget = &v
	}
	o.Consolidado = p.Consolidated
	o.RateadoPorMaterial = p.MaterialProrated
	if p.MaterialProrated {
		v := p.MaterialShare
		o.ParticipacaoMaterial = &v
	}
	o.Genero.M, o.Genero.F = k.Gender.M, k.Gender.F
	o.Classe.AB, o.Classe.C, o.Classe.DE = p.ClassPyramid.AB, p.ClassPyramid.C, p.ClassPyramid.DE
	o.Idade.De18a24, o.Idade.De25a49, o.Idade.De50 = p.AgeRanges.R18_24, p.AgeRanges.R25_49, p.AgeRanges.R50Plus
	o.Serie = make([]pontoInsights, len(p.Buckets)) // Buckets pode vir nil: sai []
	for i, b := range p.Buckets {
		o.Serie[i] = pontoInsights{Periodo: b.Bucket, Programado: b.Programado, InSlot: b.InSlot, OutSlot: b.OutSlot,
			OutDate: b.OutDate, Deficit: b.Deficit, Bonus: b.Extras}
	}
	return o
}
