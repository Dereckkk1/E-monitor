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
	"radiocheck/internal/hub"
)

// Escritas de campanha (Lote C, Task 14): criar, editar nome/datas/código do hub
// e trocar as emissoras-alvo. Nenhuma é "só cadastro": a vigência decide o
// estado que o agendador dá à campanha (e portanto se os workers sobem e o
// cliente passa a ver), e encolher as emissoras reclassifica histórico. Por
// isso as três escalam para crítico (F3-4) quando atingem o que já foi visto.

func registrarEscritasCampanhas(s *sdk.Server, cfg ConfigServidor, p *Principal) {
	ferramentaEscrita(s, cfg, p, escritaCriarCampanha(cfg))
	ferramentaEscrita(s, cfg, p, escritaEditarCampanha(cfg))
	ferramentaEscrita(s, cfg, p, escritaEditarEmissorasCampanha(cfg))
}

const (
	tetoNomeCampanha = 200
	// tetoEmissorasPorChamada: o teto de uma lista de ids de emissora numa
	// chamada (a maior campanha de produção tem 239).
	tetoEmissorasPorChamada = 500
	// tetoNomesNoPreview: quantas emissoras o preview nomeia (o resto vira "e mais N").
	tetoNomesNoPreview = 15
)

// ── Helpers de campanha (também usados pelas tools de vínculo) ──────────

// dataDaEntrada lê uma data de calendário AAAA-MM-DD (sem fuso). Data zero e anos
// fora de 2000–2100 são quase sempre erro de digitação do modelo (0001-01-01,
// 2062-…): recusa em vez de gravar.
func dataDaEntrada(campo, valor string) (time.Time, error) {
	d, ok := lerDia(aparar(valor))
	if !ok || d.IsZero() || d.Year() < 2000 || d.Year() > 2100 {
		return time.Time{}, errEntrada(fmt.Sprintf(
			"%s inválido: use a data de calendário AAAA-MM-DD, entre os anos 2000 e 2100 (ex.: 2026-11-01)", campo))
	}
	return d, nil
}

// periodoDaEntrada lê início e fim e exige fim ≥ início.
func periodoDaEntrada(inicio, fim string) (time.Time, time.Time, error) {
	ini, err := dataDaEntrada("inicio", inicio)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	f, err := dataDaEntrada("fim", fim)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	if f.Before(ini) {
		return time.Time{}, time.Time{}, errEntrada(fmt.Sprintf(
			"a data final (%s) não pode ser anterior à inicial (%s)", dia(f), dia(ini)))
	}
	return ini, f, nil
}

// lerCampanhaParaEscrever valida o id e carrega a campanha. Campanha cancelada é
// recusada (F3-9): ela fica só como histórico e fora das telas operacionais — a
// tela até deixaria mexer, o MCP não.
func lerCampanhaParaEscrever(ctx context.Context, L *Leitura, valor string) (*catalog.Campaign, error) {
	id, err := uuidDaEntrada("campanha_id", valor)
	if err != nil {
		return nil, err
	}
	c, err := L.Campanhas.Get(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNaoEncontrado("campanha")
	}
	if err != nil {
		return nil, err
	}
	if c.Status == "cancelada" {
		return nil, errRecusa(acoes.CodigoCampanhaCancelada, fmt.Sprintf(
			"a campanha %q está cancelada: ela fica só como histórico e o MCP não escreve nela; se a pessoa quer mexer, é pela tela", c.Name))
	}
	return c, nil
}

// campanhaDoRecurso é o Recurso da auditoria das tools que mexem numa campanha
// existente: o tipo e o id (nil se o id da entrada não for um uuid).
func campanhaDoRecurso(recurso, valor string) (string, *uuid.UUID) {
	if id, err := uuid.Parse(valor); err == nil {
		return recurso, &id
	}
	return recurso, nil
}

// linkDaCampanha é a tela de edição da campanha (o wizard).
func linkDaCampanha(cfg ConfigServidor, valor string) *string {
	if id, err := uuid.Parse(valor); err == nil {
		return cfg.link("/campaigns/" + id.String() + "/edit")
	}
	return cfg.link("/campaigns")
}

// resumoDeEmissoras: "3: Rádio A, Rádio B, Rádio C" (corta em tetoNomesNoPreview).
func resumoDeEmissoras(ids []uuid.UUID, nomes map[uuid.UUID]string) string {
	if len(ids) == 0 {
		return "nenhuma"
	}
	partes := make([]string, 0, min(len(ids), tetoNomesNoPreview))
	for _, id := range ids[:min(len(ids), tetoNomesNoPreview)] {
		n := nomes[id]
		if n == "" {
			n = id.String()
		}
		partes = append(partes, n)
	}
	s := fmt.Sprintf("%d: %s", len(ids), strings.Join(partes, ", "))
	if len(ids) > tetoNomesNoPreview {
		s += fmt.Sprintf(" e mais %d", len(ids)-tetoNomesNoPreview)
	}
	return s
}

// emissorasExistentes confere que todas as emissoras existem; a primeira que
// faltar é nao_encontrado. Devolve os nomes.
func emissorasExistentes(ctx context.Context, L *Leitura, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	porID, err := emissorasPorID(ctx, L, ids)
	if err != nil {
		return nil, err
	}
	nomes := make(map[uuid.UUID]string, len(porID))
	for _, id := range ids {
		st, ok := porID[id]
		if !ok {
			return nil, errNaoEncontrado(fmt.Sprintf("emissora %s", id))
		}
		nomes[id] = st.Name
	}
	return nomes, nil
}

// listaDeEmissoras lê uma lista de ids de emissora da entrada: sem repetição, no
// teto da chamada. Vazia é permitida (quem chama decide se é erro).
func listaDeEmissoras(campo string, valores []string) ([]uuid.UUID, error) {
	if len(valores) > tetoEmissorasPorChamada {
		return nil, errEntrada(fmt.Sprintf("%s: no máximo %d emissoras por chamada", campo, tetoEmissorasPorChamada))
	}
	return uuidsDaEntrada(campo, valores)
}

func hubConfigurado(E *Escritas) bool { return E.Acoes.Hub != nil && E.Acoes.Hub.Configured() }

const ressalvaCampanhaProgramada = "A campanha nasceu programada: é ativada automaticamente na data de início (o agendador roda a cada 60 s). Nada é monitorado antes disso."

// ── criar_campanha ──────────────────────────────────────────────────────

type entradaCriarCampanha struct {
	ClienteID   string   `json:"cliente_id" jsonschema:"Id (uuid) do cliente dono da campanha, vindo de listar_clientes. Nunca invente: pergunte."`
	Nome        string   `json:"nome" jsonschema:"Nome da campanha, até 200 caracteres. Antes de criar, procure em listar_campanhas: campanha duplicada do mesmo cliente é o erro mais comum."`
	Inicio      string   `json:"inicio" jsonschema:"Primeiro dia da vigência (AAAA-MM-DD, data de calendário). Início hoje ou no passado sobe para crítico: o agendador a ativa em até 60 s."`
	Fim         string   `json:"fim" jsonschema:"Último dia da vigência (AAAA-MM-DD), igual ou depois do início."`
	EmissoraIDs []string `json:"emissora_ids,omitempty" jsonschema:"Emissoras-alvo (até 500 ids de buscar_emissoras). Opcional: sem elas a campanha nasce sem emissoras e nada é monitorado até editar_emissoras_campanha. Nunca invente ids: pergunte."`
	CodigoHub   string   `json:"codigo_hub,omitempty" jsonschema:"Código da campanha no hub (EH-XXXXXX). Opcional. Nunca invente: é conferido contra o hub e recusado se for de outro cliente ou não existir."`
	CampoConfirmacao
}

// planoDeCriacao é a campanha como será criada, já validada.
type planoDeCriacao struct {
	cliente     *catalog.Client
	nome        string
	inicio, fim time.Time
	emissoras   []uuid.UUID
	nomes       map[uuid.UUID]string
	codigo      string // como veio, aparado; a conferência contra o hub é do preview/ação
}

func planejarCriacaoDeCampanha(ctx context.Context, E *Escritas, in entradaCriarCampanha) (*planoDeCriacao, error) {
	cid, err := uuidDaEntrada("cliente_id", in.ClienteID)
	if err != nil {
		return nil, err
	}
	cliente, err := E.Leitura.Clientes.Get(ctx, cid)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNaoEncontrado("cliente")
	}
	if err != nil {
		return nil, err
	}
	nome := aparar(in.Nome)
	if nome == "" {
		return nil, errEntrada("o nome da campanha não pode ficar vazio")
	}
	if err := limiteDeRunas("nome", nome, tetoNomeCampanha); err != nil {
		return nil, err
	}
	ini, fim, err := periodoDaEntrada(in.Inicio, in.Fim)
	if err != nil {
		return nil, err
	}
	emissoras, err := listaDeEmissoras("emissora_ids", in.EmissoraIDs)
	if err != nil {
		return nil, err
	}
	nomes, err := emissorasExistentes(ctx, E.Leitura, emissoras)
	if err != nil {
		return nil, err
	}
	return &planoDeCriacao{cliente: cliente, nome: nome, inicio: ini, fim: fim, emissoras: emissoras, nomes: nomes,
		codigo: aparar(in.CodigoHub)}, nil
}

func escritaCriarCampanha(cfg ConfigServidor) Escrita[entradaCriarCampanha] {
	return Escrita[entradaCriarCampanha]{
		Nome: "criar_campanha",
		Descricao: "Cria uma campanha de um cliente: nome, vigência (inicio e fim, AAAA-MM-DD) e, se a pessoa informou, as emissoras-alvo " +
			"e o código do hub (EH-XXXXXX). Nasce 'programada' e sem materiais nem regras de distribuição: o agendador a ativa sozinho na " +
			"data de início (em até 60 s depois da virada do dia, America/Sao_Paulo), quando os workers sobem e o cliente passa a ver " +
			"veiculações; monte os materiais (vincular_material) e as regras antes disso. As armadilhas: (1) campanha duplicada — procure " +
			"antes em listar_campanhas pelo cliente e pelo nome; (2) o código do hub é conferido contra o hub e a criação é recusada se " +
			"for de outro cliente ou não existir — nunca o invente. Sobe para crítico quando o início é hoje ou já passou (seria ativada " +
			"em até 60 s, como iniciar uma campanha); nesse caso nada é feito e a resposta traz o link da tela. Nunca invente ids: " +
			"pergunte. Mostre o preview e espere a pessoa aprovar antes de confirmar.",
		Nivel: Confirma,
		Escalar: func(_ context.Context, E *Escritas, in entradaCriarCampanha) (Escalada, error) {
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
		Recurso: func(entradaCriarCampanha) (string, *uuid.UUID) { return "campaign", nil },
		Tela:    func(*Escritas, entradaCriarCampanha) *string { return cfg.link("/campaigns/new") },
		Preview: func(ctx context.Context, E *Escritas, in entradaCriarCampanha) (previa, error) {
			pl, err := planejarCriacaoDeCampanha(ctx, E, in)
			if err != nil {
				return previa{}, err
			}
			var canonico string
			if pl.codigo != "" {
				// A barreira do hub (§4.4 da spec do código): recusa aqui, antes do token.
				if canonico, err = E.Acoes.ConferirCodigoHub(ctx, pl.cliente.ID, pl.codigo); err != nil {
					return previa{}, err
				}
			}
			mud := []mudanca{
				{Campo: "cliente", Para: pl.cliente.Name},
				{Campo: "nome", Para: pl.nome},
				{Campo: "inicio", Para: dia(pl.inicio)},
				{Campo: "fim", Para: dia(pl.fim)},
				{Campo: "emissoras", Para: resumoDeEmissoras(pl.emissoras, pl.nomes)},
			}
			if canonico != "" {
				mud = append(mud, mudanca{Campo: "codigo_hub", Para: canonico})
			}
			pv := previa{Resumo: fmt.Sprintf("Criar a campanha %q do cliente %q (%s a %s)", pl.nome, pl.cliente.Name, dia(pl.inicio), dia(pl.fim)), Mudancas: mud}

			hoje := hojeSP(E.agora())
			if pl.inicio.After(hoje) {
				pv.Efeitos = append(pv.Efeitos, fmt.Sprintf("A campanha nasce 'programada'. O agendador (a cada 60 s) a ativa em %s (America/Sao_Paulo): só então os workers das emissoras sobem e as veiculações passam a ser atribuídas a ela.", dia(pl.inicio)))
			} else {
				pv.Efeitos = append(pv.Efeitos, fmt.Sprintf("O início (%s) é hoje ou já passou: o agendador ativa a campanha em até 60 s, os workers das emissoras sobem e o cliente passa a ver veiculações.", dia(pl.inicio)))
			}
			pv.Efeitos = append(pv.Efeitos, "Nasce sem materiais e sem regras de distribuição: vincule os materiais (vincular_material) e monte as regras pela tela; sem isso nada é detectado nem cobrado.")
			if len(pl.emissoras) == 0 {
				pv.Ressalvas = append(pv.Ressalvas, "Sem emissoras-alvo: nada é monitorado até a campanha ganhar emissoras (editar_emissoras_campanha).")
			}
			if hubConfigurado(E) {
				if canonico != "" {
					pv.Efeitos = append(pv.Efeitos, "A campanha é enviada ao hub (campanha.upsert) em segundo plano.")
				} else {
					pv.Ressalvas = append(pv.Ressalvas, "Sem codigo_hub: a campanha não se liga a nenhuma proposta do hub (a tela exige o código na criação; informe-o se a pessoa tiver).")
				}
			}
			if !pl.cliente.IsActive {
				pv.Ressalvas = append(pv.Ressalvas, fmt.Sprintf("O cliente %q está DESATIVADO (a empresa foi suspensa): a tela e a rota deixam criar campanha mesmo assim, mas confirme com a pessoa.", pl.cliente.Name))
			}
			existentes, err := E.Leitura.Campanhas.ListFiltered(ctx, nil, []uuid.UUID{pl.cliente.ID})
			if err != nil {
				return previa{}, err
			}
			for _, c := range existentes {
				if strings.EqualFold(c.Name, pl.nome) {
					pv.Ressalvas = append(pv.Ressalvas, fmt.Sprintf(
						"Este cliente já tem uma campanha com este nome: %q (id %s, %s, %s a %s). Se for a mesma, não crie outra.",
						c.Name, c.ID, c.Status, dia(c.StartDate), dia(c.EndDate)))
					break
				}
			}
			return pv, nil
		},
		Executar: func(ctx context.Context, E *Escritas, _ *Principal, in entradaCriarCampanha) (feito, error) {
			pl, err := planejarCriacaoDeCampanha(ctx, E, in)
			if err != nil {
				return feito{}, err
			}
			c, err := E.Acoes.CriarCampanha(ctx, acoes.CriarCampanhaEntrada{
				ClienteID: pl.cliente.ID, Nome: pl.nome, Inicio: pl.inicio, Fim: pl.fim,
				EmissoraIDs: pl.emissoras, CodigoHub: pl.codigo,
			})
			if err != nil {
				return feito{}, err
			}
			saida, err := lerCampanhaCompleta(ctx, cfg, E.Leitura, c)
			if err != nil {
				return feito{}, err
			}
			f := feito{Resultado: saida, Recurso: "campaign", RecursoID: &c.ID, Ressalvas: []string{ressalvaCampanhaProgramada}}
			if hubConfigurado(E) {
				f.Ressalvas = append(f.Ressalvas, "O aviso ao hub (campanha.upsert) segue em segundo plano; se o hub estiver fora do ar a reemissão periódica leva a campanha quando ele voltar.")
			}
			return f, nil
		},
	}
}

// ── editar_campanha ─────────────────────────────────────────────────────

type entradaEditarCampanha struct {
	CampanhaID string  `json:"campanha_id" jsonschema:"Id (uuid) da campanha, vindo de listar_campanhas. Nunca invente: pergunte."`
	Nome       *string `json:"nome,omitempty" jsonschema:"Novo nome (até 200 caracteres, não pode ficar vazio). Omita para manter."`
	Inicio     *string `json:"inicio,omitempty" jsonschema:"Novo primeiro dia (AAAA-MM-DD). Omita para manter."`
	Fim        *string `json:"fim,omitempty" jsonschema:"Novo último dia (AAAA-MM-DD), igual ou depois do início. Omita para manter."`
	CodigoHub  *string `json:"codigo_hub,omitempty" jsonschema:"Novo código do hub (EH-XXXXXX); texto vazio APAGA o código (e desliga a coleta da proposta no hub). Omita para manter. Nunca invente: é conferido contra o hub."`
	CampoConfirmacao
}

// edicaoDeCampanha é a edição já validada: a campanha atual e os valores que ficam.
type edicaoDeCampanha struct {
	atual       *catalog.Campaign
	nome        string
	inicio, fim time.Time

	mudaNome, mudaInicio, mudaFim, mudaCodigo bool
	// codigoBruto é o texto do pedido (aparado) quando o código muda; vazio = apagar.
	codigoBruto string
	mudancas    []mudanca
}

func planejarEdicaoDeCampanha(ctx context.Context, E *Escritas, in entradaEditarCampanha) (*edicaoDeCampanha, error) {
	if in.Nome == nil && in.Inicio == nil && in.Fim == nil && in.CodigoHub == nil {
		return nil, errEntrada("informe ao menos um campo a alterar (nome, inicio, fim ou codigo_hub)")
	}
	atual, err := lerCampanhaParaEscrever(ctx, E.Leitura, in.CampanhaID)
	if err != nil {
		return nil, err
	}
	pl := &edicaoDeCampanha{atual: atual, nome: atual.Name, inicio: atual.StartDate, fim: atual.EndDate}

	if in.Nome != nil {
		nome := aparar(*in.Nome)
		if nome == "" {
			return nil, errEntrada("o nome da campanha não pode ficar vazio (omita o campo para manter o atual)")
		}
		if err := limiteDeRunas("nome", nome, tetoNomeCampanha); err != nil {
			return nil, err
		}
		if nome != atual.Name {
			pl.mudaNome, pl.nome = true, nome
			pl.mudancas = append(pl.mudancas, mudanca{Campo: "nome", De: atual.Name, Para: nome})
		}
	}
	if in.Inicio != nil {
		d, err := dataDaEntrada("inicio", *in.Inicio)
		if err != nil {
			return nil, err
		}
		pl.inicio = d
	}
	if in.Fim != nil {
		d, err := dataDaEntrada("fim", *in.Fim)
		if err != nil {
			return nil, err
		}
		pl.fim = d
	}
	if pl.fim.Before(pl.inicio) {
		return nil, errEntrada(fmt.Sprintf("a data final (%s) não pode ser anterior à inicial (%s)", dia(pl.fim), dia(pl.inicio)))
	}
	if dia(pl.inicio) != dia(atual.StartDate) {
		pl.mudaInicio = true
		pl.mudancas = append(pl.mudancas, mudanca{Campo: "inicio", De: dia(atual.StartDate), Para: dia(pl.inicio)})
	}
	if dia(pl.fim) != dia(atual.EndDate) {
		pl.mudaFim = true
		pl.mudancas = append(pl.mudancas, mudanca{Campo: "fim", De: dia(atual.EndDate), Para: dia(pl.fim)})
	}
	if in.CodigoHub != nil {
		bruto := aparar(*in.CodigoHub)
		canonico := hub.NormalizaCodigo(bruto)
		if canonico == "" && bruto != "" {
			return nil, errRecusa(acoes.CodigoHub, "código do hub inválido")
		}
		if canonico != textoDoPtr(atual.HubCode) {
			pl.mudaCodigo, pl.codigoBruto = true, bruto
			pl.mudancas = append(pl.mudancas, mudanca{Campo: "codigo_hub", De: textoOuNil(atual.HubCode), Para: textoOuNil(&canonico)})
		}
	}
	if len(pl.mudancas) == 0 {
		return nil, errEntrada("nada a alterar: os valores enviados já são os atuais da campanha")
	}
	return pl, nil
}

// diasPassadosDoPeriodo são os dias do período anteriores a hoje (SP): de início
// até o menor entre o fim e ontem. ha=false quando não há nenhum.
func diasPassadosDoPeriodo(inicio, fim, hoje time.Time) (de, ate time.Time, ha bool) {
	ate = fim
	if ontem := hoje.AddDate(0, 0, -1); ate.After(ontem) {
		ate = ontem
	}
	if inicio.After(ate) {
		return time.Time{}, time.Time{}, false
	}
	return inicio, ate, true
}

func descricaoDosDiasPassados(de, ate time.Time, ha bool) string {
	if !ha {
		return "nenhum"
	}
	return dia(de) + " a " + dia(ate)
}

// escalaEdicaoDeCampanha aplica as três condições de F3-4 a editar_campanha:
// (a) as datas mudam o estado que o agendador dá à campanha; (b) muda o conjunto
// de dias já passados do período; (c) muda o código do hub (é a ponte).
func escalaEdicaoDeCampanha(pl *edicaoDeCampanha, hoje time.Time) Escalada {
	var motivos []string
	if pl.mudaInicio || pl.mudaFim {
		antes := catalog.EstadoPeloCiclo(pl.atual.Status, pl.atual.StartDate, pl.atual.EndDate, hoje)
		depois := catalog.EstadoPeloCiclo(pl.atual.Status, pl.inicio, pl.fim, hoje)
		if antes != depois {
			motivos = append(motivos, fmt.Sprintf(
				"As novas datas mudam o estado que o agendador dá à campanha (%s → %s): workers sobem ou param e o que o cliente vê muda.", antes, depois))
		}
		d0, a0, h0 := diasPassadosDoPeriodo(pl.atual.StartDate, pl.atual.EndDate, hoje)
		d1, a1, h1 := diasPassadosDoPeriodo(pl.inicio, pl.fim, hoje)
		if h0 != h1 || (h0 && (dia(d0) != dia(d1) || dia(a0) != dia(a1))) {
			motivos = append(motivos, fmt.Sprintf(
				"O conjunto de dias já passados do período muda (de %s para %s): grade, déficit e cobrança de dias que o cliente já viu mudam com ele.",
				descricaoDosDiasPassados(d0, a0, h0), descricaoDosDiasPassados(d1, a1, h1)))
		}
	}
	if pl.mudaCodigo {
		motivos = append(motivos, "O código do hub muda: ele é a ponte da campanha com a proposta do hub (a coleta de resultados lá depende dele).")
	}
	if len(motivos) == 0 {
		return Escalada{}
	}
	return Escalada{Nivel: Critico, Motivo: strings.Join(motivos, " ")}
}

func escritaEditarCampanha(cfg ConfigServidor) Escrita[entradaEditarCampanha] {
	return Escrita[entradaEditarCampanha]{
		Nome: "editar_campanha",
		Descricao: "Edita uma campanha: nome, vigência (inicio, fim) e/ou código do hub — só os campos enviados mudam; codigo_hub vazio APAGA " +
			"o código. Não mexe em emissoras (editar_emissoras_campanha), materiais, regras nem cliente. A armadilha: o estado da campanha " +
			"segue as datas (o agendador roda a cada 60 s) — empurrar o início para o futuro derruba uma campanha ativa e para os workers; " +
			"estender o fim de uma concluída a reativa. Sobe para crítico quando (a) as novas datas mudam o estado que o agendador dá à " +
			"campanha, (b) muda o conjunto de dias já passados do período ou (c) muda o código do hub; nesse caso nada é feito e a resposta " +
			"traz o link da tela. Renomear e esticar o fim de uma campanha em andamento não escalam. Campanha cancelada é recusada. " +
			"Nunca invente ids nem o código do hub: pergunte. Mostre o preview e espere a pessoa aprovar antes de confirmar.",
		Nivel: Confirma,
		Escalar: func(ctx context.Context, E *Escritas, in entradaEditarCampanha) (Escalada, error) {
			pl, err := planejarEdicaoDeCampanha(ctx, E, in)
			if err != nil {
				return Escalada{}, err
			}
			return escalaEdicaoDeCampanha(pl, hojeSP(E.agora())), nil
		},
		Recurso: func(in entradaEditarCampanha) (string, *uuid.UUID) {
			return campanhaDoRecurso("campaign", in.CampanhaID)
		},
		Tela: func(_ *Escritas, in entradaEditarCampanha) *string { return linkDaCampanha(cfg, in.CampanhaID) },
		Preview: func(ctx context.Context, E *Escritas, in entradaEditarCampanha) (previa, error) {
			pl, err := planejarEdicaoDeCampanha(ctx, E, in)
			if err != nil {
				return previa{}, err
			}
			if pl.mudaCodigo && pl.codigoBruto != "" {
				// A barreira do hub: recusa aqui, antes do token.
				if _, err := E.Acoes.ConferirCodigoHub(ctx, pl.atual.ClientID, pl.codigoBruto); err != nil {
					return previa{}, err
				}
			}
			pv := previa{Resumo: fmt.Sprintf("Editar a campanha %q", pl.atual.Name), Mudancas: pl.mudancas}
			if pl.mudaInicio || pl.mudaFim {
				hoje := hojeSP(E.agora())
				antes := catalog.EstadoPeloCiclo(pl.atual.Status, pl.atual.StartDate, pl.atual.EndDate, hoje)
				depois := catalog.EstadoPeloCiclo(pl.atual.Status, pl.inicio, pl.fim, hoje)
				switch {
				case antes == depois:
					pv.Efeitos = append(pv.Efeitos, fmt.Sprintf("O estado que as datas dão à campanha (%s) não muda.", depois))
				default:
					efeito := fmt.Sprintf("O estado que o agendador (a cada 60 s) dá à campanha muda de %s para %s", antes, depois)
					switch depois {
					case "ativa":
						efeito += ": os workers das emissoras sobem e as veiculações passam a ser atribuídas a ela."
					case "concluida":
						efeito += ": os workers das emissoras que nenhuma outra campanha ativa usa param."
					default:
						efeito += ": os workers das emissoras que nenhuma outra campanha ativa usa param até a nova data de início."
					}
					pv.Efeitos = append(pv.Efeitos, efeito)
				}
				d0, a0, h0 := diasPassadosDoPeriodo(pl.atual.StartDate, pl.atual.EndDate, hoje)
				d1, a1, h1 := diasPassadosDoPeriodo(pl.inicio, pl.fim, hoje)
				if h0 != h1 || (h0 && (dia(d0) != dia(d1) || dia(a0) != dia(a1))) {
					pv.Efeitos = append(pv.Efeitos, fmt.Sprintf("Os dias já passados do período mudam (de %s para %s): grade, déficit e cobrança desses dias mudam.",
						descricaoDosDiasPassados(d0, a0, h0), descricaoDosDiasPassados(d1, a1, h1)))
				}
				pv.Efeitos = append(pv.Efeitos, "O período define o que é out_date (veiculação fora das datas) e os dias em que as regras de distribuição valem. A edição não recategoriza veiculações já registradas.")
			}
			if pl.mudaCodigo {
				if pl.codigoBruto == "" {
					pv.Efeitos = append(pv.Efeitos, "Apagar o código desliga a coleta de resultados da proposta no hub; o hub é avisado (campanha.upsert) em segundo plano.")
				} else {
					pv.Efeitos = append(pv.Efeitos, "O hub é avisado do novo código (campanha.upsert) em segundo plano.")
				}
			} else if pl.mudaNome || pl.mudaInicio || pl.mudaFim {
				pv.Ressalvas = append(pv.Ressalvas, "Renomear ou mudar datas NÃO avisa o hub (só a mudança de código): o cadastro da campanha lá não muda sozinho.")
			}
			return pv, nil
		},
		Executar: func(ctx context.Context, E *Escritas, _ *Principal, in entradaEditarCampanha) (feito, error) {
			pl, err := planejarEdicaoDeCampanha(ctx, E, in)
			if err != nil {
				return feito{}, err
			}
			entrada := acoes.EditarCampanhaEntrada{Nome: pl.nome, Inicio: pl.inicio, Fim: pl.fim}
			if pl.mudaCodigo { // nil = não mexe (e não consulta o hub nem o avisa)
				entrada.CodigoHub = &pl.codigoBruto
			}
			c, err := E.Acoes.EditarCampanha(ctx, pl.atual.ID, entrada)
			if err != nil {
				return feito{}, err
			}
			saida, err := lerCampanhaCompleta(ctx, cfg, E.Leitura, c)
			if err != nil {
				return feito{}, err
			}
			f := feito{Resultado: saida, Recurso: "campaign", RecursoID: &c.ID}
			if pl.mudaInicio || pl.mudaFim {
				f.Ressalvas = append(f.Ressalvas, "O estado mostrado é o gravado: ele segue as novas datas na próxima rodada do agendador (até 60 s).")
			}
			if pl.mudaNome || pl.mudaInicio || pl.mudaFim {
				if !pl.mudaCodigo {
					f.Ressalvas = append(f.Ressalvas, "O hub não é avisado de renomear nem de mudar datas: o cadastro da campanha lá não muda sozinho.")
				}
			}
			return f, nil
		},
	}
}

// ── editar_emissoras_campanha ───────────────────────────────────────────

type entradaEditarEmissorasCampanha struct {
	CampanhaID string   `json:"campanha_id" jsonschema:"Id (uuid) da campanha, vindo de listar_campanhas. Nunca invente: pergunte."`
	Adicionar  []string `json:"adicionar,omitempty" jsonschema:"Emissoras a ACRESCENTAR às da campanha (até 500 ids de buscar_emissoras). Nunca invente ids: pergunte."`
	Remover    []string `json:"remover,omitempty" jsonschema:"Emissoras a TIRAR da campanha (até 500 ids de obter_campanha). Só as listadas saem; as demais ficam."`
	CampoConfirmacao
}

type edicaoDeEmissorasDaCampanha struct {
	camp      *catalog.Campaign
	adicionar []uuid.UUID
	remover   []uuid.UUID
	nova      []uuid.UUID // a lista inteira como ficará (a rota substitui a lista toda)
	nomes     map[uuid.UUID]string
}

func planejarEmissorasDaCampanha(ctx context.Context, E *Escritas, in entradaEditarEmissorasCampanha) (*edicaoDeEmissorasDaCampanha, error) {
	if len(in.Adicionar) == 0 && len(in.Remover) == 0 {
		return nil, errEntrada("informe ao menos uma emissora em adicionar ou remover")
	}
	camp, err := lerCampanhaParaEscrever(ctx, E.Leitura, in.CampanhaID)
	if err != nil {
		return nil, err
	}
	add, err := listaDeEmissoras("adicionar", in.Adicionar)
	if err != nil {
		return nil, err
	}
	rem, err := listaDeEmissoras("remover", in.Remover)
	if err != nil {
		return nil, err
	}
	for _, id := range add {
		if slices.Contains(rem, id) {
			return nil, errEntrada(fmt.Sprintf("a emissora %s está em adicionar e em remover ao mesmo tempo", id))
		}
	}
	nomes, err := emissorasExistentes(ctx, E.Leitura, add)
	if err != nil {
		return nil, err
	}
	for _, id := range add {
		if slices.Contains(camp.TargetStations, id) {
			return nil, errEntrada(fmt.Sprintf("a emissora %q (%s) já está na campanha", nomes[id], id))
		}
	}
	for _, id := range rem {
		if !slices.Contains(camp.TargetStations, id) {
			return nil, errEntrada(fmt.Sprintf("a emissora %s não está na campanha, então não há o que remover (confira com obter_campanha)", id))
		}
	}
	// Os nomes das que saem (as que entram já vieram de emissorasExistentes).
	if len(rem) > 0 {
		saem, err := nomesEmissoras(ctx, E.Leitura, rem)
		if err != nil {
			return nil, err
		}
		for id, n := range saem {
			nomes[id] = n
		}
	}
	nova := make([]uuid.UUID, 0, len(camp.TargetStations)+len(add))
	for _, id := range camp.TargetStations {
		if !slices.Contains(rem, id) {
			nova = append(nova, id)
		}
	}
	nova = append(nova, add...)
	return &edicaoDeEmissorasDaCampanha{camp: camp, adicionar: add, remover: rem, nova: nova, nomes: nomes}, nil
}

// haVeiculacaoNasRemovidas: a campanha tem veiculação aprovada em alguma das
// emissoras que sairiam? É o recorte do recat (F3-3): encolher target_stations
// reclassifica histórico.
func haVeiculacaoNasRemovidas(ctx context.Context, E *Escritas, pl *edicaoDeEmissorasDaCampanha) (bool, error) {
	if len(pl.remover) == 0 {
		return false, nil
	}
	return E.Leitura.Veiculacoes.HaVeiculacao(ctx, catalog.EscopoVeiculacao{CampanhaIDs: []uuid.UUID{pl.camp.ID}, EmissoraIDs: pl.remover})
}

func escritaEditarEmissorasCampanha(cfg ConfigServidor) Escrita[entradaEditarEmissorasCampanha] {
	return Escrita[entradaEditarEmissorasCampanha]{
		Nome: "editar_emissoras_campanha",
		Descricao: "Acrescenta e/ou tira emissoras-alvo de uma campanha: adicionar e remover são listas de ids, e só as listadas mudam (a " +
			"tela troca a lista inteira; aqui nunca). Numa campanha ativa os workers sobem/param na hora. As armadilhas: (1) os materiais já " +
			"vinculados NÃO ganham as emissoras novas — cada vínculo tem lista própria (editar_emissoras_material), e sem ela a tocada da " +
			"emissora nova não é atribuída à campanha; (2) tirar emissora que já tem veiculação da campanha reclassifica o histórico. Sobe " +
			"para crítico quando alguma emissora removida tem veiculação aprovada da campanha; nesse caso nada é feito e a resposta traz o " +
			"link da tela. Acrescentar não escala. Campanha cancelada é recusada. Nunca invente ids: pergunte. Mostre o preview e espere a " +
			"pessoa aprovar antes de confirmar.",
		Nivel: Confirma,
		Escalar: func(ctx context.Context, E *Escritas, in entradaEditarEmissorasCampanha) (Escalada, error) {
			pl, err := planejarEmissorasDaCampanha(ctx, E, in)
			if err != nil {
				return Escalada{}, err
			}
			ha, err := haVeiculacaoNasRemovidas(ctx, E, pl)
			if err != nil || !ha {
				return Escalada{}, err
			}
			return Escalada{Nivel: Critico, Motivo: "A campanha já tem veiculação aprovada em emissora que sairia: encolher as emissoras-alvo reclassifica o " +
				"histórico (cota, déficit e bonificação de dias passados, que o cliente já viu)."}, nil
		},
		Recurso: func(in entradaEditarEmissorasCampanha) (string, *uuid.UUID) {
			return campanhaDoRecurso("campaign", in.CampanhaID)
		},
		Tela: func(_ *Escritas, in entradaEditarEmissorasCampanha) *string {
			return linkDaCampanha(cfg, in.CampanhaID)
		},
		Preview: func(ctx context.Context, E *Escritas, in entradaEditarEmissorasCampanha) (previa, error) {
			pl, err := planejarEmissorasDaCampanha(ctx, E, in)
			if err != nil {
				return previa{}, err
			}
			var mud []mudanca
			if len(pl.adicionar) > 0 {
				mud = append(mud, mudanca{Campo: "emissoras acrescentadas", Para: resumoDeEmissoras(pl.adicionar, pl.nomes)})
			}
			if len(pl.remover) > 0 {
				mud = append(mud, mudanca{Campo: "emissoras removidas", De: resumoDeEmissoras(pl.remover, pl.nomes)})
			}
			mud = append(mud, mudanca{Campo: "total de emissoras-alvo", De: len(pl.camp.TargetStations), Para: len(pl.nova)})
			pv := previa{
				Resumo:   fmt.Sprintf("Campanha %q: acrescentar %d e remover %d emissora(s)", pl.camp.Name, len(pl.adicionar), len(pl.remover)),
				Mudancas: mud,
			}
			if pl.camp.Status == "ativa" {
				pv.Efeitos = append(pv.Efeitos, "A campanha está ativa: ao confirmar, os workers das emissoras acrescentadas sobem agora e os das removidas param "+
					"(menos os de emissoras que outra campanha ativa ainda usa).")
			} else {
				pv.Efeitos = append(pv.Efeitos, fmt.Sprintf("A campanha está %s: só a lista de emissoras muda; nenhum worker sobe nem para agora (eles sobem quando a campanha ativar).", pl.camp.Status))
			}
			if len(pl.nova) == 0 {
				pv.Ressalvas = append(pv.Ressalvas, "A campanha ficará SEM emissoras-alvo: nada será monitorado.")
			}
			vinculos, err := E.Leitura.MateriaisCampanha.ListByCampaign(ctx, pl.camp.ID)
			if err != nil {
				return previa{}, err
			}
			if len(vinculos) > 0 {
				if len(pl.adicionar) > 0 {
					pv.Ressalvas = append(pv.Ressalvas, fmt.Sprintf(
						"Os %d material(is) já vinculado(s) NÃO ganham as emissoras acrescentadas: cada vínculo tem lista própria. Use editar_emissoras_material para incluí-las, senão a tocada dessas emissoras não é atribuída a esta campanha.", len(vinculos)))
				}
				if len(pl.remover) > 0 {
					pv.Ressalvas = append(pv.Ressalvas, "Os vínculos dos materiais (que têm lista própria de emissoras) não são alterados; confira com listar_materiais_campanha.")
				}
			}
			if len(pl.remover) > 0 {
				ha, err := haVeiculacaoNasRemovidas(ctx, E, pl)
				if err != nil {
					return previa{}, err
				}
				// O preview também é o que o ramo crítico devolve: o texto tem de dizer a verdade nos dois casos.
				if ha {
					pv.Efeitos = append(pv.Efeitos, "A campanha já tem veiculação aprovada em emissora que sai: o histórico é reclassificado (cota, déficit e bonificação de dias passados podem mudar).")
				} else {
					pv.Efeitos = append(pv.Efeitos, "Nenhuma emissora removida tem veiculação aprovada desta campanha, então hoje não há número passado a mudar.")
				}
			}
			return pv, nil
		},
		Executar: func(ctx context.Context, E *Escritas, _ *Principal, in entradaEditarEmissorasCampanha) (feito, error) {
			pl, err := planejarEmissorasDaCampanha(ctx, E, in)
			if err != nil {
				return feito{}, err
			}
			if err := E.Acoes.TrocarEmissorasCampanha(ctx, pl.camp.ID, pl.nova); err != nil {
				return feito{}, err
			}
			c, err := E.Leitura.Campanhas.Get(ctx, pl.camp.ID)
			if err != nil {
				return feito{}, err
			}
			saida, err := lerCampanhaCompleta(ctx, cfg, E.Leitura, c)
			if err != nil {
				return feito{}, err
			}
			f := feito{Resultado: saida, Recurso: "campaign", RecursoID: &pl.camp.ID}
			if pl.camp.Status == "ativa" {
				f.Ressalvas = append(f.Ressalvas, "A campanha está ativa: o supervisor sobe e para os workers da diferença agora; se algum falhar, o reconciler (30 s) corrige.")
			}
			if len(pl.adicionar) > 0 && saida.Campanha.Materiais > 0 {
				f.Ressalvas = append(f.Ressalvas, "Os materiais vinculados não ganharam as emissoras acrescentadas: use editar_emissoras_material.")
			}
			return f, nil
		},
	}
}
