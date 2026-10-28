package mcp

import (
	"context"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"radiocheck/internal/catalog"
)

type entradaDataHora struct {
	ClienteID           string   `json:"cliente_id" jsonschema:"Id do cliente (de listar_clientes); todas as campanhas têm de ser dele. Nunca invente: pergunte."`
	CampanhaIDs         []string `json:"campanha_ids" jsonschema:"De 1 a 50 campanhas do cliente (de listar_campanhas). Nunca invente: pergunte."`
	De                  string   `json:"de" jsonschema:"Primeiro dia (AAAA-MM-DD, America/Sao_Paulo). Obrigatório."`
	Ate                 string   `json:"ate" jsonschema:"Último dia (AAAA-MM-DD). Obrigatório."`
	Busca               string   `json:"busca,omitempty" jsonschema:"Até 4 palavras; cada uma tem de aparecer em emissora, cidade, UF, banda, dial, material, tipo ou cliente."`
	MaisAntigasPrimeiro bool     `json:"mais_antigas_primeiro,omitempty" jsonschema:"true para ordem cronológica (padrão: mais recentes primeiro)."`
	Paginacao
}

type linhaDataHora struct {
	veiculacaoDTO
	Cidade              *string  `json:"cidade"`
	UF                  *string  `json:"uf"`
	Banda               *string  `json:"banda"`
	Dial                *float64 `json:"dial"`
	DuracaoSeg          *float64 `json:"duracao_seg"`
	PMM                 *float64 `json:"pmm"`
	PMMTarget           *int     `json:"pmm_target"`
	ValorUnitarioTabela *float64 `json:"valor_unitario_tabela"`
	ModoPreco           *string  `json:"modo_preco"`
}

// listaDataHora embute a lista: o JSON sai {items, page, ressalvas, links}.
type listaDataHora struct {
	lista[linhaDataHora]
	Links links `json:"links"`
}

const ressalvaPrecoTabela = "valor_unitario_tabela é o preço de tabela do tipo do material naquela emissora (o mesmo que a tela mostra), NÃO o faturado da linha: out_slot e out_date não valem nada e bonus é bonificação. modo_preco consolidado = a emissora tem valor fechado no contrato, sem valor por inserção; null = sem preço cadastrado. pmm_target null = sem PMM no target do cliente nessa emissora."

// nomeModoPreco: os modos que a tela reconhece; qualquer outro (inclusive ""
// = sem linha de preço) é o "none" da tela e sai null.
var nomeModoPreco = map[string]string{catalog.PricingModePerInsertion: "por_insercao", catalog.PricingModeConsolidated: "consolidado"}

// campanhasDoCliente carrega as campanhas e recusa as de outro cliente (ou inexistentes), com os nomes.
func campanhasDoCliente(ctx context.Context, L *Leitura, cliente uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]catalog.Campaign, *sdk.CallToolResult, error) {
	todas, err := todasCampanhas(ctx, L)
	if err != nil {
		return nil, nil, err
	}
	out := make(map[uuid.UUID]catalog.Campaign, len(ids))
	var alheias []string
	for _, id := range ids {
		c, achou := todas[id]
		switch {
		case !achou:
			alheias = append(alheias, id.String()+" (não existe)")
		case c.ClientID != cliente:
			alheias = append(alheias, c.Name)
		default:
			out[id] = c
		}
	}
	if len(alheias) > 0 {
		r, _ := recusa("estas campanhas não são do cliente informado: "+strings.Join(alheias, ", ")+" — o relatório é de um cliente só", "entrada_invalida")
		return nil, r, nil
	}
	return out, nil, nil
}

// linkDataHora: /reports/airtime só busca com from E to; a competência é o mês de de.
func (c ConfigServidor) linkDataHora(cliente uuid.UUID, camps []uuid.UUID, de, ate time.Time, busca string) *string {
	q := url.Values{}
	q.Set("client_id", cliente.String())
	q.Set("competence", de.Format("2006-01"))
	q.Set("campaigns", csvIDs(camps))
	q.Set("from", dia(de))
	q.Set("to", dia(ate))
	q.Set("q", busca)
	return c.linkTela("/reports/airtime", q)
}

func registrarRelatorios(s *sdk.Server, cfg ConfigServidor) {
	ferramentaLeitura(s, cfg, "relatorio_data_hora",
		"Lista as veiculações como o Relatório Data e Hora (/reports/airtime) de UM cliente: quando, emissora (cidade, UF, "+
			"banda, dial), material, duração, campanha, categoria, PMM, PMM no target e o valor unitário de tabela, mais o link "+
			"da tela filtrada. Pede cliente_id, 1 a 50 campanhas dele e o período completo (de e ate). Para o total por material "+
			"use veiculacoes_por_material. Lista paginada com total real.",
		func(ctx context.Context, L *Leitura, in entradaDataHora) (*sdk.CallToolResult, error) {
			cliente, err := uuid.Parse(in.ClienteID)
			if err != nil {
				return recusaEntrada("cliente_id")
			}
			camps, okC := lerIDs(in.CampanhaIDs, 50)
			if !okC || len(camps) == 0 {
				return recusa("campanha_ids precisa de 1 a 50 ids válidos", "entrada_invalida")
			}
			if in.De == "" || in.Ate == "" {
				return recusa("o relatório Data e Hora pede o período completo: passe de e ate (AAAA-MM-DD)", "entrada_invalida")
			}
			ini, fim, campo := lerPeriodoSP(in.De, in.Ate)
			if campo != "" {
				return recusaCampoDoPeriodo(campo)
			}
			if _, rec, err := campanhasDoCliente(ctx, L, cliente, camps); rec != nil || err != nil {
				return rec, err
			}
			// Os preços por campanha, como a tela indexa: (campanha, emissora).
			precos := map[uuid.UUID][]catalog.StationPricing{}
			for _, c := range camps {
				ps, err := L.Precos.ListByCampaign(ctx, c)
				if err != nil {
					return nil, err
				}
				precos[c] = ps
			}
			f := catalog.ListPagedFilter{CampaignIDs: camps, StartDate: ini, EndDate: fim, Q: in.Busca}
			if in.MaisAntigasPrimeiro {
				f.Sort = "detected_at_asc"
			}
			pg, err := buscarJanela(in.Paginacao, func(page, size int) ([]catalog.DetectionEnriched, int, error) {
				f.Page, f.PageSize = page, size
				res, err := L.Veiculacoes.ListPaged(ctx, f)
				if err != nil {
					return nil, 0, err
				}
				return res.Data, res.Total, nil
			})
			if err != nil {
				return nil, err
			}
			l := mapear(pg, func(d catalog.DetectionEnriched) linhaDataHora {
				v, modo := catalog.PrecoDeTabela(precos[d.CampaignID], d.CampaignID, d.StationID, d.TypeID)
				linha := linhaDataHora{veiculacaoDTO: veiculacaoDe(d), Cidade: d.StationCity, UF: d.StationState, Banda: d.StationBand,
					Dial: d.StationFrequencyMHz, DuracaoSeg: d.MaterialDurationSec, PMM: d.StationPMM, PMMTarget: d.StationPMMTarget,
					ValorUnitarioTabela: v}
				if nome, achou := nomeModoPreco[modo]; achou {
					linha.ModoPreco = &nome
				}
				return linha
			})
			l.Ressalvas = []string{ressalvaVeiculacoes, ressalvaPrecoTabela}
			d0, _ := lerDia(in.De)
			d1, _ := lerDia(in.Ate)
			return ok(listaDataHora{lista: l, Links: links{Tela: cfg.linkDataHora(cliente, camps, d0, d1, in.Busca)}})
		})
}
