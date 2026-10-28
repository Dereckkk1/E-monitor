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

type campanhaInsights struct {
	Campanha ref    `json:"campanha"`
	Inicio   string `json:"inicio"`
	Fim      string `json:"fim"`
	Estado   string `json:"estado"`
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

type insightsDTO struct {
	Periodo            periodoDTO         `json:"periodo"`
	Campanhas          []campanhaInsights `json:"campanhas"`
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
	Investido       struct {
		Contratado float64 `json:"contratado"`
		Executado  float64 `json:"executado"`
	} `json:"investido"`
	Bonificacao struct {
		Valor     float64 `json:"valor"`
		Insercoes int64   `json:"insercoes"`
	} `json:"bonificacao"`
	CPM                  *float64 `json:"cpm"`
	CPMTarget            *float64 `json:"cpm_target"`
	Consolidado          bool     `json:"consolidado"`
	RateadoPorMaterial   bool     `json:"rateado_por_material"`
	ParticipacaoMaterial *float64 `json:"participacao_material"`
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

const ressalvaInsights = "Impactos = PMM × (in_slot + bonus); out_slot não vale nada e out_date está fora do período. veiculacoes soma as 4 categorias. CPM null = sem impactos no período (não é zero); cpm_target é sempre o dinâmico, mesmo com CPM fixo. impactos_target null = nenhuma emissora com PMM no target do cliente. Demografia só soma emissoras com PMM. Na série, bonus é a contagem da categoria bonus e deficit = programado − in_slot (out_slot não abate)."

const ressalvaConsolidado = "Há emissora com preço consolidado: investido.contratado é o valor do contrato, acumulado por mês iniciado até hoje (America/Sao_Paulo), não proporcional à janela."

const ressalvaRateio = "Com filtro de material, investido, bonificação, CPM e o programado da série são RATEIO pela participação do material nas veiculações (o contrato não separa valor por material); impactos, veiculações e demografia são exatos."

const ressalvaMudancaDeRegra = "Os números caíram em 2026-08-17 por mudança de regra (o executado deixou de somar out_slot e acabou a dupla contagem do excedente): relatório anterior a essa data não bate com este, e é o antigo que estava errado."

// mudancaDeRegra: o dia em que a categorização por cota entrou (docs/features/insights-dashboard.md).
var mudancaDeRegra = time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC)

// lerIDs valida até teto uuids e tira repetidos (mantendo a ordem): o
// repositório conta as campanhas achadas contra as pedidas, e um id repetido
// viraria um falso "não são do cliente".
func lerIDs(ss []string, teto int) ([]uuid.UUID, bool) {
	if len(ss) > teto {
		return nil, false
	}
	out := make([]uuid.UUID, 0, len(ss))
	for _, s := range ss {
		id, err := uuid.Parse(s)
		if err != nil {
			return nil, false
		}
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out, true
}

// lerDias lê de/ate (AAAA-MM-DD) com padrão; campo inválido volta no 3º valor.
func lerDias(de, ate string, padraoDe, padraoAte time.Time) (time.Time, time.Time, string) {
	ini, fim := padraoDe, padraoAte
	if de != "" {
		d, ok := lerDia(de)
		if !ok {
			return ini, fim, "de"
		}
		ini = d
	}
	if ate != "" {
		d, ok := lerDia(ate)
		if !ok {
			return ini, fim, "ate"
		}
		fim = d
	}
	if fim.Before(ini) {
		return ini, fim, campoPeriodo
	}
	return ini, fim, ""
}

func registrarInsights(s *sdk.Server, cfg ConfigServidor) {
	ferramentaLeitura(s, cfg, "insights_campanha",
		"Devolve o painel /insights de UM cliente: impactos (e no target), veiculações por categoria, investido "+
			"(contratado e executado), bonificação, CPM, demografia e a série por dia (ou por mês acima de 31 dias), "+
			"mais o link da tela já filtrada. Pede cliente_id e 1 a 50 campanhas dele; período padrão = mês corrente. "+
			"A armadilha: com material_ids, os valores em R$ viram rateio. Nunca some nem recalcule: os números vêm prontos.",
		func(ctx context.Context, L *Leitura, in entradaInsights) (*sdk.CallToolResult, error) {
			cliente, err := uuid.Parse(in.ClienteID)
			if err != nil {
				return recusaEntrada("cliente_id")
			}
			camps, okC := lerIDs(in.CampanhaIDs, 50)
			if !okC || len(camps) == 0 {
				return recusa("campanha_ids precisa de 1 a 50 ids válidos", "entrada_invalida")
			}
			emissoras, okE := lerIDs(in.EmissoraIDs, 200)
			if !okE {
				return recusaEntrada("emissora_ids")
			}
			materiais, okM := lerIDs(in.MaterialIDs, 200)
			if !okM {
				return recusaEntrada("material_ids")
			}
			pIni, pFim := mesCorrenteSP(L.agora())
			de, ate, campo := lerDias(in.De, in.Ate, pIni, pFim)
			if campo != "" {
				return recusaCampoDoPeriodo(campo)
			}
			pay, err := L.Insights.Compute(ctx, catalog.InsightsParams{
				ClientID: cliente, CampaignIDs: camps, From: de, To: ate,
				StationIDs: emissoras, MaterialIDs: materiais, Today: catalog.TodaySaoPaulo(),
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
			}
			if pay.MaterialProrated {
				out.Ressalvas = append(out.Ressalvas, ressalvaRateio)
			}
			if de.Before(mudancaDeRegra) {
				out.Ressalvas = append(out.Ressalvas, ressalvaMudancaDeRegra)
			}
			if slices.ContainsFunc(pay.Campaigns, func(c catalog.CampaignBrief) bool { return c.Status == "cancelada" }) {
				out.Ressalvas = append(out.Ressalvas, ressalvaCancelada)
			}
			return ok(out)
		})
}

func insightsDe(p *catalog.InsightsPayload) insightsDTO {
	k := p.KPIs
	var o insightsDTO
	o.Periodo = periodoDTO{De: p.Period.From, Ate: p.Period.To, Granularidade: map[string]string{"day": "dia", "month": "mes"}[p.Period.Granularity]}
	o.Campanhas = make([]campanhaInsights, len(p.Campaigns))
	for i, c := range p.Campaigns {
		o.Campanhas[i] = campanhaInsights{Campanha: refDe(c.ID, c.Name), Inicio: c.StartDate, Fim: c.EndDate, Estado: c.Status}
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
	o.Investido.Contratado = k.Investido.Contratado
	o.Investido.Executado = k.Investido.Executado
	o.Bonificacao.Valor = k.Bonificacao.Valor
	o.Bonificacao.Insercoes = k.Bonificacao.Count
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
