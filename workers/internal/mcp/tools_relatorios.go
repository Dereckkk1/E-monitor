package mcp

import (
	"context"
	"fmt"
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
			"use veiculacoes_por_material. Lista paginada (até 50 por página) com total real.",
		func(ctx context.Context, L *Leitura, in entradaDataHora) (*sdk.CallToolResult, error) {
			cliente, err := uuid.Parse(in.ClienteID)
			if err != nil {
				return recusaEntrada("cliente_id")
			}
			camps, rec := lerIDsObrigatorios("campanha_ids", in.CampanhaIDs, 50)
			if rec != nil {
				return rec, nil
			}
			if in.De == "" || in.Ate == "" {
				return recusa("o relatório Data e Hora pede o período completo: passe de e ate (AAAA-MM-DD)", "entrada_invalida")
			}
			ini, fim, campo := lerPeriodoSP(in.De, in.Ate)
			if campo != "" {
				return recusaCampoDoPeriodo(campo)
			}
			doCliente, rec, err := campanhasDoCliente(ctx, L, cliente, camps)
			if rec != nil || err != nil {
				return rec, err
			}
			f := catalog.ListPagedFilter{CampaignIDs: camps, StartDate: ini, EndDate: fim, Q: in.Busca}
			if in.MaisAntigasPrimeiro {
				f.Sort = "detected_at_asc"
			}
			pg, err := buscarJanela(in.Paginacao.comTeto(tetoPaginaDataHora), func(page, size int) ([]catalog.DetectionEnriched, int, error) {
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
			// Os preços como a tela indexa, (campanha, emissora), só das
			// campanhas que aparecem nesta página.
			precos := map[uuid.UUID][]catalog.StationPricing{}
			for _, d := range pg.Items {
				if _, lido := precos[d.CampaignID]; lido {
					continue
				}
				ps, err := L.Precos.ListByCampaign(ctx, d.CampaignID)
				if err != nil {
					return nil, err
				}
				precos[d.CampaignID] = ps
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
			for _, c := range doCliente {
				if c.Status == "cancelada" {
					l.Ressalvas = append(l.Ressalvas, ressalvaCanceladaRelatorio)
					break
				}
			}
			d0, _ := lerDia(in.De)
			d1, _ := lerDia(in.Ate)
			return ok(listaDataHora{lista: l, Links: links{Tela: cfg.linkDataHora(cliente, camps, d0, d1, in.Busca)}})
		})
}

type entradaRelatorioCampanha struct {
	CampanhaID string `json:"campanha_id" jsonschema:"Id (uuid) da campanha, de listar_campanhas ou obter_campanha. Nunca invente: pergunte."`
	De         string `json:"de,omitempty" jsonschema:"Primeiro dia (AAAA-MM-DD, America/Sao_Paulo). Padrão: início da campanha."`
	Ate        string `json:"ate,omitempty" jsonschema:"Último dia (AAAA-MM-DD). Padrão: fim da campanha."`
}

type entradaResumoRelatorio struct {
	entradaRelatorioCampanha
	Paginacao
}

type materialResumo struct {
	Material    ref      `json:"material"`
	IDCurto     *int32   `json:"id_curto"`
	Tipo        *string  `json:"tipo"`
	DuracaoSeg  *float64 `json:"duracao_seg"`
	Veiculacoes int      `json:"veiculacoes"`
}

type emissoraResumo struct {
	Emissora       ref      `json:"emissora"`
	Cidade         *string  `json:"cidade"`
	UF             *string  `json:"uf"`
	Banda          *string  `json:"banda"`
	Dial           *float64 `json:"dial"`
	PMM            *float64 `json:"pmm"`
	PMMTarget      *int     `json:"pmm_target"`
	Veiculacoes    int      `json:"veiculacoes"`
	BaseDeImpactos int      `json:"base_de_impactos"`
	Impactos       *int64   `json:"impactos"`
}

type resumoDTO struct {
	Campanha     campanhaResumida `json:"campanha"`
	Cliente      ref              `json:"cliente"`
	RotuloTarget *string          `json:"rotulo_target"`
	Periodo      struct {
		De  string `json:"de"`
		Ate string `json:"ate"`
	} `json:"periodo"`
	Totais struct {
		Veiculacoes        int    `json:"veiculacoes"`
		Materiais          int    `json:"materiais"`
		Emissoras          int    `json:"emissoras"`
		Impactos           int64  `json:"impactos"`
		ImpactosTarget     *int64 `json:"impactos_target"`
		EmissorasComTarget int    `json:"emissoras_com_target"`
	} `json:"totais"`
	PorMaterial      []materialResumo `json:"por_material"`
	PorMaterialTotal int              `json:"por_material_total"`
	lista[emissoraResumo]
	Links links `json:"links"`
}

const ressalvaResumo = "É o relatório da campanha (o mesmo do PDF): items é a lista por emissora, paginada. veiculacoes conta todas as categorias aprovadas; impactos = round(PMM × base_de_impactos), base_de_impactos = in_slot + bonus. impactos null = emissora sem PMM; impactos_target null = nenhuma emissora com PMM no target do cliente. Sem valor em R$: para dinheiro use financeiro_campanhas ou insights_campanha."

// tetoPaginaDataHora: o teto de página do relatorio_data_hora. Cada linha tem
// ~25 campos (~900 bytes com nomes longos): com o teto global (100) a página
// media 90 KB, acima do que o cliente MCP aceita (~25k tokens). Medido em
// TestRelatorioDataHora_PiorCasoCabe.
const tetoPaginaDataHora = 50

// tetoMateriaisResumo: por_material não é paginada (a lista principal é a de
// emissoras); acima disso sai cortada com o total real.
const tetoMateriaisResumo = 50

// periodoDaCampanha: de/ate da entrada, ou a vigência da campanha; devolve os
// instantes de SP (o recorte das consultas, como a tela manda) e os dias (o eco e o link).
// rec != nil é a recusa pronta.
func periodoDaCampanha(c *catalog.Campaign, de, ate string) (ini, fim *time.Time, d0, d1 time.Time, rec *sdk.CallToolResult) {
	d0, d1, rec = lerDias(de, ate, c.StartDate, c.EndDate)
	if rec != nil {
		return nil, nil, d0, d1, rec
	}
	i, _ := limitesDoDiaSP(d0)
	_, f := limitesDoDiaSP(d1)
	return &i, &f, d0, d1, nil
}

func registrarRelatoriosCampanha(s *sdk.Server, cfg ConfigServidor) {
	ferramentaLeitura(s, cfg, "resumo_relatorio_campanha",
		"Devolve o relatório de UMA campanha (o mesmo que vira PDF no portal): totais de veiculações, materiais, emissoras, "+
			"impactos e impactos no target, a contagem por material e a lista por emissora (paginada, com PMM e impactos), "+
			"mais o link da tela. Período padrão = vigência da campanha. Para baixar o CSV use exportar_consolidado.",
		func(ctx context.Context, L *Leitura, in entradaResumoRelatorio) (*sdk.CallToolResult, error) {
			c, rec, err := lerCampanha(ctx, L, in.CampanhaID)
			if rec != nil || err != nil {
				return rec, err
			}
			ini, fim, d0, d1, rec := periodoDaCampanha(c, in.De, in.Ate)
			if rec != nil {
				return rec, nil
			}
			cli, err := L.Clientes.Get(ctx, c.ClientID)
			if err != nil {
				return nil, err
			}
			f := catalog.AggregateFilter{CampaignIDs: []uuid.UUID{c.ID}, StartDate: ini, EndDate: fim}
			porMat, err := L.Veiculacoes.AggregateByMaterial(ctx, f)
			if err != nil {
				return nil, err
			}
			porEst, err := L.Veiculacoes.AggregateByStation(ctx, f)
			if err != nil {
				return nil, err
			}
			tot := catalog.TotaisDoResumo(porMat, porEst)
			var o resumoDTO
			o.Campanha = campanhaResumidaDe(c)
			o.Cliente = refDe(cli.ID, cli.Name)
			o.RotuloTarget = cli.TargetLabel
			o.Periodo.De, o.Periodo.Ate = dia(d0), dia(d1)
			o.Totais.Veiculacoes, o.Totais.Materiais, o.Totais.Emissoras = tot.Detections, tot.DistinctMaterials, tot.DistinctStations
			o.Totais.Impactos, o.Totais.EmissorasComTarget = tot.Impactos, tot.StationsWithTarget
			if tot.StationsWithTarget > 0 {
				v := tot.ImpactosTarget
				o.Totais.ImpactosTarget = &v
			}
			mats, n := cortar(porMat.Data, tetoMateriaisResumo)
			o.PorMaterialTotal = n
			o.PorMaterial = make([]materialResumo, len(mats))
			for i, m := range mats {
				o.PorMaterial[i] = materialResumo{Material: refDe(m.MaterialID, m.MaterialTitle), IDCurto: m.MaterialShortID,
					Tipo: m.MaterialTypeName, DuracaoSeg: m.MaterialDurationSec, Veiculacoes: m.Count}
			}
			o.lista = mapear(paginar(porEst, in.Paginacao), func(s catalog.StationAggregateRow) emissoraResumo {
				return emissoraResumo{Emissora: refDe(s.StationID, s.StationName), Cidade: s.StationCity, UF: s.StationState,
					Banda: s.StationBand, Dial: s.StationFrequencyMHz, PMM: s.StationPMM, PMMTarget: s.StationPMMTarget,
					Veiculacoes: s.Count, BaseDeImpactos: s.ImpactCount, Impactos: catalog.ImpactosDaEmissora(s)}
			})
			o.Ressalvas = []string{ressalvaResumo}
			if n > tetoMateriaisResumo {
				o.Ressalvas = append(o.Ressalvas, fmt.Sprintf("por_material cortado em %d de %d materiais (por_material_total traz o número real)", tetoMateriaisResumo, n))
			}
			if c.Status == "cancelada" {
				o.Ressalvas = append(o.Ressalvas, ressalvaCanceladaRelatorio)
			}
			o.Links = links{Tela: cfg.linkDataHora(c.ClientID, []uuid.UUID{c.ID}, d0, d1, ""),
				ComoBaixar: "Na tela, botão Relatórios → PDF (o mesmo conteúdo) ou CSV Consolidado."}
			return ok(o)
		})

	ferramentaLeitura(s, cfg, "exportar_consolidado",
		"Prepara o download do CSV Consolidado de UMA campanha (uma linha por material × emissora, com categorias, PMM e "+
			"impactos): devolve quantas linhas o arquivo vai ter, o período e o link da tela onde baixar. O arquivo não vem "+
			"no resultado nem por link direto: o portal exige o login da pessoa. Período padrão = vigência da campanha.",
		func(ctx context.Context, L *Leitura, in entradaRelatorioCampanha) (*sdk.CallToolResult, error) {
			c, rec, err := lerCampanha(ctx, L, in.CampanhaID)
			if rec != nil || err != nil {
				return rec, err
			}
			ini, fim, d0, d1, rec := periodoDaCampanha(c, in.De, in.Ate)
			if rec != nil {
				return rec, nil
			}
			// As mesmas linhas que o handler escreve (reportcsv.WriteConsolidated: uma por linha, sem rodapé).
			rows, err := L.Veiculacoes.AggregateByMaterialStation(ctx, catalog.AggregateFilter{CampaignIDs: []uuid.UUID{c.ID}, StartDate: ini, EndDate: fim})
			if err != nil {
				return nil, err
			}
			return ok(exportacaoDe(c, d0, d1, len(rows), cfg.linkDataHora(c.ClientID, []uuid.UUID{c.ID}, d0, d1, ""),
				"Na tela, botão Relatórios → CSV Consolidado (sai com o período da tela)."))
		})

	ferramentaLeitura(s, cfg, "exportar_veiculacoes",
		"Prepara o download do CSV Detalhado de UMA campanha (uma linha por veiculação: data, hora, rádio, peça, status, "+
			"PMM e preço): devolve quantas linhas o arquivo vai ter, o período e o link da tela onde baixar. O arquivo não vem "+
			"no resultado nem por link direto. Período padrão = vigência da campanha. Para ler as linhas aqui, use relatorio_data_hora.",
		func(ctx context.Context, L *Leitura, in entradaRelatorioCampanha) (*sdk.CallToolResult, error) {
			c, rec, err := lerCampanha(ctx, L, in.CampanhaID)
			if rec != nil || err != nil {
				return rec, err
			}
			ini, fim, d0, d1, rec := periodoDaCampanha(c, in.De, in.Ate)
			if rec != nil {
				return rec, nil
			}
			n, err := contarExportacao(ctx, L, c.ID, ini, fim)
			if err != nil {
				return nil, err
			}
			return ok(exportacaoDe(c, d0, d1, n, cfg.linkDataHora(c.ClientID, []uuid.UUID{c.ID}, d0, d1, ""),
				"Na tela, botão Relatórios → CSV Detalhado (só admin; sai com o período da tela). Depois das linhas, o arquivo traz o rodapé de totais do fornecedor."))
		})
}

type exportacaoDTO struct {
	Campanha campanhaResumida `json:"campanha"`
	Periodo  struct {
		De  string `json:"de"`
		Ate string `json:"ate"`
	} `json:"periodo"`
	Linhas    int      `json:"linhas"`
	Links     links    `json:"links"`
	Ressalvas []string `json:"ressalvas"`
}

func exportacaoDe(c *catalog.Campaign, d0, d1 time.Time, linhas int, tela *string, comoBaixar string) exportacaoDTO {
	o := exportacaoDTO{Campanha: campanhaResumidaDe(c), Linhas: linhas, Links: links{Tela: tela, ComoBaixar: comoBaixar}}
	o.Periodo.De, o.Periodo.Ate = dia(d0), dia(d1)
	o.Ressalvas = []string{}
	if c.Status == "cancelada" {
		o.Ressalvas = append(o.Ressalvas, ressalvaCanceladaRelatorio)
	}
	return o
}

// contarExportacao: o nº de linhas de dados do CSV Detalhado. IterateForExport
// (o que o /detections/export escreve) e ListPaged têm o MESMO WHERE (ignorada,
// retratada e audit_rejected fora; uma linha por atribuição) e os JOINs de preço
// do export são 1:1 (chave primária por campanha × emissora [× tipo]), então o
// total do ListPaged é o nº de linhas — provado por
// TestExportarVeiculacoes_LinhasBatemComOCSV.
func contarExportacao(ctx context.Context, L *Leitura, campanha uuid.UUID, ini, fim *time.Time) (int, error) {
	res, err := L.Veiculacoes.ListPaged(ctx, catalog.ListPagedFilter{CampaignIDs: []uuid.UUID{campanha}, StartDate: ini, EndDate: fim, Page: 1, PageSize: 1})
	if err != nil {
		return 0, err
	}
	return res.Total, nil
}
