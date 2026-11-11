package mcp

import (
	"context"
	"errors"
	"fmt"
	"math/bits"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"radiocheck/internal/acoes"
	"radiocheck/internal/catalog"
)

// programar_campanha (2026-11-11): a campanha nova INTEIRA numa chamada — os dados
// dela, as regras de distribuição e os preços —, com UM preview e UMA aprovação, e
// gravada numa transação só (catalog.ProgramacaoDeCampanha).
//
// Por que existe: programando pelo Gemini, cada regra era uma chamada com preview e
// "aprovo" próprios, e o conector do Google caía no meio (os 503 nunca chegaram à
// API — log do chi na VM, 2026-11-10): uma campanha de 6 regras levou ~1 h e ficou
// pela metade várias vezes. Aqui o modelo manda tudo, o servidor confere os totais
// (inserções e valor previsto por emissora) e grava tudo ou nada.
//
// Nível: confirma; sobe para crítico com o início hoje ou no passado — a mesma
// escalada de criar_campanha (a campanha seria ativada em até 60 s). Regras e
// preços não escalam por conta própria: numa campanha que ainda não existe não há
// veiculação, déficit nem investido que alguém já tenha visto.

func escritasProgramacao(cfg ConfigServidor) []escritaRegistravel {
	return []escritaRegistravel{escritaProgramarCampanha(cfg)}
}

const (
	// tetoRegrasPorProgramacao: regras numa programação (a maior campanha de produção tem 49).
	tetoRegrasPorProgramacao = 150
	// tetoConferenciaNoPreview: quantas emissoras a conferência do preview lista.
	tetoConferenciaNoPreview = 100
	// tetoSobreposicoesNoPreview: quantos pares de regras sobrepostas o preview nomeia.
	tetoSobreposicoesNoPreview = 10
)

type linhaRegraProgramacao struct {
	Nome            string   `json:"nome,omitempty" jsonschema:"Rótulo curto da regra (até 60 caracteres), ex.: PI 6966 - Novembro."`
	TipoID          string   `json:"tipo_id" jsonschema:"Id (uuid) do tipo de material que a regra cobre, vindo de listar_tipos_material. Nunca invente: pergunte."`
	EmissoraIDs     []string `json:"emissora_ids" jsonschema:"Emissoras em que a regra vale (pelo menos uma; todas precisam estar em emissora_ids da campanha)."`
	Inicio          string   `json:"inicio" jsonschema:"Primeiro dia de vigência da regra (AAAA-MM-DD), dentro do período da campanha."`
	Fim             string   `json:"fim" jsonschema:"Último dia de vigência (AAAA-MM-DD), igual ou depois do início e dentro do período da campanha."`
	Dias            []string `json:"dias" jsonschema:"Dias da semana em que a regra vale, pelo menos um: dom, seg, ter, qua, qui, sex, sab."`
	InicioHorario   string   `json:"inicio_horario" jsonschema:"Início da faixa horária (HH:MM, 00:00 a 23:59)."`
	FimHorario      string   `json:"fim_horario" jsonschema:"Fim da faixa horária (HH:MM), maior que o início; não atravessa a meia-noite."`
	InsercoesPorDia int      `json:"insercoes_por_dia" jsonschema:"Inserções por dia por emissora, de 1 a 100 (a meta N do dia)."`
}

type entradaProgramarCampanha struct {
	ClienteID   string                  `json:"cliente_id" jsonschema:"Id (uuid) do cliente dono da campanha, vindo de listar_clientes. Nunca invente: pergunte."`
	Nome        string                  `json:"nome" jsonschema:"Nome da campanha, até 200 caracteres."`
	Inicio      string                  `json:"inicio" jsonschema:"Primeiro dia da vigência (AAAA-MM-DD). Início hoje ou no passado sobe para crítico."`
	Fim         string                  `json:"fim" jsonschema:"Último dia da vigência (AAAA-MM-DD), igual ou depois do início."`
	EmissoraIDs []string                `json:"emissora_ids" jsonschema:"Emissoras-alvo da campanha (1 a 500 ids de buscar_emissoras). Nunca invente ids: pergunte."`
	CodigoHub   string                  `json:"codigo_hub,omitempty" jsonschema:"Código da campanha no hub (EH-XXXXXX). Opcional. Nunca invente: é conferido contra o hub."`
	Regras      []linhaRegraProgramacao `json:"regras" jsonschema:"As regras de distribuição (1 a 150). Cada uma vale por tipo de material; regras do mesmo tipo que valem no mesmo dia e emissora SOMAM a meta."`
	Precos      []linhaPrecoEmissora    `json:"precos,omitempty" jsonschema:"Opcional: o preço de cada emissora (até 300), no mesmo formato de editar_precos_campanha. Emissora sem preço fica fora do investido."`
	CampoConfirmacao
}

// regraProgramada é uma regra já conferida, com o total de inserções que ela gera por emissora.
type regraProgramada struct {
	pl          *planoDeRegra
	porEmissora int // inserções que a regra gera em CADA emissora dela (dias cobertos × N)
	dias        int // dias que a regra cobre
}

type planoDeProgramacao struct {
	base   *planoDeCriacao
	camp   *catalog.Campaign // a campanha como será criada (sem id)
	regras []regraProgramada
	precos []catalog.EntradaPreco
	tipos  map[uuid.UUID]string
}

// comPrefixo põe "regras[i] (nome): " na mensagem de uma recusa de uma regra do lote.
func comPrefixo(err error, prefixo string) error {
	var r *acoes.Recusa
	if errors.As(err, &r) {
		return &acoes.Recusa{Codigo: r.Codigo, Mensagem: prefixo + r.Mensagem, Causa: r.Causa}
	}
	return err
}

// diasCobertos conta os dias de [ini, fim] cujo dia da semana a máscara cobre, em forma fechada
// (semanas cheias × dias da máscara + o resto, no máximo 6 dias): a vigência aceita vai até 100
// anos, e o preview roda isto para cada par de regras.
func diasCobertos(ini, fim time.Time, mascara int16) int {
	if fim.Before(ini) {
		return 0
	}
	total := int(fim.Sub(ini).Hours()/24+0.5) + 1 // datas de calendário (meia-noite UTC): sem horário de verão
	n := (total / 7) * bits.OnesCount16(uint16(mascara)&0x7f)
	for i, d := 0, ini.AddDate(0, 0, (total/7)*7); i < total%7; i, d = i+1, d.AddDate(0, 0, 1) {
		if (1<<int(d.Weekday()))&int(mascara) != 0 {
			n++
		}
	}
	return n
}

func planejarProgramacao(ctx context.Context, E *Escritas, in entradaProgramarCampanha) (*planoDeProgramacao, error) {
	base, err := planejarCriacaoDeCampanha(ctx, E, entradaCriarCampanha{
		ClienteID: in.ClienteID, Nome: in.Nome, Inicio: in.Inicio, Fim: in.Fim,
		EmissoraIDs: in.EmissoraIDs, CodigoHub: in.CodigoHub,
	})
	if err != nil {
		return nil, err
	}
	if len(base.emissoras) == 0 {
		return nil, errEntrada("emissora_ids: informe as emissoras-alvo da campanha (ao menos uma); sem elas as regras não têm onde valer")
	}
	if len(in.Regras) == 0 {
		return nil, errEntrada("regras: informe ao menos uma regra de distribuição (para criar só a campanha, use criar_campanha)")
	}
	if len(in.Regras) > tetoRegrasPorProgramacao {
		return nil, errEntrada(fmt.Sprintf("regras: no máximo %d regras por programação", tetoRegrasPorProgramacao))
	}
	// A campanha como será criada: conferirRegra e validarLinhasDePreco só leem
	// período, emissoras-alvo, cliente e nome.
	camp := &catalog.Campaign{
		ClientID: base.cliente.ID, Name: base.nome, StartDate: base.inicio, EndDate: base.fim,
		TargetStations: base.emissoras, Status: "programada",
	}
	pl := &planoDeProgramacao{base: base, camp: camp}

	for i, l := range in.Regras {
		prefixo := fmt.Sprintf("regras[%d]", i)
		if n := aparar(l.Nome); n != "" {
			prefixo += fmt.Sprintf(" (%q)", n)
		}
		prefixo += ": "
		r, err := planejarRegraDaProgramacao(ctx, E, camp, l)
		if err != nil {
			return nil, comPrefixo(err, prefixo)
		}
		pl.regras = append(pl.regras, *r)
	}
	if pl.tipos, err = nomesTipos(ctx, E.Leitura); err != nil {
		return nil, err
	}
	if len(in.Precos) > 0 {
		precos, _, _, err := validarLinhasDePreco(ctx, E, camp, in.Precos)
		if err != nil {
			return nil, comPrefixo(err, "precos: ")
		}
		pl.precos = precos
	}
	return pl, nil
}

func planejarRegraDaProgramacao(ctx context.Context, E *Escritas, camp *catalog.Campaign, l linhaRegraProgramacao) (*regraProgramada, error) {
	tid, err := uuidDaEntrada("tipo_id", l.TipoID)
	if err != nil {
		return nil, err
	}
	emissoras, err := listaDeEmissoras("emissora_ids", l.EmissoraIDs)
	if err != nil {
		return nil, err
	}
	if len(emissoras) == 0 {
		return nil, errEntrada("emissora_ids: informe ao menos uma emissora da campanha (uma regra sem emissora não vale para nenhuma)")
	}
	ini, fim, err := periodoDaEntrada(l.Inicio, l.Fim)
	if err != nil {
		return nil, err
	}
	mascara, err := mascaraDosDias(l.Dias)
	if err != nil {
		return nil, err
	}
	hIni, hFim, err := faixaDaEntrada(l.InicioHorario, l.FimHorario)
	if err != nil {
		return nil, err
	}
	if l.InsercoesPorDia < 1 || l.InsercoesPorDia > 100 {
		return nil, errEntrada("insercoes_por_dia: use um número de 1 a 100")
	}
	nome := aparar(l.Nome)
	if err := limiteDeRunas("nome", nome, tetoNomeRegra); err != nil {
		return nil, err
	}
	pl, err := conferirRegra(ctx, E, camp, nil, estadoDaRegra{
		tipo: tid, emissoras: emissoras, nome: nome, inicio: ini, fim: fim,
		mascara: mascara, horaIni: hIni, horaFim: hFim, porDia: int16(l.InsercoesPorDia),
	}, limitesEnviados{inicio: true, fim: true})
	if err != nil {
		return nil, err
	}
	dias := diasCobertos(ini, fim, mascara)
	if dias == 0 {
		return nil, errEntrada(fmt.Sprintf("a vigência %s a %s não tem nenhum dia de %s: a regra nunca valeria", dia(ini), dia(fim), resumoDeDias(mascara)))
	}
	return &regraProgramada{pl: pl, dias: dias, porEmissora: dias * l.InsercoesPorDia}, nil
}

// ── Conferência ─────────────────────────────────────────────────────────

// conferenciaEmissora: o que a programação dá a uma emissora (inserções por tipo e valor previsto).
type conferenciaEmissora struct {
	emissora       uuid.UUID
	porTipo        map[uuid.UUID]int
	preco          *catalog.EntradaPreco // nil = sem preço
	valor          float64               // previsto: Σ inserções × unitário (por inserção) ou o consolidado
	semPrecoDoTipo []uuid.UUID           // tipos com inserção e sem valor unitário (por inserção)
}

func (pl *planoDeProgramacao) conferencia() []conferenciaEmissora {
	precoDe := make(map[uuid.UUID]*catalog.EntradaPreco, len(pl.precos))
	for i := range pl.precos {
		precoDe[pl.precos[i].StationID] = &pl.precos[i]
	}
	out := make([]conferenciaEmissora, 0, len(pl.camp.TargetStations))
	for _, st := range pl.camp.TargetStations {
		c := conferenciaEmissora{emissora: st, porTipo: map[uuid.UUID]int{}, preco: precoDe[st]}
		for _, r := range pl.regras {
			if slices.Contains(r.pl.nova.emissoras, st) {
				c.porTipo[r.pl.nova.tipo] += r.porEmissora
			}
		}
		if c.preco != nil {
			if c.preco.Mode == catalog.PricingModePerInsertion {
				unit := make(map[uuid.UUID]float64, len(c.preco.PerType))
				for _, t := range c.preco.PerType {
					unit[t.TypeID] = t.UnitValue
				}
				for _, tipo := range tiposOrdenados(c.porTipo, pl.tipos) {
					v, ok := unit[tipo]
					if !ok {
						c.semPrecoDoTipo = append(c.semPrecoDoTipo, tipo)
						continue
					}
					c.valor += float64(c.porTipo[tipo]) * v
				}
			} else if c.preco.ConsolidatedValue != nil {
				c.valor = *c.preco.ConsolidatedValue
			}
		}
		out = append(out, c)
	}
	return out
}

// tiposOrdenados: os tipos do mapa em ordem de nome (o preview precisa ser determinístico).
func tiposOrdenados(m map[uuid.UUID]int, nomes map[uuid.UUID]string) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if nomes[ids[i]] != nomes[ids[j]] {
			return nomes[ids[i]] < nomes[ids[j]]
		}
		return ids[i].String() < ids[j].String()
	})
	return ids
}

func (pl *planoDeProgramacao) nomeDoTipo(id uuid.UUID) string {
	if n := pl.tipos[id]; n != "" {
		return n
	}
	return id.String()
}

func (pl *planoDeProgramacao) nomeDaEmissora(id uuid.UUID) string {
	if n := pl.base.nomes[id]; n != "" {
		return n
	}
	return id.String()
}

// textoDaConferencia: "Spot 30\": 30 inserções × R$ 56,25 = R$ 1.687,50" (por inserção),
// "Spot 30\": 30 inserções; consolidado R$ 14.850,00" ou "Spot 30\": 30 inserções; sem preço".
func (pl *planoDeProgramacao) textoDaConferencia(c conferenciaEmissora) string {
	if len(c.porTipo) == 0 {
		return "nenhuma regra: 0 inserções; " + pl.textoDoPreco(c)
	}
	unit := map[uuid.UUID]float64{}
	porInsercao := c.preco != nil && c.preco.Mode == catalog.PricingModePerInsertion
	if porInsercao {
		for _, t := range c.preco.PerType {
			unit[t.TypeID] = t.UnitValue
		}
	}
	partes := []string{}
	for _, tipo := range tiposOrdenados(c.porTipo, pl.tipos) {
		n := c.porTipo[tipo]
		if v, ok := unit[tipo]; ok {
			partes = append(partes, fmt.Sprintf("%s: %d inserções × %s = %s", pl.nomeDoTipo(tipo), n, reais(v), reais(float64(n)*v)))
		} else {
			partes = append(partes, fmt.Sprintf("%s: %d inserções", pl.nomeDoTipo(tipo), n))
		}
	}
	s := strings.Join(partes, "; ")
	switch {
	case c.preco == nil:
		s += "; sem preço"
	case !porInsercao:
		s += "; " + pl.textoDoPreco(c)
	case len(c.porTipo) > 1:
		s += "; total " + reais(c.valor)
	}
	return s
}

func (pl *planoDeProgramacao) textoDoPreco(c conferenciaEmissora) string {
	if c.preco == nil {
		return "sem preço"
	}
	return descricaoDoPreco(c.preco.Mode, c.preco.ConsolidatedValue, c.preco.PerType, pl.tipos)
}

// sobreposicoes: pares de regras do mesmo tipo que valem no mesmo dia e na mesma emissora — a meta
// desses dias é a SOMA das duas, o erro mais comum de quem programa em lote (regra repetida).
func (pl *planoDeProgramacao) sobreposicoes() []string {
	var out []string
	for i := 0; i < len(pl.regras); i++ {
		for j := i + 1; j < len(pl.regras); j++ {
			a, b := pl.regras[i].pl.nova, pl.regras[j].pl.nova
			if a.tipo != b.tipo {
				continue
			}
			comuns := 0
			for _, e := range a.emissoras {
				if slices.Contains(b.emissoras, e) {
					comuns++
				}
			}
			if comuns == 0 {
				continue
			}
			ini, fim := a.inicio, a.fim
			if b.inicio.After(ini) {
				ini = b.inicio
			}
			if b.fim.Before(fim) {
				fim = b.fim
			}
			if ini.After(fim) {
				continue
			}
			dias := diasCobertos(ini, fim, a.mascara&b.mascara)
			if dias == 0 {
				continue
			}
			out = append(out, fmt.Sprintf("regras[%d] e regras[%d] (%s) valem juntas em %d dia(s) em %d emissora(s): a meta desses dias é a SOMA (%d + %d = %d por dia)",
				i, j, pl.nomeDoTipo(a.tipo), dias, comuns, a.porDia, b.porDia, a.porDia+b.porDia))
		}
	}
	return out
}

// ── A tool ──────────────────────────────────────────────────────────────

type regraCriadaDTO struct {
	ID   string `json:"id"`
	Nome string `json:"nome"`
}

type totaisProgramacao struct {
	Insercoes     int     `json:"insercoes"`
	ValorPrevisto float64 `json:"valor_previsto"`
}

type saidaProgramarCampanha struct {
	Campanha       any               `json:"campanha"`
	Regras         []regraCriadaDTO  `json:"regras"`
	PrecosGravados int               `json:"precos_gravados"`
	Totais         totaisProgramacao `json:"totais"`
}

func (pl *planoDeProgramacao) totais(conf []conferenciaEmissora) totaisProgramacao {
	var t totaisProgramacao
	for _, c := range conf {
		for _, n := range c.porTipo {
			t.Insercoes += n
		}
		t.ValorPrevisto += c.valor
	}
	return t
}

func escritaProgramarCampanha(cfg ConfigServidor) Escrita[entradaProgramarCampanha] {
	return Escrita[entradaProgramarCampanha]{
		Nome: "programar_campanha",
		Descricao: "Programa uma campanha NOVA inteira numa chamada só: os dados da campanha (cliente, nome, vigência, emissoras-alvo, código do " +
			"hub), TODAS as regras de distribuição e, se houver, os preços das emissoras. Um preview só, uma aprovação só, e a gravação é numa " +
			"transação: ou entra tudo, ou nada (uma queda no meio não deixa a campanha pela metade). O preview traz a conferência que o servidor " +
			"calcula: inserções por emissora e tipo (dias cobertos × inserções por dia, somando as regras) e o valor previsto (inserções × valor " +
			"unitário, ou o consolidado) — compare com o documento antes de pedir a aprovação. É o caminho preferido para criar campanha com " +
			"programação; criar_campanha + criar_regra_distribuicao + editar_precos_campanha seguem para ajustes depois. As armadilhas: (1) campanha " +
			"duplicada — o preview avisa se o cliente já tem uma com o mesmo nome; (2) regras do mesmo tipo que valem no mesmo dia e emissora SOMAM " +
			"a meta — o preview lista as sobreposições; (3) emissora sem preço fica fora do investido; (4) nasce sem materiais: vincule-os depois " +
			"(vincular_material). Sobe para crítico quando o início é hoje ou já passou (a campanha seria ativada em até 60 s); nesse caso vira um " +
			"pedido que uma pessoa aprova em /pendentes. Nunca invente ids: pergunte. Mostre o preview e espere a pessoa aprovar antes de confirmar.",
		Nivel: Confirma,
		Escalar: func(_ context.Context, E *Escritas, in entradaProgramarCampanha) (Escalada, error) {
			ini, _, err := periodoDaEntrada(in.Inicio, in.Fim)
			if err != nil {
				return Escalada{}, err
			}
			hoje := hojeSP(E.agora())
			if ini.After(hoje) {
				return Escalada{}, nil
			}
			return Escalada{Nivel: Critico, Motivo: fmt.Sprintf(
				"O início (%s) é hoje ou já passou (hoje é %s em America/Sao_Paulo): o agendador ativa a campanha em até 60 s, os workers das "+
					"emissoras sobem e o cliente passa a ver veiculações — é o mesmo que iniciar a campanha.", dia(ini), dia(hoje))}, nil
		},
		Recurso: func(entradaProgramarCampanha) (string, *uuid.UUID) { return "campaign", nil },
		Tela:    func(*Escritas, entradaProgramarCampanha) *string { return cfg.link("/campaigns/new") },
		Preview: func(ctx context.Context, E *Escritas, in entradaProgramarCampanha) (previa, error) {
			pl, err := planejarProgramacao(ctx, E, in)
			if err != nil {
				return previa{}, err
			}
			return previaDaProgramacao(ctx, E, pl)
		},
		Executar: func(ctx context.Context, E *Escritas, _ *Principal, in entradaProgramarCampanha) (feito, error) {
			pl, err := planejarProgramacao(ctx, E, in)
			if err != nil {
				return feito{}, err
			}
			regras := make([]acoes.EntradaRegra, len(pl.regras))
			for i, r := range pl.regras {
				regras[i] = r.pl.nova.paraEntrada()
			}
			feita, err := E.Acoes.ProgramarCampanha(ctx, acoes.ProgramarCampanhaEntrada{
				Campanha: acoes.CriarCampanhaEntrada{
					ClienteID: pl.base.cliente.ID, Nome: pl.base.nome, Inicio: pl.base.inicio, Fim: pl.base.fim,
					EmissoraIDs: pl.base.emissoras, CodigoHub: pl.base.codigo,
				},
				Regras: regras,
				Precos: pl.precos,
			})
			if err != nil {
				return feito{}, err
			}
			c := feita.Campanha
			saida := saidaProgramarCampanha{
				Regras:         make([]regraCriadaDTO, len(feita.Regras)),
				PrecosGravados: feita.Precos,
				Totais:         pl.totais(pl.conferencia()),
			}
			for i, r := range feita.Regras {
				saida.Regras[i] = regraCriadaDTO{ID: r.ID.String(), Nome: r.Name}
			}
			ressalvas := []string{ressalvaCampanhaProgramada,
				"Nasce sem materiais: vincule os áudios com vincular_material (sem material do tipo, a regra não tem o que detectar)."}
			if hubConfigurado(E) {
				ressalvas = append(ressalvas, "O aviso ao hub (campanha.upsert) segue em segundo plano; se o hub estiver fora do ar a reemissão periódica leva a campanha quando ele voltar.")
			}
			// Tudo já está gravado: se só a releitura da campanha falhar, a escrita não vira erro (o
			// modelo programaria de novo e duplicaria a campanha).
			if completa, rerr := lerCampanhaCompleta(ctx, cfg, E.Leitura, c); rerr == nil {
				saida.Campanha = completa
			} else {
				cfg.log().Error("mcp: releitura após programar_campanha falhou")
				saida.Campanha = saidaCampanhaSemLeitura{Campanha: refDe(c.ID, c.Name)}
				ressalvas = append(ressalvas, "A programação foi gravada, mas não consegui reler a campanha agora: confira o estado com obter_campanha (campanha_id "+c.ID.String()+").")
			}
			return feito{Resultado: saida, Recurso: "campaign", RecursoID: &c.ID, Ressalvas: ressalvas}, nil
		},
	}
}

func previaDaProgramacao(ctx context.Context, E *Escritas, pl *planoDeProgramacao) (previa, error) {
	b := pl.base
	var canonico string
	if b.codigo != "" {
		var err error
		if canonico, err = E.Acoes.ConferirCodigoHub(ctx, b.cliente.ID, b.codigo); err != nil {
			return previa{}, err
		}
	}
	conf := pl.conferencia()
	tot := pl.totais(conf)

	mud := []mudanca{
		{Campo: "cliente", Para: b.cliente.Name},
		{Campo: "nome", Para: b.nome},
		{Campo: "inicio", Para: dia(b.inicio)},
		{Campo: "fim", Para: dia(b.fim)},
		{Campo: "emissoras", Para: resumoDeEmissoras(b.emissoras, b.nomes)},
	}
	if canonico != "" {
		mud = append(mud, mudanca{Campo: "codigo_hub", Para: canonico})
	}
	for i, r := range pl.regras {
		n := r.pl.nova
		titulo := n.nome
		if titulo == "" {
			titulo = "sem nome"
		}
		mud = append(mud, mudanca{
			Campo: fmt.Sprintf("regras[%d] %q", i, titulo),
			Para: fmt.Sprintf("%d/dia do tipo %s, %s, %s–%s, de %s a %s (%d dias) em %s → %d inserções por emissora",
				n.porDia, r.pl.tipo.Name, resumoDeDias(n.mascara), n.horaIni, n.horaFim, dia(n.inicio), dia(n.fim), r.dias,
				resumoDeEmissoras(n.emissoras, r.pl.emissoras), r.porEmissora),
		})
	}
	var ressalvas []string
	for i, c := range conf {
		if i == tetoConferenciaNoPreview {
			ressalvas = append(ressalvas, fmt.Sprintf("a conferência mostra %d de %d emissoras", tetoConferenciaNoPreview, len(conf)))
			break
		}
		mud = append(mud, mudanca{Campo: "conferência: " + pl.nomeDaEmissora(c.emissora), Para: pl.textoDaConferencia(c)})
	}
	mud = append(mud, mudanca{Campo: "conferência: total", Para: fmt.Sprintf("%d inserções; valor previsto %s", tot.Insercoes, reais(tot.ValorPrevisto))})

	pv := previa{
		Resumo: fmt.Sprintf("Programar a campanha %q do cliente %q (%s a %s): %d emissora(s), %d regra(s), preço em %d emissora(s); %d inserções, valor previsto %s",
			b.nome, b.cliente.Name, dia(b.inicio), dia(b.fim), len(b.emissoras), len(pl.regras), len(pl.precos), tot.Insercoes, reais(tot.ValorPrevisto)),
		Mudancas: mud,
	}

	hoje := hojeSP(E.agora())
	if b.inicio.After(hoje) {
		pv.Efeitos = append(pv.Efeitos, fmt.Sprintf("A campanha nasce 'programada'. O agendador (a cada 60 s) a ativa em %s (America/Sao_Paulo): só então os workers das emissoras sobem e as veiculações passam a ser atribuídas a ela.", dia(b.inicio)))
	} else {
		pv.Efeitos = append(pv.Efeitos, fmt.Sprintf("O início (%s) é hoje ou já passou: o agendador ativa a campanha em até 60 s, os workers das emissoras sobem e o cliente passa a ver veiculações.", dia(b.inicio)))
	}
	pv.Efeitos = append(pv.Efeitos,
		"Campanha, regras e preços são gravados numa transação só: ou entra tudo, ou nada.",
		"A meta do dia de cada emissora e tipo é a soma das regras que valem no dia; a conferência acima é essa soma sobre a vigência.",
		"Nasce sem materiais: vincule os áudios (vincular_material) — sem material do tipo, a regra não tem o que detectar.")
	if hubConfigurado(E) {
		pv.Efeitos = append(pv.Efeitos, "A campanha é enviada ao hub (campanha.upsert) em segundo plano.")
		if canonico == "" {
			ressalvas = append(ressalvas, "Sem codigo_hub: o hub IGNORA o aviso da campanha (ela não se liga a nenhuma proposta lá); a tela exige o código na criação — informe-o se a pessoa tiver.")
		}
	}
	if !b.cliente.IsActive {
		ressalvas = append(ressalvas, fmt.Sprintf("O cliente %q está DESATIVADO (a empresa foi suspensa): a tela e a rota deixam criar campanha mesmo assim, mas confirme com a pessoa.", b.cliente.Name))
	}
	existentes, err := E.Leitura.Campanhas.ListFiltered(ctx, nil, []uuid.UUID{b.cliente.ID})
	if err != nil {
		return previa{}, err
	}
	for _, c := range existentes {
		if strings.EqualFold(c.Name, b.nome) {
			ressalvas = append(ressalvas, fmt.Sprintf(
				"Este cliente já tem uma campanha com este nome: %q (id %s, %s, %s a %s). Se for a mesma, não programe outra.",
				c.Name, c.ID, c.Status, dia(c.StartDate), dia(c.EndDate)))
			break
		}
	}
	if sob := pl.sobreposicoes(); len(sob) > 0 {
		n := min(len(sob), tetoSobreposicoesNoPreview)
		txt := "Regras que se SOMAM (confira se não é regra repetida): " + strings.Join(sob[:n], "; ")
		if len(sob) > n {
			txt += fmt.Sprintf("; e mais %d", len(sob)-n)
		}
		ressalvas = append(ressalvas, txt+".")
	}
	var semRegra, semPreco []uuid.UUID
	var tipoSemPreco []string
	for _, c := range conf {
		if len(c.porTipo) == 0 {
			semRegra = append(semRegra, c.emissora)
		}
		if c.preco == nil {
			semPreco = append(semPreco, c.emissora)
		}
		for _, t := range c.semPrecoDoTipo {
			tipoSemPreco = append(tipoSemPreco, fmt.Sprintf("%s em %s", pl.nomeDoTipo(t), pl.nomeDaEmissora(c.emissora)))
		}
	}
	if len(semRegra) > 0 {
		ressalvas = append(ressalvas, fmt.Sprintf("%d emissora(s)-alvo sem nenhuma regra (meta zero: nada é cobrado delas): %s.", len(semRegra), listaDeNomes(semRegra, b.nomes)))
	}
	if len(pl.precos) == 0 {
		ressalvas = append(ressalvas, "Sem preços: a campanha fica sem investido até editar_precos_campanha.")
	} else if len(semPreco) > 0 {
		ressalvas = append(ressalvas, fmt.Sprintf("%d emissora(s)-alvo sem preço (fora do investido): %s.", len(semPreco), listaDeNomes(semPreco, b.nomes)))
	}
	if len(tipoSemPreco) > 0 {
		ressalvas = append(ressalvas, fmt.Sprintf("Tipo com regra mas sem valor unitário no preço por inserção (essas inserções ficam fora do investido): %s.", listaCortada(tipoSemPreco)))
	}
	for _, r := range pl.regras {
		ressalvas = append(ressalvas, r.pl.ressalvas...)
	}
	pv.Ressalvas = ressalvas
	return pv, nil
}
