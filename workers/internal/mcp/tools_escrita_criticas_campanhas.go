package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"radiocheck/internal/acoes"
	"radiocheck/internal/catalog"
)

// Escritas CRÍTICAS de campanha (Fatia 4, Task 15): cancelar, apagar, CPM fixo e
// preços. Nenhuma executa na chamada: o motor grava um pedido pendente e quem
// executa é a aprovação do dono em /pendentes, que reroda o Preview (por isso
// TODA recusa que depende do estado mora nele ou no planejar* que ele chama) e
// só então o Executar. Nenhuma recebe token de confirmação.

func escritasCriticasCampanhas(cfg ConfigServidor) []escritaRegistravel {
	return []escritaRegistravel{
		escritaCancelarCampanha(cfg),
		escritaApagarCampanha(cfg),
		escritaEditarCPMFixo(cfg),
		escritaEditarPrecosCampanha(cfg),
	}
}

const (
	// codigoCampanhaComHistorico: só o MCP recusa (a rota apagaria o histórico).
	codigoCampanhaComHistorico = "campanha_com_historico"

	// tetoPrecosPorChamada: quantas emissoras um pedido de preços carrega (a maior
	// campanha de produção tem 239).
	tetoPrecosPorChamada = 300

	// limiteValorMonetario: as colunas de dinheiro são NUMERIC(12,2), que não
	// comportam 1e10 ou mais.
	limiteValorMonetario = 1e10

	// fraseCritica fecha a descrição das tools críticas: o contrato §3.5 (entregar
	// o link, não prometer execução) e a regra de nunca inventar id.
	fraseCritica = "É crítica: vira um pedido que a pessoa aprova em /pendentes — entregue o link aprovar_em e diga que nada aconteceu ainda. " +
		"Nunca invente ids: pergunte."
)

// ── Helpers ─────────────────────────────────────────────────────────────

// linkDaCampanhaNaLista é a lista de campanhas já filtrada na campanha (a tela
// onde a pessoa cancelaria ou apagaria).
func linkDaCampanhaNaLista(cfg ConfigServidor, valor string) *string {
	if id, err := uuid.Parse(valor); err == nil {
		return cfg.linkTela("/campaigns", url.Values{"campaign": {id.String()}})
	}
	return cfg.link("/campaigns")
}

// reais formata um valor em reais ao modo brasileiro: R$ 1.500,75.
func reais(v float64) string {
	inteiro, centavos, _ := strings.Cut(strconv.FormatFloat(v, 'f', 2, 64), ".")
	var sb strings.Builder
	for i, r := range inteiro {
		if i > 0 && (len(inteiro)-i)%3 == 0 {
			sb.WriteByte('.')
		}
		sb.WriteRune(r)
	}
	return "R$ " + sb.String() + "," + centavos
}

// valorMonetario valida um valor em reais antes de ele chegar à coluna
// NUMERIC(12,2): não negativo, abaixo do teto e SEM arredondar em silêncio (mais
// de 2 casas é recusa — dinheiro não se corta sem a pessoa saber).
func valorMonetario(campo string, v float64) error {
	if v < 0 {
		return errEntrada(fmt.Sprintf("%s não pode ser negativo (recebi %v)", campo, v))
	}
	if v >= limiteValorMonetario {
		return errEntrada(fmt.Sprintf("%s precisa ser menor que %s", campo, reais(limiteValorMonetario)))
	}
	// A forma mais curta que volta ao mesmo número: 12.34 → "12.34"; 0.1+0.2 → 17 casas.
	if _, casas, tem := strings.Cut(strconv.FormatFloat(v, 'f', -1, 64), "."); tem && len(casas) > 2 {
		return errEntrada(fmt.Sprintf("%s aceita no máximo 2 casas decimais (centavos): recebi %s", campo, strconv.FormatFloat(v, 'f', -1, 64)))
	}
	return nil
}

// listaDeNomes: "A, B, C e mais N" (corta em tetoNomesNoPreview).
func listaDeNomes(ids []uuid.UUID, nomes map[uuid.UUID]string) string {
	partes := make([]string, 0, min(len(ids), tetoNomesNoPreview))
	for _, id := range ids[:min(len(ids), tetoNomesNoPreview)] {
		n := nomes[id]
		if n == "" {
			n = id.String()
		}
		partes = append(partes, n)
	}
	s := strings.Join(partes, ", ")
	if len(ids) > tetoNomesNoPreview {
		s += fmt.Sprintf(" e mais %d", len(ids)-tetoNomesNoPreview)
	}
	return s
}

// nomeDoCliente: o nome do dono da campanha, para o resumo do pedido.
func nomeDoCliente(ctx context.Context, E *Escritas, id uuid.UUID) (string, error) {
	c, err := E.Leitura.Clientes.Get(ctx, id)
	if err != nil {
		return "", err
	}
	return c.Name, nil
}

// saidaCampanhaSemLeitura é o resultado de uma escrita de campanha gravada cuja releitura caiu: a
// campanha no formato mínimo do obter_campanha (campanha.id e campanha.nome).
type saidaCampanhaSemLeitura struct {
	Campanha ref `json:"campanha"`
}

// campanhaFeita é o feito de uma escrita de campanha JÁ gravada: o resultado é a campanha como
// ficou, no formato de obter_campanha. Se só a releitura falhar, a escrita NÃO vira erro (o
// pedido ficaria `falhou` com a campanha já alterada): devolve o id e o nome e manda conferir.
func campanhaFeita(ctx context.Context, cfg ConfigServidor, E *Escritas, c *catalog.Campaign, ressalvas ...string) feito {
	f := feito{Recurso: "campaign", RecursoID: &c.ID, Ressalvas: ressalvas}
	saida, err := lerCampanhaCompleta(ctx, cfg, E.Leitura, c)
	if err != nil {
		cfg.log().Error("mcp: releitura da campanha após a escrita falhou", zap.Error(err))
		f.Resultado = saidaCampanhaSemLeitura{Campanha: refDe(c.ID, c.Name)}
		f.Ressalvas = append(f.Ressalvas,
			"A escrita foi gravada, mas não consegui reler a campanha agora: confira o estado com obter_campanha (campanha_id "+c.ID.String()+").")
		return f
	}
	f.Resultado = saida
	return f
}

// ── cancelar_campanha ───────────────────────────────────────────────────

// campanhaCancelada é a campanha cancelada para a resposta. O CancelarCampanha devolve nil
// (sem erro) quando só a releitura dele falhou — o cancelamento já está gravado (o acoes
// documenta o caso): relê aqui e, se também falhar, usa a campanha do plano marcada como
// cancelada (uma cópia: o plano não muda).
func campanhaCancelada(ctx context.Context, L *Leitura, c, antes *catalog.Campaign) *catalog.Campaign {
	if c != nil {
		return c
	}
	if relida, err := L.Campanhas.Get(ctx, antes.ID); err == nil {
		return relida
	}
	cp := *antes
	cp.Status = "cancelada"
	return &cp
}

type entradaCancelarCampanha struct {
	CampanhaID string `json:"campanha_id" jsonschema:"Id (uuid) da campanha, vindo de listar_campanhas. Nunca invente: pergunte."`
}

// planoDeCancelamento é a campanha como ela está e o que o cancelamento faria
// com os workers.
type planoDeCancelamento struct {
	camp      *catalog.Campaign
	cliente   string
	param     []uuid.UUID // emissoras cujo worker para (nenhuma outra campanha ativa as usa)
	continuam []uuid.UUID // emissoras que outra campanha ativa ainda usa
	nomes     map[uuid.UUID]string
}

func planejarCancelamento(ctx context.Context, E *Escritas, in entradaCancelarCampanha) (*planoDeCancelamento, error) {
	c, err := lerCampanhaQualquerEstado(ctx, E.Leitura, in.CampanhaID)
	if err != nil {
		return nil, err
	}
	if c.Status != "programada" && c.Status != "ativa" {
		return nil, &acoes.Recusa{
			Codigo: acoes.CodigoEstadoTerminal,
			Mensagem: fmt.Sprintf("a campanha %q já está %s e não pode ser cancelada: só campanha programada ou ativa se cancela",
				c.Name, c.Status),
			Dados: map[string]any{"estado_atual": c.Status},
		}
	}
	cliente, err := nomeDoCliente(ctx, E, c.ClientID)
	if err != nil {
		return nil, err
	}
	// O supervisor derruba o worker das emissoras da campanha que nenhuma OUTRA
	// campanha ativa usa (StopWorkersForCampaign, depois de a transição gravar).
	ativas, err := E.Leitura.Campanhas.ListFiltered(ctx, []string{"ativa"}, nil)
	if err != nil {
		return nil, err
	}
	usadas := make(map[uuid.UUID]bool)
	for _, a := range ativas {
		if a.ID == c.ID {
			continue
		}
		for _, id := range a.TargetStations {
			usadas[id] = true
		}
	}
	pl := &planoDeCancelamento{camp: c, cliente: cliente}
	for _, id := range c.TargetStations {
		if usadas[id] {
			pl.continuam = append(pl.continuam, id)
		} else {
			pl.param = append(pl.param, id)
		}
	}
	pl.nomes, err = nomesEmissoras(ctx, E.Leitura, c.TargetStations)
	if err != nil {
		return nil, err
	}
	return pl, nil
}

func escritaCancelarCampanha(cfg ConfigServidor) Escrita[entradaCancelarCampanha] {
	return Escrita[entradaCancelarCampanha]{
		Nome: "cancelar_campanha",
		Descricao: "Cancela uma campanha programada ou ativa: o estado vira 'cancelada', a data do cancelamento é gravada (o déficit congela nela) e os " +
			"workers das emissoras que nenhuma outra campanha ativa usa param. Use quando a pessoa quer encerrar a campanha ANTES do fim; mudar só as " +
			"datas é editar_campanha. A armadilha: a campanha sai de seletores, telas ao vivo, KPIs operacionais e da cobrança (fica marcada no histórico " +
			"e nos relatórios, e as veiculações anteriores continuam contando) e o MCP não tem como reativá-la; campanha já cancelada ou concluída é " +
			"recusada. Cancelar não apaga nada — para tirar a campanha do sistema, apagar_campanha (só sem histórico). " + fraseCritica,
		Nivel:  Critico,
		Motivo: "congela o déficit e tira a campanha das telas operacionais e da cobrança",
		Recurso: func(in entradaCancelarCampanha) (string, *uuid.UUID) {
			return campanhaDoRecurso("campaign", in.CampanhaID)
		},
		Tela: func(_ *Escritas, in entradaCancelarCampanha) *string {
			return linkDaCampanhaNaLista(cfg, in.CampanhaID)
		},
		Preview: func(ctx context.Context, E *Escritas, in entradaCancelarCampanha) (previa, error) {
			pl, err := planejarCancelamento(ctx, E, in)
			if err != nil {
				return previa{}, err
			}
			pv := previa{
				Resumo:   fmt.Sprintf("Cancelar a campanha %q do cliente %q (%s a %s)", pl.camp.Name, pl.cliente, dia(pl.camp.StartDate), dia(pl.camp.EndDate)),
				Mudancas: []mudanca{{Campo: "estado", De: pl.camp.Status, Para: "cancelada"}},
			}
			pv.Efeitos = append(pv.Efeitos, fmt.Sprintf(
				"A campanha passa de %s para cancelada agora e a data do cancelamento é gravada: o déficit dela congela nesse dia e os dias seguintes não geram obrigação.",
				pl.camp.Status))
			switch {
			case pl.camp.Status != "ativa":
				pv.Efeitos = append(pv.Efeitos, "A campanha está programada: ainda não monitora nada, então nenhum worker cai por causa dela.")
			case len(pl.param) == 0:
				pv.Efeitos = append(pv.Efeitos, "Nenhum worker para: todas as emissoras da campanha também são usadas por outra campanha ativa.")
			default:
				pv.Efeitos = append(pv.Efeitos, fmt.Sprintf(
					"Os workers de %d emissora(s) param agora, porque nenhuma outra campanha ativa as usa: %s.", len(pl.param), listaDeNomes(pl.param, pl.nomes)))
			}
			if pl.camp.Status == "ativa" && len(pl.continuam) > 0 {
				pv.Efeitos = append(pv.Efeitos, fmt.Sprintf(
					"As outras %d emissora(s) (%s) seguem monitoradas por outra campanha ativa: o worker delas fica.", len(pl.continuam), listaDeNomes(pl.continuam, pl.nomes)))
			}
			pv.Efeitos = append(pv.Efeitos,
				"A campanha sai de seletores, telas ao vivo e KPIs operacionais e da cobrança; fica marcada como cancelada no histórico e nos relatórios "+
					"(as veiculações anteriores continuam contando).")
			pv.Ressalvas = append(pv.Ressalvas,
				"Cancelar não tem volta pelo MCP: não existe tool para reativar, e a campanha cancelada fica só como histórico (o MCP não escreve mais nela).")
			if pl.camp.HubCode != nil && *pl.camp.HubCode != "" {
				pv.Ressalvas = append(pv.Ressalvas, fmt.Sprintf("A campanha tem o código do hub %s: o cancelamento não é avisado ao hub.", *pl.camp.HubCode))
			}
			return pv, nil
		},
		Executar: func(ctx context.Context, E *Escritas, _ *Principal, in entradaCancelarCampanha) (feito, error) {
			pl, err := planejarCancelamento(ctx, E, in)
			if err != nil {
				return feito{}, err
			}
			c, err := E.Acoes.CancelarCampanha(ctx, pl.camp.ID)
			if err != nil {
				return feito{}, err
			}
			// O cancelamento já está gravado: nada daqui para baixo vira erro.
			return campanhaFeita(ctx, cfg, E, campanhaCancelada(ctx, E.Leitura, c, pl.camp),
				"Os workers das emissoras que nenhuma outra campanha ativa usa foram parados; o déficit da campanha está congelado em cancelada_em."), nil
		},
	}
}

// ── apagar_campanha ─────────────────────────────────────────────────────

type entradaApagarCampanha struct {
	CampanhaID string `json:"campanha_id" jsonschema:"Id (uuid) da campanha, vindo de listar_campanhas. Nunca invente: pergunte."`
}

type saidaApagarCampanha struct {
	Apagada ref `json:"apagada"`
}

type planoDeExclusao struct {
	camp    *catalog.Campaign
	cliente string
	pegada  catalog.CampaignFootprint
}

func planejarExclusao(ctx context.Context, E *Escritas, in entradaApagarCampanha) (*planoDeExclusao, error) {
	c, err := lerCampanhaQualquerEstado(ctx, E.Leitura, in.CampanhaID)
	if err != nil {
		return nil, err
	}
	pegada, err := E.Leitura.Campanhas.Footprint(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	// A rota apaga as próprias veiculações da campanha junto com ela; o MCP não:
	// qualquer histórico é motivo para cancelar em vez de apagar.
	var historico []string
	if pegada.HasDetections || pegada.HasProjections {
		historico = append(historico, "veiculações")
	}
	if pegada.HasManualBatches {
		historico = append(historico, "comprovantes de veiculação manual")
	}
	if pegada.HasPostSale {
		historico = append(historico, "relatório de pós-venda")
	}
	if len(historico) > 0 {
		return nil, errRecusa(codigoCampanhaComHistorico, fmt.Sprintf(
			"a campanha %q tem histórico (%s) e apagá-la o destruiria sem volta: cancele em vez de apagar (cancelar_campanha) — a campanha sai das telas "+
				"operacionais e da cobrança e o histórico fica marcado nos relatórios", c.Name, strings.Join(historico, ", ")))
	}
	cliente, err := nomeDoCliente(ctx, E, c.ClientID)
	if err != nil {
		return nil, err
	}
	return &planoDeExclusao{camp: c, cliente: cliente, pegada: pegada}, nil
}

func escritaApagarCampanha(cfg ConfigServidor) Escrita[entradaApagarCampanha] {
	return Escrita[entradaApagarCampanha]{
		Nome: "apagar_campanha",
		Descricao: "Apaga uma campanha e TUDO que é dela, sem volta: vínculos de material, regras de distribuição, ajustes do dia e preços por emissora " +
			"(os materiais da biblioteca do cliente ficam). Serve para campanha criada por engano, SEM histórico: com qualquer veiculação, comprovante " +
			"de veiculação manual ou pós-venda o pedido é recusado (campanha_com_historico) e o caminho é cancelar_campanha, que mantém o histórico. A " +
			"armadilha é a irreversibilidade: a campanha é cancelada (os workers das emissoras que nenhuma outra campanha ativa usa param) e logo " +
			"depois apagada; não há tela nem tool que a recupere. Campanha cancelada pode ser apagada. " + fraseCritica,
		Nivel:  Critico,
		Motivo: "irreversível: apaga a campanha e tudo que é dela (vínculos de material, regras, ajustes do dia e preços)",
		Recurso: func(in entradaApagarCampanha) (string, *uuid.UUID) {
			return campanhaDoRecurso("campaign", in.CampanhaID)
		},
		Tela: func(_ *Escritas, in entradaApagarCampanha) *string { return linkDaCampanhaNaLista(cfg, in.CampanhaID) },
		Preview: func(ctx context.Context, E *Escritas, in entradaApagarCampanha) (previa, error) {
			pl, err := planejarExclusao(ctx, E, in)
			if err != nil {
				return previa{}, err
			}
			p := pl.pegada
			pv := previa{
				Resumo:   fmt.Sprintf("Apagar a campanha %q do cliente %q (%s a %s, %s)", pl.camp.Name, pl.cliente, dia(pl.camp.StartDate), dia(pl.camp.EndDate), pl.camp.Status),
				Mudancas: []mudanca{{Campo: "campanha", De: pl.camp.Name, Para: "apagada"}},
			}
			pv.Efeitos = append(pv.Efeitos,
				"A campanha é cancelada (os workers das emissoras que nenhuma outra campanha ativa usa param) e logo depois apagada, sem volta: nem a tela nem o MCP a recuperam.",
				fmt.Sprintf("Vão junto: vínculos de material: %d; regras de distribuição: %d; ajustes do dia: %d; preços por emissora: %d.",
					p.MaterialLinks, p.Rules, p.Overrides, p.Prices),
				"Os materiais da biblioteca do cliente não são apagados; só deixam de estar vinculados a esta campanha.")
			pv.Ressalvas = append(pv.Ressalvas,
				"A campanha não tem veiculação, comprovante manual nem pós-venda; é só por isso que pode ser apagada (com qualquer um deles o MCP recusa e manda cancelar).")
			if pl.camp.HubCode != nil && *pl.camp.HubCode != "" {
				pv.Ressalvas = append(pv.Ressalvas, fmt.Sprintf("A campanha tem o código do hub %s: o hub não é avisado de que ela foi apagada.", *pl.camp.HubCode))
			}
			return pv, nil
		},
		Executar: func(ctx context.Context, E *Escritas, _ *Principal, in entradaApagarCampanha) (feito, error) {
			pl, err := planejarExclusao(ctx, E, in)
			if err != nil {
				return feito{}, err
			}
			// ExigirSemHistorico: o planejarExclusao acima já recusou o histórico que existia, mas
			// uma veiculação pode ter sido persistida entre ele e o DELETE — a transação do delete
			// reconfere (e aborta sem apagar nada), o que a rota de apagar não faz.
			if err := E.Acoes.ApagarCampanhaCom(ctx, pl.camp.ID, acoes.OpcoesApagarCampanha{ExigirSemHistorico: true}); err != nil {
				if errors.Is(err, acoes.ErrAlvoInexistente) {
					return feito{}, errNaoEncontrado("campanha")
				}
				return feito{}, err
			}
			return feito{
				Resultado: saidaApagarCampanha{Apagada: refDe(pl.camp.ID, pl.camp.Name)},
				Recurso:   "campaign", RecursoID: &pl.camp.ID,
				Ressalvas: []string{"Não há como desfazer: a campanha e os vínculos, regras, ajustes e preços dela não existem mais."},
			}, nil
		},
	}
}

// ── editar_cpm_fixo ─────────────────────────────────────────────────────

type entradaEditarCPMFixo struct {
	CampanhaID string `json:"campanha_id" jsonschema:"Id (uuid) da campanha, vindo de listar_campanhas. Nunca invente: pergunte."`
	// SEM omitempty e ponteiro: o campo é obrigatório no schema e aceita null.
	// Ausente ≠ null — esquecer o campo nunca limpa o CPM fixo da campanha.
	CPMFixo *float64 `json:"cpm_fixo" jsonschema:"CPM fixo em reais (0 ou mais, até 2 casas decimais) ou null para voltar ao CPM dinâmico (calculado). Obrigatório, mesmo quando null: omitir o campo não limpa nada."`
}

type edicaoDeCPM struct {
	camp *catalog.Campaign
	novo *float64
}

func igualCPM(a, b *float64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func descricaoDoCPM(v *float64) string {
	if v == nil {
		return "dinâmico (sem CPM fixo)"
	}
	return reais(*v) + " (fixo)"
}

func planejarCPMFixo(ctx context.Context, E *Escritas, in entradaEditarCPMFixo) (*edicaoDeCPM, error) {
	camp, err := lerCampanhaParaEscrever(ctx, E.Leitura, in.CampanhaID)
	if err != nil {
		return nil, err
	}
	if in.CPMFixo != nil {
		if err := valorMonetario("cpm_fixo", *in.CPMFixo); err != nil {
			return nil, err
		}
	}
	if igualCPM(camp.FixedCPM, in.CPMFixo) {
		return nil, errRecusa(codigoSemMudanca, fmt.Sprintf("nada a alterar: o CPM da campanha %q já é %s", camp.Name, descricaoDoCPM(camp.FixedCPM)))
	}
	return &edicaoDeCPM{camp: camp, novo: in.CPMFixo}, nil
}

func escritaEditarCPMFixo(cfg ConfigServidor) Escrita[entradaEditarCPMFixo] {
	return Escrita[entradaEditarCPMFixo]{
		Nome: "editar_cpm_fixo",
		Descricao: "Define o CPM fixo de uma campanha (em reais, até 2 casas decimais) ou, com cpm_fixo null, volta ao CPM dinâmico. O fixo sobrescreve o CPM " +
			"mostrado em /campaigns, /insights e no dashboard; não muda investido, bonificação nem impactos. O campo cpm_fixo é obrigatório mesmo quando " +
			"null (omitir não limpa nada). As armadilhas: (1) o CPM no target é SEMPRE dinâmico, mesmo com CPM fixo; (2) pós-venda já enviado não muda (o " +
			"documento é congelado). Campanha cancelada é recusada, e pedir o valor que já está gravado também (sem_mudanca). " + fraseCritica,
		Nivel:  Critico,
		Motivo: "muda o CPM que o cliente vê",
		Recurso: func(in entradaEditarCPMFixo) (string, *uuid.UUID) {
			return campanhaDoRecurso("campaign", in.CampanhaID)
		},
		Tela: func(_ *Escritas, in entradaEditarCPMFixo) *string { return linkDaCampanha(cfg, in.CampanhaID) },
		Preview: func(ctx context.Context, E *Escritas, in entradaEditarCPMFixo) (previa, error) {
			pl, err := planejarCPMFixo(ctx, E, in)
			if err != nil {
				return previa{}, err
			}
			pv := previa{
				Resumo:   fmt.Sprintf("CPM fixo da campanha %q: de %s para %s", pl.camp.Name, descricaoDoCPM(pl.camp.FixedCPM), descricaoDoCPM(pl.novo)),
				Mudancas: []mudanca{{Campo: "cpm_fixo", De: descricaoDoCPM(pl.camp.FixedCPM), Para: descricaoDoCPM(pl.novo)}},
				Efeitos: []string{
					"Muda o CPM mostrado desta campanha em /campaigns, /insights e dashboard (no /insights entra na média ponderada por impactos); " +
						"investido, bonificação e impactos não mudam; pós-venda já enviado não muda (o documento é congelado).",
				},
				Ressalvas: []string{
					"O CPM no target é SEMPRE dinâmico, mesmo com CPM fixo (o fixo é contratado sobre a audiência total): este valor não o afeta.",
				},
			}
			if pl.novo == nil {
				pv.Efeitos = append(pv.Efeitos, "O CPM fixo é removido: volta a valer o CPM dinâmico, (investido + bonificação) ÷ impactos × 1000.")
			}
			return pv, nil
		},
		Executar: func(ctx context.Context, E *Escritas, _ *Principal, in entradaEditarCPMFixo) (feito, error) {
			pl, err := planejarCPMFixo(ctx, E, in)
			if err != nil {
				return feito{}, err
			}
			c, err := E.Acoes.DefinirCPMFixo(ctx, pl.camp.ID, pl.novo)
			if err != nil {
				return feito{}, err
			}
			return campanhaFeita(ctx, cfg, E, c, "O CPM no target continua dinâmico; pós-venda já enviado não muda."), nil
		},
	}
}

// ── editar_precos_campanha ──────────────────────────────────────────────

type linhaPrecoTipo struct {
	TipoID        string  `json:"tipo_id" jsonschema:"Id (uuid) do tipo de material, vindo de listar_tipos_material. Nunca invente: pergunte."`
	ValorUnitario float64 `json:"valor_unitario" jsonschema:"Valor em reais de UMA inserção deste tipo (0 ou mais, até 2 casas decimais)."`
}

type linhaPrecoEmissora struct {
	EmissoraID       string           `json:"emissora_id" jsonschema:"Id (uuid) de uma emissora-alvo da campanha, vindo de obter_campanha. Nunca invente: pergunte."`
	Modo             string           `json:"modo" jsonschema:"'consolidado' (um valor fechado da emissora na campanha, em valor_consolidado) ou 'por_insercao' (valor unitário por tipo de material, em por_tipo)."`
	ValorConsolidado *float64         `json:"valor_consolidado,omitempty" jsonschema:"Só no modo consolidado: o valor do contrato da emissora, em reais (0 ou mais, até 2 casas decimais)."`
	PorTipo          []linhaPrecoTipo `json:"por_tipo,omitempty" jsonschema:"Só no modo por_insercao: um valor unitário por tipo de material (ao menos um)."`
}

type entradaEditarPrecosCampanha struct {
	CampanhaID string               `json:"campanha_id" jsonschema:"Id (uuid) da campanha, vindo de listar_campanhas. Nunca invente: pergunte."`
	Precos     []linhaPrecoEmissora `json:"precos" jsonschema:"De 1 a 300 emissoras da campanha. Cada item SUBSTITUI por inteiro o preço anterior daquela emissora; as emissoras que não estão na lista ficam como estão."`
}

type precoPlanejado struct {
	emissora uuid.UUID
	entrada  catalog.EntradaPreco
	atual    *catalog.StationPricing // nil = a emissora não tem preço ainda
}

type edicaoDePrecos struct {
	camp      *catalog.Campaign
	efetivas  []precoPlanejado // só as que mudam o que está gravado, na ordem do pedido
	semEfeito int              // entradas que já estavam como pedido
	nomes     map[uuid.UUID]string
	tipos     map[uuid.UUID]string
	// semPreco são as emissoras-alvo que continuam sem preço depois da edição.
	semPreco []uuid.UUID
}

// modoDaEntrada traduz o vocabulário da tool para o do catálogo; um modo
// desconhecido passa como veio e a ValidatePricingInput o recusa.
func modoDaEntrada(m string) string {
	switch strings.TrimSpace(m) {
	case "consolidado":
		return catalog.PricingModeConsolidated
	case "por_insercao":
		return catalog.PricingModePerInsertion
	}
	return m
}

// recusaDePreco traduz as mensagens do catálogo (inglesas) para a pessoa.
func recusaDePreco(emissora string, in catalog.UpsertInput, err error) error {
	prefixo := fmt.Sprintf("emissora %q: ", emissora)
	switch {
	case errors.Is(err, catalog.ErrPricingInvalidMode):
		return errEntrada(fmt.Sprintf("%smodo %q inválido: use \"consolidado\" ou \"por_insercao\"", prefixo, in.Mode))
	case errors.Is(err, catalog.ErrPricingConsolidatedRequiresValue):
		return errEntrada(prefixo + "o modo consolidado exige valor_consolidado")
	case errors.Is(err, catalog.ErrPricingPerInsertionRequiresTypes):
		return errEntrada(prefixo + "o modo por_insercao exige ao menos um item em por_tipo")
	case errors.Is(err, catalog.ErrPricingMixedFields):
		if in.Mode == catalog.PricingModeConsolidated {
			return errEntrada(prefixo + "o modo consolidado não aceita por_tipo (use só valor_consolidado)")
		}
		return errEntrada(prefixo + "o modo por_insercao não aceita valor_consolidado (use só por_tipo)")
	case errors.Is(err, catalog.ErrPricingNegativeValue):
		return errEntrada(prefixo + "os valores não podem ser negativos")
	}
	return errEntrada(prefixo + err.Error())
}

func mesmoPreco(a *catalog.StationPricing, b catalog.EntradaPreco) bool {
	if a == nil || a.Mode != b.Mode {
		return false
	}
	if (a.ConsolidatedValue == nil) != (b.ConsolidatedValue == nil) ||
		(a.ConsolidatedValue != nil && *a.ConsolidatedValue != *b.ConsolidatedValue) {
		return false
	}
	if len(a.PerType) != len(b.PerType) {
		return false
	}
	atual := make(map[uuid.UUID]float64, len(a.PerType))
	for _, t := range a.PerType {
		atual[t.TypeID] = t.UnitValue
	}
	for _, t := range b.PerType {
		if v, ok := atual[t.TypeID]; !ok || v != t.UnitValue {
			return false
		}
	}
	return true
}

// descricaoDoPreco: "consolidado: R$ 1.500,75" ou "por inserção: Spot R$ 10,00; Vinheta R$ 4,00".
func descricaoDoPreco(modo string, consolidado *float64, porTipo []catalog.TypePricing, tipos map[uuid.UUID]string) string {
	if modo != catalog.PricingModePerInsertion {
		if consolidado == nil {
			return "consolidado"
		}
		return "consolidado: " + reais(*consolidado)
	}
	partes := make([]string, 0, len(porTipo))
	for _, t := range porTipo {
		nome := tipos[t.TypeID]
		if nome == "" {
			nome = t.TypeID.String()
		}
		partes = append(partes, nome+" "+reais(t.UnitValue))
	}
	sort.Strings(partes)
	return "por inserção: " + strings.Join(partes, "; ")
}

func planejarPrecos(ctx context.Context, E *Escritas, in entradaEditarPrecosCampanha) (*edicaoDePrecos, error) {
	camp, err := lerCampanhaParaEscrever(ctx, E.Leitura, in.CampanhaID)
	if err != nil {
		return nil, err
	}
	if len(in.Precos) == 0 {
		return nil, errEntrada("precos vazio: passe ao menos uma emissora com o preço dela")
	}
	if len(in.Precos) > tetoPrecosPorChamada {
		return nil, errEntrada(fmt.Sprintf("no máximo %d emissoras por chamada", tetoPrecosPorChamada))
	}

	// 1. Os ids (emissoras e tipos), sem repetição.
	emissoras := make([]uuid.UUID, len(in.Precos))
	vistas := make(map[uuid.UUID]bool, len(in.Precos))
	tiposDaLinha := make([][]uuid.UUID, len(in.Precos))
	for i, l := range in.Precos {
		id, err := uuidDaEntrada("emissora_id", l.EmissoraID)
		if err != nil {
			return nil, err
		}
		if vistas[id] {
			return nil, errEntrada(fmt.Sprintf("a emissora %s aparece mais de uma vez em precos: mande um preço só por emissora", id))
		}
		vistas[id] = true
		emissoras[i] = id
		tvistos := make(map[uuid.UUID]bool, len(l.PorTipo))
		for _, t := range l.PorTipo {
			tid, err := uuidDaEntrada("tipo_id", t.TipoID)
			if err != nil {
				return nil, err
			}
			if tvistos[tid] {
				return nil, errEntrada(fmt.Sprintf("o tipo de material %s aparece mais de uma vez em por_tipo da emissora %s", tid, id))
			}
			tvistos[tid] = true
			tiposDaLinha[i] = append(tiposDaLinha[i], tid)
		}
	}

	// 2. Nomes: das emissoras do pedido e das do alvo (o preview nomeia as que ficam sem preço).
	nomes, err := nomesEmissoras(ctx, E.Leitura, append(slices.Clone(emissoras), camp.TargetStations...))
	if err != nil {
		return nil, err
	}
	nomeDe := func(id uuid.UUID) string {
		if n := nomes[id]; n != "" {
			return n
		}
		return id.String()
	}
	tipos, err := nomesTipos(ctx, E.Leitura)
	if err != nil {
		return nil, err
	}

	// 3. Cada linha vira uma EntradaPreco validada.
	pl := &edicaoDePrecos{camp: camp, nomes: nomes, tipos: tipos}
	entradas := make([]catalog.EntradaPreco, len(in.Precos))
	for i, l := range in.Precos {
		id := emissoras[i]
		if !slices.Contains(camp.TargetStations, id) {
			quem := id.String()
			if n := nomes[id]; n != "" {
				quem = fmt.Sprintf("%q (%s)", n, id)
			}
			return nil, errEntrada(fmt.Sprintf("a emissora %s não está nas emissoras-alvo da campanha %q: só emissora da campanha tem preço (inclua-a antes em editar_emissoras_campanha)", quem, camp.Name))
		}
		for _, tid := range tiposDaLinha[i] {
			if _, ok := tipos[tid]; !ok {
				return nil, errNaoEncontrado(fmt.Sprintf("tipo de material %s", tid))
			}
		}
		rotulo := fmt.Sprintf("valor_consolidado da emissora %q", nomeDe(id))
		if l.ValorConsolidado != nil {
			if err := valorMonetario(rotulo, *l.ValorConsolidado); err != nil {
				return nil, err
			}
		}
		porTipo := make([]catalog.TypePricing, len(l.PorTipo))
		for j, t := range l.PorTipo {
			if err := valorMonetario(fmt.Sprintf("valor_unitario do tipo %s na emissora %q", tipos[tiposDaLinha[i][j]], nomeDe(id)), t.ValorUnitario); err != nil {
				return nil, err
			}
			porTipo[j] = catalog.TypePricing{TypeID: tiposDaLinha[i][j], UnitValue: t.ValorUnitario}
		}
		e := catalog.EntradaPreco{StationID: id, Mode: modoDaEntrada(l.Modo), ConsolidatedValue: l.ValorConsolidado, PerType: porTipo}
		if err := catalog.ValidatePricingInput(catalog.UpsertInput{
			CampaignID: camp.ID, StationID: id, Mode: e.Mode, ConsolidatedValue: e.ConsolidatedValue, PerType: e.PerType,
		}); err != nil {
			return nil, recusaDePreco(nomeDe(id), catalog.UpsertInput{Mode: e.Mode}, err)
		}
		entradas[i] = e
	}

	// 4. O que já está gravado: só entra o que muda.
	atuais, err := E.Leitura.Precos.ListByCampaign(ctx, camp.ID)
	if err != nil {
		return nil, err
	}
	porEmissora := make(map[uuid.UUID]*catalog.StationPricing, len(atuais))
	for i := range atuais {
		porEmissora[atuais[i].StationID] = &atuais[i]
	}
	precificadas := make(map[uuid.UUID]bool, len(atuais)+len(entradas))
	for id := range porEmissora {
		precificadas[id] = true
	}
	for _, e := range entradas {
		precificadas[e.StationID] = true
		if mesmoPreco(porEmissora[e.StationID], e) {
			pl.semEfeito++
			continue
		}
		pl.efetivas = append(pl.efetivas, precoPlanejado{emissora: e.StationID, entrada: e, atual: porEmissora[e.StationID]})
	}
	if len(pl.efetivas) == 0 {
		return nil, errRecusa(codigoSemMudanca, "nada a alterar: todas as emissoras do pedido já estão com esse preço")
	}
	for _, id := range camp.TargetStations {
		if !precificadas[id] {
			pl.semPreco = append(pl.semPreco, id)
		}
	}
	return pl, nil
}

type saidaEditarPrecosCampanha struct {
	Campanha ref        `json:"campanha"`
	Gravadas int        `json:"gravadas"`
	Precos   []precoDTO `json:"precos"`
}

func escritaEditarPrecosCampanha(cfg ConfigServidor) Escrita[entradaEditarPrecosCampanha] {
	return Escrita[entradaEditarPrecosCampanha]{
		Nome: "editar_precos_campanha",
		Descricao: "Define o preço de uma ou mais emissoras da campanha de uma vez, numa transação só (tudo ou nada; um item inválido recusa o pedido " +
			"inteiro). Cada item de precos SUBSTITUI por inteiro o preço anterior da emissora: modo 'consolidado' (um valor fechado da emissora na " +
			"campanha, em valor_consolidado) ou 'por_insercao' (valor unitário por tipo de material, em por_tipo); as emissoras fora da lista ficam como " +
			"estão. Valores em reais, até 2 casas decimais; de 1 a 300 emissoras. As armadilhas: (1) só emissora-alvo da campanha tem preço; (2) o preço " +
			"não tem histórico — muda o investido, a bonificação e o CPM de toda a campanha, inclusive dos dias já veiculados; (3) só emissora com preço " +
			"entra no investido; (4) pós-venda já enviado não muda. Campanha cancelada é recusada, e pedir os preços que já estão gravados também " +
			"(sem_mudanca). " + fraseCritica,
		Nivel:  Critico,
		Motivo: "dinheiro: muda o investido, a bonificação e o CPM que o cliente vê",
		Recurso: func(in entradaEditarPrecosCampanha) (string, *uuid.UUID) {
			return campanhaDoRecurso("campaign", in.CampanhaID)
		},
		Tela: func(_ *Escritas, in entradaEditarPrecosCampanha) *string { return linkDaCampanha(cfg, in.CampanhaID) },
		Preview: func(ctx context.Context, E *Escritas, in entradaEditarPrecosCampanha) (previa, error) {
			pl, err := planejarPrecos(ctx, E, in)
			if err != nil {
				return previa{}, err
			}
			mud := make([]mudanca, 0, len(pl.efetivas))
			for _, p := range pl.efetivas {
				de := "sem preço"
				if p.atual != nil {
					de = descricaoDoPreco(p.atual.Mode, p.atual.ConsolidatedValue, p.atual.PerType, pl.tipos)
				}
				mud = append(mud, mudanca{
					Campo: fmt.Sprintf("%s (%s)", pl.nomes[p.emissora], p.emissora), De: de,
					Para: descricaoDoPreco(p.entrada.Mode, p.entrada.ConsolidatedValue, p.entrada.PerType, pl.tipos),
				})
			}
			pv := previa{
				Resumo: fmt.Sprintf("Preços da campanha %q: gravar %d emissora(s)", pl.camp.Name, len(pl.efetivas)),
				Efeitos: []string{
					"Cada emissora listada tem o preço anterior SUBSTITUÍDO por inteiro (o detalhe por tipo é apagado e regravado); as outras ficam como estão.",
					"O investido, a bonificação e o CPM dinâmico de /campaigns, /insights e dashboard são recalculados na hora, inclusive sobre as veiculações já feitas (o preço não tem histórico).",
				},
				Ressalvas: []string{"O pós-venda já enviado não muda (o documento é congelado)."},
			}
			if len(mud) > tetoLinhasPMMNaResposta {
				pv.Ressalvas = append(pv.Ressalvas, fmt.Sprintf("mostrando %d de %d mudanças", tetoLinhasPMMNaResposta, len(mud)))
				mud = mud[:tetoLinhasPMMNaResposta]
			}
			pv.Mudancas = mud
			if pl.semEfeito > 0 {
				pv.Ressalvas = append(pv.Ressalvas, fmt.Sprintf("%d emissora(s) já estavam como pedido e ficam de fora (nada muda nelas).", pl.semEfeito))
			}
			if len(pl.semPreco) > 0 {
				pv.Ressalvas = append(pv.Ressalvas, fmt.Sprintf("%d emissora(s)-alvo da campanha ficam sem preço (e fora do investido): %s.",
					len(pl.semPreco), listaDeNomes(pl.semPreco, pl.nomes)))
			}
			return pv, nil
		},
		Executar: func(ctx context.Context, E *Escritas, _ *Principal, in entradaEditarPrecosCampanha) (feito, error) {
			pl, err := planejarPrecos(ctx, E, in)
			if err != nil {
				return feito{}, err
			}
			entradas := make([]catalog.EntradaPreco, len(pl.efetivas))
			for i, p := range pl.efetivas {
				entradas[i] = p.entrada
			}
			gravadas, err := E.Acoes.DefinirPrecos(ctx, pl.camp.ID, entradas)
			if err != nil {
				return feito{}, err
			}
			f := feito{
				Resultado: saidaEditarPrecosCampanha{
					Campanha: refDe(pl.camp.ID, pl.camp.Name), Gravadas: len(gravadas), Precos: precosDTO(gravadas, pl.tipos, pl.nomes),
				},
				Recurso: "campaign", RecursoID: &pl.camp.ID,
				Ressalvas: []string{"Pós-venda já enviado não muda; o investido e o CPM de /campaigns, /insights e dashboard já usam os novos valores."},
			}
			if len(pl.semPreco) > 0 {
				f.Ressalvas = append(f.Ressalvas, fmt.Sprintf("%d emissora(s)-alvo continuam sem preço e fora do investido: %s.", len(pl.semPreco), listaDeNomes(pl.semPreco, pl.nomes)))
			}
			return f, nil
		},
	}
}
