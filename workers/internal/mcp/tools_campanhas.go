package mcp

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"radiocheck/internal/catalog"
	"radiocheck/internal/hub"
)

type campanhaDTO struct {
	ID            string   `json:"id"`
	Nome          string   `json:"nome"`
	Cliente       ref      `json:"cliente"`
	Estado        string   `json:"estado"`
	Inicio        string   `json:"inicio"`
	Fim           string   `json:"fim"`
	EmissorasAlvo int      `json:"emissoras_alvo"`
	Materiais     int      `json:"materiais"`
	CPMFixo       *float64 `json:"cpm_fixo"`
	CodigoHub     *string  `json:"codigo_hub"`
	CanceladaEm   *string  `json:"cancelada_em"`
}

func campanhaDe(c catalog.Campaign, clientes map[uuid.UUID]string, canceladas map[uuid.UUID]time.Time) campanhaDTO {
	d := campanhaDTO{
		ID: c.ID.String(), Nome: c.Name, Cliente: refDe(c.ClientID, clientes[c.ClientID]), Estado: c.Status,
		Inicio: dia(c.StartDate), Fim: dia(c.EndDate), EmissorasAlvo: len(c.TargetStations),
		Materiais: c.MaterialCount, CPMFixo: c.FixedCPM, CodigoHub: c.HubCode,
	}
	if em, ok := canceladas[c.ID]; ok {
		s := instante(em)
		d.CanceladaEm = &s
	}
	return d
}

var estadosCampanha = []string{"programada", "ativa", "concluida", "cancelada"}

// tetoCampanhas: a tabela tem centenas de linhas; o corte só existe para a
// consulta nunca ser ilimitada.
const tetoCampanhas = 10_000

const ressalvaCancelada = "Há campanha cancelada no resultado: ela aparece só como histórico — fica fora de KPIs, seletores e cobrança, e o déficit dela está congelado em cancelada_em."

const ressalvaBaseFinanceira = "Inserções, impactos, investido e bonificação usam a base in_slot + bonus (out_slot não vale nada e out_date está fora do período). O consolidado acumula por mês iniciado até hoje (America/Sao_Paulo). impactos_target só soma emissoras com PMM no target do cliente. CPM: o fixo, quando a campanha tem; senão (investido + bonificação) ÷ impactos × 1000."

// lerCampanha valida o id e carrega a campanha; rec != nil é a recusa pronta.
func lerCampanha(ctx context.Context, L *Leitura, s string) (*catalog.Campaign, *sdk.CallToolResult, error) {
	id, err := uuid.Parse(s)
	if err != nil {
		rec, _ := recusaEntrada("campanha_id")
		return nil, rec, nil
	}
	c, err := L.Campanhas.Get(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		rec, _ := naoEncontrado("campanha")
		return nil, rec, nil
	}
	if err != nil {
		return nil, nil, err
	}
	return c, nil, nil
}

type entradaListarCampanhas struct {
	Estados           []string `json:"estados,omitempty" jsonschema:"Filtra por estado: programada, ativa, concluida, cancelada. Vazio = todos menos cancelada."`
	ClienteID         string   `json:"cliente_id,omitempty" jsonschema:"Só as campanhas deste cliente (id de listar_clientes)."`
	Busca             string   `json:"busca,omitempty" jsonschema:"Texto no nome da campanha ou do cliente. Ignora acento e caixa."`
	Competencia       string   `json:"competencia,omitempty" jsonschema:"Mês AAAA-MM: só campanhas cuja vigência cruza esse mês."`
	IncluirCanceladas bool     `json:"incluir_canceladas,omitempty" jsonschema:"true para trazer as canceladas junto (por padrão ficam fora)."`
	Paginacao
}

type entradaObterCampanha struct {
	CampanhaID string `json:"campanha_id,omitempty" jsonschema:"Id (uuid) da campanha no E-monitor. Passe este OU codigo."`
	Codigo     string `json:"codigo,omitempty" jsonschema:"Código da campanha no hub (EH-XXXXXX), o mesmo que o MCP do hub devolve. Passe este OU campanha_id."`
}

type linksTela struct {
	Tela *string `json:"tela"`
}

type saidaObterCampanha struct {
	Campanha  campanhaDTO `json:"campanha"`
	Emissoras []ref       `json:"emissoras"`
	Links     linksTela   `json:"links"`
	Ressalvas []string    `json:"ressalvas,omitempty"`
}

type entradaFinanceiro struct {
	CampanhaIDs []string `json:"campanha_ids" jsonschema:"Ids das campanhas (1 a 100), vindos de listar_campanhas. Nunca invente: pergunte."`
}

type linhaFinanceira struct {
	Campanha           ref      `json:"campanha"`
	Estado             string   `json:"estado"`
	Investido          float64  `json:"investido"`
	Bonificacao        float64  `json:"bonificacao"`
	Insercoes          int      `json:"insercoes"`
	Impactos           float64  `json:"impactos"`
	ImpactosTarget     float64  `json:"impactos_target"`
	EmissorasComTarget int      `json:"emissoras_com_target"`
	CPMFixo            *float64 `json:"cpm_fixo"`
	CPM                *float64 `json:"cpm"`
}

func registrarCampanhas(s *sdk.Server, cfg ConfigServidor) {
	ferramentaLeitura(s, cfg, "listar_campanhas",
		"Lista as campanhas na ordem da tela /campaigns (ativas, programadas, concluídas): id, nome, cliente, estado, vigência, "+
			"nº de emissoras-alvo e de materiais, CPM fixo e código do hub. Filtros: estados, cliente_id, busca, competencia (AAAA-MM). "+
			"A armadilha: canceladas ficam FORA por padrão (são só histórico); peça incluir_canceladas ou estados=[cancelada]. "+
			"Lista paginada: siga page.has_more.",
		func(ctx context.Context, L *Leitura, in entradaListarCampanhas) (*sdk.CallToolResult, error) {
			var clientes []uuid.UUID // nil = sem filtro; slice vazio filtraria TUDO
			if in.ClienteID != "" {
				id, err := uuid.Parse(in.ClienteID)
				if err != nil {
					return recusaEntrada("cliente_id")
				}
				clientes = []uuid.UUID{id}
			}
			if in.Competencia != "" {
				if _, err := time.Parse("2006-01", in.Competencia); err != nil {
					return recusa("competencia tem de ser AAAA-MM (ex.: 2026-10)", "entrada_invalida")
				}
			}
			for _, e := range in.Estados {
				if !slices.Contains(estadosCampanha, e) {
					return recusa(fmt.Sprintf("estado desconhecido %q: use programada, ativa, concluida ou cancelada", e), "entrada_invalida")
				}
			}
			todas, total, err := L.Campanhas.ListPaged(ctx, in.Busca, in.Competencia, clientes, nil, 1, tetoCampanhas)
			if err != nil {
				return nil, err
			}
			querCancelada := in.IncluirCanceladas || slices.Contains(in.Estados, "cancelada")
			filtradas := make([]catalog.Campaign, 0, len(todas))
			for _, c := range todas {
				if len(in.Estados) > 0 && !slices.Contains(in.Estados, c.Status) {
					continue
				}
				if c.Status == "cancelada" && !querCancelada {
					continue
				}
				filtradas = append(filtradas, c)
			}
			pg := paginar(filtradas, in.Paginacao)
			ids := make([]uuid.UUID, len(pg.Items))
			for i, c := range pg.Items {
				ids[i] = c.ID
			}
			canceladas, err := L.Campanhas.CancelledAtFor(ctx, ids)
			if err != nil {
				return nil, err
			}
			nomes, err := nomesClientes(ctx, L)
			if err != nil {
				return nil, err
			}
			out := mapear(pg, func(c catalog.Campaign) campanhaDTO { return campanhaDe(c, nomes, canceladas) })
			if len(canceladas) > 0 {
				out.Ressalvas = append(out.Ressalvas, ressalvaCancelada)
			}
			if total > tetoCampanhas {
				out.Ressalvas = append(out.Ressalvas, fmt.Sprintf(
					"a busca casou %d campanhas e só as %d primeiras foram consideradas: estreite com cliente_id, competencia ou busca", total, tetoCampanhas))
			}
			return ok(out)
		})

	ferramentaLeitura(s, cfg, "obter_campanha",
		"Devolve uma campanha (cliente, estado, vigência, CPM fixo, código do hub, nº de materiais), as emissoras-alvo com nome e "+
			"o link da tela. Aceita campanha_id OU codigo (EH-XXXXXX, o código canônico do hub, a ponte entre os MCPs). Um código "+
			"pode estar em mais de uma campanha: aí a tool lista as candidatas e você pergunta qual. Nunca invente ids.",
		func(ctx context.Context, L *Leitura, in entradaObterCampanha) (*sdk.CallToolResult, error) {
			if (in.CampanhaID == "") == (in.Codigo == "") {
				return recusa("passe campanha_id OU codigo (exatamente um dos dois)", "entrada_invalida")
			}
			var c *catalog.Campaign
			if in.CampanhaID != "" {
				got, rec, err := lerCampanha(ctx, L, in.CampanhaID)
				if rec != nil || err != nil {
					return rec, err
				}
				c = got
			} else {
				cod := hub.NormalizaCodigo(in.Codigo)
				if cod == "" {
					return recusa("codigo não é um código de campanha do hub (formato EH-XXXXXX)", "entrada_invalida")
				}
				achadas, err := L.Campanhas.ListByHubCode(ctx, cod)
				if err != nil {
					return nil, err
				}
				switch len(achadas) {
				case 0:
					return naoEncontrado("campanha com o código " + cod)
				case 1:
					c = &achadas[0]
				default:
					cands := make([]string, len(achadas))
					for i, a := range achadas {
						cands[i] = fmt.Sprintf("%s (id %s, %s)", a.Name, a.ID, a.Status)
					}
					return recusa(fmt.Sprintf("o código %s está em %d campanhas (dois PIs podem dividir um código): %s. Pergunte ao usuário qual e chame de novo com campanha_id",
						cod, len(achadas), strings.Join(cands, "; ")), "ambiguo")
				}
			}
			vinculos, err := L.MateriaisCampanha.ListByCampaign(ctx, c.ID)
			if err != nil {
				return nil, err
			}
			c.MaterialCount = len(vinculos)
			nomes, err := nomesClientes(ctx, L)
			if err != nil {
				return nil, err
			}
			canceladas, err := L.Campanhas.CancelledAtFor(ctx, []uuid.UUID{c.ID})
			if err != nil {
				return nil, err
			}
			emissoras, err := nomesEmissoras(ctx, L, c.TargetStations)
			if err != nil {
				return nil, err
			}
			out := saidaObterCampanha{
				Campanha:  campanhaDe(*c, nomes, canceladas),
				Emissoras: refs(c.TargetStations, emissoras),
				Links:     linksTela{Tela: cfg.link("/campaigns/" + c.ID.String() + "/edit")},
			}
			if c.Status == "cancelada" {
				out.Ressalvas = []string{ressalvaCancelada}
			}
			return ok(out)
		})

	ferramentaLeitura(s, cfg, "financeiro_campanhas",
		"Devolve, por campanha, o que a tela /campaigns mostra de dinheiro e audiência: investido executado, bonificação, "+
			"inserções, impactos, impactos no target, emissoras com target e CPM (o fixo, ou o dinâmico já calculado). "+
			"Pede de 1 a 100 campanha_ids (pegue de listar_campanhas). Nunca some nem recalcule: os números vêm prontos.",
		func(ctx context.Context, L *Leitura, in entradaFinanceiro) (*sdk.CallToolResult, error) {
			if len(in.CampanhaIDs) == 0 || len(in.CampanhaIDs) > limiteTeto {
				return recusa("campanha_ids precisa de 1 a 100 ids", "entrada_invalida")
			}
			ids := make([]uuid.UUID, 0, len(in.CampanhaIDs))
			for _, s := range in.CampanhaIDs {
				id, err := uuid.Parse(s)
				if err != nil {
					return recusaEntrada("campanha_ids")
				}
				ids = append(ids, id)
			}
			fins, err := L.Campanhas.FinancialsByCampaign(ctx, nil, ids, catalog.TodaySaoPaulo())
			if err != nil {
				return nil, err
			}
			camps, err := todasCampanhas(ctx, L)
			if err != nil {
				return nil, err
			}
			achou := map[uuid.UUID]bool{}
			cancelada := false
			linhas := make([]linhaFinanceira, 0, len(fins))
			for _, f := range fins {
				c := camps[f.CampaignID]
				achou[f.CampaignID] = true
				cancelada = cancelada || c.Status == "cancelada"
				linhas = append(linhas, linhaFinanceira{
					Campanha: refDe(f.CampaignID, c.Name), Estado: c.Status,
					Investido: f.TotalInvested, Bonificacao: f.TotalBonusValue, Insercoes: f.TotalInsertions,
					Impactos: f.TotalAudience, ImpactosTarget: f.TotalAudienceTarget,
					EmissorasComTarget: f.StationsWithTarget, CPMFixo: f.FixedCPM, CPM: catalog.CPMDaCampanha(f),
				})
			}
			l := listaInteira(linhas)
			l.Ressalvas = []string{ressalvaBaseFinanceira}
			var faltam []string
			for _, id := range ids {
				if !achou[id] {
					faltam = append(faltam, id.String())
				}
			}
			if len(faltam) > 0 {
				l.Ressalvas = append(l.Ressalvas, "sem campanha no E-monitor para: "+strings.Join(faltam, ", "))
			}
			if cancelada {
				l.Ressalvas = append(l.Ressalvas, ressalvaCancelada)
			}
			return ok(l)
		})
}
