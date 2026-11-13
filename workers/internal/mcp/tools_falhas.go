package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"radiocheck/internal/catalog"
)

const (
	// tetoDiasFalha / tetoFaixaFalhas: os limites dos handlers de falha
	// (admin_station_failures.go, admin_failures_daily.go).
	tetoDiasFalha   = 90
	tetoFaixaFalhas = 92
	minQuedaPadrao  = 60
	// tetoPaginaFalhasEmissoras: cada emissora leva até tetoPorItem quedas e
	// tetoPorItem campanhas (cada uma com o cliente como ref); com o teto global
	// (100) uma página passava de 600 KB, e com 8 chegou a 61 KB. Medido em
	// TestFalhasEmissoras_PiorCasoCabe.
	tetoPaginaFalhasEmissoras = 7
)

var ressalvaFalhaSemSplit = fmt.Sprintf("Esta visão mostra o déficit TOTAL do dia (esperado − in_slot). A separação \"não tocou\" × \"fora do horário\" só existe em falhas_campanhas e obter_falhas_campanha: confira lá antes de cobrar uma emissora por ausência. "+
	"Só entra emissora com déficit em campanha não cancelada; queda sem déficit fica de fora. Tempos em segundos; uma queda ainda aberta cresce até ela fechar. "+
	"quedas e campanhas de cada emissora: as %d primeiras (quedas_total e campanhas_total trazem o número real).", tetoPorItem)

// ressalvaParesEmissora: o resumo do modo dia (e do aviso diário) soma o
// número de emissoras de cada campanha (campaign_failures.go, totalStations).
const ressalvaParesEmissora = "emissoras (no resumo) soma pares campanha × emissora, não emissoras distintas: a mesma emissora falhando em duas campanhas conta duas vezes. Para emissoras distintas no dia, use falhas_emissoras."

const ressalvaFalhasPorDia = "Série com todos os dias da faixa (zerados quando não houve falha). campanhas soma pares emissora × campanha, não campanhas distintas. Compare médias por dia, nunca somas de blocos com nº de dias diferente. segundos_fora_do_ar em segundos (a tela plota minutos). A série tem um degrau em 2026-08-17 (out_slot deixou de fechar a obrigação)."

type entradaFalhasEmissoras struct {
	Data        string `json:"data,omitempty" jsonschema:"Dia (AAAA-MM-DD), até 90 dias atrás; nunca futuro. Padrão: ontem (America/Sao_Paulo)."`
	MinQuedaSeg *int   `json:"min_queda_seg,omitempty" jsonschema:"Ignora quedas que somam menos que isto no dia: 0, 60, 300 ou 1800 s (padrão 60)."`
	Paginacao
}

type quedaDTO struct {
	Inicio     string `json:"inicio"`
	DuracaoSeg int    `json:"duracao_seg"`
}

type campanhaComFalha struct {
	Campanha ref      `json:"campanha"`
	Cliente  ref      `json:"cliente"`
	Esperado int      `json:"esperado"`
	Entregue int      `json:"entregue"`
	Deficit  int      `json:"deficit"`
	Motivos  []string `json:"motivos"`
}

type emissoraComFalha struct {
	Emissora            ref                `json:"emissora"`
	Dial                *string            `json:"dial"`
	Cidade              *string            `json:"cidade"`
	SegundosForaDoAr    int                `json:"segundos_fora_do_ar"`
	TemLacunaSilenciosa bool               `json:"tem_lacuna_silenciosa"`
	Quedas              []quedaDTO         `json:"quedas"`
	QuedasTotal         int                `json:"quedas_total"`
	Campanhas           []campanhaComFalha `json:"campanhas"`
	CampanhasTotal      int                `json:"campanhas_total"`
}

type resumoFalhasEmissoras struct {
	EmissorasComFalha int `json:"emissoras_com_falha"`
	SegundosForaDoAr  int `json:"segundos_fora_do_ar"`
	CampanhasAfetadas int `json:"campanhas_afetadas"`
}

type falhasEmissorasDTO struct {
	Data   string                `json:"data"`
	Resumo resumoFalhasEmissoras `json:"resumo"`
	lista[emissoraComFalha]
	Links links `json:"links"`
}

type entradaFalhasPorDia struct {
	De          string `json:"de,omitempty" jsonschema:"Primeiro dia (AAAA-MM-DD), até 90 dias atrás. Padrão: hoje − 29."`
	Ate         string `json:"ate,omitempty" jsonschema:"Último dia (AAAA-MM-DD). Padrão: hoje; futuro vira hoje. Faixa de até 92 dias."`
	MinQuedaSeg *int   `json:"min_queda_seg,omitempty" jsonschema:"0, 60, 300 ou 1800 s (padrão 60)."`
}

type diaDeFalha struct {
	Data             string `json:"data"`
	Emissoras        int    `json:"emissoras"`
	Campanhas        int    `json:"campanhas"`
	Deficit          int    `json:"deficit"`
	SegundosForaDoAr int    `json:"segundos_fora_do_ar"`
	Quedas           int    `json:"quedas"`
}

type resumoFalhasPorDia struct {
	Dias                 int     `json:"dias"`
	DiasComFalha         int     `json:"dias_com_falha"`
	PicoEmissoras        int     `json:"pico_emissoras"`
	DiaDoPico            *string `json:"dia_do_pico"`
	DeficitTotal         int     `json:"deficit_total"`
	SegundosForaDoAr     int     `json:"segundos_fora_do_ar"`
	MediaEmissorasPorDia float64 `json:"media_emissoras_por_dia"`
}

type falhasPorDiaDTO struct {
	De     string             `json:"de"`
	Ate    string             `json:"ate"`
	Resumo resumoFalhasPorDia `json:"resumo"`
	lista[diaDeFalha]
	Links links `json:"links"`
}

type campanhaDigest struct {
	Campanha          ref `json:"campanha"`
	Cliente           ref `json:"cliente"`
	EmissorasComFalha int `json:"emissoras_com_falha"`
}

// resumoDiarioDTO: o aviso diário do admin, sem o "seen" (estado de UI por
// pessoa, F2-10). As contagens não têm omitempty: 0 é zero, não ausência.
type resumoDiarioDTO struct {
	Data              string `json:"data"`
	CampanhasComFalha int    `json:"campanhas_com_falha"`
	EmissorasComFalha int    `json:"emissoras_com_falha"`
	lista[campanhaDigest]
	Links links `json:"links"`
}

// textoOpc: o catalog de falhas devolve "" para dial/cidade ausentes; no
// contrato isso é null (não se aplica), não texto vazio.
func textoOpc(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// hojeLocal: o "hoje" dos handlers de falha (time.Local do processo).
func hojeLocal(agora time.Time) time.Time {
	t := agora.In(time.Local)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local)
}

// emLocal: a mesma data de calendário à meia-noite de time.Local — a leitura
// que os handlers de falha fazem (time.ParseInLocation(…, time.Local)).
func emLocal(d time.Time) time.Time {
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.Local)
}

// lerDiaFalha lê o dia como o handler (time.Local) com padrão = ontem em SP
// (F2-5); recusa futuro e mais de 90 dias, como o handler.
func lerDiaFalha(s string, agora time.Time) (time.Time, *sdk.CallToolResult) {
	if s == "" {
		s = dia(ontemSP(agora))
	}
	d, ok := lerDia(s)
	if !ok {
		r, _ := recusaEntrada("data")
		return d, r
	}
	d = emLocal(d)
	hoje := hojeLocal(agora)
	if d.After(hoje) {
		r, _ := recusa("data no futuro: as falhas só existem de hoje para trás", "entrada_invalida")
		return d, r
	}
	if hoje.Sub(d) > tetoDiasFalha*24*time.Hour {
		r, _ := recusa("data com mais de 90 dias: o histórico de falhas vai até 90 dias atrás", "entrada_invalida")
		return d, r
	}
	return d, nil
}

// minQuedasDaTela: os limiares que o seletor de /admin/station-failures oferece
// (MIN_DOWN_OPTIONS). A API aceita 0..3600, mas um valor fora desta lista
// abriria o link na tela com outro limiar — e o número não bateria.
var minQuedasDaTela = []int{0, 60, 300, 1800}

func lerMinQueda(v *int) (int, *sdk.CallToolResult) {
	if v == nil {
		return minQuedaPadrao, nil
	}
	if !slices.Contains(minQuedasDaTela, *v) {
		r, _ := recusa("min_queda_seg tem de ser 0, 60, 300 ou 1800 (os limiares da tela)", "entrada_invalida")
		return 0, r
	}
	return *v, nil
}

func linkFalhas(cfg ConfigServidor, params map[string]string) *string {
	q := url.Values{}
	for k, v := range params {
		q.Set(k, v)
	}
	return cfg.linkTela("/admin/station-failures", q)
}

func emissoraComFalhaDe(sf catalog.StationFailure) emissoraComFalha {
	qs, nq := cortar(sf.Incidents, tetoPorItem)
	cs, nc := cortar(sf.Campaigns, tetoPorItem)
	e := emissoraComFalha{Emissora: refDe(sf.Station.ID, sf.Station.Name), Dial: textoOpc(sf.Station.Dial),
		Cidade: textoOpc(sf.Station.City), SegundosForaDoAr: sf.TotalDownSeconds, TemLacunaSilenciosa: sf.HasSilentGap,
		QuedasTotal: nq, CampanhasTotal: nc, Quedas: make([]quedaDTO, len(qs)), Campanhas: make([]campanhaComFalha, len(cs))}
	for i, q := range qs {
		e.Quedas[i] = quedaDTO{Inicio: instante(q.EventAt), DuracaoSeg: q.DurationSeconds}
	}
	for i, c := range cs {
		motivos := c.AffectedBy
		if motivos == nil {
			motivos = []string{}
		}
		e.Campanhas[i] = campanhaComFalha{Campanha: refDe(c.CampaignID, c.CampaignName), Cliente: refDe(c.ClientID, c.ClientName),
			Esperado: c.Expected, Entregue: c.Delivered, Deficit: c.Deficit, Motivos: motivos}
	}
	return e
}

func registrarFalhas(s *sdk.Server, cfg ConfigServidor) {
	ferramentaLeitura(s, cfg, "falhas_emissoras",
		"Lista as emissoras que falharam num dia (/admin/station-failures, aba Por emissora): tempo fora do ar, quedas e, "+
			"por campanha afetada, esperado, entregue, déficit e o motivo (stream-down ou silent-gap), mais o link da tela. "+
			"Padrão: ontem. A armadilha: o déficit aqui é o total — o split \"não tocou\" × \"fora do horário\" está em "+
			"falhas_campanhas. Lista paginada (até 7 emissoras por página).",
		func(ctx context.Context, L *Leitura, in entradaFalhasEmissoras) (*sdk.CallToolResult, error) {
			d, rec := lerDiaFalha(in.Data, L.agora())
			if rec != nil {
				return rec, nil
			}
			minQ, rec := lerMinQueda(in.MinQuedaSeg)
			if rec != nil {
				return rec, nil
			}
			res, err := L.FalhasEmissoras.ListForDate(ctx, d, minQ)
			if err != nil {
				return nil, err
			}
			o := falhasEmissorasDTO{Data: res.Date, Resumo: resumoFalhasEmissoras{
				EmissorasComFalha: res.Summary.StationsWithFailure,
				SegundosForaDoAr:  res.Summary.TotalDownSeconds,
				CampanhasAfetadas: res.Summary.AffectedCampaigns,
			}}
			o.lista = mapear(paginar(res.Stations, in.Paginacao.comTeto(tetoPaginaFalhasEmissoras)), emissoraComFalhaDe)
			o.Ressalvas = []string{ressalvaFalhaSemSplit}
			o.Links = links{Tela: linkFalhas(cfg, map[string]string{"view": "by_station", "date": res.Date, "min_down": strconv.Itoa(minQ)})}
			return ok(o)
		})

	ferramentaLeitura(s, cfg, "falhas_por_dia",
		"Devolve a série diária de falhas (/admin/station-failures, aba Por dia): por dia, emissoras com falha, pares "+
			"emissora × campanha, déficit, tempo fora do ar e quedas; no resumo, dias com falha, o pico e a média de "+
			"emissoras por dia. Padrão: últimos 30 dias até hoje (America/Sao_Paulo); faixa de até 92 dias, no máximo 90 dias "+
			"para trás.",
		func(ctx context.Context, L *Leitura, in entradaFalhasPorDia) (*sdk.CallToolResult, error) {
			minQ, rec := lerMinQueda(in.MinQuedaSeg)
			if rec != nil {
				return rec, nil
			}
			// Padrão em America/Sao_Paulo (F2-5); os limites (futuro, 90 dias, 92
			// dias de faixa) seguem o "hoje" do handler (time.Local do processo).
			hoje := hojeLocal(L.agora())
			hojeCal := hojeSP(L.agora())
			de, ate, rec := lerDias(in.De, in.Ate, hojeCal.AddDate(0, 0, -29), hojeCal)
			if rec != nil {
				return rec, nil
			}
			// lerDias devolve meia-noite UTC; os limites do handler são em time.Local.
			de, ate = emLocal(de), emLocal(ate)
			if ate.After(hoje) {
				ate = hoje // o handler corta o futuro em hoje
			}
			if de.After(ate) {
				return recusa("período inválido: de está no futuro (ate é cortado em hoje)", "entrada_invalida")
			}
			if hoje.Sub(de) > tetoDiasFalha*24*time.Hour {
				return recusa("de com mais de 90 dias: o histórico de falhas vai até 90 dias atrás", "entrada_invalida")
			}
			if ate.Sub(de) > (tetoFaixaFalhas-1)*24*time.Hour {
				return recusa("faixa de até 92 dias", "entrada_invalida")
			}
			res, err := L.FalhasPorDia.ListDaily(ctx, de, ate, minQ)
			if err != nil {
				return nil, err
			}
			sm := res.Summary
			o := falhasPorDiaDTO{De: res.From, Ate: res.To, Resumo: resumoFalhasPorDia{
				Dias: sm.DaysTotal, DiasComFalha: sm.DaysWithFailure, PicoEmissoras: sm.PeakStations,
				DiaDoPico: textoOpc(sm.PeakDate), DeficitTotal: sm.TotalDeficit, SegundosForaDoAr: sm.TotalDownSeconds,
				MediaEmissorasPorDia: sm.AvgStations,
			}}
			dias := make([]diaDeFalha, len(res.Days))
			for i, p := range res.Days {
				dias[i] = diaDeFalha{Data: p.Date, Emissoras: p.Stations, Campanhas: p.Campaigns, Deficit: p.Deficit,
					SegundosForaDoAr: p.DownSeconds, Quedas: p.DownEvents}
			}
			o.lista = listaInteira(dias)
			o.Ressalvas = []string{ressalvaFalhasPorDia}
			o.Links = links{Tela: linkFalhas(cfg, map[string]string{"view": "by_day", "from": res.From, "to": res.To, "min_down": strconv.Itoa(minQ)})}
			return ok(o)
		})

	ferramentaLeitura(s, cfg, "resumo_diario_falhas",
		"Devolve o resumo diário de falhas de ontem em America/Sao_Paulo (o mesmo cálculo do aviso que o admin vê ao "+
			"abrir o portal; o aviso usa o fuso do servidor e, entre 21h e 24h de SP, pode estar mostrando o dia corrente): "+
			"quantas campanhas e emissoras falharam e, por campanha, quantas emissoras. Não marca nada como visto. Para o detalhe use "+
			"falhas_campanhas (modo dia) ou falhas_emissoras. Lista paginada de campanhas.",
		func(ctx context.Context, L *Leitura, in struct{ Paginacao }) (*sdk.CallToolResult, error) {
			d, rec := lerDiaFalha("", L.agora())
			if rec != nil {
				return rec, nil
			}
			res, err := L.FalhasCampanhas.ListForDate(ctx, d)
			if err != nil {
				return nil, err
			}
			// O mesmo mapeamento do digestFromDaily (admin_daily_failures_digest.go),
			// na ordem da tela (mais emissoras com falha primeiro).
			o := resumoDiarioDTO{Data: res.Date, CampanhasComFalha: res.Summary.Campaigns, EmissorasComFalha: res.Summary.Stations,
				lista: mapear(paginar(res.Campaigns, in.Paginacao), func(c catalog.CampaignDailyFailure) campanhaDigest {
					return campanhaDigest{Campanha: refDe(c.Campaign.ID, c.Campaign.Name), Cliente: refDe(c.Campaign.ClientID, c.Campaign.ClientName),
						EmissorasComFalha: len(c.Stations)}
				}),
				Links: links{Tela: linkFalhas(cfg, map[string]string{"view": "by_campaign", "date": res.Date})}}
			o.Ressalvas = []string{strings.Replace(ressalvaParesEmissora, "emissoras (no resumo)", "emissoras_com_falha", 1)}
			return ok(o)
		})
}

// tetoPaginaFalhasDia: cada campanha do modo dia leva até tetoPorItem
// emissoras. tetoPaginaFalhasCampanha: cada emissora do detalhe leva até
// tetoPorItem dias de falha. Medidos em TestFalhasCampanhas_PiorCasoCabe e
// TestObterFalhasCampanha_PiorCasoCabe.
const (
	tetoPaginaFalhasDia      = 8
	tetoPaginaFalhasCampanha = 50
)

const ressalvaSplit = "deficit = esperado − in_slot (out_slot não fecha a obrigação). deficit_nao_tocou = a emissora não tocou; " +
	"deficit_fora_do_horario = tocou, mas fora da faixa — só o primeiro é ausência cobrável. extras = out_slot + out_date + bonus; " +
	"bonificada = os extras cobrem o déficit (provisório enquanto a campanha está ativa). Horizonte: dias já encerrados (antes de " +
	"hoje em America/Sao_Paulo). Campanha cancelada não entra (sai da cobrança)."

type entradaFalhasCampanhas struct {
	Modo string `json:"modo,omitempty" jsonschema:"dia (padrão): as campanhas com emissora falhando no dia; historico: todas as campanhas com falha na vigência."`
	Data string `json:"data,omitempty" jsonschema:"Só no modo dia: AAAA-MM-DD, até 90 dias atrás. Padrão: ontem (America/Sao_Paulo)."`
	Paginacao
}

type emissoraDeficit struct {
	Emissora             ref      `json:"emissora"`
	Dial                 *string  `json:"dial"`
	Cidade               *string  `json:"cidade"`
	Programado           int      `json:"programado"`
	Identificado         int      `json:"identificado"`
	Deficit              int      `json:"deficit"`
	DeficitNaoTocou      int      `json:"deficit_nao_tocou"`
	DeficitForaDoHorario int      `json:"deficit_fora_do_horario"`
	Extras               int      `json:"extras"`
	Bonificada           bool     `json:"bonificada"`
	DiasDeFalha          []string `json:"dias_de_falha,omitempty"`
	DiasDeFalhaTotal     *int     `json:"dias_de_falha_total,omitempty"`
}

func emissoraDeficitDe(s catalog.CampaignFailureStation) emissoraDeficit {
	e := emissoraDeficit{Emissora: refDe(s.Station.ID, s.Station.Name), Dial: textoOpc(s.Station.Dial), Cidade: textoOpc(s.Station.City),
		Programado: s.Programmed, Identificado: s.Identified, Deficit: s.Deficit, DeficitNaoTocou: s.DeficitAbsent,
		DeficitForaDoHorario: s.DeficitOffSlot, Extras: s.Extras, Bonificada: s.IsBonified}
	if s.FailureDays != nil { // só o detalhe traz os dias
		ds, n := cortar(s.FailureDays, tetoPorItem)
		e.DiasDeFalha, e.DiasDeFalhaTotal = ds, &n
	}
	return e
}

type campanhaFalhaDia struct {
	Campanha       ref               `json:"campanha"`
	Cliente        ref               `json:"cliente"`
	Estado         string            `json:"estado"`
	Emissoras      []emissoraDeficit `json:"emissoras"`
	EmissorasTotal int               `json:"emissoras_total"`
}

type campanhaFalhaHistorico struct {
	Campanha             ref    `json:"campanha"`
	Cliente              ref    `json:"cliente"`
	Estado               string `json:"estado"`
	EmissorasComFalha    int    `json:"emissoras_com_falha"`
	DiasDeFalha          int    `json:"dias_de_falha"`
	Deficit              int    `json:"deficit"`
	DeficitNaoTocou      int    `json:"deficit_nao_tocou"`
	DeficitForaDoHorario int    `json:"deficit_fora_do_horario"`
	TotalmenteBonificada bool   `json:"totalmente_bonificada"`
}

// deficitSplit: o déficit e a sua divisão, comum ao modo dia e ao detalhe.
// Embutido, sai plano no JSON.
type deficitSplit struct {
	Deficit              int `json:"deficit"`
	DeficitNaoTocou      int `json:"deficit_nao_tocou"`
	DeficitForaDoHorario int `json:"deficit_fora_do_horario"`
}

// Sem omitempty nas contagens: 0 campanhas com falha é zero, não ausência.
type resumoFalhasDia struct {
	Campanhas int `json:"campanhas"`
	Emissoras int `json:"emissoras"`
	deficitSplit
}

type resumoFalhasDetalhe struct {
	Emissoras   int `json:"emissoras"`
	DiasDeFalha int `json:"dias_de_falha"`
	deficitSplit
}

type falhasCampanhasDiaDTO struct {
	Data   string          `json:"data"`
	Resumo resumoFalhasDia `json:"resumo"`
	lista[campanhaFalhaDia]
	Links links `json:"links"`
}

type falhasCampanhasHistoricoDTO struct {
	lista[campanhaFalhaHistorico]
	Links links `json:"links"`
}

type falhasCampanhaDTO struct {
	Campanha campanhaResumida    `json:"campanha"`
	Cliente  ref                 `json:"cliente"`
	Resumo   resumoFalhasDetalhe `json:"resumo"`
	lista[emissoraDeficit]
	Links links `json:"links"`
}

func registrarFalhasCampanhas(s *sdk.Server, cfg ConfigServidor) {
	ferramentaLeitura(s, cfg, "falhas_campanhas",
		"Lista as campanhas com falha (/admin/station-failures, aba Por campanha). Modo dia (padrão: ontem): as campanhas "+
			"com emissora falhando no dia, cada uma com as emissoras e o déficit partido em \"não tocou\" × \"fora do horário\" "+
			"(números da vigência até ontem; até 8 campanhas por página). Modo historico: uma linha por campanha com falha. "+
			"Para o detalhe de uma, obter_falhas_campanha. Lista paginada.",
		func(ctx context.Context, L *Leitura, in entradaFalhasCampanhas) (*sdk.CallToolResult, error) {
			switch in.Modo {
			case "", "dia":
			case "historico":
				if in.Data != "" {
					return recusa("data só vale no modo dia", "entrada_invalida")
				}
				pg, err := buscarJanela(in.Paginacao, func(page, size int) ([]catalog.CampaignHistoricalRow, int, error) {
					res, err := L.FalhasCampanhas.ListHistorical(ctx, page, size)
					if err != nil {
						return nil, 0, err
					}
					return res.Campaigns, res.Total, nil
				})
				if err != nil {
					return nil, err
				}
				o := falhasCampanhasHistoricoDTO{Links: links{Tela: linkFalhas(cfg, map[string]string{"view": "by_campaign", "sub": "historical"})}}
				o.lista = mapear(pg, func(r catalog.CampaignHistoricalRow) campanhaFalhaHistorico {
					return campanhaFalhaHistorico{Campanha: refDe(r.Campaign.ID, r.Campaign.Name), Cliente: refDe(r.Campaign.ClientID, r.Campaign.ClientName),
						Estado: r.Campaign.Status, EmissorasComFalha: r.StationsWithFailure, DiasDeFalha: r.TotalFailureDays,
						Deficit: r.TotalDeficit, DeficitNaoTocou: r.TotalDeficitAbsent, DeficitForaDoHorario: r.TotalDeficitOffSlot,
						TotalmenteBonificada: r.IsFullyBonified}
				})
				o.Ressalvas = []string{ressalvaSplit}
				return ok(o)
			default:
				return recusa(fmt.Sprintf("modo desconhecido %q: use dia ou historico", in.Modo), "entrada_invalida")
			}
			d, rec := lerDiaFalha(in.Data, L.agora())
			if rec != nil {
				return rec, nil
			}
			res, err := L.FalhasCampanhas.ListForDate(ctx, d)
			if err != nil {
				return nil, err
			}
			sm := res.Summary
			o := falhasCampanhasDiaDTO{Data: res.Date, Resumo: resumoFalhasDia{Campanhas: sm.Campaigns, Emissoras: sm.Stations,
				deficitSplit: deficitSplit{Deficit: sm.TotalDeficit, DeficitNaoTocou: sm.TotalDeficitAbsent, DeficitForaDoHorario: sm.TotalDeficitOffSlot}},
				Links: links{Tela: linkFalhas(cfg, map[string]string{"view": "by_campaign", "date": res.Date})}}
			o.lista = mapear(paginar(res.Campaigns, in.Paginacao.comTeto(tetoPaginaFalhasDia)), func(c catalog.CampaignDailyFailure) campanhaFalhaDia {
				es, n := cortar(c.Stations, tetoPorItem)
				x := campanhaFalhaDia{Campanha: refDe(c.Campaign.ID, c.Campaign.Name), Cliente: refDe(c.Campaign.ClientID, c.Campaign.ClientName),
					Estado: c.Campaign.Status, EmissorasTotal: n, Emissoras: make([]emissoraDeficit, len(es))}
				for i, e := range es {
					x.Emissoras[i] = emissoraDeficitDe(e)
				}
				return x
			})
			o.Ressalvas = []string{ressalvaSplit, ressalvaParesEmissora, fmt.Sprintf("Modo dia: entram só as emissoras que falharam no dia, mas os números delas "+
				"somam a vigência inteira até ontem. emissoras de cada campanha: as %d primeiras (emissoras_total traz o real; "+
				"obter_falhas_campanha lista todas).", tetoPorItem)}
			return ok(o)
		})

	ferramentaLeitura(s, cfg, "obter_falhas_campanha",
		"Devolve as falhas de UMA campanha na vigência até ontem (o drill-in de /admin/station-failures): por emissora, "+
			"programado, identificado, déficit partido em \"não tocou\" × \"fora do horário\", extras, se está bonificada e "+
			"os dias de falha; e o link que abre o detalhe na tela (de onde sai o PDF de cobrança). Lista paginada de "+
			"emissoras (até 50 por página).",
		func(ctx context.Context, L *Leitura, in entradaCampanhaPaginada) (*sdk.CallToolResult, error) {
			id, err := uuid.Parse(in.CampanhaID)
			if err != nil {
				return recusaEntrada("campanha_id")
			}
			res, err := L.FalhasCampanhas.Get(ctx, id)
			if errors.Is(err, catalog.ErrCampaignNotFound) {
				return naoEncontrado("campanha (inexistente ou cancelada — cancelada não entra em falhas)")
			}
			if err != nil {
				return nil, err
			}
			sm := res.Summary
			c := res.Campaign
			o := falhasCampanhaDTO{
				Campanha: campanhaResumida{Campanha: refDe(c.ID, c.Name), Inicio: c.StartDate, Fim: c.EndDate, Estado: c.Status},
				Cliente:  refDe(c.ClientID, c.ClientName),
				Resumo: resumoFalhasDetalhe{Emissoras: sm.StationsWithFailure, DiasDeFalha: sm.TotalFailureDays, deficitSplit: deficitSplit{
					Deficit: sm.TotalDeficit, DeficitNaoTocou: sm.TotalDeficitAbsent, DeficitForaDoHorario: sm.TotalDeficitOffSlot}},
				Links: links{Tela: linkFalhas(cfg, map[string]string{"view": "by_campaign", "campaign": id.String()}),
					ComoBaixar: "Na tela, o detalhe da campanha abre sozinho; o PDF de cobrança é o botão \"Baixar PDF de cobrança\" dele."},
			}
			o.lista = mapear(paginar(res.Stations, in.Paginacao.comTeto(tetoPaginaFalhasCampanha)), emissoraDeficitDe)
			o.Ressalvas = []string{ressalvaSplit, fmt.Sprintf("dias_de_falha de cada emissora: os %d primeiros (dias_de_falha_total traz o real).", tetoPorItem)}
			if c.Status == "ativa" {
				o.Ressalvas = append(o.Ressalvas, "Campanha ativa: os números ainda mudam (o déficit pode ser compensado até o fim da vigência).")
			}
			return ok(o)
		})
}
