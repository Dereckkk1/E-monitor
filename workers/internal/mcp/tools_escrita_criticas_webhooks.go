package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"radiocheck/internal/acoes"
	"radiocheck/internal/catalog"
)

// Escritas CRÍTICAS de webhook (Fatia 4, Task 19): editar a configuração de envio
// de um cliente e mandar um evento de teste. Nenhuma executa na chamada: o motor
// grava um pedido pendente e quem executa é a aprovação do dono em /pendentes, que
// reroda o Preview (por isso TODA recusa que depende do estado — inclusive a
// conferência de SSRF da URL, que olha o DNS e o ambiente de agora — mora nele ou
// no planejar* que ele chama) e só então o Executar. Nenhuma recebe token de
// confirmação.
//
// F4-11 — o segredo do webhook não passa pelo chat. editar_webhook_cliente NÃO tem
// campo de segredo (só url, ativo e eventos): o segredo continua na tela, e o
// UpdateWebhookInput que o Executar monta nunca o preenche (o COALESCE da gravação
// o mantém). Pelo mesmo motivo a URL com credencial (https://usuario:senha@…) é
// recusada. A URL ANTIGA também não sai inteira: o preview a mostra só com esquema e
// host (como obter_webhook_cliente), porque o caminho e a query dela podem carregar
// a credencial do receptor (Slack, Discord e Zapier põem o segredo ali). A URL NOVA
// aparece inteira no preview — é o destino que a pessoa aprova — e por isso fica
// inteira no pedido gravado (24 h) e na auditoria; o preview avisa disso.
//
// F4-10 — o MCP é mais estrito que a rota onde a rota responde sucesso sem fazer:
// testar_webhook que seria descartado (sem URL, desligado ou sem webhook.test nos
// eventos) é recusado, em vez de o 202 da rota.

func escritasCriticasWebhooks(cfg ConfigServidor) []escritaRegistravel {
	return []escritaRegistravel{
		escritaEditarWebhookCliente(cfg),
		escritaTestarWebhook(cfg),
	}
}

const (
	// Códigos que só o MCP recusa (a rota deixa fazer qualquer um destes).
	codigoWebhookNaoConfigurado  = "webhook_nao_configurado"
	codigoWebhookTesteDescartado = "webhook_teste_descartado"
	codigoWebhookIndisponivel    = "webhook_indisponivel"

	// tetoURLWebhook: o pedido fica gravado 24 h e a URL entra na auditoria.
	tetoURLWebhook = 2000

	// nomesDosEventos é a lista que as mensagens de evento desconhecido mostram.
	nomesDosEventos = "detection.confirmed, detection.retracted, webhook.test ou * (todos)"

	msgURLInvalida = "url inválida: informe o endereço completo do receptor, começando por https:// (ex.: https://sistema.cliente.com.br/webhook)"
)

// ── Helpers ─────────────────────────────────────────────────────────────

// urlDoWebhook confere o que dá para conferir sem consultar o ambiente: formato,
// credencial na URL e tamanho. As mensagens NÃO repetem a URL (ela pode carregar
// uma credencial). http/https em produção e a guarda de SSRF ficam com
// acoes.ValidarWebhook, chamada só para a URL que de fato muda.
func urlDoWebhook(valor string) (string, error) {
	u := aparar(valor)
	if u == "" {
		return "", errEntrada("url: não pode ficar vazia — não há como apagar a URL por aqui; para parar os envios use ativo=false")
	}
	if err := limiteDeRunas("url", u, tetoURLWebhook); err != nil {
		return "", err
	}
	p, err := url.ParseRequestURI(u)
	if err != nil || (p.Scheme != "http" && p.Scheme != "https") || p.Hostname() == "" {
		return "", errEntrada(msgURLInvalida)
	}
	if p.User != nil {
		return "", errEntrada("a URL não pode levar usuário e senha (https://usuario:senha@…): credencial não passa pelo chat. " +
			"Se o receptor exige autenticação, configure o segredo do webhook pela tela (/clients, modal Webhook)")
	}
	return u, nil
}

// eventosDoWebhook confere a lista de eventos e a devolve sem repetição, na ordem
// em que veio. Lista vazia é recusada: no banco ela significa "assina tudo", o
// oposto do que quem a manda costuma querer (para parar os envios é ativo=false;
// para receber tudo é "*").
func eventosDoWebhook(valores []string) ([]string, error) {
	if len(valores) == 0 {
		return nil, errEntrada("eventos: informe ao menos um evento (" + nomesDosEventos + ") ou omita o campo para não mexer na lista. " +
			"Lista vazia não é aceita: para parar os envios use ativo=false")
	}
	out := make([]string, 0, len(valores))
	for _, v := range valores {
		e := aparar(v)
		if !acoes.EventoWebhookValido(e) {
			return nil, errEntrada(fmt.Sprintf("evento desconhecido (%q): use %s", e, nomesDosEventos))
		}
		if !slices.Contains(out, e) {
			out = append(out, e)
		}
	}
	return out, nil
}

// webhookEmPortugues traduz as mensagens inglesas de acoes.ValidarWebhook (iguais
// às da rota PATCH, por paridade) para o MCP. O código fica; qualquer outro erro
// passa intacto. Nenhuma tradução repete a URL.
func webhookEmPortugues(err error) error {
	var r *acoes.Recusa
	if !errors.As(err, &r) || r.Codigo != acoes.CodigoEntradaInvalida {
		return err
	}
	m := r.Mensagem
	switch {
	case strings.HasPrefix(m, "webhook_url must be"):
		return errEntrada(msgURLInvalida)
	case strings.HasPrefix(m, "http URLs are not allowed"):
		return errEntrada("a URL precisa começar por https://: http não é aceito em produção (só em ambiente de desenvolvimento, e só em localhost), " +
			"porque o conteúdo do webhook sairia sem criptografia")
	case strings.HasPrefix(m, "webhook_url rejected: "):
		switch {
		case strings.Contains(m, "ssrf:"):
			return errEntrada("a URL aponta para a rede interna (endereço privado, loopback ou link-local) e o E-monitor não envia webhook para esse destino " +
				"(proteção contra SSRF): informe uma URL pública do receptor")
		case strings.Contains(m, "empty host"):
			return errEntrada(msgURLInvalida)
		}
		return errEntrada("não consegui verificar o endereço da URL (ele não resolve no DNS ou é inválido): confira o domínio e se o receptor é público")
	case strings.HasPrefix(m, "unknown event: "):
		return errEntrada(fmt.Sprintf("evento desconhecido (%q): use %s", strings.TrimPrefix(m, "unknown event: "), nomesDosEventos))
	}
	return err
}

// mesmoConjunto compara duas listas de eventos sem olhar a ordem.
func mesmoConjunto(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for _, e := range a {
		if !slices.Contains(b, e) {
			return false
		}
	}
	return true
}

// diferenca devolve o que está em a e não está em b, na ordem de a.
func diferenca(a, b []string) []string {
	var out []string
	for _, e := range a {
		if !slices.Contains(b, e) {
			out = append(out, e)
		}
	}
	return out
}

// saidaWebhookSemLeitura é o resultado quando a escrita foi gravada mas a releitura
// falhou: só quem é o cliente.
type saidaWebhookSemLeitura struct {
	Cliente ref `json:"cliente"`
}

// webhookFeito é o feito de uma escrita de webhook JÁ gravada: o resultado é a
// configuração como ficou, no formato de obter_webhook_cliente. Se só a releitura
// falhar, a escrita NÃO vira erro (o modelo a repetiria): devolve o cliente e manda
// conferir.
func webhookFeito(ctx context.Context, cfg ConfigServidor, E *Escritas, c *catalog.Client, ressalvas ...string) feito {
	f := feito{Recurso: "client", RecursoID: &c.ID, Ressalvas: ressalvas}
	saida, err := montarWebhook(ctx, E.Leitura, c)
	if err != nil {
		cfg.log().Error("mcp: releitura do webhook após a escrita falhou", zap.Error(err))
		f.Resultado = saidaWebhookSemLeitura{Cliente: refDe(c.ID, c.Name)}
		f.Ressalvas = append(f.Ressalvas,
			"A escrita foi gravada, mas não consegui reler o webhook agora: confira o estado com obter_webhook_cliente (cliente_id "+c.ID.String()+").")
		return f
	}
	f.Resultado = saida
	return f
}

// ── editar_webhook_cliente ──────────────────────────────────────────────

type entradaEditarWebhook struct {
	ClienteID string   `json:"cliente_id" jsonschema:"Id (uuid) do cliente, vindo de listar_clientes. Nunca invente: pergunte."`
	URL       *string  `json:"url,omitempty" jsonschema:"Nova URL de destino, completa, em https (http só em ambiente de desenvolvimento, em localhost), sem usuário e senha na URL, até 2000 caracteres. Omita para manter. Não há como apagar a URL: para parar os envios use ativo=false."`
	Ativo     *bool    `json:"ativo,omitempty" jsonschema:"true liga o envio, false desliga. Omita para manter."`
	Eventos   []string `json:"eventos,omitempty" jsonschema:"Eventos que o cliente recebe, ao menos um: detection.confirmed, detection.retracted, webhook.test ou * (todos). SUBSTITUI a lista atual inteira. Omita para manter."`
}

// planoDeWebhook é a configuração como está e SÓ o que o pedido de fato muda.
type planoDeWebhook struct {
	cliente *catalog.Client
	atual   *catalog.WebhookConfig // nil = o cliente não tem URL de webhook
	url     *string
	ativo   *bool
	eventos []string
	naFila  int // entregas pendentes (inclui retentativas)
}

func (pl *planoDeWebhook) ativoAtual() bool { return pl.atual != nil && pl.atual.Enabled }

// ativoDepois: o estado do envio depois da edição.
func (pl *planoDeWebhook) ativoDepois() bool {
	if pl.ativo != nil {
		return *pl.ativo
	}
	return pl.ativoAtual()
}

func (pl *planoDeWebhook) semSegredo() bool { return pl.atual == nil || pl.atual.Secret == "" }

func planejarWebhook(ctx context.Context, E *Escritas, in entradaEditarWebhook) (*planoDeWebhook, error) {
	c, err := lerClienteParaEscrever(ctx, E.Leitura, in.ClienteID)
	if err != nil {
		return nil, err
	}
	if in.URL == nil && in.Ativo == nil && in.Eventos == nil {
		return nil, errEntrada("informe ao menos um de url, ativo ou eventos: sem isso não há o que mudar")
	}
	var pedidaURL *string
	if in.URL != nil {
		u, err := urlDoWebhook(*in.URL)
		if err != nil {
			return nil, err
		}
		pedidaURL = &u
	}
	var pedidosEventos []string
	if in.Eventos != nil {
		if pedidosEventos, err = eventosDoWebhook(in.Eventos); err != nil {
			return nil, err
		}
	}
	atual, err := E.Leitura.Clientes.GetWebhookConfig(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	pl := &planoDeWebhook{cliente: c, atual: atual}
	// Sem URL gravada, ativo e eventos sozinhos não têm onde valer, e a leitura
	// (obter_webhook_cliente) nem os mostraria: a URL é o que cria a configuração.
	if atual == nil && pedidaURL == nil {
		return nil, errRecusa(codigoWebhookNaoConfigurado, fmt.Sprintf(
			"o cliente %q ainda não tem webhook configurado: informe a url (junto com ativo e eventos, se quiser) para criar a configuração; "+
				"ativo e eventos sozinhos não têm onde valer", c.Name))
	}
	// Só o que muda entra no plano (e na gravação): campo igual ao gravado é no-op.
	// Sem configuração gravada não se sabe os eventos de hoje, então eles sempre mudam.
	urlMuda := pedidaURL != nil && (atual == nil || *pedidaURL != atual.URL)
	ativoMuda := in.Ativo != nil && *in.Ativo != pl.ativoAtual()
	eventosMuda := pedidosEventos != nil && (atual == nil || !mesmoConjunto(atual.Events, pedidosEventos))
	if !urlMuda && !ativoMuda && !eventosMuda {
		return nil, errRecusa(codigoSemMudanca, fmt.Sprintf(
			"o webhook do cliente %q já está como foi pedido (a URL, o ativo e os eventos informados são os que ele já tem): nada a fazer", c.Name))
	}
	if urlMuda {
		// http em produção e rede interna (SSRF) — a mesma conferência da rota, com o
		// DNS e o ambiente de agora. Só a URL que muda: a que já está gravada não é
		// reaberta por um pedido que não a toca.
		if err := acoes.ValidarWebhook(ctx, catalog.UpdateWebhookInput{URL: pedidaURL}); err != nil {
			return nil, webhookEmPortugues(err)
		}
		pl.url = pedidaURL
	}
	if ativoMuda {
		v := *in.Ativo
		pl.ativo = &v
	}
	if eventosMuda {
		pl.eventos = pedidosEventos
	}
	_, pl.naFila, err = E.Leitura.Clientes.ListWebhookDeliveries(ctx, c.ID, "pending", 1, 0)
	if err != nil {
		return nil, err
	}
	return pl, nil
}

func escritaEditarWebhookCliente(cfg ConfigServidor) Escrita[entradaEditarWebhook] {
	return Escrita[entradaEditarWebhook]{
		Nome: "editar_webhook_cliente",
		Descricao: "Altera o webhook de um cliente (o modal Webhook de /clients): a URL para onde o E-monitor envia, por POST, as veiculações do cliente, se o envio está " +
			"ligado e quais eventos ele recebe (detection.confirmed, detection.retracted, webhook.test ou * para todos). É parcial: só o que for informado muda, e " +
			"eventos SUBSTITUI a lista inteira. Use para apontar o cliente para outro sistema ou desligar o envio (ativo=false; não há como apagar a URL). " +
			"As armadilhas: (1) é dado do cliente passando a sair para OUTRO sistema — por isso a URL inteira aparece no pedido, para a pessoa conferir o destino " +
			"(https obrigatório; URL de rede interna e URL com usuário e senha são recusadas); (2) o SEGREDO de assinatura não passa por aqui: nunca peça nem informe " +
			"segredo, ele se configura pela tela, e sem ele as entregas não são assinadas; (3) desligar não cancela o que já está na fila de envio, e trocar a URL " +
			"redireciona o que está na fila; (4) editar não envia nada — para conferir o destino, testar_webhook. Cliente sem webhook só é configurado se vier a url " +
			"(webhook_nao_configurado); pedido que não muda nada é recusado (sem_mudanca). O resultado é o webhook no formato de obter_webhook_cliente (a URL só " +
			"com esquema e host). " + fraseCritica,
		Nivel:   Critico,
		Motivo:  "dado do cliente passa a sair para outro sistema",
		Recurso: func(in entradaEditarWebhook) (string, *uuid.UUID) { return clienteDoRecurso(in.ClienteID) },
		Tela:    func(*Escritas, entradaEditarWebhook) *string { return linkDosClientes(cfg) },
		Preview: func(ctx context.Context, E *Escritas, in entradaEditarWebhook) (previa, error) {
			pl, err := planejarWebhook(ctx, E, in)
			if err != nil {
				return previa{}, err
			}
			pv := previa{Resumo: fmt.Sprintf("Alterar o webhook do cliente %q", pl.cliente.Name)}
			if pl.atual == nil {
				pv.Resumo = fmt.Sprintf("Configurar o webhook do cliente %q", pl.cliente.Name)
			}
			if pl.url != nil {
				var de any
				if pl.atual != nil {
					de = soEsquemaEHost(pl.atual.URL) // a URL antiga pode carregar a credencial do receptor
				}
				pv.Mudancas = append(pv.Mudancas, mudanca{Campo: "url", De: de, Para: *pl.url})
				if pl.atual == nil {
					pv.Efeitos = append(pv.Efeitos, fmt.Sprintf(
						"O cliente passa a ter webhook configurado: os eventos que ele assinar serão enviados (POST) para %s.", *pl.url))
				} else {
					pv.Efeitos = append(pv.Efeitos, fmt.Sprintf(
						"O destino do webhook muda de %s para %s: os eventos que o cliente assina passam a ser enviados (POST) para a URL nova.",
						soEsquemaEHost(pl.atual.URL), *pl.url))
				}
				pv.Efeitos = append(pv.Efeitos, "O receptor da URL nova recebe dado de veiculação do cliente (emissora, material e horário de cada tocada).")
				if pl.naFila > 0 {
					pv.Efeitos = append(pv.Efeitos, fmt.Sprintf(
						"%s na fila de envio (inclusive retentativas) saem para a URL nova: o worker lê a URL gravada no momento do envio.",
						quantasEntregas(pl.naFila)))
				}
			}
			if pl.ativo != nil {
				pv.Mudancas = append(pv.Mudancas, mudanca{Campo: "ativo", De: pl.ativoAtual(), Para: *pl.ativo})
				if *pl.ativo {
					pv.Efeitos = append(pv.Efeitos, "O envio é LIGADO: a partir da aprovação, os eventos que o cliente assina passam a ser enfileirados e enviados.")
				} else {
					pv.Efeitos = append(pv.Efeitos, "O envio é DESLIGADO: nenhuma veiculação nova é enfileirada para este cliente.")
					if pl.naFila > 0 {
						pv.Efeitos = append(pv.Efeitos, fmt.Sprintf(
							"%s na fila de envio (inclusive retentativas) ainda são enviadas: desligar impede só novos enfileiramentos.", quantasEntregas(pl.naFila)))
					}
				}
			}
			if pl.eventos != nil {
				var de any
				var antigos []string
				if pl.atual != nil {
					antigos = pl.atual.Events
					de = antigos
					if len(antigos) == 0 {
						de = "todos os eventos (lista vazia)"
					}
				}
				partes := []string{"A lista de eventos assinados é SUBSTITUÍDA pela pedida (" + strings.Join(pl.eventos, ", ") + ")."}
				if novos := diferenca(pl.eventos, antigos); len(novos) > 0 {
					partes = append(partes, "Passa a receber: "+strings.Join(novos, ", ")+".")
				}
				if saem := diferenca(antigos, pl.eventos); len(saem) > 0 {
					partes = append(partes, "Deixa de receber: "+strings.Join(saem, ", ")+".")
				}
				if pl.atual != nil && len(antigos) == 0 {
					partes = append(partes, "Hoje a lista está vazia, que assina todos os eventos.")
				}
				if slices.Contains(pl.eventos, "*") {
					partes = append(partes, "\"*\" assina todos os tipos de evento, inclusive os que forem criados no futuro.")
				}
				pv.Mudancas = append(pv.Mudancas, mudanca{Campo: "eventos", De: de, Para: pl.eventos})
				pv.Efeitos = append(pv.Efeitos, strings.Join(partes, " "))
			}
			if pl.url != nil && !pl.ativoDepois() {
				pv.Ressalvas = append(pv.Ressalvas,
					"O webhook fica desligado (ativo=false): nada é enviado para a URL nova até ele ser ligado (ativo=true).")
			}
			if pl.semSegredo() {
				pv.Ressalvas = append(pv.Ressalvas,
					"Este cliente está sem segredo configurado: as entregas não são assinadas (a assinatura sai calculada com chave vazia e o receptor não consegue "+
						"conferir a origem) — configure pela tela (/clients, modal Webhook); o segredo não passa pelo chat.")
			}
			if pl.url != nil {
				pv.Ressalvas = append(pv.Ressalvas,
					"A URL fica gravada por inteiro neste pedido (24 h) e na auditoria. Se ela carrega um token no caminho ou na consulta (?token=), prefira configurá-la pela tela (/clients, modal Webhook).",
					"Esta edição não envia nada para a URL nova: para conferir o destino use testar_webhook (outra aprovação; o cliente precisa assinar webhook.test).")
			}
			return pv, nil
		},
		Executar: func(ctx context.Context, E *Escritas, _ *Principal, in entradaEditarWebhook) (feito, error) {
			pl, err := planejarWebhook(ctx, E, in)
			if err != nil {
				return feito{}, err
			}
			// O segredo fica nil de propósito: a gravação o mantém como está.
			if _, err := E.Acoes.EditarWebhook(ctx, pl.cliente.ID, catalog.UpdateWebhookInput{URL: pl.url, Enabled: pl.ativo, Events: pl.eventos}); err != nil {
				return feito{}, webhookEmPortugues(clienteEmPortugues(err))
			}
			var ressalvas []string
			if pl.semSegredo() {
				ressalvas = append(ressalvas, "O cliente segue sem segredo configurado: as entregas não são assinadas. Configure pela tela (/clients, modal Webhook).")
			}
			return webhookFeito(ctx, cfg, E, pl.cliente, ressalvas...), nil
		},
	}
}

// quantasEntregas: "1 entrega(s)" / "2 entrega(s)" — o "(s)" é o mesmo dos outros previews.
func quantasEntregas(n int) string { return fmt.Sprintf("%d entrega(s)", n) }

// ── testar_webhook ──────────────────────────────────────────────────────

// saidaTesteWebhook é o resultado de testar_webhook. O aviso de que a entrega é
// assíncrona vai nas ressalvas (a aprovação as devolve e grava), não no resultado.
type saidaTesteWebhook struct {
	Enfileirado bool `json:"enfileirado"`
	Cliente     ref  `json:"cliente"`
}

const avisoDoTesteEnfileirado = "O teste foi enfileirado, mas a entrega é assíncrona: o worker a envia em instantes e o desfecho (delivered, failed, pending…) " +
	"só aparece depois. Confira com listar_entregas_webhook; não diga que o receptor recebeu antes de ver a entrega."

// planoDeTeste é o cliente e o webhook que o teste usaria.
type planoDeTeste struct {
	cliente *catalog.Client
	config  *catalog.WebhookConfig
}

func planejarTesteWebhook(ctx context.Context, E *Escritas, in entradaCliente) (*planoDeTeste, error) {
	c, err := lerClienteParaEscrever(ctx, E.Leitura, in.ClienteID)
	if err != nil {
		return nil, err
	}
	if E.Acoes.Webhooks == nil {
		return nil, errRecusa(codigoWebhookIndisponivel,
			"o envio de webhook não está disponível neste servidor (a fila de entregas de webhook não foi configurada): o teste não pode ser feito por aqui")
	}
	cfgWh, err := E.Leitura.Clientes.GetWebhookConfig(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	// A regra é a da outbox (acoes.MotivoDoTesteDescartado). A dica segue a ordem
	// dela: sem configuração, desligado, evento não assinado.
	if motivo := acoes.MotivoDoTesteDescartado(cfgWh); motivo != "" {
		dica := "Inclua webhook.test (ou *) nos eventos com editar_webhook_cliente (eventos)."
		switch {
		case cfgWh == nil:
			dica = "Configure a URL antes com editar_webhook_cliente (url)."
		case !cfgWh.Enabled:
			dica = "Ligue o webhook antes com editar_webhook_cliente (ativo:true)."
		}
		return nil, errRecusa(codigoWebhookTesteDescartado, fmt.Sprintf(
			"o teste de webhook do cliente %q seria descartado e nada chegaria ao receptor: %s. %s", c.Name, motivo, dica))
	}
	return &planoDeTeste{cliente: c, config: cfgWh}, nil
}

func escritaTestarWebhook(cfg ConfigServidor) Escrita[entradaCliente] {
	return Escrita[entradaCliente]{
		Nome: "testar_webhook",
		Descricao: "Enfileira um evento de teste (webhook.test) para o webhook do cliente, para validar o receptor sem esperar uma veiculação real (o botão Testar webhook " +
			"do modal de /clients). O corpo é fixo (uma mensagem de teste), sem dado de veiculação. A armadilha: a rota da tela responde sucesso mesmo quando o teste é " +
			"descartado em silêncio; esta tool não — recusa com webhook_teste_descartado e diz a condição: cliente sem URL, com o webhook desligado ou sem webhook.test " +
			"(nem *) nos eventos (corrija com editar_webhook_cliente). Quando passa, a entrega é assíncrona: o resultado é {enfileirado:true} e o desfecho só aparece " +
			"em instantes, em listar_entregas_webhook — não diga que o receptor recebeu. Sem a fila de entregas no servidor, webhook_indisponivel. " + fraseCritica,
		Nivel:   Critico,
		Motivo:  "manda um evento de teste para o sistema do cliente",
		Recurso: func(in entradaCliente) (string, *uuid.UUID) { return clienteDoRecurso(in.ClienteID) },
		Tela:    func(*Escritas, entradaCliente) *string { return linkDosClientes(cfg) },
		Preview: func(ctx context.Context, E *Escritas, in entradaCliente) (previa, error) {
			pl, err := planejarTesteWebhook(ctx, E, in)
			if err != nil {
				return previa{}, err
			}
			pv := previa{
				Resumo: fmt.Sprintf("Enviar um evento de teste (webhook.test) ao webhook do cliente %q", pl.cliente.Name),
				Efeitos: []string{
					fmt.Sprintf("Um evento webhook.test é enfileirado agora e enviado (POST) para %s.", soEsquemaEHost(pl.config.URL)),
					"O corpo é fixo (mensagem de teste, horário e environment=internal): não leva dado de veiculação do cliente.",
				},
				Ressalvas: []string{
					"O envio é assíncrono: o worker o pega em instantes e o desfecho (delivered, failed, pending…) só aparece depois, em listar_entregas_webhook ou na tela de entregas.",
					"Se o receptor não responder com sucesso, o worker repete a entrega com espera crescente.",
				},
			}
			if pl.config.Secret == "" {
				pv.Ressalvas = append(pv.Ressalvas,
					"Este cliente está sem segredo configurado: as entregas não são assinadas — configure pela tela (/clients, modal Webhook); o segredo não passa pelo chat.")
			}
			return pv, nil
		},
		Executar: func(ctx context.Context, E *Escritas, _ *Principal, in entradaCliente) (feito, error) {
			pl, err := planejarTesteWebhook(ctx, E, in)
			if err != nil {
				return feito{}, err
			}
			r, err := E.Acoes.TestarWebhook(ctx, pl.cliente.ID)
			if errors.Is(err, acoes.ErrWebhooksIndisponivel) {
				return feito{}, errRecusa(codigoWebhookIndisponivel,
					"o envio de webhook não está disponível neste servidor (a fila de entregas de webhook não foi configurada): o teste não pode ser feito por aqui")
			}
			if err != nil {
				return feito{}, clienteEmPortugues(err)
			}
			if !r.Enfileirado {
				// A configuração mudou entre a conferência e o envio: nada foi enfileirado.
				return feito{}, errRecusa(codigoWebhookTesteDescartado, fmt.Sprintf(
					"o teste de webhook do cliente %q foi descartado e nada chegou ao receptor: %s", pl.cliente.Name, r.Motivo))
			}
			return feito{
				Resultado: saidaTesteWebhook{Enfileirado: true, Cliente: refDe(pl.cliente.ID, pl.cliente.Name)},
				Recurso:   "client", RecursoID: &pl.cliente.ID,
				Ressalvas: []string{avisoDoTesteEnfileirado},
			}, nil
		},
	}
}
