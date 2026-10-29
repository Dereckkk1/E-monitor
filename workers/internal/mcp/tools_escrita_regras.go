package mcp

import (
	"context"
	"errors"
	"fmt"
	"regexp"
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

// Escritas das regras de distribuição (Lote C, Task 16): criar, editar e apagar.
// Uma regra é a PROGRAMAÇÃO da campanha (X inserções por dia do tipo T, nas
// emissoras E, na faixa H, nos dias D da vigência V): ela gera a meta que a grade
// cobra, e o gatilho (acoes.CriarRegra/EditarRegra/ApagarRegra) recategoriza o
// período INTEIRO da campanha. Por isso as três escalam para crítico (F3-4)
// quando a campanha já tem veiculação do(s) tipo(s) que o recat varre, ou quando
// a vigência (recortada ao período da campanha) inclui dia que já passou — mudar a
// meta de um dia passado muda déficit e "esperado" que o cliente já viu, mesmo
// sem tocada.
//
// O que a TELA exige e o MCP espelha (RuleSidePanel.jsx / DistributionStep.jsx):
//   - emissoras: só as da campanha (ruleEditorStations = target_stations);
//   - material por escopo: só materiais DO TIPO e VINCULADOS à campanha
//     (materialsByType, DistributionStep.jsx:375);
//   - datas: dentro do período da campanha (min/max do <input type="date">);
//   - horário: HH:MM de 00:00 a 23:59 (<input type="time">) e fim > início. "24:00"
//     não é válido: o categorizador lê o horário com time.Parse("15:04:05") e uma
//     hora 24 vira 00:00 em silêncio (catalog/detections.go, loadRulesForCell);
//   - nome: até 60 caracteres (maxLength=60).
//
// A tela não valida o que as rotas HTTP aceitam (datas fora da campanha, máscara 0,
// 24:00): o MCP é mais estrito de propósito (F3-8).

func registrarEscritasRegras(s *sdk.Server, cfg ConfigServidor, p *Principal) {
	ferramentaEscrita(s, cfg, p, escritaCriarRegra(cfg))
	ferramentaEscrita(s, cfg, p, escritaEditarRegra(cfg))
	ferramentaEscrita(s, cfg, p, escritaApagarRegra(cfg))
}

const (
	// tetoNomeRegra é o maxLength do campo "Nome do conjunto" da tela.
	tetoNomeRegra = 60
	// tetoMateriaisPorRegra: o teto de materiais (escopo carve-out) numa chamada.
	tetoMateriaisPorRegra = 200

	ressalvaRecatDaRegra = "A recategorização das veiculações da campanha roda em segundo plano e leva até 30 s: a grade e o plano do dia podem mostrar a categoria antiga nesse intervalo."
)

// ── Dias da semana ↔ máscara ────────────────────────────────────────────

// mascaraDosDias converte os nomes dos dias (dom, seg, ter, qua, qui, sex, sab) na
// weekday_mask da regra: bit 0 = domingo … bit 6 = sábado (a ordem de
// RuleSidePanel.jsx — WEEKDAY_NAMES + `1 << i` —, de EXTRACT(DOW) e do
// categorizer; seg a sex = 62, fim de semana = 65, todos = 127). O modelo erra
// bitmask, então a tool só fala em nomes. Repetido é aceito; vazio não: a regra sem
// dia nenhum nunca vale (a rota aceita máscara 0, o MCP não).
func mascaraDosDias(dias []string) (int16, error) {
	if len(dias) == 0 {
		return 0, errEntrada("dias: informe ao menos um dia da semana (dom, seg, ter, qua, qui, sex, sab); uma regra sem dia nenhum nunca vale")
	}
	var m int16
	for _, d := range dias {
		nome := strings.ReplaceAll(strings.ToLower(aparar(d)), "sáb", "sab")
		i := slices.Index(nomesDosDias[:], nome)
		if i < 0 {
			return 0, errEntrada(fmt.Sprintf("dia %q inválido: use dom, seg, ter, qua, qui, sex ou sab", d))
		}
		m |= 1 << i
	}
	return m, nil
}

// ── Faixa horária ───────────────────────────────────────────────────────

// formaDoHorario: HH:MM de 00:00 a 23:59 — o que o <input type="time"> da tela
// produz e o regex que a rota de ajustes sempre exigiu.
var formaDoHorario = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

func horarioDaEntrada(campo, valor string) (string, error) {
	v := aparar(valor)
	if !formaDoHorario.MatchString(v) {
		return "", errEntrada(fmt.Sprintf(
			"%s inválido: use HH:MM de 00:00 a 23:59 (ex.: 08:30). 24:00 não existe e a faixa não atravessa a meia-noite", campo))
	}
	return v, nil
}

// faixaDaEntrada lê início e fim e exige fim > início (texto compara certo: HH:MM com zero à esquerda).
func faixaDaEntrada(ini, fim string) (string, string, error) {
	i, err := horarioDaEntrada("inicio_horario", ini)
	if err != nil {
		return "", "", err
	}
	f, err := horarioDaEntrada("fim_horario", fim)
	if err != nil {
		return "", "", err
	}
	if f <= i {
		return "", "", errEntrada(fmt.Sprintf("o fim da faixa (%s) precisa ser maior que o início (%s); a faixa não atravessa a meia-noite", f, i))
	}
	return i, f, nil
}

// ── Estado e plano da regra ─────────────────────────────────────────────

// estadoDaRegra é a regra como ficará (criação, ou edição já mesclada).
type estadoDaRegra struct {
	tipo             uuid.UUID
	emissoras        []uuid.UUID
	materiais        []uuid.UUID
	nome             string
	inicio, fim      time.Time
	mascara          int16
	horaIni, horaFim string
	porDia           int16
}

func estadoDe(r *catalog.DistributionRule) estadoDaRegra {
	return estadoDaRegra{
		tipo: r.TypeID, emissoras: slices.Clone(r.StationIDs), materiais: slices.Clone(r.MaterialIDs), nome: r.Name,
		inicio: r.StartDate, fim: r.EndDate, mascara: r.WeekdayMask, horaIni: r.TimeStart, horaFim: r.TimeEnd, porDia: r.PlaysPerDay,
	}
}

func (e estadoDaRegra) paraEntrada() acoes.EntradaRegra {
	emissoras := slices.Clone(e.emissoras)
	if emissoras == nil {
		emissoras = []uuid.UUID{} // as ações recusam nil; vazio é o que uma regra legada sem emissora tem
	}
	return acoes.EntradaRegra{
		TipoID: e.tipo, EmissoraIDs: emissoras, MaterialIDs: slices.Clone(e.materiais), Nome: e.nome,
		Inicio: e.inicio, Fim: e.fim, MascaraDias: e.mascara, HoraInicio: e.horaIni, HoraFim: e.horaFim, InsercoesPorDia: e.porDia,
	}
}

// planoDeRegra é a regra validada contra a campanha, com os nomes para o preview.
type planoDeRegra struct {
	camp      *catalog.Campaign
	atual     *catalog.DistributionRule // nil na criação
	nova      estadoDaRegra
	tipo      *catalog.MaterialType
	tipos     map[uuid.UUID]string
	emissoras map[uuid.UUID]string // as da regra nova e as da atual
	materiais map[uuid.UUID]string
	mudancas  []mudanca
	ressalvas []string
}

// conferirRegra valida o estado final da regra contra o banco: tipo, vigência,
// emissoras e materiais. Na EDIÇÃO, o que já estava na regra e não mudou é mantido
// mesmo que hoje esteja fora da campanha (a tela faz igual: o submit preserva o
// escopo gravado), com uma ressalva; só o que a chamada ACRESCENTA ou muda é conferido.
func conferirRegra(ctx context.Context, E *Escritas, camp *catalog.Campaign, atual *catalog.DistributionRule, n estadoDaRegra, enviou limitesEnviados) (*planoDeRegra, error) {
	L := E.Leitura
	tipo, err := L.TiposMaterial.Get(ctx, n.tipo)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNaoEncontrado("tipo de material")
	}
	if err != nil {
		return nil, err
	}
	tipos, err := nomesTipos(ctx, L)
	if err != nil {
		return nil, err
	}
	pl := &planoDeRegra{camp: camp, atual: atual, nova: n, tipo: tipo, tipos: tipos}

	// Vigência dentro do período da campanha.
	foraInicio, foraFim := n.inicio.Before(camp.StartDate), n.fim.After(camp.EndDate)
	if foraInicio || foraFim {
		mudou := atual == nil || dia(n.inicio) != dia(atual.StartDate) || dia(n.fim) != dia(atual.EndDate)
		if mudou {
			var quais []string
			if foraInicio {
				quais = append(quais, limiteForaDaCampanha("inicio", n.inicio, "é anterior ao início da campanha", camp.StartDate, enviou.inicio))
			}
			if foraFim {
				quais = append(quais, limiteForaDaCampanha("fim", n.fim, "é posterior ao fim da campanha", camp.EndDate, enviou.fim))
			}
			return nil, errEntrada(fmt.Sprintf(
				"a vigência da regra (%s a %s) sai do período da campanha (%s a %s): %s. A regra só pode valer dentro do período (a tela restringe as datas, e fora dele a grade passaria a cobrar meta em dias sem campanha); para valer fora, altere antes as datas da campanha (editar_campanha)",
				dia(n.inicio), dia(n.fim), dia(camp.StartDate), dia(camp.EndDate), strings.Join(quais, "; ")))
		}
		pl.ressalvas = append(pl.ressalvas, fmt.Sprintf(
			"A vigência desta regra (%s a %s) já ultrapassa o período da campanha (%s a %s); ficou como estava.",
			dia(n.inicio), dia(n.fim), dia(camp.StartDate), dia(camp.EndDate)))
	}

	// Emissoras: as da campanha.
	var foraNovas, foraAntigas []string
	for _, id := range n.emissoras {
		if slices.Contains(camp.TargetStations, id) {
			continue
		}
		if atual != nil && slices.Contains(atual.StationIDs, id) {
			foraAntigas = append(foraAntigas, id.String())
			continue
		}
		foraNovas = append(foraNovas, id.String())
	}
	if len(foraNovas) > 0 {
		return nil, errEntrada(fmt.Sprintf(
			"emissora(s) fora da campanha: %s (a regra só pode usar emissoras da própria campanha; acrescente-as antes com editar_emissoras_campanha)",
			strings.Join(foraNovas, ", ")))
	}
	if len(foraAntigas) > 0 {
		pl.ressalvas = append(pl.ressalvas, fmt.Sprintf(
			"%d emissora(s) da regra já não estão na campanha (%s) e foram mantidas como estavam.", len(foraAntigas), strings.Join(foraAntigas, ", ")))
	}
	todas := slices.Clone(n.emissoras)
	if atual != nil {
		todas = append(todas, atual.StationIDs...)
	}
	if pl.emissoras, err = nomesEmissoras(ctx, L, todas); err != nil {
		return nil, err
	}
	if len(n.emissoras) == 0 {
		pl.ressalvas = append(pl.ressalvas, "A regra não tem nenhuma emissora (regra legada): ela não vale para nenhuma. Acrescente emissoras com adicionar_emissoras ou apague-a com apagar_regra_distribuicao.")
	}

	// Materiais (escopo carve-out): do tipo e vinculados à campanha.
	if pl.materiais, err = nomesMateriais(ctx, L, camp.ClientID); err != nil {
		return nil, err
	}
	if len(n.materiais) > 0 {
		vinculos, err := L.MateriaisCampanha.ListByCampaign(ctx, camp.ID)
		if err != nil {
			return nil, err
		}
		vinculado := make(map[uuid.UUID]bool, len(vinculos))
		for _, v := range vinculos {
			vinculado[v.MaterialID] = true
		}
		herdam := atual != nil && atual.TypeID == n.tipo // com tipo trocado nada é herdado
		var foraDoVinculo []string
		for _, id := range n.materiais {
			if herdam && slices.Contains(atual.MaterialIDs, id) {
				if !vinculado[id] {
					foraDoVinculo = append(foraDoVinculo, id.String())
				}
				continue
			}
			m, err := L.Materiais.Get(ctx, id)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, errNaoEncontrado(fmt.Sprintf("material %s", id))
			}
			if err != nil {
				return nil, err
			}
			pl.materiais[id] = m.Title
			if !vinculado[id] {
				return nil, errEntrada(fmt.Sprintf(
					"o material %q não está vinculado a esta campanha: a regra por material só aceita materiais da campanha (vincule-o antes com vincular_material)", m.Title))
			}
			if m.TypeID == nil || *m.TypeID != n.tipo {
				do := "sem tipo"
				if m.TypeID != nil {
					do = "do tipo " + tipos[*m.TypeID]
				}
				return nil, errEntrada(fmt.Sprintf(
					"o material %q é %s, não do tipo da regra (%s): a regra por material só aceita materiais do tipo dela", m.Title, do, tipo.Name))
			}
		}
		if len(foraDoVinculo) > 0 {
			pl.ressalvas = append(pl.ressalvas, fmt.Sprintf(
				"%d material(is) da regra já não estão vinculados à campanha (%s) e foram mantidos como estavam.", len(foraDoVinculo), strings.Join(foraDoVinculo, ", ")))
		}
	}
	return pl, nil
}

// limitesEnviados diz quais limites da vigência vieram NA chamada (na criação, os dois).
// Numa edição, o limite que não veio é o valor guardado na regra.
type limitesEnviados struct{ inicio, fim bool }

// limiteForaDaCampanha nomeia o limite que está fora do período e, se a chamada não o
// enviou, avisa que é o valor guardado na regra (não um erro de quem chamou).
func limiteForaDaCampanha(campo string, valor time.Time, porque string, limite time.Time, enviado bool) string {
	s := fmt.Sprintf("%s (%s) %s (%s)", campo, dia(valor), porque, dia(limite))
	if !enviado {
		s += fmt.Sprintf(" — é o valor que já está guardado na regra (esta chamada não enviou %s); envie %s dentro do período para corrigir", campo, campo)
	}
	return s
}

// nomeDaRegra: o nome, ou o id quando não tem nome.
func nomeDaRegra(r catalog.DistributionRule) string {
	if r.Name != "" {
		return r.Name
	}
	return r.ID.String()
}

// resumoDeMateriais: "todos os materiais do tipo" ou "2: A, B".
func resumoDeMateriais(ids []uuid.UUID, nomes map[uuid.UUID]string) string {
	if len(ids) == 0 {
		return "todos os materiais do tipo"
	}
	partes := make([]string, len(ids))
	for i, id := range ids {
		partes[i] = nomes[id]
		if partes[i] == "" {
			partes[i] = id.String()
		}
	}
	return fmt.Sprintf("%d: %s", len(ids), strings.Join(partes, ", "))
}

func resumoDeDias(mascara int16) string { return strings.Join(diasDaMascara(mascara), ", ") }

// somamComA diz se duas regras de uma mesma célula (campanha × tipo × emissora × dia)
// se SOMAM: mesmo tipo, alguma emissora, algum dia da vigência e algum dia da semana em comum.
func somamComA(r catalog.DistributionRule, n estadoDaRegra) bool {
	if r.TypeID != n.tipo || r.WeekdayMask&n.mascara == 0 || r.EndDate.Before(n.inicio) || r.StartDate.After(n.fim) {
		return false
	}
	return slices.ContainsFunc(r.StationIDs, func(id uuid.UUID) bool { return slices.Contains(n.emissoras, id) })
}

// ressalvaDeSoma avisa que regras existentes do mesmo tipo valem nas mesmas
// células: a meta do dia é a SOMA delas.
func ressalvaDeSoma(ctx context.Context, E *Escritas, pl *planoDeRegra) (string, error) {
	existentes, err := E.Leitura.Regras.ListByCampaign(ctx, pl.camp.ID)
	if err != nil {
		return "", err
	}
	var quais []string
	for _, r := range existentes {
		if pl.atual != nil && r.ID == pl.atual.ID {
			continue
		}
		if somamComA(r, pl.nova) {
			quais = append(quais, fmt.Sprintf("%q (%s, %d/dia, %s–%s)", nomeDaRegra(r), resumoDeDias(r.WeekdayMask), r.PlaysPerDay, r.TimeStart, r.TimeEnd))
		}
	}
	if len(quais) == 0 {
		return "", nil
	}
	return fmt.Sprintf(
		"Já existe(m) %d regra(s) do mesmo tipo que valem nas mesmas emissoras e em dias em comum: %s. A meta de uma célula (emissora × tipo × dia) é a SOMA das regras que valem no dia, então esta regra se SOMA a elas, não as substitui. Se a intenção era trocar a regra existente, use editar_regra_distribuicao (ou apague a antiga).",
		len(quais), strings.Join(quais[:min(len(quais), 3)], "; ")), nil
}

// ── Escalada (F3-4) ─────────────────────────────────────────────────────

// vigenciaDaRegra é uma vigência para a escalada, com a máscara de dias da MESMA versão
// da regra (a antiga ou a nova): um dia que a máscara não cobre não tem meta.
type vigenciaDaRegra struct {
	rotulo  string
	de, ate time.Time
	mascara int16
}

// escalaDeRegra aplica as duas condições de F3-4: (a) a campanha tem veiculação
// aprovada do(s) tipo(s) que o recat varre (tipos nil = todos); (b) alguma
// vigência, recortada ao período da campanha, contém dia anterior a hoje (SP) que
// a máscara de dias da semana dela cobre — da regra antiga ou da nova: num dia que
// nenhuma das duas cobre não há meta, e o déficit não pode mudar.
func escalaDeRegra(ctx context.Context, E *Escritas, camp *catalog.Campaign, tipos []uuid.UUID, nomesDosTipos string, vigencias []vigenciaDaRegra) (Escalada, error) {
	var motivos []string
	hoje := hojeSP(E.agora())
	for _, v := range vigencias {
		de, ate := v.de, v.ate
		if de.Before(camp.StartDate) {
			de = camp.StartDate
		}
		if ate.After(camp.EndDate) {
			ate = camp.EndDate
		}
		if de.After(ate) {
			continue // fora do período da campanha: nada a recortar
		}
		pde, pate, ha := diasPassadosDoPeriodo(de, ate, hoje)
		if !ha {
			continue
		}
		// Basta olhar os 7 primeiros dias: a máscara se repete a cada semana.
		for d, n := pde, 0; !d.After(pate) && n < 7; d, n = d.AddDate(0, 0, 1), n+1 {
			if !categorizer.RuleCoversDay(categorizer.Rule{StartDate: pde, EndDate: pate, WeekdayMask: v.mascara}, d) {
				continue
			}
			motivos = append(motivos, fmt.Sprintf(
				"A vigência %s da regra (%s a %s, no período da campanha; dias da semana: %s) inclui dias já passados em que ela vale (%s a %s, a partir de %s; hoje é %s em America/Sao_Paulo): mudar a meta de um dia passado muda o déficit e o \"esperado\" que o cliente já viu, mesmo sem nenhuma veiculação.",
				v.rotulo, dia(de), dia(ate), resumoDeDias(v.mascara), dia(pde), dia(pate), dia(d), dia(hoje)))
			break
		}
	}
	cid := camp.ID
	ha, err := E.Leitura.Veiculacoes.HaVeiculacao(ctx, catalog.EscopoVeiculacao{CampanhaIDs: []uuid.UUID{cid}, TipoIDs: tipos})
	if err != nil {
		return Escalada{}, err
	}
	if ha {
		motivos = append(motivos, fmt.Sprintf(
			"A campanha já tem veiculação aprovada %s: a regra dispara a recategorização do período inteiro da campanha e mexe na cota, no déficit e na bonificação de dias já veiculados, que o cliente já viu.",
			nomesDosTipos))
	}
	if len(motivos) == 0 {
		return Escalada{}, nil
	}
	return Escalada{Nivel: Critico, Motivo: strings.Join(motivos, " ")}, nil
}

// vigenciasParaEscalar: a antiga (se houver) e a nova, sem repetir quando iguais
// (mesmas datas e mesma máscara).
func vigenciasParaEscalar(atual *catalog.DistributionRule, nova estadoDaRegra) []vigenciaDaRegra {
	out := []vigenciaDaRegra{}
	if atual != nil {
		out = append(out, vigenciaDaRegra{rotulo: "atual", de: atual.StartDate, ate: atual.EndDate, mascara: atual.WeekdayMask})
		if dia(atual.StartDate) == dia(nova.inicio) && dia(atual.EndDate) == dia(nova.fim) && atual.WeekdayMask == nova.mascara {
			return out
		}
	}
	return append(out, vigenciaDaRegra{rotulo: "nova", de: nova.inicio, ate: nova.fim, mascara: nova.mascara})
}

// ── Resultado (o item de listar_regras_distribuicao) ────────────────────

type saidaRegra struct {
	Campanha ref      `json:"campanha"`
	Regra    regraDTO `json:"regra"`
}

// regraDepois monta a regra no formato de listar_regras_distribuicao.
func regraDepois(ctx context.Context, L *Leitura, camp *catalog.Campaign, r *catalog.DistributionRule) (*saidaRegra, bool, error) {
	tipos, err := nomesTipos(ctx, L)
	if err != nil {
		return nil, false, err
	}
	corte, total := cortarIDs(r.StationIDs, tetoEmissorasPorItem)
	emissoras, err := nomesEmissoras(ctx, L, corte)
	if err != nil {
		return nil, false, err
	}
	mats, err := nomesMateriais(ctx, L, camp.ClientID)
	if err != nil {
		return nil, false, err
	}
	return &saidaRegra{Campanha: refDe(camp.ID, camp.Name), Regra: montarRegraDTO(*r, tipos, corte, total, emissoras, mats)}, total > len(corte), nil
}

// regraDoRecurso é o Recurso da auditoria das tools que mexem numa regra existente.
func regraDoRecurso(valor string) (string, *uuid.UUID) {
	if id, err := uuid.Parse(valor); err == nil {
		return "distribution_rule", &id
	}
	return "distribution_rule", nil
}

// efeitosDaRegra são os efeitos de criar/editar, no preview e no ramo barrado.
func efeitosDaRegra(pl *planoDeRegra, editando bool) []string {
	n := pl.nova
	meta := fmt.Sprintf("Nas emissoras da regra, na vigência %s a %s e nos dias %s, a meta do dia do tipo %s passa a incluir %d inserção(ões)/dia na faixa %s–%s (tolerância de 15 min em cada ponta). A meta de uma célula é a soma das regras que valem no dia; um ajuste do dia (definir_override) substitui as regras da célula.",
		dia(n.inicio), dia(n.fim), resumoDeDias(n.mascara), pl.tipo.Name, n.porDia, n.horaIni, n.horaFim)
	recat := fmt.Sprintf("Ao confirmar, as veiculações do tipo %s da campanha inteira (todas as emissoras, período todo da campanha) são recategorizadas em segundo plano (até 30 s): in_slot, bonus, out_slot e déficit de dias já veiculados podem mudar.", pl.tipo.Name)
	if editando && pl.atual.TypeID != n.tipo {
		recat = fmt.Sprintf("Ao confirmar, as veiculações dos DOIS tipos (o antigo, %s, e o novo, %s) da campanha inteira são recategorizadas em segundo plano (até 30 s): in_slot, bonus, out_slot e déficit de dias já veiculados podem mudar.", pl.tipos[pl.atual.TypeID], pl.tipo.Name)
	}
	ef := []string{meta, recat}
	if len(n.materiais) > 0 {
		ef = append(ef, "Escopo por material: os materiais listados passam a ser julgados SÓ pelas regras que os nomeiam (as regras gerais do tipo deixam de valer para eles); fora do período ou do dia da regra deles a tocada vira out_date (ou bonus, se for dentro das datas mas em dia da semana sem regra). A meta N do dia segue sendo a soma de todas as regras da célula, sem filtro por material.")
	}
	return ef
}

// ── criar_regra_distribuicao ────────────────────────────────────────────

type entradaCriarRegra struct {
	CampanhaID      string   `json:"campanha_id" jsonschema:"Id (uuid) da campanha, vindo de listar_campanhas. Nunca invente: pergunte."`
	TipoID          string   `json:"tipo_id" jsonschema:"Id (uuid) do tipo de material que a regra cobre (a regra é por TIPO, não por material), vindo de listar_tipos_material. Nunca invente: pergunte."`
	EmissoraIDs     []string `json:"emissora_ids" jsonschema:"Emissoras em que a regra vale (até 500 ids; pelo menos uma; todas precisam ser da campanha — veja obter_campanha). Nunca invente ids: pergunte."`
	MaterialIDs     []string `json:"material_ids,omitempty" jsonschema:"Opcional: restringe a regra a materiais específicos (escopo por material, até 200 ids; precisam ser do tipo da regra e estar vinculados à campanha). Omita para a regra valer para TODOS os materiais do tipo (o normal)."`
	Nome            string   `json:"nome,omitempty" jsonschema:"Rótulo opcional do conjunto de regras (até 60 caracteres), ex.: Rede Nova Brasil manhã."`
	Inicio          string   `json:"inicio" jsonschema:"Primeiro dia de vigência da regra (AAAA-MM-DD), dentro do período da campanha."`
	Fim             string   `json:"fim" jsonschema:"Último dia de vigência (AAAA-MM-DD), igual ou depois do início e dentro do período da campanha."`
	Dias            []string `json:"dias" jsonschema:"Dias da semana em que a regra vale, pelo menos um: dom, seg, ter, qua, qui, sex, sab. Ex.: segunda a sexta = [seg,ter,qua,qui,sex]."`
	InicioHorario   string   `json:"inicio_horario" jsonschema:"Início da faixa horária (HH:MM, 00:00 a 23:59), ex.: 08:00. A faixa vale com tolerância de 15 min em cada ponta."`
	FimHorario      string   `json:"fim_horario" jsonschema:"Fim da faixa horária (HH:MM, 00:00 a 23:59), maior que o início. A faixa não atravessa a meia-noite."`
	InsercoesPorDia int      `json:"insercoes_por_dia" jsonschema:"Inserções esperadas por dia por emissora, de 1 a 100 (é a meta N do dia)."`
	CampoConfirmacao
}

func planejarCriacaoDeRegra(ctx context.Context, E *Escritas, in entradaCriarRegra) (*planoDeRegra, error) {
	camp, err := lerCampanhaParaEscrever(ctx, E.Leitura, in.CampanhaID)
	if err != nil {
		return nil, err
	}
	tid, err := uuidDaEntrada("tipo_id", in.TipoID)
	if err != nil {
		return nil, err
	}
	emissoras, err := listaDeEmissoras("emissora_ids", in.EmissoraIDs)
	if err != nil {
		return nil, err
	}
	if len(emissoras) == 0 {
		return nil, errEntrada("emissora_ids: informe ao menos uma emissora da campanha (uma regra sem emissora não vale para nenhuma)")
	}
	materiais, err := listaDeMateriais("material_ids", in.MaterialIDs)
	if err != nil {
		return nil, err
	}
	ini, fim, err := periodoDaEntrada(in.Inicio, in.Fim)
	if err != nil {
		return nil, err
	}
	mascara, err := mascaraDosDias(in.Dias)
	if err != nil {
		return nil, err
	}
	hIni, hFim, err := faixaDaEntrada(in.InicioHorario, in.FimHorario)
	if err != nil {
		return nil, err
	}
	if in.InsercoesPorDia < 1 || in.InsercoesPorDia > 100 {
		return nil, errEntrada("insercoes_por_dia: use um número de 1 a 100")
	}
	nome := aparar(in.Nome)
	if err := limiteDeRunas("nome", nome, tetoNomeRegra); err != nil {
		return nil, err
	}
	pl, err := conferirRegra(ctx, E, camp, nil, estadoDaRegra{
		tipo: tid, emissoras: emissoras, materiais: materiais, nome: nome, inicio: ini, fim: fim,
		mascara: mascara, horaIni: hIni, horaFim: hFim, porDia: int16(in.InsercoesPorDia),
	}, limitesEnviados{inicio: true, fim: true})
	if err != nil {
		return nil, err
	}
	return pl, nil
}

// listaDeMateriais lê uma lista de ids de material: sem repetição, no teto da chamada.
func listaDeMateriais(campo string, valores []string) ([]uuid.UUID, error) {
	if len(valores) > tetoMateriaisPorRegra {
		return nil, errEntrada(fmt.Sprintf("%s: no máximo %d materiais por chamada", campo, tetoMateriaisPorRegra))
	}
	return uuidsDaEntrada(campo, valores)
}

func escritaCriarRegra(cfg ConfigServidor) Escrita[entradaCriarRegra] {
	return Escrita[entradaCriarRegra]{
		Nome: "criar_regra_distribuicao",
		Descricao: "Cria uma regra de distribuição numa campanha: X inserções por dia (insercoes_por_dia) de um TIPO de material (tipo_id), nas " +
			"emissoras escolhidas, na faixa horária (inicio_horario–fim_horario), nos dias da semana (dias, por nome: seg, ter…) e na " +
			"vigência (inicio–fim, dentro do período da campanha). É a programação que gera a meta da grade (grade_campanha). Dá para " +
			"restringir a regra a materiais específicos (material_ids) — o escopo por material: eles passam a ser julgados SÓ pelas regras que " +
			"os nomeiam. As armadilhas: (1) a meta de uma célula (emissora × tipo × dia) é a SOMA das regras que valem no dia, então uma regra " +
			"nova não substitui a existente, ela se soma (para mudar uma regra use editar_regra_distribuicao; confira antes em " +
			"listar_regras_distribuicao); (2) um ajuste do dia (definir_override) substitui as regras daquela célula; (3) a criação " +
			"recategoriza em segundo plano (até 30 s) as veiculações do tipo na campanha inteira; (4) a regra por TIPO cobre qualquer material " +
			"do tipo, então ela só é cumprida se houver material do tipo vinculado (vincular_material). Sobe para crítico quando a campanha já " +
			"tem veiculação aprovada do tipo OU a vigência inclui dia que já passou e que os dias da semana da regra cobrem (em campanha em andamento, comece a vigência hoje ou depois " +
			"para não escalar); nesse caso nada é feito e a resposta traz o link da tela. Campanha cancelada é recusada. Nunca invente ids: " +
			"pergunte. Mostre o preview e espere a pessoa aprovar antes de confirmar.",
		Nivel: Confirma,
		Escalar: func(ctx context.Context, E *Escritas, in entradaCriarRegra) (Escalada, error) {
			pl, err := planejarCriacaoDeRegra(ctx, E, in)
			if err != nil {
				return Escalada{}, err
			}
			return escalaDeRegra(ctx, E, pl.camp, []uuid.UUID{pl.nova.tipo}, "do tipo "+pl.tipo.Name, vigenciasParaEscalar(nil, pl.nova))
		},
		Recurso: func(entradaCriarRegra) (string, *uuid.UUID) { return "distribution_rule", nil },
		Tela:    func(_ *Escritas, in entradaCriarRegra) *string { return linkDaCampanha(cfg, in.CampanhaID) },
		Preview: func(ctx context.Context, E *Escritas, in entradaCriarRegra) (previa, error) {
			pl, err := planejarCriacaoDeRegra(ctx, E, in)
			if err != nil {
				return previa{}, err
			}
			n := pl.nova
			titulo := n.nome
			if titulo == "" {
				titulo = "sem nome"
			}
			pv := previa{
				Resumo: fmt.Sprintf("Criar a regra %q na campanha %q: %d/dia do tipo %s, %s–%s, %s, de %s a %s",
					titulo, pl.camp.Name, n.porDia, pl.tipo.Name, n.horaIni, n.horaFim, resumoDeDias(n.mascara), dia(n.inicio), dia(n.fim)),
				Mudancas: []mudanca{
					{Campo: "tipo de material", Para: pl.tipo.Name},
					{Campo: "emissoras", Para: resumoDeEmissoras(n.emissoras, pl.emissoras)},
					{Campo: "materiais", Para: resumoDeMateriais(n.materiais, pl.materiais)},
					{Campo: "nome", Para: textoOuNil(ptrTexto(n.nome))},
					{Campo: "vigência", Para: dia(n.inicio) + " a " + dia(n.fim)},
					{Campo: "dias da semana", Para: resumoDeDias(n.mascara)},
					{Campo: "faixa horária", Para: n.horaIni + "–" + n.horaFim},
					{Campo: "inserções por dia", Para: n.porDia},
				},
				Efeitos:   efeitosDaRegra(pl, false),
				Ressalvas: pl.ressalvas,
			}
			soma, err := ressalvaDeSoma(ctx, E, pl)
			if err != nil {
				return previa{}, err
			}
			if soma != "" {
				pv.Ressalvas = append(pv.Ressalvas, soma)
			}
			return pv, nil
		},
		Executar: func(ctx context.Context, E *Escritas, _ *Principal, in entradaCriarRegra) (feito, error) {
			pl, err := planejarCriacaoDeRegra(ctx, E, in)
			if err != nil {
				return feito{}, err
			}
			r, err := E.Acoes.CriarRegra(ctx, pl.camp.ID, pl.nova.paraEntrada())
			if err != nil {
				return feito{}, err
			}
			saida, cortada, err := regraDepois(ctx, E.Leitura, pl.camp, r)
			if err != nil {
				return feito{}, err
			}
			f := feito{Resultado: saida, Recurso: "distribution_rule", RecursoID: &r.ID, Ressalvas: []string{ressalvaRecatDaRegra}}
			if cortada {
				f.Ressalvas = append(f.Ressalvas, ressalvaEmissorasCortadas)
			}
			return f, nil
		},
	}
}

// ── editar_regra_distribuicao ───────────────────────────────────────────

type entradaEditarRegra struct {
	CampanhaID         string   `json:"campanha_id" jsonschema:"Id (uuid) da campanha, vindo de listar_campanhas. Nunca invente: pergunte."`
	RegraID            string   `json:"regra_id" jsonschema:"Id (uuid) da regra, vindo de listar_regras_distribuicao (precisa ser desta campanha). Nunca invente: pergunte."`
	TipoID             *string  `json:"tipo_id,omitempty" jsonschema:"Novo tipo de material da regra (a tela não deixa trocar; troque só se for mesmo o que a pessoa quer — os materiais da regra precisam ser do tipo novo). Omita para manter."`
	Nome               *string  `json:"nome,omitempty" jsonschema:"Novo rótulo (até 60 caracteres); texto vazio apaga o rótulo. Omita para manter."`
	Inicio             *string  `json:"inicio,omitempty" jsonschema:"Novo primeiro dia de vigência (AAAA-MM-DD), dentro do período da campanha. Omita para manter."`
	Fim                *string  `json:"fim,omitempty" jsonschema:"Novo último dia de vigência (AAAA-MM-DD), dentro do período da campanha. Omita para manter."`
	Dias               []string `json:"dias,omitempty" jsonschema:"Os dias da semana em que a regra passa a valer — a lista INTEIRA, pelo menos um: dom, seg, ter, qua, qui, sex, sab. Omita para manter."`
	InicioHorario      *string  `json:"inicio_horario,omitempty" jsonschema:"Novo início da faixa (HH:MM, 00:00 a 23:59). Omita para manter."`
	FimHorario         *string  `json:"fim_horario,omitempty" jsonschema:"Novo fim da faixa (HH:MM, 00:00 a 23:59), maior que o início. Omita para manter."`
	InsercoesPorDia    *int     `json:"insercoes_por_dia,omitempty" jsonschema:"Nova meta por dia por emissora, de 1 a 100. Omita para manter."`
	AdicionarEmissoras []string `json:"adicionar_emissoras,omitempty" jsonschema:"Emissoras a ACRESCENTAR à regra (até 500 ids; precisam ser da campanha). Nunca invente ids: pergunte."`
	RemoverEmissoras   []string `json:"remover_emissoras,omitempty" jsonschema:"Emissoras a TIRAR da regra (até 500 ids de listar_regras_distribuicao). Só as listadas saem; a regra não pode ficar sem emissora (para isso use apagar_regra_distribuicao)."`
	AdicionarMateriais []string `json:"adicionar_materiais,omitempty" jsonschema:"Materiais a ACRESCENTAR ao escopo por material da regra (do tipo da regra e vinculados à campanha). Numa regra geral, passa a regra a ser específica desses materiais."`
	RemoverMateriais   []string `json:"remover_materiais,omitempty" jsonschema:"Materiais a TIRAR do escopo por material. Não deixa a regra sem nenhum material: para voltar a regra a valer para todos use todos_os_materiais_do_tipo."`
	TodosOsMateriais   bool     `json:"todos_os_materiais_do_tipo,omitempty" jsonschema:"true = tira o escopo por material e a regra volta a valer para TODOS os materiais do tipo. Não combina com adicionar_materiais."`
	CampoConfirmacao
}

func planejarEdicaoDeRegra(ctx context.Context, E *Escritas, in entradaEditarRegra) (*planoDeRegra, error) {
	if in.TipoID == nil && in.Nome == nil && in.Inicio == nil && in.Fim == nil && in.Dias == nil && in.InicioHorario == nil &&
		in.FimHorario == nil && in.InsercoesPorDia == nil && len(in.AdicionarEmissoras) == 0 && len(in.RemoverEmissoras) == 0 &&
		len(in.AdicionarMateriais) == 0 && len(in.RemoverMateriais) == 0 && !in.TodosOsMateriais {
		return nil, errEntrada("informe ao menos um campo a alterar (tipo_id, nome, inicio, fim, dias, inicio_horario, fim_horario, insercoes_por_dia, adicionar/remover_emissoras, adicionar/remover_materiais ou todos_os_materiais_do_tipo)")
	}
	camp, err := lerCampanhaParaEscrever(ctx, E.Leitura, in.CampanhaID)
	if err != nil {
		return nil, err
	}
	rid, err := uuidDaEntrada("regra_id", in.RegraID)
	if err != nil {
		return nil, err
	}
	atual, err := E.Leitura.Regras.Get(ctx, rid)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNaoEncontrado("regra de distribuição")
	}
	if err != nil {
		return nil, err
	}
	if atual.CampaignID != camp.ID {
		// A rota deixava editar a regra de outra campanha; para o MCP ela não existe aqui.
		return nil, errNaoEncontrado("regra de distribuição nesta campanha")
	}
	n := estadoDe(atual)

	if in.TipoID != nil {
		if n.tipo, err = uuidDaEntrada("tipo_id", *in.TipoID); err != nil {
			return nil, err
		}
	}
	if in.Nome != nil {
		n.nome = aparar(*in.Nome)
		if err := limiteDeRunas("nome", n.nome, tetoNomeRegra); err != nil {
			return nil, err
		}
	}
	if in.Inicio != nil {
		if n.inicio, err = dataDaEntrada("inicio", *in.Inicio); err != nil {
			return nil, err
		}
	}
	if in.Fim != nil {
		if n.fim, err = dataDaEntrada("fim", *in.Fim); err != nil {
			return nil, err
		}
	}
	if n.fim.Before(n.inicio) {
		return nil, errEntrada(fmt.Sprintf("a data final (%s) não pode ser anterior à inicial (%s)", dia(n.fim), dia(n.inicio)))
	}
	if in.Dias != nil {
		if n.mascara, err = mascaraDosDias(in.Dias); err != nil {
			return nil, err
		}
	}
	if in.InicioHorario != nil {
		n.horaIni = *in.InicioHorario
	}
	if in.FimHorario != nil {
		n.horaFim = *in.FimHorario
	}
	if in.InicioHorario != nil || in.FimHorario != nil {
		if n.horaIni, n.horaFim, err = faixaDaEntrada(n.horaIni, n.horaFim); err != nil {
			return nil, err
		}
	}
	if in.InsercoesPorDia != nil {
		if *in.InsercoesPorDia < 1 || *in.InsercoesPorDia > 100 {
			return nil, errEntrada("insercoes_por_dia: use um número de 1 a 100")
		}
		n.porDia = int16(*in.InsercoesPorDia)
	}
	if n.emissoras, err = mesclarLista("emissoras", atual.StationIDs, in.AdicionarEmissoras, in.RemoverEmissoras, tetoEmissorasPorChamada); err != nil {
		return nil, err
	}
	// Regra legada que JÁ não tem emissora (station_ids = {}) segue editável no resto; só
	// se recusa quando a chamada mexe nas emissoras e a regra termina sem nenhuma.
	if len(n.emissoras) == 0 && (len(in.AdicionarEmissoras) > 0 || len(in.RemoverEmissoras) > 0) {
		return nil, errEntrada("a regra ficaria sem nenhuma emissora (não valeria para nenhuma); para tirá-la use apagar_regra_distribuicao")
	}
	switch {
	case in.TodosOsMateriais:
		if len(in.AdicionarMateriais) > 0 || len(in.RemoverMateriais) > 0 {
			return nil, errEntrada("todos_os_materiais_do_tipo não combina com adicionar_materiais nem com remover_materiais")
		}
		if len(atual.MaterialIDs) == 0 {
			return nil, errEntrada("a regra já vale para todos os materiais do tipo")
		}
		n.materiais = []uuid.UUID{}
	case len(in.AdicionarMateriais) > 0 || len(in.RemoverMateriais) > 0:
		if n.materiais, err = mesclarLista("materiais", atual.MaterialIDs, in.AdicionarMateriais, in.RemoverMateriais, tetoMateriaisPorRegra); err != nil {
			return nil, err
		}
		if len(atual.MaterialIDs) > 0 && len(n.materiais) == 0 {
			return nil, errEntrada("a regra ficaria sem nenhum material no escopo, o que a transformaria em regra GERAL (vale para todos os materiais do tipo); se é isso que a pessoa quer, use todos_os_materiais_do_tipo")
		}
	}

	pl, err := conferirRegra(ctx, E, camp, atual, n, limitesEnviados{inicio: in.Inicio != nil, fim: in.Fim != nil})
	if err != nil {
		return nil, err
	}
	pl.mudancas = diferencasDaRegra(atual, pl)
	if len(pl.mudancas) == 0 {
		return nil, errEntrada("nada a alterar: os valores enviados já são os atuais da regra")
	}
	return pl, nil
}

// mesclarLista aplica adicionar/remover a uma lista de ids: remover tem de estar
// na lista, adicionar não, e o mesmo id não pode estar nas duas.
func mesclarLista(campo string, atuais []uuid.UUID, adicionar, remover []string, teto int) ([]uuid.UUID, error) {
	if len(adicionar) > teto || len(remover) > teto {
		return nil, errEntrada(fmt.Sprintf("%s: no máximo %d por chamada em cada lista", campo, teto))
	}
	add, err := uuidsDaEntrada("adicionar_"+campo, adicionar)
	if err != nil {
		return nil, err
	}
	rem, err := uuidsDaEntrada("remover_"+campo, remover)
	if err != nil {
		return nil, err
	}
	for _, id := range add {
		switch {
		case slices.Contains(rem, id):
			return nil, errEntrada(fmt.Sprintf("%s: %s está em adicionar e em remover ao mesmo tempo", campo, id))
		case slices.Contains(atuais, id):
			return nil, errEntrada(fmt.Sprintf("%s: %s já está na regra", campo, id))
		}
	}
	for _, id := range rem {
		if !slices.Contains(atuais, id) {
			return nil, errEntrada(fmt.Sprintf("%s: %s não está na regra, então não há o que remover (confira com listar_regras_distribuicao)", campo, id))
		}
	}
	nova := make([]uuid.UUID, 0, len(atuais)+len(add))
	for _, id := range atuais {
		if !slices.Contains(rem, id) {
			nova = append(nova, id)
		}
	}
	return append(nova, add...), nil
}

// diferencasDaRegra lista só o que muda, de → para, com nomes.
func diferencasDaRegra(a *catalog.DistributionRule, pl *planoDeRegra) []mudanca {
	n := pl.nova
	var out []mudanca
	if a.TypeID != n.tipo {
		out = append(out, mudanca{Campo: "tipo de material", De: pl.tipos[a.TypeID], Para: pl.tipo.Name})
	}
	if a.Name != n.nome {
		out = append(out, mudanca{Campo: "nome", De: textoOuNil(ptrTexto(a.Name)), Para: textoOuNil(ptrTexto(n.nome))})
	}
	if dia(a.StartDate) != dia(n.inicio) {
		out = append(out, mudanca{Campo: "início da vigência", De: dia(a.StartDate), Para: dia(n.inicio)})
	}
	if dia(a.EndDate) != dia(n.fim) {
		out = append(out, mudanca{Campo: "fim da vigência", De: dia(a.EndDate), Para: dia(n.fim)})
	}
	if a.WeekdayMask != n.mascara {
		out = append(out, mudanca{Campo: "dias da semana", De: resumoDeDias(a.WeekdayMask), Para: resumoDeDias(n.mascara)})
	}
	if a.TimeStart != n.horaIni || a.TimeEnd != n.horaFim {
		out = append(out, mudanca{Campo: "faixa horária", De: a.TimeStart + "–" + a.TimeEnd, Para: n.horaIni + "–" + n.horaFim})
	}
	if a.PlaysPerDay != n.porDia {
		out = append(out, mudanca{Campo: "inserções por dia", De: a.PlaysPerDay, Para: n.porDia})
	}
	var entram, saem []uuid.UUID
	for _, id := range n.emissoras {
		if !slices.Contains(a.StationIDs, id) {
			entram = append(entram, id)
		}
	}
	for _, id := range a.StationIDs {
		if !slices.Contains(n.emissoras, id) {
			saem = append(saem, id)
		}
	}
	if len(entram) > 0 {
		out = append(out, mudanca{Campo: "emissoras acrescentadas", Para: resumoDeEmissoras(entram, pl.emissoras)})
	}
	if len(saem) > 0 {
		out = append(out, mudanca{Campo: "emissoras removidas", De: resumoDeEmissoras(saem, pl.emissoras)})
	}
	if len(entram) > 0 || len(saem) > 0 {
		out = append(out, mudanca{Campo: "total de emissoras da regra", De: len(a.StationIDs), Para: len(n.emissoras)})
	}
	if !mesmosIDs(a.MaterialIDs, n.materiais) {
		out = append(out, mudanca{Campo: "materiais", De: resumoDeMateriais(a.MaterialIDs, pl.materiais), Para: resumoDeMateriais(n.materiais, pl.materiais)})
	}
	return out
}

// mesmosIDs compara dois conjuntos de ids (sem ordem).
func mesmosIDs(a, b []uuid.UUID) bool {
	return len(a) == len(b) && !slices.ContainsFunc(a, func(id uuid.UUID) bool { return !slices.Contains(b, id) })
}

// tiposDaEdicao: o tipo novo e, se mudou, o antigo — os que o recat varre.
func tiposDaEdicao(pl *planoDeRegra) ([]uuid.UUID, string) {
	if pl.atual.TypeID == pl.nova.tipo {
		return []uuid.UUID{pl.nova.tipo}, "do tipo " + pl.tipo.Name
	}
	return []uuid.UUID{pl.nova.tipo, pl.atual.TypeID}, fmt.Sprintf("do tipo %s ou do tipo %s", pl.tipo.Name, pl.tipos[pl.atual.TypeID])
}

func escritaEditarRegra(cfg ConfigServidor) Escrita[entradaEditarRegra] {
	return Escrita[entradaEditarRegra]{
		Nome: "editar_regra_distribuicao",
		Descricao: "Edita uma regra de distribuição existente de uma campanha: só os campos enviados mudam (a rota substitui a regra inteira; aqui o " +
			"resto é preservado). Dá para mudar o tipo, o nome, a vigência (inicio/fim), os dias (dias: a lista inteira, por nome), a faixa " +
			"horária, as inserções por dia, acrescentar/tirar emissoras (adicionar_emissoras/remover_emissoras) e o escopo por material " +
			"(adicionar_materiais/remover_materiais/todos_os_materiais_do_tipo). Pegue o regra_id em listar_regras_distribuicao — a regra precisa " +
			"ser desta campanha. As armadilhas: (1) QUALQUER edição — até uma só do nome — regrava a regra e refaz a recategorização em segundo " +
			"plano (até 30 s) das veiculações do tipo na campanha inteira (e do tipo ANTIGO também, se o tipo mudou), por isso escala igual: " +
			"numa campanha com dias passados ou veiculações, renomear também sobe para crítico; (2) mudar a vigência, os dias, a faixa ou a " +
			"meta de dias que já passaram muda o déficit e a cobrança que o cliente já viu; (3) tirar o último material do escopo transformaria " +
			"a regra em geral (por isso é recusado: use todos_os_materiais_do_tipo); (4) a meta de uma célula é a soma das regras que valem no " +
			"dia. Sobe para crítico quando a campanha já tem veiculação aprovada do(s) tipo(s) da regra (o novo e o antigo) OU a vigência (a " +
			"antiga ou a nova, no período da campanha) inclui dia que já passou e que os dias da semana da regra (a antiga ou a nova) cobrem; " +
			"nesse caso nada é feito e a resposta traz o link da tela. Campanha cancelada é recusada. Nunca invente ids: pergunte. Mostre o " +
			"preview e espere a pessoa aprovar antes de confirmar.",
		Nivel: Confirma,
		Escalar: func(ctx context.Context, E *Escritas, in entradaEditarRegra) (Escalada, error) {
			pl, err := planejarEdicaoDeRegra(ctx, E, in)
			if err != nil {
				return Escalada{}, err
			}
			tipos, nomes := tiposDaEdicao(pl)
			return escalaDeRegra(ctx, E, pl.camp, tipos, nomes, vigenciasParaEscalar(pl.atual, pl.nova))
		},
		Recurso: func(in entradaEditarRegra) (string, *uuid.UUID) { return regraDoRecurso(in.RegraID) },
		Tela:    func(_ *Escritas, in entradaEditarRegra) *string { return linkDaCampanha(cfg, in.CampanhaID) },
		Preview: func(ctx context.Context, E *Escritas, in entradaEditarRegra) (previa, error) {
			pl, err := planejarEdicaoDeRegra(ctx, E, in)
			if err != nil {
				return previa{}, err
			}
			pv := previa{
				Resumo:    fmt.Sprintf("Editar a regra %q da campanha %q (%d mudança(s))", nomeDaRegra(*pl.atual), pl.camp.Name, len(pl.mudancas)),
				Mudancas:  pl.mudancas,
				Efeitos:   efeitosDaRegra(pl, true),
				Ressalvas: pl.ressalvas,
			}
			if len(pl.atual.MaterialIDs) == 0 && len(pl.nova.materiais) > 0 {
				pv.Ressalvas = append(pv.Ressalvas, "A regra passa de GERAL a específica: os materiais listados deixam de ser julgados pelas regras gerais do tipo.")
			}
			soma, err := ressalvaDeSoma(ctx, E, pl)
			if err != nil {
				return previa{}, err
			}
			if soma != "" {
				pv.Ressalvas = append(pv.Ressalvas, soma)
			}
			return pv, nil
		},
		Executar: func(ctx context.Context, E *Escritas, _ *Principal, in entradaEditarRegra) (feito, error) {
			pl, err := planejarEdicaoDeRegra(ctx, E, in)
			if err != nil {
				return feito{}, err
			}
			if err := E.Acoes.EditarRegra(ctx, pl.camp.ID, pl.atual.ID, pl.nova.paraEntrada()); err != nil {
				return feito{}, err
			}
			r, err := E.Leitura.Regras.Get(ctx, pl.atual.ID)
			if err != nil {
				return feito{}, err
			}
			saida, cortada, err := regraDepois(ctx, E.Leitura, pl.camp, r)
			if err != nil {
				return feito{}, err
			}
			f := feito{Resultado: saida, Recurso: "distribution_rule", RecursoID: &pl.atual.ID, Ressalvas: []string{ressalvaRecatDaRegra}}
			if cortada {
				f.Ressalvas = append(f.Ressalvas, ressalvaEmissorasCortadas)
			}
			return f, nil
		},
	}
}

// ── apagar_regra_distribuicao ───────────────────────────────────────────

type entradaApagarRegra struct {
	CampanhaID string `json:"campanha_id" jsonschema:"Id (uuid) da campanha, vindo de listar_campanhas. Nunca invente: pergunte."`
	RegraID    string `json:"regra_id" jsonschema:"Id (uuid) da regra a apagar, vindo de listar_regras_distribuicao (precisa ser desta campanha). Nunca invente: pergunte."`
	CampoConfirmacao
}

type planoDeApagarRegra struct {
	camp      *catalog.Campaign
	regra     *catalog.DistributionRule
	tipo      string
	emissoras map[uuid.UUID]string
	materiais map[uuid.UUID]string
}

func planejarApagarRegra(ctx context.Context, E *Escritas, in entradaApagarRegra) (*planoDeApagarRegra, error) {
	camp, err := lerCampanhaParaEscrever(ctx, E.Leitura, in.CampanhaID)
	if err != nil {
		return nil, err
	}
	rid, err := uuidDaEntrada("regra_id", in.RegraID)
	if err != nil {
		return nil, err
	}
	r, err := E.Leitura.Regras.Get(ctx, rid)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNaoEncontrado("regra de distribuição")
	}
	if err != nil {
		return nil, err
	}
	if r.CampaignID != camp.ID {
		return nil, errNaoEncontrado("regra de distribuição nesta campanha")
	}
	tipos, err := nomesTipos(ctx, E.Leitura)
	if err != nil {
		return nil, err
	}
	emissoras, err := nomesEmissoras(ctx, E.Leitura, r.StationIDs)
	if err != nil {
		return nil, err
	}
	mats, err := nomesMateriais(ctx, E.Leitura, camp.ClientID)
	if err != nil {
		return nil, err
	}
	return &planoDeApagarRegra{camp: camp, regra: r, tipo: tipos[r.TypeID], emissoras: emissoras, materiais: mats}, nil
}

type saidaApagarRegra struct {
	Campanha        ref      `json:"campanha"`
	Apagada         bool     `json:"apagada"`
	Regra           regraDTO `json:"regra_apagada"`
	RegrasRestantes int      `json:"regras_restantes"`
}

func escritaApagarRegra(cfg ConfigServidor) Escrita[entradaApagarRegra] {
	return Escrita[entradaApagarRegra]{
		Nome: "apagar_regra_distribuicao",
		Descricao: "Apaga uma regra de distribuição de uma campanha (a regra e só ela; as demais ficam). Pegue o regra_id em " +
			"listar_regras_distribuicao — a regra precisa ser desta campanha; regra que não existe é recusada. As armadilhas: (1) a meta que a " +
			"regra gerava some dos dias dela (o déficit deles cai; se era a única regra da célula, a meta vira 0 e toda tocada passa a ser " +
			"bonus); (2) como a regra apagada deixa de dizer o escopo, a campanha INTEIRA (todos os tipos e emissoras) é recategorizada em " +
			"segundo plano (até 30 s); (3) um ajuste do dia (listar_overrides) que cobria a regra continua valendo. Sobe para crítico quando a " +
			"campanha já tem qualquer veiculação aprovada OU a vigência da regra (no período da campanha) inclui dia que já passou e que os dias da semana dela cobrem; nesse caso " +
			"nada é feito e a resposta traz o link da tela. Campanha cancelada é recusada. Nunca invente ids: pergunte. Mostre o preview e " +
			"espere a pessoa aprovar antes de confirmar.",
		Nivel: Confirma,
		Escalar: func(ctx context.Context, E *Escritas, in entradaApagarRegra) (Escalada, error) {
			pl, err := planejarApagarRegra(ctx, E, in)
			if err != nil {
				return Escalada{}, err
			}
			return escalaDeRegra(ctx, E, pl.camp, nil, "de algum tipo", []vigenciaDaRegra{{rotulo: "atual", de: pl.regra.StartDate, ate: pl.regra.EndDate, mascara: pl.regra.WeekdayMask}})
		},
		Recurso: func(in entradaApagarRegra) (string, *uuid.UUID) { return regraDoRecurso(in.RegraID) },
		Tela:    func(_ *Escritas, in entradaApagarRegra) *string { return linkDaCampanha(cfg, in.CampanhaID) },
		Preview: func(ctx context.Context, E *Escritas, in entradaApagarRegra) (previa, error) {
			pl, err := planejarApagarRegra(ctx, E, in)
			if err != nil {
				return previa{}, err
			}
			r := pl.regra
			return previa{
				Resumo: fmt.Sprintf("Apagar a regra %q da campanha %q: %d/dia do tipo %s, %s–%s, %s, de %s a %s",
					nomeDaRegra(*r), pl.camp.Name, r.PlaysPerDay, pl.tipo, r.TimeStart, r.TimeEnd, resumoDeDias(r.WeekdayMask), dia(r.StartDate), dia(r.EndDate)),
				Mudancas: []mudanca{
					{Campo: "regra", De: fmt.Sprintf("%s — %d/dia do tipo %s, %s–%s, %s, de %s a %s", nomeDaRegra(*r), r.PlaysPerDay, pl.tipo, r.TimeStart, r.TimeEnd, resumoDeDias(r.WeekdayMask), dia(r.StartDate), dia(r.EndDate))},
					{Campo: "emissoras", De: resumoDeEmissoras(r.StationIDs, pl.emissoras)},
					{Campo: "materiais", De: resumoDeMateriais(r.MaterialIDs, pl.materiais)},
				},
				Efeitos: []string{
					fmt.Sprintf("A meta de %d inserção(ões)/dia do tipo %s some dos dias e emissoras da regra: o déficit desses dias cai; se esta era a única regra da célula, a meta vira 0 e toda tocada do dia passa a ser bonus.", r.PlaysPerDay, pl.tipo),
					"Ao confirmar, a campanha INTEIRA (todos os tipos e emissoras) é recategorizada em segundo plano (até 30 s), porque a regra apagada deixa de dizer o escopo.",
					"Ajustes do dia (listar_overrides) que cobriam a regra continuam valendo.",
				},
			}, nil
		},
		Executar: func(ctx context.Context, E *Escritas, _ *Principal, in entradaApagarRegra) (feito, error) {
			pl, err := planejarApagarRegra(ctx, E, in)
			if err != nil {
				return feito{}, err
			}
			if err := E.Acoes.ApagarRegra(ctx, pl.camp.ID, pl.regra.ID); err != nil {
				return feito{}, err
			}
			restantes, err := E.Leitura.Regras.ListByCampaign(ctx, pl.camp.ID)
			if err != nil {
				return feito{}, err
			}
			corte, total := cortarIDs(pl.regra.StationIDs, tetoEmissorasPorItem)
			tipos, err := nomesTipos(ctx, E.Leitura)
			if err != nil {
				return feito{}, err
			}
			dto := montarRegraDTO(*pl.regra, tipos, corte, total, pl.emissoras, pl.materiais)
			f := feito{
				Resultado: saidaApagarRegra{Campanha: refDe(pl.camp.ID, pl.camp.Name), Apagada: true, Regra: dto, RegrasRestantes: len(restantes)},
				Recurso:   "distribution_rule", RecursoID: &pl.regra.ID, Ressalvas: []string{ressalvaRecatDaRegra},
			}
			if total > len(corte) {
				f.Ressalvas = append(f.Ressalvas, ressalvaEmissorasCortadas)
			}
			return f, nil
		},
	}
}
