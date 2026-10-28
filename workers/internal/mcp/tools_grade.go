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
	"radiocheck/internal/categorizer"
)

type entradaGrade struct {
	CampanhaID string `json:"campanha_id" jsonschema:"Id (uuid) da campanha. Nunca invente: pergunte."`
	De         string `json:"de" jsonschema:"Primeiro dia (AAAA-MM-DD, dia de America/Sao_Paulo)."`
	Ate        string `json:"ate" jsonschema:"Último dia (AAAA-MM-DD), no máximo 93 dias depois de de."`
	EmissoraID string `json:"emissora_id,omitempty" jsonschema:"Só as células desta emissora (id de buscar_emissoras; opcional). Nunca invente: use o id de outra tool."`
	Paginacao
}

type celulaGrade struct {
	Emissora ref    `json:"emissora"`
	Tipo     ref    `json:"tipo"`
	Data     string `json:"data"`
	Esperado int32  `json:"esperado"`
	InSlot   int32  `json:"in_slot"`
	Deficit  int32  `json:"deficit"`
	Bonus    int32  `json:"bonus"`
	OutSlot  int32  `json:"out_slot"`
	OutDate  int32  `json:"out_date"`
}

type totaisGrade struct {
	Esperado int64 `json:"esperado"`
	InSlot   int64 `json:"in_slot"`
	Deficit  int64 `json:"deficit"`
	Bonus    int64 `json:"bonus"`
	OutSlot  int64 `json:"out_slot"`
	OutDate  int64 `json:"out_date"`
}

// saidaGrade é a lista da grade com os totais do período inteiro filtrado.
type saidaGrade struct {
	lista[celulaGrade]
	Totais totaisGrade `json:"totais"`
}

type entradaPlano struct {
	CampanhaID string `json:"campanha_id" jsonschema:"Id (uuid) da campanha. Nunca invente: pergunte."`
	EmissoraID string `json:"emissora_id" jsonschema:"Id (uuid) da emissora da célula, vindo de grade_campanha."`
	TipoID     string `json:"tipo_id" jsonschema:"Id (uuid) do tipo de material da célula (a grade é por tipo), vindo de grade_campanha."`
	Data       string `json:"data" jsonschema:"Dia (AAAA-MM-DD) em America/Sao_Paulo."`
}

type saldoDia struct {
	Esperado int32 `json:"esperado"`
	InSlot   int32 `json:"in_slot"`
	Deficit  int32 `json:"deficit"`
	Bonus    int32 `json:"bonus"`
	OutSlot  int32 `json:"out_slot"`
	OutDate  int32 `json:"out_date"`
}

type faixaDoDia struct {
	RegraID         string       `json:"regra_id"`
	Nome            string       `json:"nome"`
	Faixa           faixaHoraria `json:"faixa"`
	InsercoesPorDia int16        `json:"insercoes_por_dia"`
	Materiais       []ref        `json:"materiais"`
}

type regraForaDoDia struct {
	RegraID string `json:"regra_id"`
	Nome    string `json:"nome"`
	Motivo  string `json:"motivo"`
}

type ajusteDoDia struct {
	InsercoesEsperadas int16        `json:"insercoes_esperadas"`
	Faixa              faixaHoraria `json:"faixa"`
	Motivo             *string      `json:"motivo"`
}

type tocadaDoDia struct {
	ID        string `json:"id"`
	Quando    string `json:"quando"`
	Material  ref    `json:"material"`
	Categoria string `json:"categoria"`
	Manual    bool   `json:"manual"`
}

type saidaPlano struct {
	Campanha          ref              `json:"campanha"`
	Emissora          ref              `json:"emissora"`
	Tipo              ref              `json:"tipo"`
	Data              string           `json:"data"`
	MetaVemDe         string           `json:"meta_vem_de"`
	Saldo             *saldoDia        `json:"saldo"`
	AjusteDoDia       *ajusteDoDia     `json:"ajuste_do_dia"`
	Faixas            []faixaDoDia     `json:"faixas"`
	RegrasQueNaoValem []regraForaDoDia `json:"regras_que_nao_valem"`
	Veiculacoes       []tocadaDoDia    `json:"veiculacoes"`
	Ressalvas         []string         `json:"ressalvas"`
}

const ressalvaGrade = "A grade é emissora × TIPO de material × dia (America/Sao_Paulo). esperado = meta do dia; deficit = esperado − in_slot; out_slot não vale nada (não fatura nem abate déficit); bonus é o que passou da cota. Os totais somam o período inteiro filtrado, não só a página."

const ressalvaPlano = "A categoria de cada veiculação é a do servidor (cota da célula-dia: as N primeiras dentro da faixa são in_slot, o resto é bonus; fora da faixa é out_slot enquanto a cota não fecha). Não recalcule: o saldo vem pronto."

// tetoTocadasDoDia: o modal pede 200; o MCP pede o máximo do repositório.
const tetoTocadasDoDia = 1000

// motivoForaDoDia explica por que uma regra da célula não vale no dia. A
// DECISÃO é de categorizer.RuleCoversDay; aqui é só o porquê, para o modelo.
func motivoForaDoDia(r catalog.DistributionRule, d time.Time) string {
	switch dd := dia(d); {
	case dd < dia(r.StartDate):
		return "começa em " + dia(r.StartDate)
	case dd > dia(r.EndDate):
		return "encerrou em " + dia(r.EndDate)
	default:
		return "não vale neste dia da semana (vale: " + strings.Join(diasDaMascara(r.WeekdayMask), ", ") + ")"
	}
}

// categoriaDe: "orphan" é o nome antigo de bonus (renomeada em 2026-08-17).
func categoriaDe(c string) string {
	if c == categorizer.CatOrphan {
		return categorizer.CatBonus
	}
	return c
}

func registrarGrade(s *sdk.Server, cfg ConfigServidor) {
	ferramentaLeitura(s, cfg, "grade_campanha",
		"Devolve a grade de veiculação de uma campanha (a tela /detections): para cada emissora × tipo de material × dia, "+
			"esperado, in_slot, deficit, bonus, out_slot e out_date, mais os totais do período. A armadilha: a grade é por TIPO, "+
			"não por material, e out_slot não vale nada. Período de até 93 dias. Para entender UMA célula, use plano_do_dia. "+
			"Nunca invente ids. Lista paginada.",
		func(ctx context.Context, L *Leitura, in entradaGrade) (*sdk.CallToolResult, error) {
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
			if ate.Before(de) || ate.Sub(de) > 92*24*time.Hour {
				return recusa("período inválido: ate tem de ser >= de e no máximo 93 dias depois", "entrada_invalida")
			}
			var soEmissora *uuid.UUID
			if in.EmissoraID != "" {
				id, err := uuid.Parse(in.EmissoraID)
				if err != nil {
					return recusaEntrada("emissora_id")
				}
				soEmissora = &id
			}
			rows, err := L.Grade.ListByCampaign(ctx, c.ID, de, ate)
			if err != nil {
				return nil, err
			}
			var ests []uuid.UUID
			if soEmissora != nil {
				rows = slices.DeleteFunc(rows, func(r catalog.DailySummaryRow) bool { return r.StationID != *soEmissora })
			}
			for _, r := range rows {
				ests = append(ests, r.StationID)
			}
			emissoras, err := nomesEmissoras(ctx, L, ests)
			if err != nil {
				return nil, err
			}
			tipos, err := nomesTipos(ctx, L)
			if err != nil {
				return nil, err
			}
			var tot totaisGrade
			celulas := make([]celulaGrade, 0, len(rows))
			for _, r := range rows {
				tot.Esperado += int64(r.Expected)
				tot.InSlot += int64(r.InSlot)
				tot.Deficit += int64(r.Deficit)
				tot.Bonus += int64(r.Bonus)
				tot.OutSlot += int64(r.OutSlot)
				tot.OutDate += int64(r.OutDate)
				celulas = append(celulas, celulaGrade{
					Emissora: refDe(r.StationID, emissoras[r.StationID]), Tipo: refDe(r.TypeID, tipos[r.TypeID]),
					Data: dia(r.ForDate), Esperado: r.Expected, InSlot: r.InSlot, Deficit: r.Deficit,
					Bonus: r.Bonus, OutSlot: r.OutSlot, OutDate: r.OutDate,
				})
			}
			out := saidaGrade{lista: paginar(celulas, in.Paginacao), Totais: tot}
			out.Ressalvas = []string{ressalvaGrade}
			if c.Status == "cancelada" {
				out.Ressalvas = append(out.Ressalvas, ressalvaCancelada)
			}
			return ok(out)
		})

	ferramentaLeitura(s, cfg, "plano_do_dia",
		"Explica UMA célula da grade (campanha × emissora × tipo × dia): o saldo do dia, de onde vem a meta (ajuste do dia, "+
			"regras ou nenhuma), as faixas que valem naquele dia, as regras que não valem e por quê, e as veiculações do dia com "+
			"a categoria do servidor. Use para responder 'por que essa tocada não contou' ou 'por que deu déficit'. "+
			"Os ids vêm de grade_campanha; nunca invente.",
		func(ctx context.Context, L *Leitura, in entradaPlano) (*sdk.CallToolResult, error) {
			c, rec, err := lerCampanha(ctx, L, in.CampanhaID)
			if rec != nil || err != nil {
				return rec, err
			}
			eid, err := uuid.Parse(in.EmissoraID)
			if err != nil {
				return recusaEntrada("emissora_id")
			}
			tid, err := uuid.Parse(in.TipoID)
			if err != nil {
				return recusaEntrada("tipo_id")
			}
			d, okDia := lerDia(in.Data)
			if !okDia {
				return recusaEntrada("data")
			}
			est, err := L.Emissoras.Get(ctx, eid)
			if errors.Is(err, pgx.ErrNoRows) {
				return naoEncontrado("emissora")
			}
			if err != nil {
				return nil, err
			}
			tipo, err := L.TiposMaterial.Get(ctx, tid)
			if errors.Is(err, pgx.ErrNoRows) {
				return naoEncontrado("tipo de material")
			}
			if err != nil {
				return nil, err
			}
			out := saidaPlano{
				Campanha: refDe(c.ID, c.Name), Emissora: refDe(est.ID, est.Name), Tipo: refDe(tipo.ID, tipo.Name),
				Data: dia(d), Faixas: []faixaDoDia{}, RegrasQueNaoValem: []regraForaDoDia{}, Veiculacoes: []tocadaDoDia{},
				Ressalvas: []string{ressalvaPlano},
			}

			// Saldo: a mesma linha da grade (daily_play_summary_for).
			linhas, err := L.Grade.ListByCampaign(ctx, c.ID, d, d)
			if err != nil {
				return nil, err
			}
			for _, r := range linhas {
				if r.StationID == eid && r.TypeID == tid {
					out.Saldo = &saldoDia{Esperado: r.Expected, InSlot: r.InSlot, Deficit: r.Deficit,
						Bonus: r.Bonus, OutSlot: r.OutSlot, OutDate: r.OutDate}
				}
			}

			// Ajuste do dia: substitui as regras.
			ajustes, err := L.Ajustes.ListByCampaignAndDateRange(ctx, c.ID, d, d)
			if err != nil {
				return nil, err
			}
			for _, a := range ajustes {
				if a.StationID == eid && a.TypeID == tid {
					out.AjusteDoDia = &ajusteDoDia{InsercoesEsperadas: a.PlaysExpected,
						Faixa: faixaHoraria{Inicio: a.TimeStart, Fim: a.TimeEnd}, Motivo: a.Reason}
				}
			}

			// Regras da célula: valem ou não no dia pela MESMA decisão do Settle.
			regras, err := L.Regras.ListByCampaign(ctx, c.ID)
			if err != nil {
				return nil, err
			}
			mats, err := nomesMateriais(ctx, L, c.ClientID)
			if err != nil {
				return nil, err
			}
			for _, r := range regras {
				if r.TypeID != tid || !slices.Contains(r.StationIDs, eid) {
					continue
				}
				if categorizer.RuleCoversDay(categorizer.Rule{StartDate: r.StartDate, EndDate: r.EndDate, WeekdayMask: r.WeekdayMask}, d) {
					out.Faixas = append(out.Faixas, faixaDoDia{
						RegraID: r.ID.String(), Nome: r.Name, Faixa: faixaHoraria{Inicio: r.TimeStart, Fim: r.TimeEnd},
						InsercoesPorDia: r.PlaysPerDay, Materiais: refs(r.MaterialIDs, mats),
					})
				} else {
					out.RegrasQueNaoValem = append(out.RegrasQueNaoValem,
						regraForaDoDia{RegraID: r.ID.String(), Nome: r.Name, Motivo: motivoForaDoDia(r, d)})
				}
			}
			switch {
			case out.AjusteDoDia != nil:
				out.MetaVemDe = "ajuste_do_dia"
				out.Ressalvas = append(out.Ressalvas, "Há ajuste do dia: a meta e a faixa dele substituem as regras listadas em faixas.")
				if out.AjusteDoDia.InsercoesEsperadas == 0 {
					out.Ressalvas = append(out.Ressalvas, "O ajuste zera a meta: toda tocada deste dia é bonus, nunca out_slot (exceto out_date: tocada fora da vigência da campanha, ou de material com regra própria fora do período dela).")
				}
			case len(out.Faixas) > 0:
				out.MetaVemDe = "regras"
			default:
				out.MetaVemDe = "sem_meta"
				out.Ressalvas = append(out.Ressalvas, "Nenhuma regra nem ajuste vale neste dia: a meta é 0 e as tocadas entram como bonus, nunca out_slot (exceto out_date: tocada fora da vigência da campanha, ou de material com regra própria fora do período dela).")
			}

			if c.Status == "cancelada" {
				out.Ressalvas = append(out.Ressalvas, ressalvaCancelada)
				canc, err := L.Campanhas.CancelledAtFor(ctx, []uuid.UUID{c.ID})
				if err != nil {
					return nil, err
				}
				if em, achou := canc[c.ID]; achou && dia(d) > dia(em.In(fusoSP)) {
					out.Ressalvas = append(out.Ressalvas, "Dia depois do cancelamento: não há obrigação; as faixas abaixo não geram meta.")
				}
			}
			if out.Saldo == nil {
				out.Ressalvas = append(out.Ressalvas, "A grade não tem linha para esta célula neste dia (sem meta e sem veiculação contada).")
			}

			// Veiculações do dia: o recorte do DayDetailModal (conjunto aprovado).
			ini, fim := limitesDoDiaSP(d)
			dets, err := L.Veiculacoes.List(ctx, catalog.ListFilter{
				CampaignID: &c.ID, StationID: &eid, StartDate: &ini, EndDate: &fim, Limit: tetoTocadasDoDia,
			})
			if err != nil {
				return nil, err
			}
			for i := len(dets) - 1; i >= 0; i-- { // List vem da mais nova para a mais velha
				v := dets[i]
				if v.TypeID == nil || *v.TypeID != tid {
					continue
				}
				out.Veiculacoes = append(out.Veiculacoes, tocadaDoDia{
					ID: v.ID.String(), Quando: instante(v.DetectedAt), Material: refDe(v.CommercialID, v.CommercialName),
					Categoria: categoriaDe(v.Category), Manual: v.ManualAt != nil,
				})
			}
			if len(dets) == tetoTocadasDoDia {
				out.Ressalvas = append(out.Ressalvas, fmt.Sprintf(
					"o dia tem mais de %d veiculações nesta emissora; a lista foi cortada (o saldo continua completo)", tetoTocadasDoDia))
			}
			return ok(out)
		})
}
