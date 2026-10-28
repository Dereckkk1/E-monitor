package mcp

import (
	"context"
	"net/url"
	"regexp"
	"slices"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"radiocheck/internal/catalog"
)

type saidaWebhook struct {
	Cliente     ref      `json:"cliente"`
	Configurado bool     `json:"configurado"`
	URL         *string  `json:"url"`
	Ativo       bool     `json:"ativo"`
	Eventos     []string `json:"eventos"`
	TemSegredo  bool     `json:"tem_segredo"`
	Ressalvas   []string `json:"ressalvas"`
}

type entregaDTO struct {
	ID                 string  `json:"id"`
	Evento             string  `json:"evento"`
	Status             string  `json:"status"`
	Tentativas         int     `json:"tentativas"`
	CodigoResposta     *int    `json:"codigo_resposta"`
	UltimoErro         *string `json:"ultimo_erro"`
	ProximaTentativaEm string  `json:"proxima_tentativa_em"`
	EntregueEm         *string `json:"entregue_em"`
	CriadoEm           string  `json:"criado_em"`
}

type entradaEntregas struct {
	ClienteID string `json:"cliente_id" jsonschema:"Id (uuid) do cliente, vindo de listar_clientes. Nunca invente: pergunte."`
	Status    string `json:"status,omitempty" jsonschema:"pending, delivered, failed ou dead. Vazio = todas."`
	Paginacao
}

type chaveAPIDTO struct {
	ID            string  `json:"id"`
	Identificador string  `json:"identificador"`
	CriadaEm      string  `json:"criada_em"`
	UltimoUsoEm   *string `json:"ultimo_uso_em"`
}

type notificacaoDTO struct {
	Chave    string  `json:"chave"`
	Campanha ref     `json:"campanha"`
	Cliente  ref     `json:"cliente"`
	Dia      string  `json:"dia"`
	LidaEm   *string `json:"lida_em"`
}

type saidaNotificacoes struct {
	lista[notificacaoDTO]
	NaoLidas int `json:"nao_lidas"`
}

// tetoSininho: o LIMIT 50 de Notifications.List.
const tetoSininho = 50

var statusDeEntrega = []string{"", "pending", "delivered", "failed", "dead"}

// urlsNoTexto casa URLs http(s) dentro de um texto livre ou de erro, sem
// diferenciar maiúsculas ("HTTPS://" também é URL para o navegador).
var urlsNoTexto = regexp.MustCompile(`(?i)https?://[^\s"']+`)

// soEsquemaEHost reduz a URL a esquema+host: o segredo de um webhook mora na
// query (?token=) OU no path (Slack, Discord e Zapier põem o segredo no path),
// então nenhum dos dois sai. URL que não parseia vira "…".
func soEsquemaEHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "…"
	}
	return u.Scheme + "://" + u.Host + "/…"
}

// semCredencialDeURL aplica soEsquemaEHost a toda URL de um last_error: o
// webhook/worker grava o err.Error() do http.Client, que imprime a URL inteira
// do cliente.
func semCredencialDeURL(s *string) *string {
	if s == nil {
		return nil
	}
	r := urlsNoTexto.ReplaceAllStringFunc(*s, soEsquemaEHost)
	return &r
}

func registrarIntegracoes(s *sdk.Server, cfg ConfigServidor, p *Principal) {
	ferramentaLeitura(s, cfg, "obter_webhook_cliente",
		"Diz se o cliente tem webhook configurado: URL (só esquema e host, o resto pode ter credencial), se está ativo, "+
			"eventos e se há segredo. O segredo nunca sai (nem parte dele); trocar é no portal. Nunca invente o cliente_id.",
		func(ctx context.Context, L *Leitura, in entradaCliente) (*sdk.CallToolResult, error) {
			c, rec, err := lerCliente(ctx, L, in.ClienteID)
			if rec != nil || err != nil {
				return rec, err
			}
			wc, err := L.Clientes.GetWebhookConfig(ctx, c.ID)
			if err != nil {
				return nil, err
			}
			out := saidaWebhook{Cliente: refDe(c.ID, c.Name), Eventos: []string{},
				Ressalvas: []string{
					"A URL vem só como esquema+host: o caminho e a query podem carregar a credencial do webhook. A URL completa e o segredo estão no portal, onde também se configura, troca o segredo e testa.",
					"O segredo do webhook nunca sai pelo MCP."}}
			if wc != nil {
				host := soEsquemaEHost(wc.URL)
				out.Configurado, out.URL, out.Ativo, out.Eventos, out.TemSegredo = true, &host, wc.Enabled, wc.Events, wc.Secret != ""
			}
			return ok(out)
		})

	ferramentaLeitura(s, cfg, "listar_entregas_webhook",
		"Lista as entregas de webhook de um cliente, da mais nova para a mais velha: evento, status (pending, delivered, failed, "+
			"dead), tentativas, código de resposta e último erro. Use para diagnosticar 'o sistema do cliente não recebeu'. "+
			"O conteúdo enviado não vem. Nunca invente o cliente_id. Lista paginada.",
		func(ctx context.Context, L *Leitura, in entradaEntregas) (*sdk.CallToolResult, error) {
			c, rec, err := lerCliente(ctx, L, in.ClienteID)
			if rec != nil || err != nil {
				return rec, err
			}
			if !slices.Contains(statusDeEntrega, in.Status) {
				return recusa("status desconhecido: use pending, delivered, failed ou dead", "entrada_invalida")
			}
			limit, offset := in.Paginacao.normalizar()
			ents, total, err := L.Clientes.ListWebhookDeliveries(ctx, c.ID, in.Status, limit, offset)
			if err != nil {
				return nil, err
			}
			if len(ents) == 0 && offset > 0 { // COUNT(*) OVER () dá 0 além do fim
				if _, total, err = L.Clientes.ListWebhookDeliveries(ctx, c.ID, in.Status, 1, 0); err != nil {
					return nil, err
				}
			}
			linhas := make([]entregaDTO, len(ents))
			for i, e := range ents {
				linhas[i] = entregaDTO{
					ID: e.ID.String(), Evento: e.EventType, Status: e.Status, Tentativas: e.AttemptCount,
					CodigoResposta: e.ResponseCode, UltimoErro: semCredencialDeURL(e.LastError), ProximaTentativaEm: instante(e.NextAttemptAt),
					EntregueEm: instanteOpc(e.DeliveredAt), CriadoEm: instante(e.CreatedAt),
				}
			}
			return ok(novaLista(linhas, total, limit, offset))
		})

	ferramentaLeitura(s, cfg, "listar_chaves_api",
		"Lista as chaves de API ativas de um cliente (a integração /v1 dele): id, identificador (início do hash, nunca a chave), "+
			"criação e último uso. Criar ou revogar é no portal. Nunca invente o cliente_id. Lista paginada.",
		func(ctx context.Context, L *Leitura, in entradaClientePaginada) (*sdk.CallToolResult, error) {
			c, rec, err := lerCliente(ctx, L, in.ClienteID)
			if rec != nil || err != nil {
				return rec, err
			}
			chaves, err := L.Clientes.ListAPIKeys(ctx, c.ID)
			if err != nil {
				return nil, err
			}
			return ok(mapear(paginar(chaves, in.Paginacao), func(k catalog.APIKeyInfo) chaveAPIDTO {
				return chaveAPIDTO{ID: k.ID.String(), Identificador: k.KeyMasked,
					CriadaEm: instante(k.CreatedAt), UltimoUsoEm: instanteOpc(k.LastUsedAt)}
			}))
		})

	ferramentaLeitura(s, cfg, "minhas_notificacoes",
		"Devolve as notificações do sininho de quem está conectado: campanhas com déficit nos últimos 7 dias (até ontem), "+
			"com cliente, dia e se já foi lida, mais a contagem de não lidas. O sininho tem teto de 50 itens: se vier a ressalva, há mais. "+
			"Canceladas não entram. Não recebe argumentos.",
		func(ctx context.Context, L *Leitura, _ struct{}) (*sdk.CallToolResult, error) {
			if p == nil {
				return recusa("sem identidade: o pedido não passou pela autenticação do MCP", "sem_identidade")
			}
			res, err := L.Notificacoes.List(ctx, p.UserID)
			if err != nil {
				return nil, err
			}
			linhas := make([]notificacaoDTO, len(res.Items))
			for i, n := range res.Items {
				linhas[i] = notificacaoDTO{
					Chave: n.Key, Campanha: refDe(n.CampaignID, n.CampaignName), Cliente: refDe(n.ClientID, n.ClientName),
					Dia: n.OccurredOn, LidaEm: instanteOpc(n.ReadAt),
				}
			}
			out := saidaNotificacoes{lista: listaInteira(linhas), NaoLidas: res.UnreadCount}
			if len(res.Items) == tetoSininho {
				out.Ressalvas = []string{"O sininho mostra as 50 notificações mais recentes; pode haver mais, e nao_lidas conta só estas."}
			}
			return ok(out)
		})
}
