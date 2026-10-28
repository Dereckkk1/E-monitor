package mcp

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"time"

	"github.com/google/uuid"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"radiocheck/internal/assertiveness"
	"radiocheck/internal/catalog"
	"radiocheck/internal/supervisor"
)

type entradaGerencial struct {
	ClienteID   string   `json:"cliente_id,omitempty" jsonschema:"Só este cliente (de listar_clientes). Vazio = a operação inteira."`
	CampanhaIDs []string `json:"campanha_ids,omitempty" jsonschema:"Só estas campanhas (até 200). Vazio = todas no escopo."`
	Estado      string   `json:"estado,omitempty" jsonschema:"programada, ativa, concluida ou cancelada. Vazio = todos menos cancelada."`
	De          string   `json:"de,omitempty" jsonschema:"Primeiro dia (AAAA-MM-DD). Padrão: 1º de janeiro do ano corrente (America/Sao_Paulo)."`
	Ate         string   `json:"ate,omitempty" jsonschema:"Último dia (AAAA-MM-DD). Padrão: hoje."`
}

type gerencialDTO struct {
	Periodo struct {
		De  string `json:"de"`
		Ate string `json:"ate"`
	} `json:"periodo"`
	KPIs struct {
		EmissorasMonitoradas int   `json:"emissoras_monitoradas"`
		EmissorasAoVivo      *int  `json:"emissoras_ao_vivo"`
		Estados              int   `json:"estados"`
		MateriaisMonitorados int   `json:"materiais_monitorados"`
		Campanhas            int   `json:"campanhas"`
		VeiculacoesNoPeriodo int64 `json:"veiculacoes_no_periodo"`
		VeiculacoesHoje      int64 `json:"veiculacoes_hoje"`
	} `json:"kpis"`
	EmissorasNoMapa int      `json:"emissoras_no_mapa"`
	Links           links    `json:"links"`
	Ressalvas       []string `json:"ressalvas"`
}

const ressalvaGerencial = "Os filtros escolhem QUAIS campanhas entram; o período limita só veiculacoes_no_periodo e a sobreposição das campanhas. Sem estado, canceladas ficam fora. veiculacoes_hoje é o dia corrente, independe do período. emissoras_ao_vivo = monitoradas com worker ativo agora (null = não medido neste ambiente). A lista de emissoras do mapa e o feed ao vivo não vêm aqui: use mapa_cobertura_campanha e listar_veiculacoes."

type entradaAssertividade struct {
	ClienteID   string   `json:"cliente_id,omitempty" jsonschema:"Só este cliente (de listar_clientes). Vazio = a plataforma inteira."`
	CampanhaIDs []string `json:"campanha_ids,omitempty" jsonschema:"Só estas campanhas (até 200)."`
}

type mesAssertividade struct {
	Mes                string   `json:"mes"`
	Percentual         *float64 `json:"percentual"`
	DetectadasSozinhas int64    `json:"detectadas_sozinhas"`
	NaoDetectadas      int64    `json:"nao_detectadas"`
	ManuaisTotal       int64    `json:"manuais_total"`
	ManuaisDesculpadas struct {
		Redigitadas      int64 `json:"redigitadas"`
		SemFingerprint   int64 `json:"sem_fingerprint"`
		StreamForaDoAr   int64 `json:"stream_fora_do_ar"`
		SemMonitoramento int64 `json:"sem_monitoramento"`
	} `json:"manuais_desculpadas"`
}

type assertividadeDTO struct {
	MesFechado  mesAssertividade  `json:"mes_fechado"`
	MesAnterior *mesAssertividade `json:"mes_anterior"`
	VariacaoPP  *float64          `json:"variacao_pp"`
	Links       links             `json:"links"`
	Ressalvas   []string          `json:"ressalvas"`
}

const ressalvaAssertividade = "Repita sempre o volume junto do percentual (ex.: \"97,5% — 5 não detectadas de 200\"): a métrica só enxerga miss REPORTADO (veiculação lançada à mão que o matcher deveria ter pego), então percentual alto com pouco volume não prova nada. A janela é sempre o último mês fechado (America/Sao_Paulo) e não aceita período: o mês em curso mente para cima, porque as manuais chegam depois. percentual null = sem veiculação no mês (não é 0% nem 100%); variacao_pp null = mês anterior sem base de comparação."

// lerEscopo: cliente e campanhas opcionais (os filtros de escopo do
// /management e do card de assertividade).
func lerEscopo(clienteID string, campanhaIDs []string) (*uuid.UUID, []uuid.UUID, *sdk.CallToolResult) {
	var cliente *uuid.UUID
	if clienteID != "" {
		id, err := uuid.Parse(clienteID)
		if err != nil {
			r, _ := recusaEntrada("cliente_id")
			return nil, nil, r
		}
		cliente = &id
	}
	camps, okC := lerIDs(campanhaIDs, 200)
	if !okC {
		r, _ := recusa("campanha_ids: até 200 ids válidos", "entrada_invalida")
		return nil, nil, r
	}
	return cliente, camps, nil
}

func queryEscopo(cliente *uuid.UUID, camps []uuid.UUID) url.Values {
	q := url.Values{}
	if cliente != nil {
		q.Set("client_id", cliente.String())
	}
	if len(camps) > 0 {
		q.Set("campaigns", csvIDs(camps))
	}
	return q
}

func registrarGerencial(s *sdk.Server, cfg ConfigServidor) {
	ferramentaLeitura(s, cfg, "visao_gerencial",
		"Devolve os KPIs da Visão Gerencial (/management) da operação inteira ou de um recorte: emissoras monitoradas e "+
			"ao vivo agora, estados, materiais, campanhas, veiculações no período e hoje, e o link da tela. Todos os filtros "+
			"são opcionais; período padrão = 1º de janeiro até hoje. A armadilha: o período só recorta as veiculações, não as campanhas.",
		func(ctx context.Context, L *Leitura, in entradaGerencial) (*sdk.CallToolResult, error) {
			cliente, camps, rec := lerEscopo(in.ClienteID, in.CampanhaIDs)
			if rec != nil {
				return rec, nil
			}
			if in.Estado != "" && !slices.Contains(estadosCampanha, in.Estado) {
				return recusa(fmt.Sprintf("estado desconhecido %q: use programada, ativa, concluida ou cancelada", in.Estado), "entrada_invalida")
			}
			hoje := hojeSP(L.agora())
			de, ate, campo := lerDias(in.De, in.Ate, time.Date(hoje.Year(), 1, 1, 0, 0, 0, 0, time.UTC), hoje)
			if campo != "" {
				return recusaCampoDoPeriodo(campo)
			}
			res, err := L.Gerencial.Get(ctx, catalog.ManagementParams{ClientID: cliente, CampaignIDs: camps, Status: in.Estado, From: de, To: ate})
			if err != nil {
				return nil, err
			}
			var o gerencialDTO
			o.Periodo.De, o.Periodo.Ate = dia(de), dia(ate)
			k := res.KPIs
			o.KPIs.EmissorasMonitoradas = k.StationsMonitored
			o.KPIs.Estados = k.StatesCount
			o.KPIs.MateriaisMonitorados = k.MaterialsMonitored
			o.KPIs.Campanhas = k.CampaignsCount
			o.KPIs.VeiculacoesNoPeriodo = k.AiringsTotal
			o.KPIs.VeiculacoesHoje = k.AiringsToday
			if L.Workers != nil {
				n := supervisor.ContarAtivas(L.Workers.WorkerStatuses(), res.MonitoredStationIDs)
				o.KPIs.EmissorasAoVivo = &n
			}
			o.EmissorasNoMapa = len(res.Stations)
			q := queryEscopo(cliente, camps)
			if in.Estado != "" {
				q.Set("status", in.Estado)
			}
			q.Set("from", dia(de))
			q.Set("to", dia(ate))
			o.Links = links{Tela: cfg.linkTela("/management", q)}
			o.Ressalvas = []string{ressalvaGerencial}
			if in.Estado == "cancelada" {
				o.Ressalvas = append(o.Ressalvas, ressalvaCancelada)
			}
			return ok(o)
		})

	ferramentaLeitura(s, cfg, "assertividade",
		"Devolve a assertividade da detecção no último mês fechado (e no anterior): percentual de veiculações que o "+
			"matcher pegou sozinho, quantas tiveram de ser lançadas à mão por erro nosso, as manuais desculpadas por motivo, "+
			"a variação em pontos percentuais e o link da Visão Gerencial. Filtros opcionais: cliente_id, campanha_ids. "+
			"A armadilha: não aceita período e o percentual nunca se apresenta sem o volume.",
		func(ctx context.Context, L *Leitura, in entradaAssertividade) (*sdk.CallToolResult, error) {
			cliente, camps, rec := lerEscopo(in.ClienteID, in.CampanhaIDs)
			if rec != nil {
				return rec, nil
			}
			res, err := L.Assertividade.Get(ctx, L.agora(), assertiveness.Filter{ClientID: cliente, CampaignIDs: camps})
			if err != nil {
				return nil, err
			}
			o := assertividadeDTO{MesFechado: mesAssertividadeDe(res.Current), VariacaoPP: res.DeltaPP}
			if res.Previous != nil {
				m := mesAssertividadeDe(*res.Previous)
				o.MesAnterior = &m
			}
			o.Links = links{Tela: cfg.linkTela("/management", queryEscopo(cliente, camps))}
			// ressalvaAssertividade fica SEMPRE em primeiro (o volume junto do percentual).
			o.Ressalvas = []string{ressalvaAssertividade}
			return ok(o)
		})
}

func mesAssertividadeDe(s assertiveness.Summary) mesAssertividade {
	m := mesAssertividade{Mes: s.Month, Percentual: s.Pct, DetectadasSozinhas: s.Auto, NaoDetectadas: s.Miss, ManuaisTotal: s.ManualTotal}
	m.ManuaisDesculpadas.Redigitadas = s.Duplicate
	m.ManuaisDesculpadas.SemFingerprint = s.NoFingerprint
	m.ManuaisDesculpadas.StreamForaDoAr = s.StreamDown
	m.ManuaisDesculpadas.SemMonitoramento = s.Unmonitored
	return m
}
