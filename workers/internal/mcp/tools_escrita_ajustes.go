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

	"radiocheck/internal/acoes"
	"radiocheck/internal/catalog"
	"radiocheck/internal/categorizer"
)

// Escritas do ajuste do dia (override), Lote C, Task 17: definir e apagar. Um
// ajuste é uma EXCEÇÃO de UMA célula-dia (campanha × tipo × emissora × dia): a meta
// e a faixa dele SUPERSEDEM as regras daquela célula naquele dia. A gravação
// recategoriza a célula-dia de forma SÍNCRONA (acoes.DefinirAjuste/ApagarAjuste),
// então o resultado já devolve o plano do dia recalculado. Escalam para crítico
// (F3-4) quando o dia já passou (hoje em America/Sao_Paulo) ou a célula-dia já tem
// veiculação aprovada.
//
// O que a TELA exige e o MCP espelha (OverridePopover.jsx): meta de 0 a 100; faixa
// HH:MM com fim > início (sempre gravada, mesmo com meta 0: o banco exige); a faixa
// padrão é a do ajuste atual, depois a da regra única do dia, e só então 06:00–22:00.
// As células da grade só existem dentro do período da campanha e nas emissoras dela,
// e a tela não mostra célula fora disso: a rota aceita, o MCP não (F3-8).

func registrarEscritasAjustes(s *sdk.Server, cfg ConfigServidor, p *Principal) {
	ferramentaEscrita(s, cfg, p, escritaDefinirOverride(cfg))
	ferramentaEscrita(s, cfg, p, escritaApagarOverride(cfg))
}

const (
	// tetoInsercoesDoAjuste é o max do <input type="number"> do OverridePopover.
	tetoInsercoesDoAjuste = 100
	// tetoMotivoDoAjuste: o motivo é texto livre (TEXT no banco); o MCP corta em 500.
	tetoMotivoDoAjuste = 500
	// faixaPadraoDoAjuste é o último recurso da tela quando não há ajuste nem regra.
	faixaPadraoIni, faixaPadraoFim = "06:00", "22:00"

	ressalvaMetaZero = "Meta 0 NÃO é \"nada esperado\": é meta zero, e por isso TODA tocada desta célula-dia vira bonus (bonificação), nunca out_slot (exceto out_date: tocada fora da vigência da campanha, ou de material com regra própria fora do período dela). A emissora não é cobrada por déficit, mas a tocada passa a contar como bônus."
)

// ── Célula-dia ──────────────────────────────────────────────────────────

// celulaDoAjuste é a célula-dia (e o que há nela hoje) lida do banco.
type celulaDoAjuste struct {
	camp   *catalog.Campaign
	tipo   *catalog.MaterialType
	est    *catalog.Station
	dia    time.Time
	atual  *catalog.DistributionOverride // nil = ainda sem ajuste
	regras []catalog.DistributionRule    // as regras do tipo/emissora que valem NO DIA
}

// lerCelulaDoAjuste valida e lê a célula. paraGravar exige a emissora na campanha e
// o dia dentro do período (criar); apagar não exige, para poder limpar ajuste que a
// rota gravou fora disso.
func lerCelulaDoAjuste(ctx context.Context, E *Escritas, campanhaID, tipoID, emissoraID, data string, paraGravar bool) (*celulaDoAjuste, error) {
	L := E.Leitura
	camp, err := lerCampanhaParaEscrever(ctx, L, campanhaID)
	if err != nil {
		return nil, err
	}
	tid, err := uuidDaEntrada("tipo_id", tipoID)
	if err != nil {
		return nil, err
	}
	eid, err := uuidDaEntrada("emissora_id", emissoraID)
	if err != nil {
		return nil, err
	}
	d, err := dataDaEntrada("data", data)
	if err != nil {
		return nil, err
	}
	tipo, err := L.TiposMaterial.Get(ctx, tid)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNaoEncontrado("tipo de material")
	}
	if err != nil {
		return nil, err
	}
	est, err := L.Emissoras.Get(ctx, eid)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNaoEncontrado("emissora")
	}
	if err != nil {
		return nil, err
	}
	if paraGravar {
		if !slices.Contains(camp.TargetStations, eid) {
			return nil, errEntrada(fmt.Sprintf(
				"a emissora %q não é da campanha: o ajuste só vale para emissora da campanha (acrescente-a antes com editar_emissoras_campanha)", est.Name))
		}
		if d.Before(camp.StartDate) || d.After(camp.EndDate) {
			return nil, errEntrada(fmt.Sprintf(
				"a data %s está fora do período da campanha (%s a %s): a grade só tem células nesses dias, e uma tocada fora deles é out_date de qualquer jeito", dia(d), dia(camp.StartDate), dia(camp.EndDate)))
		}
	}
	c := &celulaDoAjuste{camp: camp, tipo: tipo, est: est, dia: d}
	ajustes, err := L.Ajustes.ListByCampaignAndDateRange(ctx, camp.ID, d, d)
	if err != nil {
		return nil, err
	}
	for i := range ajustes {
		if ajustes[i].TypeID == tid && ajustes[i].StationID == eid {
			c.atual = &ajustes[i]
		}
	}
	regras, err := L.Regras.ListByCampaign(ctx, camp.ID)
	if err != nil {
		return nil, err
	}
	for _, r := range regras {
		if r.TypeID == tid && slices.Contains(r.StationIDs, eid) &&
			categorizer.RuleCoversDay(categorizer.Rule{StartDate: r.StartDate, EndDate: r.EndDate, WeekdayMask: r.WeekdayMask}, d) {
			c.regras = append(c.regras, r)
		}
	}
	return c, nil
}

// metaDasRegras é a soma das inserções por dia das regras que valem na célula-dia.
func (c *celulaDoAjuste) metaDasRegras() int {
	var n int
	for _, r := range c.regras {
		n += int(r.PlaysPerDay)
	}
	return n
}

// metaAtual descreve a meta que vale hoje na célula-dia.
func (c *celulaDoAjuste) metaAtual() any {
	if c.atual != nil {
		return fmt.Sprintf("%d (ajuste do dia)", c.atual.PlaysExpected)
	}
	if len(c.regras) == 0 {
		return "0 (nenhuma regra vale neste dia)"
	}
	return fmt.Sprintf("%d (soma de %d regra(s))", c.metaDasRegras(), len(c.regras))
}

func (c *celulaDoAjuste) faixaAtual() any {
	if c.atual != nil {
		return c.atual.TimeStart + "–" + c.atual.TimeEnd
	}
	if len(c.regras) == 0 {
		return nil
	}
	faixas := make([]string, 0, len(c.regras))
	for _, r := range c.regras {
		faixas = append(faixas, r.TimeStart+"–"+r.TimeEnd)
	}
	return strings.Join(faixas, " + ")
}

func (c *celulaDoAjuste) recurso() (string, *uuid.UUID) {
	id := c.camp.ID
	return "distribution_override", &id
}

// nomesDaCelula são os mapas de nome que o ajusteDTO usa (tipo e emissora da célula).
func (c *celulaDoAjuste) nomes() (tipos, emissoras map[uuid.UUID]string) {
	return map[uuid.UUID]string{c.tipo.ID: c.tipo.Name}, map[uuid.UUID]string{c.est.ID: c.est.Name}
}

// escalaDeAjuste aplica as duas condições de F3-4 a um ajuste (definir ou apagar):
// o dia já passou, ou a célula-dia já tem veiculação aprovada.
func escalaDeAjuste(ctx context.Context, E *Escritas, c *celulaDoAjuste) (Escalada, error) {
	var motivos []string
	hoje := hojeSP(E.agora())
	if c.dia.Before(hoje) {
		motivos = append(motivos, fmt.Sprintf(
			"O dia %s já passou (hoje é %s em America/Sao_Paulo): mudar a meta ou a faixa de um dia passado muda o déficit e a categoria do que o cliente já viu.",
			dia(c.dia), dia(hoje)))
	}
	d := c.dia
	ha, err := E.Leitura.Veiculacoes.HaVeiculacao(ctx, catalog.EscopoVeiculacao{
		CampanhaIDs: []uuid.UUID{c.camp.ID}, TipoIDs: []uuid.UUID{c.tipo.ID}, EmissoraIDs: []uuid.UUID{c.est.ID}, DiaDe: &d, DiaAte: &d,
	})
	if err != nil {
		return Escalada{}, err
	}
	if ha {
		motivos = append(motivos, fmt.Sprintf(
			"A célula (%s × %s × %s) já tem veiculação aprovada: o ajuste recategoriza esse dia agora e pode mudar in_slot, bonus, out_slot e déficit do que o cliente já viu.",
			c.est.Name, c.tipo.Name, dia(c.dia)))
	}
	if len(motivos) == 0 {
		return Escalada{}, nil
	}
	return Escalada{Nivel: Critico, Motivo: strings.Join(motivos, " ")}, nil
}

// linkDosAjustes: o ajuste do dia se faz no grid de /detections (botão "Ajustar faixa deste dia")
// ou no passo de distribuição do wizard; o deep link existente é o da campanha em /detections.
func linkDosAjustes(cfg ConfigServidor, campanhaID string) *string {
	if id, err := uuid.Parse(campanhaID); err == nil {
		return cfg.link("/detections?campaign_id=" + id.String())
	}
	return cfg.link("/detections")
}

func ajusteDoRecurso(campanhaID string) (string, *uuid.UUID) {
	return campanhaDoRecurso("distribution_override", campanhaID)
}

// ── Resultado ───────────────────────────────────────────────────────────

type saidaAjuste struct {
	Campanha ref         `json:"campanha"`
	Ajuste   ajusteDTO   `json:"ajuste"`
	Plano    *saidaPlano `json:"plano_do_dia"`
}

type saidaApagarAjuste struct {
	Campanha       ref         `json:"campanha"`
	Apagado        bool        `json:"apagado"`
	AjusteRemovido ajusteDTO   `json:"ajuste_removido"`
	Plano          *saidaPlano `json:"plano_do_dia"`
}

// ── definir_override ────────────────────────────────────────────────────

type entradaDefinirOverride struct {
	CampanhaID    string `json:"campanha_id" jsonschema:"Id (uuid) da campanha, vindo de listar_campanhas. Nunca invente: pergunte."`
	TipoID        string `json:"tipo_id" jsonschema:"Id (uuid) do tipo de material da célula (a grade é por TIPO), vindo de grade_campanha. Nunca invente: pergunte."`
	EmissoraID    string `json:"emissora_id" jsonschema:"Id (uuid) da emissora da célula (precisa ser da campanha), vindo de grade_campanha. Nunca invente: pergunte."`
	Data          string `json:"data" jsonschema:"Dia da célula (AAAA-MM-DD, America/Sao_Paulo), dentro do período da campanha."`
	Insercoes     int    `json:"insercoes" jsonschema:"A meta do dia nesta célula, de 0 a 100. ATENÇÃO: 0 não é 'nada esperado', é meta zero — toda tocada do dia vira bonus (bonificação)."`
	InicioHorario string `json:"inicio_horario,omitempty" jsonschema:"Início da faixa do dia (HH:MM, 00:00 a 23:59). Informe junto com fim_horario, ou omita os dois para usar a faixa do ajuste atual ou da regra única do dia."`
	FimHorario    string `json:"fim_horario,omitempty" jsonschema:"Fim da faixa do dia (HH:MM), maior que o início. Informe junto com inicio_horario, ou omita os dois."`
	Motivo        string `json:"motivo,omitempty" jsonschema:"Por que o ajuste (texto livre, até 500 caracteres), ex.: emissora trocou a grade."`
	CampoConfirmacao
}

// planoDeAjuste é o ajuste validado, com a faixa já resolvida.
type planoDeAjuste struct {
	*celulaDoAjuste
	insercoes int16
	ini, fim  string
	origem    string // "" = faixa informada; senão, de onde ela veio
	motivo    *string
	ressalvas []string
}

func planejarAjuste(ctx context.Context, E *Escritas, in entradaDefinirOverride) (*planoDeAjuste, error) {
	if in.Insercoes < 0 || in.Insercoes > tetoInsercoesDoAjuste {
		return nil, errEntrada(fmt.Sprintf("insercoes: use um número de 0 a %d (0 = meta zero: toda tocada do dia vira bonus)", tetoInsercoesDoAjuste))
	}
	if (aparar(in.InicioHorario) == "") != (aparar(in.FimHorario) == "") {
		return nil, errEntrada("informe inicio_horario e fim_horario juntos, ou omita os dois")
	}
	motivo := aparar(in.Motivo)
	if err := limiteDeRunas("motivo", motivo, tetoMotivoDoAjuste); err != nil {
		return nil, err
	}
	c, err := lerCelulaDoAjuste(ctx, E, in.CampanhaID, in.TipoID, in.EmissoraID, in.Data, true)
	if err != nil {
		return nil, err
	}
	pl := &planoDeAjuste{celulaDoAjuste: c, insercoes: int16(in.Insercoes), motivo: ptrTexto(motivo)}
	if aparar(in.InicioHorario) != "" {
		if pl.ini, pl.fim, err = faixaDaEntrada(in.InicioHorario, in.FimHorario); err != nil {
			return nil, err
		}
	} else if err := pl.herdarFaixa(); err != nil {
		return nil, err
	}
	if c.atual != nil && c.atual.PlaysExpected == pl.insercoes && c.atual.TimeStart == pl.ini && c.atual.TimeEnd == pl.fim &&
		igualTexto(c.atual.Reason, motivo) {
		return nil, errEntrada("nada a alterar: esta célula-dia já tem exatamente este ajuste")
	}
	pl.ressalvas = pl.avisosDoAjuste()
	return pl, nil
}

// herdarFaixa resolve a faixa omitida como a tela faz (OverridePopover.jsx,
// defaultWindow): ajuste atual → regra única do dia → (várias regras: o modelo
// precisa escolher) → 06:00–22:00.
func (pl *planoDeAjuste) herdarFaixa() error {
	c := pl.celulaDoAjuste
	var ini, fim string
	switch {
	case c.atual != nil:
		ini, fim, pl.origem = c.atual.TimeStart, c.atual.TimeEnd, "a faixa do ajuste atual da célula"
	case len(c.regras) > 0:
		faixas := map[string]bool{}
		for _, r := range c.regras {
			faixas[r.TimeStart+"–"+r.TimeEnd] = true
		}
		if len(faixas) > 1 && pl.insercoes > 0 {
			var lista []string
			for _, r := range c.regras {
				if f := r.TimeStart + "–" + r.TimeEnd; !slices.Contains(lista, f) {
					lista = append(lista, f)
				}
			}
			return errEntrada(fmt.Sprintf(
				"a célula tem %d regras com faixas diferentes neste dia (%s): informe inicio_horario e fim_horario para dizer qual faixa vale no ajuste",
				len(c.regras), strings.Join(lista, ", ")))
		}
		r := c.regras[0]
		ini, fim = r.TimeStart, r.TimeEnd
		pl.origem = "a faixa da regra que vale neste dia"
		if len(faixas) > 1 {
			pl.origem = "a faixa da primeira regra do dia (com meta 0 a faixa não pesa)"
		}
	default:
		ini, fim, pl.origem = faixaPadraoIni, faixaPadraoFim, "a faixa padrão da tela ("+faixaPadraoIni+"–"+faixaPadraoFim+"), porque nenhuma regra vale neste dia"
	}
	var err error
	if pl.ini, pl.fim, err = faixaDaEntrada(ini, fim); err != nil {
		return errEntrada(fmt.Sprintf("a faixa herdada (%s–%s) não serve para um ajuste (HH:MM de 00:00 a 23:59): informe inicio_horario e fim_horario", ini, fim))
	}
	return nil
}

// avisosDoAjuste: as ressalvas do preview.
func (pl *planoDeAjuste) avisosDoAjuste() []string {
	var out []string
	if pl.insercoes == 0 {
		out = append(out, ressalvaMetaZero)
	}
	if pl.origem != "" {
		out = append(out, "A faixa não foi informada: usei "+pl.origem+" ("+pl.ini+"–"+pl.fim+").")
	}
	c := pl.celulaDoAjuste
	// O mesmo aviso do popover da tela: faixa mais estreita que a da regra única.
	if c.atual == nil && len(c.regras) == 1 && pl.insercoes > 0 && (pl.ini > c.regras[0].TimeStart || pl.fim < c.regras[0].TimeEnd) {
		out = append(out, fmt.Sprintf("A faixa do ajuste (%s–%s) é mais estreita que a da regra (%s–%s): veiculações fora dela contam como fora do prazo (out_slot) enquanto a meta não fechar.",
			pl.ini, pl.fim, c.regras[0].TimeStart, c.regras[0].TimeEnd))
	}
	if len(c.regras) == 0 && c.atual == nil && pl.insercoes > 0 {
		out = append(out, "Nenhuma regra de distribuição vale neste dia: o ajuste cria uma meta onde não havia nenhuma.")
	}
	return out
}

func escritaDefinirOverride(cfg ConfigServidor) Escrita[entradaDefinirOverride] {
	return Escrita[entradaDefinirOverride]{
		Nome: "definir_override",
		Descricao: "Define o ajuste do dia (override) de UMA célula — campanha × tipo de material × emissora × dia: a meta de inserções do dia " +
			"(insercoes) e a faixa horária própria (inicio_horario–fim_horario) que SUBSTITUEM as regras de distribuição daquela célula naquele " +
			"dia. Se a célula já tem ajuste, ele é trocado. Omita a faixa para usar a do ajuste atual ou a da regra única do dia (com várias " +
			"regras de faixas diferentes, informe a faixa). A armadilha principal: insercoes=0 NÃO é 'nada esperado', é meta zero — toda tocada " +
			"da célula-dia vira bonus (bonificação), nunca out_slot (exceto out_date); a emissora não é cobrada por déficit, mas a tocada passa a " +
			"contar como bônus. Outras armadilhas: o ajuste vale só para o dia e a célula (não para a semana); a data tem de estar no período da " +
			"campanha e a emissora na campanha. A gravação recategoriza a célula-dia AGORA (síncrona): o resultado traz o plano do dia " +
			"recalculado. Sobe para crítico quando a data já passou (hoje é o de America/Sao_Paulo) OU a célula-dia já tem veiculação " +
			"aprovada; nesse caso nada é feito e a resposta traz o link da tela. Campanha cancelada é recusada. Nunca invente ids: pergunte. " +
			"Mostre o preview e espere a pessoa aprovar antes de confirmar.",
		Nivel: Confirma,
		Escalar: func(ctx context.Context, E *Escritas, in entradaDefinirOverride) (Escalada, error) {
			pl, err := planejarAjuste(ctx, E, in)
			if err != nil {
				return Escalada{}, err
			}
			return escalaDeAjuste(ctx, E, pl.celulaDoAjuste)
		},
		Recurso: func(in entradaDefinirOverride) (string, *uuid.UUID) { return ajusteDoRecurso(in.CampanhaID) },
		Tela:    func(_ *Escritas, in entradaDefinirOverride) *string { return linkDosAjustes(cfg, in.CampanhaID) },
		Preview: func(ctx context.Context, E *Escritas, in entradaDefinirOverride) (previa, error) {
			pl, err := planejarAjuste(ctx, E, in)
			if err != nil {
				return previa{}, err
			}
			c := pl.celulaDoAjuste
			mud := []mudanca{
				{Campo: "meta do dia (inserções)", De: c.metaAtual(), Para: pl.insercoes},
				{Campo: "faixa horária", De: c.faixaAtual(), Para: pl.ini + "–" + pl.fim},
			}
			if c.atual != nil || pl.motivo != nil {
				var de any
				if c.atual != nil {
					de = textoOuNil(c.atual.Reason)
				}
				mud = append(mud, mudanca{Campo: "motivo", De: de, Para: textoOuNil(pl.motivo)})
			}
			return previa{
				Resumo:   fmt.Sprintf("Ajuste do dia %s em %s × %s × %q: meta %d, faixa %s–%s", dia(c.dia), c.est.Name, c.tipo.Name, c.camp.Name, pl.insercoes, pl.ini, pl.fim),
				Mudancas: mud,
				Efeitos: []string{
					"O ajuste SUBSTITUI as regras desta célula (emissora × tipo × dia): a meta do dia vira a dele e a faixa dele é a única válida (tolerância de 15 min em cada ponta). Os outros dias e as outras células não mudam.",
					"Ao confirmar, as veiculações aprovadas desta célula-dia são recategorizadas AGORA (síncrono) — in_slot, bonus, out_slot e déficit do dia podem mudar.",
				},
				Ressalvas: pl.ressalvas,
			}, nil
		},
		Executar: func(ctx context.Context, E *Escritas, p *Principal, in entradaDefinirOverride) (feito, error) {
			pl, err := planejarAjuste(ctx, E, in)
			if err != nil {
				return feito{}, err
			}
			c := pl.celulaDoAjuste
			if err := E.Acoes.DefinirAjuste(ctx, acoes.EntradaAjuste{
				CampanhaID: c.camp.ID, TipoID: c.tipo.ID, EmissoraID: c.est.ID, Dia: c.dia, Insercoes: pl.insercoes,
				HoraInicio: pl.ini, HoraFim: pl.fim, Motivo: pl.motivo, CriadoPor: &p.UserID,
			}); err != nil {
				return feito{}, err
			}
			gravado, err := ajusteGravado(ctx, E.Leitura, c)
			if err != nil {
				return feito{}, err
			}
			plano, err := montarPlanoDoDia(ctx, E.Leitura, c.camp, c.est, c.tipo, c.dia)
			if err != nil {
				return feito{}, err
			}
			tipos, emissoras := c.nomes()
			camp := c.camp
			return feito{
				Resultado: saidaAjuste{Campanha: refDe(camp.ID, camp.Name), Ajuste: montarAjusteDTO(*gravado, tipos, emissoras), Plano: plano},
				Recurso:   "distribution_override", RecursoID: &camp.ID,
				Ressalvas: []string{"A recategorização da célula-dia roda junto da gravação: o plano do dia abaixo já traz as categorias recalculadas."},
			}, nil
		},
	}
}

// ajusteGravado relê o ajuste da célula-dia. Se sumiu, a escrita JÁ aconteceu: é
// falha interna (não recusa), para o token não voltar a valer.
func ajusteGravado(ctx context.Context, L *Leitura, c *celulaDoAjuste) (*catalog.DistributionOverride, error) {
	ajustes, err := L.Ajustes.ListByCampaignAndDateRange(ctx, c.camp.ID, c.dia, c.dia)
	if err != nil {
		return nil, err
	}
	for i := range ajustes {
		if ajustes[i].TypeID == c.tipo.ID && ajustes[i].StationID == c.est.ID {
			return &ajustes[i], nil
		}
	}
	return nil, errors.New("mcp: o ajuste gravado não foi encontrado na releitura")
}

// ── apagar_override ─────────────────────────────────────────────────────

type entradaApagarOverride struct {
	CampanhaID string `json:"campanha_id" jsonschema:"Id (uuid) da campanha, vindo de listar_campanhas. Nunca invente: pergunte."`
	TipoID     string `json:"tipo_id" jsonschema:"Id (uuid) do tipo de material da célula, vindo de listar_overrides. Nunca invente: pergunte."`
	EmissoraID string `json:"emissora_id" jsonschema:"Id (uuid) da emissora da célula, vindo de listar_overrides. Nunca invente: pergunte."`
	Data       string `json:"data" jsonschema:"Dia do ajuste (AAAA-MM-DD, America/Sao_Paulo), vindo de listar_overrides."`
	CampoConfirmacao
}

func planejarApagarAjuste(ctx context.Context, E *Escritas, in entradaApagarOverride) (*celulaDoAjuste, error) {
	c, err := lerCelulaDoAjuste(ctx, E, in.CampanhaID, in.TipoID, in.EmissoraID, in.Data, false)
	if err != nil {
		return nil, err
	}
	if c.atual == nil {
		// A rota responde 204 mesmo sem ajuste; o MCP não diz "feito" quando nada foi feito.
		return nil, errNaoEncontrado(fmt.Sprintf("ajuste do dia de %s × %s em %s (confira com listar_overrides)", c.est.Name, c.tipo.Name, dia(c.dia)))
	}
	return c, nil
}

func escritaApagarOverride(cfg ConfigServidor) Escrita[entradaApagarOverride] {
	return Escrita[entradaApagarOverride]{
		Nome: "apagar_override",
		Descricao: "Apaga o ajuste do dia (override) de UMA célula — campanha × tipo de material × emissora × dia: a célula volta a ser julgada " +
			"pelas regras de distribuição daquele dia. Pegue os ids e a data em listar_overrides; ajuste que não existe é recusado (a rota " +
			"responderia 'feito'). A armadilha: a meta e a faixa voltam ao que as regras dão — se nenhuma regra vale naquele dia, a meta vira 0 e " +
			"toda tocada do dia passa a ser bonus. A remoção recategoriza a célula-dia AGORA (síncrona): o resultado traz o plano do dia " +
			"recalculado. Sobe para crítico quando a data já passou (hoje é o de America/Sao_Paulo) OU a célula-dia já tem veiculação aprovada; " +
			"nesse caso nada é feito e a resposta traz o link da tela. Campanha cancelada é recusada. Nunca invente ids: pergunte. Mostre o " +
			"preview e espere a pessoa aprovar antes de confirmar.",
		Nivel: Confirma,
		Escalar: func(ctx context.Context, E *Escritas, in entradaApagarOverride) (Escalada, error) {
			c, err := planejarApagarAjuste(ctx, E, in)
			if err != nil {
				return Escalada{}, err
			}
			return escalaDeAjuste(ctx, E, c)
		},
		Recurso: func(in entradaApagarOverride) (string, *uuid.UUID) { return ajusteDoRecurso(in.CampanhaID) },
		Tela:    func(_ *Escritas, in entradaApagarOverride) *string { return linkDosAjustes(cfg, in.CampanhaID) },
		Preview: func(ctx context.Context, E *Escritas, in entradaApagarOverride) (previa, error) {
			c, err := planejarApagarAjuste(ctx, E, in)
			if err != nil {
				return previa{}, err
			}
			a := c.atual
			depois := "0 (nenhuma regra vale neste dia)"
			if len(c.regras) > 0 {
				depois = fmt.Sprintf("%d (soma de %d regra(s))", c.metaDasRegras(), len(c.regras))
			}
			pv := previa{
				Resumo: fmt.Sprintf("Apagar o ajuste do dia %s em %s × %s × %q (meta %d, faixa %s–%s)", dia(c.dia), c.est.Name, c.tipo.Name, c.camp.Name, a.PlaysExpected, a.TimeStart, a.TimeEnd),
				Mudancas: []mudanca{
					{Campo: "ajuste do dia", De: fmt.Sprintf("meta %d, faixa %s–%s", a.PlaysExpected, a.TimeStart, a.TimeEnd), Para: "removido"},
					{Campo: "meta do dia (inserções)", De: a.PlaysExpected, Para: depois},
				},
				Efeitos: []string{
					"A célula volta a ser julgada pelas regras de distribuição deste dia (meta e faixa delas). Os outros dias e as outras células não mudam.",
					"Ao confirmar, as veiculações aprovadas desta célula-dia são recategorizadas AGORA (síncrono) — in_slot, bonus, out_slot e déficit do dia podem mudar.",
				},
			}
			if a.Reason != nil && *a.Reason != "" {
				pv.Mudancas = append(pv.Mudancas, mudanca{Campo: "motivo registrado", De: *a.Reason, Para: nil})
			}
			if len(c.regras) == 0 {
				pv.Ressalvas = append(pv.Ressalvas, "Nenhuma regra de distribuição vale neste dia: sem o ajuste a meta vira 0 e toda tocada do célula-dia passa a ser bonus (nunca out_slot).")
			}
			return pv, nil
		},
		Executar: func(ctx context.Context, E *Escritas, _ *Principal, in entradaApagarOverride) (feito, error) {
			c, err := planejarApagarAjuste(ctx, E, in)
			if err != nil {
				return feito{}, err
			}
			removido := *c.atual
			if err := E.Acoes.ApagarAjuste(ctx, c.camp.ID, c.tipo.ID, c.est.ID, c.dia); err != nil {
				return feito{}, err
			}
			plano, err := montarPlanoDoDia(ctx, E.Leitura, c.camp, c.est, c.tipo, c.dia)
			if err != nil {
				return feito{}, err
			}
			tipos, emissoras := c.nomes()
			camp := c.camp
			return feito{
				Resultado: saidaApagarAjuste{Campanha: refDe(camp.ID, camp.Name), Apagado: true,
					AjusteRemovido: montarAjusteDTO(removido, tipos, emissoras), Plano: plano},
				Recurso: "distribution_override", RecursoID: &camp.ID,
				Ressalvas: []string{"A recategorização da célula-dia roda junto da remoção: o plano do dia abaixo já traz as categorias recalculadas."},
			}, nil
		},
	}
}
