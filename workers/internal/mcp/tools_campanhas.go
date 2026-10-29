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

// lerCampanhaCompleta monta o que obter_campanha devolve; as escritas que mexem
// numa campanha devolvem o MESMO formato depois de gravar. Preenche
// c.MaterialCount (o Get do repositório não o traz).
func lerCampanhaCompleta(ctx context.Context, cfg ConfigServidor, L *Leitura, c *catalog.Campaign) (*saidaObterCampanha, error) {
	vinculos, err := L.MateriaisCampanha.ListByCampaign(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	c.MaterialCount = len(vinculos)
	cliente, err := L.Clientes.Get(ctx, c.ClientID)
	if err != nil {
		return nil, err
	}
	nomes := map[uuid.UUID]string{cliente.ID: cliente.Name}
	canceladas, err := L.Campanhas.CancelledAtFor(ctx, []uuid.UUID{c.ID})
	if err != nil {
		return nil, err
	}
	emissoras, err := nomesEmissoras(ctx, L, c.TargetStations)
	if err != nil {
		return nil, err
	}
	out := &saidaObterCampanha{
		Campanha:  campanhaDe(*c, nomes, canceladas),
		Emissoras: refs(c.TargetStations, emissoras),
		Links:     linksTela{Tela: cfg.link("/campaigns/" + c.ID.String() + "/edit")},
	}
	if c.Status == "cancelada" {
		out.Ressalvas = []string{ressalvaCancelada}
	}
	return out, nil
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
			var idsClientes []uuid.UUID
			for _, c := range pg.Items {
				idsClientes = append(idsClientes, c.ClientID)
			}
			cls, err := L.Clientes.ListByIDs(ctx, idsClientes)
			if err != nil {
				return nil, err
			}
			nomes := make(map[uuid.UUID]string, len(cls))
			for _, cl := range cls {
				nomes[cl.ID] = cl.Name
			}
			out := mapear(pg, func(c catalog.Campaign) campanhaDTO { return campanhaDe(c, nomes, canceladas) })
			if slices.ContainsFunc(pg.Items, func(c catalog.Campaign) bool { return c.Status == "cancelada" }) {
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
			out, err := lerCampanhaCompleta(ctx, cfg, L, c)
			if err != nil {
				return nil, err
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
			porID := make(map[uuid.UUID]catalog.CampaignFinancials, len(fins))
			for _, f := range fins {
				porID[f.CampaignID] = f
			}
			cancelada := false
			var faltam []string
			vistos := map[uuid.UUID]bool{}
			linhas := make([]linhaFinanceira, 0, len(fins))
			for _, id := range ids { // ordem da entrada, sem repetir
				if vistos[id] {
					continue
				}
				vistos[id] = true
				f, achou := porID[id]
				if !achou {
					faltam = append(faltam, id.String())
					continue
				}
				c := camps[f.CampaignID]
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
			if len(faltam) > 0 {
				l.Ressalvas = append(l.Ressalvas, "sem campanha no E-monitor para: "+strings.Join(faltam, ", "))
			}
			if cancelada {
				l.Ressalvas = append(l.Ressalvas, ressalvaCancelada)
			}
			return ok(l)
		})
}

type entradaCampanhaPaginada struct {
	CampanhaID string `json:"campanha_id" jsonschema:"Id (uuid) da campanha, vindo de listar_campanhas ou obter_campanha. Nunca invente: pergunte."`
	Paginacao
}

type entradaOverrides struct {
	CampanhaID string `json:"campanha_id" jsonschema:"Id (uuid) da campanha, vindo de listar_campanhas ou obter_campanha. Nunca invente: pergunte."`
	De         string `json:"de" jsonschema:"Primeiro dia (AAAA-MM-DD)."`
	Ate        string `json:"ate" jsonschema:"Último dia (AAAA-MM-DD), até 366 dias depois de de."`
	Paginacao
}

type faixaHoraria struct {
	Inicio string `json:"inicio"`
	Fim    string `json:"fim"`
}

type materialDaCampanha struct {
	Material  ref   `json:"material"`
	Tipo      *ref  `json:"tipo"`
	Emissoras []ref `json:"emissoras"`
	// EmissorasTotal é o número real; Emissoras traz no máximo tetoEmissorasPorItem.
	EmissorasTotal int    `json:"emissoras_total"`
	VinculadoEm    string `json:"vinculado_em"`
}

type regraDTO struct {
	ID              string       `json:"id"`
	Nome            string       `json:"nome"`
	Tipo            ref          `json:"tipo"`
	Emissoras       []ref        `json:"emissoras"`
	EmissorasTotal  int          `json:"emissoras_total"`
	Materiais       []ref        `json:"materiais"`
	Inicio          string       `json:"inicio"`
	Fim             string       `json:"fim"`
	DiasDaSemana    []string     `json:"dias_da_semana"`
	Faixa           faixaHoraria `json:"faixa"`
	InsercoesPorDia int16        `json:"insercoes_por_dia"`
}

// montarRegraDTO é o item de listar_regras_distribuicao; as tools de escrita de
// regra devolvem a regra neste mesmo formato. emissorasCortadas são as emissoras
// que entram na resposta (cortadas em tetoEmissorasPorItem) e total o número real.
func montarRegraDTO(r catalog.DistributionRule, tipos map[uuid.UUID]string, emissorasCortadas []uuid.UUID,
	total int, emissoras, materiais map[uuid.UUID]string) regraDTO {
	return regraDTO{
		ID: r.ID.String(), Nome: r.Name, Tipo: refDe(r.TypeID, tipos[r.TypeID]),
		Emissoras: refs(emissorasCortadas, emissoras), EmissorasTotal: total, Materiais: refs(r.MaterialIDs, materiais),
		Inicio: dia(r.StartDate), Fim: dia(r.EndDate), DiasDaSemana: diasDaMascara(r.WeekdayMask),
		Faixa: faixaHoraria{Inicio: r.TimeStart, Fim: r.TimeEnd}, InsercoesPorDia: r.PlaysPerDay,
	}
}

// montarAjusteDTO é o item de listar_overrides; as tools de escrita de ajuste
// do dia devolvem o ajuste neste mesmo formato.
func montarAjusteDTO(a catalog.DistributionOverride, tipos, emissoras map[uuid.UUID]string) ajusteDTO {
	return ajusteDTO{
		Data: dia(a.ForDate), Tipo: refDe(a.TypeID, tipos[a.TypeID]),
		Emissora: refDe(a.StationID, emissoras[a.StationID]), InsercoesEsperadas: a.PlaysExpected,
		Faixa: faixaHoraria{Inicio: a.TimeStart, Fim: a.TimeEnd}, Motivo: a.Reason, CriadoEm: instante(a.CreatedAt),
	}
}

type ajusteDTO struct {
	Data               string       `json:"data"`
	Tipo               ref          `json:"tipo"`
	Emissora           ref          `json:"emissora"`
	InsercoesEsperadas int16        `json:"insercoes_esperadas"`
	Faixa              faixaHoraria `json:"faixa"`
	Motivo             *string      `json:"motivo"`
	CriadoEm           string       `json:"criado_em"`
}

type precoPorTipo struct {
	Tipo          ref     `json:"tipo"`
	ValorUnitario float64 `json:"valor_unitario"`
}

type precoDTO struct {
	Emissora         ref            `json:"emissora"`
	Modo             string         `json:"modo"`
	ValorConsolidado *float64       `json:"valor_consolidado"`
	PorTipo          []precoPorTipo `json:"por_tipo"`
}

var nomesDosDias = [7]string{"dom", "seg", "ter", "qua", "qui", "sex", "sab"}

// diasDaMascara: bit 0 = domingo … bit 6 = sábado (migration 0019).
func diasDaMascara(m int16) []string {
	out := []string{}
	for i, n := range nomesDosDias {
		if m&(1<<i) != 0 {
			out = append(out, n)
		}
	}
	return out
}

// modoDoPreco: vocabulário da tela.
func modoDoPreco(m string) string {
	if m == catalog.PricingModePerInsertion {
		return "por_insercao"
	}
	return "consolidado"
}

// tetoEmissorasPorItem: a paginação limita itens, não as emissoras dentro de
// cada um; uma campanha de 239 emissoras com 10 materiais passava de 60k tokens
// numa página só. obter_campanha lista todas as emissoras-alvo.
const tetoEmissorasPorItem = 20

const ressalvaEmissorasCortadas = "Listas de emissoras cortadas em 20 por item (emissoras_total traz o número real); obter_campanha lista todas as emissoras-alvo da campanha."

// cortarIDs devolve até teto ids (na ordem) e o total real.
func cortarIDs(ids []uuid.UUID, teto int) ([]uuid.UUID, int) {
	return ids[:min(len(ids), teto)], len(ids)
}

const ressalvaRegras = "materiais vazio = a regra vale para todos os materiais do tipo; com materiais, é um recorte só para eles. A meta de uma célula (emissora × tipo × dia) é a soma das regras que valem naquele dia; um ajuste do dia (listar_overrides) substitui as regras da célula."

const ressalvaAjustes = "O ajuste do dia substitui as regras daquela célula: insercoes_esperadas vira a meta e a faixa dele é a única válida. insercoes_esperadas = 0 → toda tocada do dia vira bonus, nunca out_slot (exceto out_date: tocada fora da vigência da campanha, ou de material com regra própria fora do período dela)."

const ressalvaPrecos = "Só emissoras com linha aqui entram no investido. Consolidado = valor do contrato da emissora, acumulado por mês iniciado; por inserção = valor unitário × in_slot (e × bonus na bonificação)."

func registrarCampanhasDetalhe(s *sdk.Server, cfg ConfigServidor) {
	ferramentaLeitura(s, cfg, "listar_materiais_campanha",
		"Lista os materiais (áudios) vinculados a uma campanha, com o tipo e as emissoras em que cada um pode tocar. "+
			"Use antes de interpretar a grade (que é por tipo, não por material). Nunca invente o campanha_id. Lista paginada.",
		func(ctx context.Context, L *Leitura, in entradaCampanhaPaginada) (*sdk.CallToolResult, error) {
			c, rec, err := lerCampanha(ctx, L, in.CampanhaID)
			if rec != nil || err != nil {
				return rec, err
			}
			vinculos, err := L.MateriaisCampanha.ListByCampaign(ctx, c.ID)
			if err != nil {
				return nil, err
			}
			mats, err := L.Materiais.ListByClient(ctx, c.ClientID, "")
			if err != nil {
				return nil, err
			}
			porID := make(map[uuid.UUID]catalog.Material, len(mats))
			for _, m := range mats {
				porID[m.ID] = m
			}
			tipos, err := nomesTipos(ctx, L)
			if err != nil {
				return nil, err
			}
			var mantidas []uuid.UUID
			cortes := make([][]uuid.UUID, len(vinculos))
			totais := make([]int, len(vinculos))
			for i, v := range vinculos {
				cortes[i], totais[i] = cortarIDs(v.TargetStations, tetoEmissorasPorItem)
				mantidas = append(mantidas, cortes[i]...)
			}
			emissoras, err := nomesEmissoras(ctx, L, mantidas)
			if err != nil {
				return nil, err
			}
			linhas := make([]materialDaCampanha, len(vinculos))
			for i, v := range vinculos {
				m := porID[v.MaterialID]
				linhas[i] = materialDaCampanha{
					Material: refDe(v.MaterialID, m.Title), Emissoras: refs(cortes[i], emissoras),
					EmissorasTotal: totais[i], VinculadoEm: instante(v.AddedAt),
				}
				if m.TypeID != nil {
					r := refDe(*m.TypeID, tipos[*m.TypeID])
					linhas[i].Tipo = &r
				}
			}
			l := paginar(linhas, in.Paginacao)
			for _, it := range l.Items {
				if it.EmissorasTotal > len(it.Emissoras) {
					l.Ressalvas = []string{ressalvaEmissorasCortadas}
					break
				}
			}
			return ok(l)
		})

	ferramentaLeitura(s, cfg, "listar_regras_distribuicao",
		"Lista as regras de distribuição de uma campanha: tipo de material, emissoras, materiais (vazio = todos do tipo), "+
			"vigência, dias da semana, faixa horária e inserções por dia. É a programação que gera a meta da grade. "+
			"Nunca invente o campanha_id. Lista paginada.",
		func(ctx context.Context, L *Leitura, in entradaCampanhaPaginada) (*sdk.CallToolResult, error) {
			c, rec, err := lerCampanha(ctx, L, in.CampanhaID)
			if rec != nil || err != nil {
				return rec, err
			}
			regras, err := L.Regras.ListByCampaign(ctx, c.ID)
			if err != nil {
				return nil, err
			}
			tipos, err := nomesTipos(ctx, L)
			if err != nil {
				return nil, err
			}
			mats, err := nomesMateriais(ctx, L, c.ClientID)
			if err != nil {
				return nil, err
			}
			var mantidas []uuid.UUID
			cortes := make([][]uuid.UUID, len(regras))
			totais := make([]int, len(regras))
			for i, r := range regras {
				cortes[i], totais[i] = cortarIDs(r.StationIDs, tetoEmissorasPorItem)
				mantidas = append(mantidas, cortes[i]...)
			}
			emissoras, err := nomesEmissoras(ctx, L, mantidas)
			if err != nil {
				return nil, err
			}
			linhas := make([]regraDTO, len(regras))
			for i, r := range regras {
				linhas[i] = montarRegraDTO(r, tipos, cortes[i], totais[i], emissoras, mats)
			}
			l := paginar(linhas, in.Paginacao)
			l.Ressalvas = []string{ressalvaRegras}
			for _, it := range l.Items {
				if it.EmissorasTotal > len(it.Emissoras) {
					l.Ressalvas = append(l.Ressalvas, ressalvaEmissorasCortadas)
					break
				}
			}
			return ok(l)
		})

	ferramentaLeitura(s, cfg, "listar_overrides",
		"Lista os ajustes do dia (overrides) de uma campanha num período: para cada célula (emissora × tipo × dia), a meta e a "+
			"faixa que substituem as regras naquele dia. A armadilha: meta 0 faz a tocada do dia virar bônus (exceto out_date, fora da vigência). "+
			"Pede de/ate (AAAA-MM-DD, até 366 dias). Nunca invente o campanha_id. Lista paginada.",
		func(ctx context.Context, L *Leitura, in entradaOverrides) (*sdk.CallToolResult, error) {
			c, rec, err := lerCampanha(ctx, L, in.CampanhaID)
			if rec != nil || err != nil {
				return rec, err
			}
			de, okDe := lerDia(in.De)
			ate, okAte := lerDia(in.Ate)
			if !okDe {
				return recusaEntrada("de")
			}
			if !okAte {
				return recusaEntrada("ate")
			}
			if ate.Before(de) || ate.Sub(de) > 366*24*time.Hour {
				return recusa("período inválido: ate tem de ser >= de e no máximo 366 dias depois", "entrada_invalida")
			}
			ajustes, err := L.Ajustes.ListByCampaignAndDateRange(ctx, c.ID, de, ate)
			if err != nil {
				return nil, err
			}
			tipos, err := nomesTipos(ctx, L)
			if err != nil {
				return nil, err
			}
			var ests []uuid.UUID
			for _, a := range ajustes {
				ests = append(ests, a.StationID)
			}
			emissoras, err := nomesEmissoras(ctx, L, ests)
			if err != nil {
				return nil, err
			}
			linhas := make([]ajusteDTO, len(ajustes))
			for i, a := range ajustes {
				linhas[i] = montarAjusteDTO(a, tipos, emissoras)
			}
			l := paginar(linhas, in.Paginacao)
			l.Ressalvas = []string{ressalvaAjustes}
			return ok(l)
		})

	ferramentaLeitura(s, cfg, "listar_precos_campanha",
		"Lista os preços de uma campanha por emissora: modo (consolidado ou por_insercao), valor consolidado ou valor unitário por "+
			"tipo de material. Só emissora com preço entra no investido. Valores em reais. Nunca invente o campanha_id. Lista paginada.",
		func(ctx context.Context, L *Leitura, in entradaCampanhaPaginada) (*sdk.CallToolResult, error) {
			c, rec, err := lerCampanha(ctx, L, in.CampanhaID)
			if rec != nil || err != nil {
				return rec, err
			}
			precos, err := L.Precos.ListByCampaign(ctx, c.ID)
			if err != nil {
				return nil, err
			}
			tipos, err := nomesTipos(ctx, L)
			if err != nil {
				return nil, err
			}
			ests := make([]uuid.UUID, len(precos))
			for i, p := range precos {
				ests[i] = p.StationID
			}
			emissoras, err := nomesEmissoras(ctx, L, ests)
			if err != nil {
				return nil, err
			}
			linhas := make([]precoDTO, len(precos))
			for i, p := range precos {
				pt := make([]precoPorTipo, len(p.PerType))
				for j, tp := range p.PerType {
					pt[j] = precoPorTipo{Tipo: refDe(tp.TypeID, tipos[tp.TypeID]), ValorUnitario: tp.UnitValue}
				}
				linhas[i] = precoDTO{
					Emissora: refDe(p.StationID, emissoras[p.StationID]), Modo: modoDoPreco(p.Mode),
					ValorConsolidado: p.ConsolidatedValue, PorTipo: pt,
				}
			}
			l := paginar(linhas, in.Paginacao)
			l.Ressalvas = []string{ressalvaPrecos}
			return ok(l)
		})
}
