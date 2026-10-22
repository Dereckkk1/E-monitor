package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"radiocheck/internal/auth"
	"radiocheck/internal/catalog"
	"radiocheck/internal/hub"
)

/*
prazoDeConferencia é o teto de espera de UMA conferência de código contra o hub
— tanto a desta rota (a tela, ao sair do campo) quanto a da barreira do `Create`.

⚠️ Existe porque o `hub.New` fixa `http.Client{Timeout: 10s}`, e esses 10s NÃO
podem ser mexidos: é o mesmo cliente do `Exchange`, que roda dentro de um login
e precisa do fôlego. Aqui é outra coisa — quem espera é uma pessoa, e um hub que
aceita a conexão e não responde faz a espera inteira para chegar ao MESMO
desfecho que dois segundos já dariam: âmbar, segue, o servidor confere de novo
ao salvar. Medido em 2026-09-21: 10,0 s no POST, devolvendo o mesmo 201.

E os dois cenários mais prováveis de lentidão são justamente os que não melhoram
esperando — 401 por chave errada ou produto desmarcado, e 429 pelo balde por IP
que esta rota divide com o job de reemissão de 15 em 15 minutos.
*/
const prazoDeConferencia = 2 * time.Second

/*
HubCodesHandler repassa a conferência de código do front para o hub.

⚠️ Existe SÓ para a chave da plataforma não descer para o navegador. Uma chamada
direta do front ao hub publicaria a chave para qualquer pessoa com o DevTools
aberto — e essa mesma chave abre a porta de eventos do hub, ou seja, vazá-la não
é "ler campanha alheia", é escrever no hub em nome desta plataforma.

⚠️ E é por isso que a rota NÃO mora sob `/v1/internal/hub` — aquele sub-router é
guardado pelo `RequireHubKeyScoped`, a porta por onde o HUB entra aqui. Montada
lá, ela exigiria do navegador exatamente a chave que existe para esconder.
*/
type HubCodesHandler struct {
	Hub *hub.Client
	// Ligacoes nil = conferência sem o estado da ligação (instalação sem
	// banco nos testes antigos). Em produção é sempre preenchido (main.go).
	Ligacoes *catalog.Ligacoes
	// Campanhas reemite, logo depois de ligar, o que estava na fila do job de
	// 15 minutos. nil = espera o job.
	Campanhas *catalog.Campaigns
}

// Get responde o que a §4.3 da spec precisa para as três caras da tela: 200 (✓
// ou ✗, a comparação de cliente é da tela, que sabe qual cliente está
// escolhido), 404 (✗ vermelho, o único caso em que o problema é do que a pessoa
// digitou) e qualquer outra coisa (⚠ âmbar, "não deu para conferir agora").
func (h *HubCodesHandler) Get(w http.ResponseWriter, r *http.Request) {
	/* ⚠️ SEM guarda de `h.Hub == nil || !Configured()` aqui, e a ausência é
	   medida, não esquecimento. O `ConferirCodigo` já começa por
	   `if !c.Configured()` e devolve `hub.ErrNotConfigured` — 503, com esta
	   mesma mensagem —, e o `Configured()` é nil-safe (`c != nil && …`). Uma
	   guarda aqui ficava idêntica byte a byte: apagá-la não mudava NENHUMA
	   resposta, o que a tornava código que dois testes diziam guardar e não
	   guardavam. Pior, ela duplicava a string da mensagem, e duas fontes para o
	   mesmo texto divergem na primeira vez que alguém reescreve uma.

	   Quem prende o comportamento são o `TestHubCodesSemHubConfigurado` e o
	   `TestHubCodesHubNilNaoPanica`. */
	ctx, cancel := context.WithTimeout(r.Context(), prazoDeConferencia)
	defer cancel()

	doHub, err := h.Hub.ConferirCodigo(ctx, chi.URLParam(r, "code"))
	if err != nil {
		/* O status vem do `ConferirCodigo` e desce CRU.

		   ⚠️ E são QUATRO, não dois — o contrato da tela depende de saber disto:

		     404  o código não existe, ou nem é código  → ✗ VERMELHO
		     503  hub mudo, 401, 429, 5xx, não configurado → ⚠ âmbar
		     502  respondeu 200 com corpo que não dá para usar → ⚠ âmbar
		     500  `HUB_URL` malformada                  → ⚠ âmbar

		   Por isso a regra do front é **"404 é vermelho; QUALQUER outro não-200
		   é âmbar"**, e nunca `status === 503 ? âmbar : erro`. O 502 é
		   exatamente a assinatura da falha mais provável do dia a dia — o hub
		   mudando o envelope da resposta, ou um proxy respondendo 200 com JSON
		   próprio —, e tratá-lo como erro duro pintaria de vermelho um cadastro
		   perfeito, que é o contrário da decisão 2. */
		var e *hub.Error
		if errors.As(err, &e) {
			http.Error(w, e.Message, e.Status)
			return
		}
		http.Error(w, "não deu para conferir o código agora", http.StatusServiceUnavailable)
		return
	}

	/* Repassa só o que a TELA precisa. `hubCampaignId` e `cliente.id` são ids do
	   hub e não servem para nada aqui — mandá-los seria pôr identificador de
	   outro sistema dentro do navegador sem motivo, e convidar a próxima pessoa
	   a construir sobre um id que este repositório não controla. */
	resp := map[string]any{
		// A forma CANÔNICA do que foi consultado. Vai junto para a tela poder
		// mostrar o que vai ser GRAVADO: quem digita escreve `eh7k4m2x` e a
		// coluna guarda `EH-7K4M2X` (§10). Sem isto, a tela mostra uma string e
		// o banco guarda outra.
		"codigo": doHub.Codigo,
		"nome":   doHub.Nome,
		"inicio": doHub.Inicio,
		"fim":    doHub.Fim,
		"cliente": map[string]any{
			"nome": doHub.Cliente.Nome,
			// É com ele que a tela decide entre o ✓ verde e o ✗ vermelho de
			// "é de outro cliente". Vazio = o cliente do hub ainda não foi
			// ligado a este E-monitor, que é estado normal e não é erro.
			"idNaPlataforma": doHub.Cliente.IDNaPlataforma,
		},
	}
	/* O estado da ligação (spec 2026-10-22 §3.1). Calculado AQUI e não na tela:
	   a barreira do salvar usa a mesma função, e o id do cliente do hub — que
	   a regra precisa — não desce para o navegador. */
	if lig := h.ligacaoPara(r.Context(), r.URL.Query().Get("client_id"), doHub); lig != nil {
		resp["ligacao"] = lig
	}
	writeJSON(w, http.StatusOK, resp)
}

/*
ligacaoPara calcula o estado da ligação para a conferência. nil = a resposta
segue sem o campo: sem banco, `client_id` ausente ou inválido, cliente que não
existe ou fora do escopo de quem pergunta, ou banco lento demais.
*/
func (h *HubCodesHandler) ligacaoPara(ctx context.Context, clientID string, doHub *hub.CampanhaDoHub) *catalog.Ligacao {
	if h.Ligacoes == nil {
		return nil
	}
	id, err := uuid.Parse(clientID)
	if err != nil {
		return nil
	}
	if !auth.ScopeAllows(ctx, id) {
		return nil
	}
	// O mesmo teto da conferência: quem espera é uma pessoa saindo do campo.
	ctx, cancel := context.WithTimeout(ctx, prazoDeConferencia)
	defer cancel()
	f, err := h.Ligacoes.Fatos(ctx, id, doHub.Cliente.ID, doHub.Cliente.IDNaPlataforma)
	if err != nil {
		// Cliente que não existe é resposta, não falha: não polui o log.
		if !errors.Is(err, pgx.ErrNoRows) {
			zap.L().Warn("conferência: não deu para calcular a ligação",
				zap.String("cliente", id.String()), zap.Error(err))
		}
		return nil
	}
	l := catalog.EstadoDaLigacao(*f, doHub.Cliente.ID, doHub.Cliente.Nome)
	return &l
}

// respostaLigacao escreve `{"ligacao": ...}` — o único formato que a tela lê.
func respostaLigacao(w http.ResponseWriter, status int, l catalog.Ligacao) {
	writeJSON(w, status, map[string]any{"ligacao": l})
}

// statusDoEstado: 409 só para bloqueado; qualquer outro estado a tela pinta.
func statusDoEstado(l catalog.Ligacao) int {
	if l.Estado == catalog.EstadoBloqueado {
		return http.StatusConflict
	}
	return http.StatusOK
}

// Banco lento (ou pedido abandonado): é "não deu agora", não erro interno.
func estourouOPrazo(ctx context.Context, err error) bool {
	return errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil
}

var ligacaoSemPrazo = catalog.Ligacao{
	Estado:   catalog.EstadoFaltaLigar,
	Mensagem: "O E-monitor demorou para responder. Aperte de novo em instantes.",
}

/*
Ligar é o botão "Ligar os dois clientes" (spec 2026-10-22 §4.1.2).

⚠️ Confere o código DE NOVO no hub em vez de confiar na tela: o cliente do hub
é o dono do código, e só o hub sabe quem é.

⚠️ O lado local é gravado ANTES de chamar o hub, e isso é proposital: se o hub
cair no meio, apertar de novo pula a transação (já ligado) e refaz só a chamada.

⚠️ Janela aceita: entre o `Fatos` e o `LigarCliente`, o cliente daqui para quem
o hub aponta (`ApontadoPeloHub`) pode ganhar uma campanha — ele não é travado
aqui. Aceito porque a janela é um clique de botão, e quem segura o id do lado
do hub é barrado pela própria checagem `id_em_uso` do hub (409).
*/
func (h *HubCodesHandler) Ligar(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var in struct {
		ClientID uuid.UUID `json:"client_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.ClientID == uuid.Nil {
		http.Error(w, "client_id é obrigatório", http.StatusBadRequest)
		return
	}
	// Hoje só admin/operator chegam aqui (rota de escrita de campanha); a
	// guarda vale para o dia em que um papel com carteira chegar. 404 e não
	// 403, como os outros checks pontuais: não confirma que o cliente existe.
	if !auth.ScopeAllows(r.Context(), in.ClientID) {
		http.Error(w, "cliente não encontrado", http.StatusNotFound)
		return
	}
	if h.Ligacoes == nil {
		http.Error(w, "ligação indisponível", http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	doHub, err := h.Hub.ConferirCodigo(ctx, chi.URLParam(r, "code"))
	if err != nil {
		var e *hub.Error
		if errors.As(err, &e) {
			http.Error(w, e.Message, e.Status)
			return
		}
		http.Error(w, "não deu para conferir o código agora", http.StatusServiceUnavailable)
		return
	}
	H, nome := doHub.Cliente.ID, doHub.Cliente.Nome
	if H == "" {
		// Sem o dono não há a quem ligar; o `LigarLocal` recusaria com 500.
		respostaLigacao(w, http.StatusServiceUnavailable, catalog.Ligacao{
			Estado:   catalog.EstadoFaltaLigar,
			Mensagem: "A Central não informou o cliente dono deste código. Tente de novo em instantes.",
		})
		return
	}

	f, err := h.Ligacoes.Fatos(ctx, in.ClientID, H, doHub.Cliente.IDNaPlataforma)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		http.Error(w, "cliente não encontrado", http.StatusNotFound)
		return
	case err != nil && estourouOPrazo(ctx, err):
		respostaLigacao(w, http.StatusServiceUnavailable, ligacaoSemPrazo)
		return
	case err != nil:
		zap.L().Warn("ligar: Fatos falhou", zap.String("cliente", in.ClientID.String()), zap.Error(err))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	estado := catalog.EstadoDaLigacao(*f, H, nome)
	switch estado.Estado {
	case catalog.EstadoLigado, catalog.EstadoBloqueado:
		respostaLigacao(w, statusDoEstado(estado), estado)
		return
	}

	var ator *uuid.UUID
	if c, ok := auth.ClaimsFromContext(r.Context()); ok {
		ator = &c.UserID
	}
	if _, err := h.Ligacoes.LigarLocal(ctx, in.ClientID, H, ator); err != nil {
		switch {
		case errors.Is(err, catalog.ErrDetentorEmUso), errors.Is(err, catalog.ErrEscolhidoDeOutro),
			errors.Is(err, catalog.ErrLigacaoConcorrente):
			/* Alguém mudou o banco entre o Fatos e a transação: relê e conta.
			   409 só se o estado relido for bloqueio — se o outro clique já
			   ligou este mesmo cliente, é "ligado" e a tela pinta verde. */
			f2, ferr := h.Ligacoes.Fatos(ctx, in.ClientID, H, doHub.Cliente.IDNaPlataforma)
			if ferr == nil {
				l := catalog.EstadoDaLigacao(*f2, H, nome)
				respostaLigacao(w, statusDoEstado(l), l)
				return
			}
			err = ferr
		case errors.Is(err, pgx.ErrNoRows):
			// Apagado entre o Fatos e a transação.
			http.Error(w, "cliente não encontrado", http.StatusNotFound)
			return
		}
		if estourouOPrazo(ctx, err) {
			respostaLigacao(w, http.StatusServiceUnavailable, ligacaoSemPrazo)
			return
		}
		zap.L().Warn("ligar: a transação local falhou", zap.String("cliente", in.ClientID.String()), zap.Error(err))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	/* Sempre avisa o hub, mesmo que ele já aponte para o escolhido: o ligar é
	   idempotente e é ele que zera o backoff do coletor lá. */
	if _, err := h.Hub.LigarCliente(ctx, doHub.Codigo, in.ClientID.String()); err != nil {
		var e *hub.Error
		if errors.As(err, &e) && e.Status == http.StatusConflict {
			respostaLigacao(w, http.StatusConflict, catalog.Ligacao{Estado: catalog.EstadoBloqueado, Mensagem: e.Message})
			return
		}
		zap.L().Warn("ligar: o hub não confirmou", zap.String("cliente", in.ClientID.String()), zap.Error(err))
		// A frase do hub vai junto: "avise o suporte" (404/401) e "aperte
		// de novo" (o resto) pedem coisas diferentes de quem está na tela.
		respostaLigacao(w, http.StatusServiceUnavailable, catalog.Ligacao{
			Estado:   catalog.EstadoFaltaLigar,
			Mensagem: "O E-monitor já está ligado; falta o hub confirmar: " + err.Error(),
		})
		return
	}

	h.reemitirPendentes(in.ClientID)
	respostaLigacao(w, http.StatusOK, catalog.Ligacao{Estado: catalog.EstadoLigado})
}

/*
reemitirPendentes manda já o que estava esperando o job de 15 minutos — as
campanhas deste cliente com código e sem confirmação do hub. Sem isto, a pessoa
liga, vê verde e a campanha só chega no próximo ciclo.

⚠️ UMA goroutine para tudo, e a resposta não espera por ela: a consulta e as
emissões, uma depois da outra. Uma goroutine por campanha viraria rajada contra
o balde por IP do hub, que esta porta divide com o job de reemissão.
*/
func (h *HubCodesHandler) reemitirPendentes(cliente uuid.UUID) {
	if h.Campanhas == nil || !h.Hub.Configured() {
		return
	}
	hb, repo := h.Hub, h.Campanhas
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		pend, err := repo.PendentesDoCliente(ctx, cliente)
		cancel()
		if err != nil {
			zap.L().Warn("ligar: não deu para ler as campanhas pendentes",
				zap.String("cliente", cliente.String()), zap.Error(err))
			return
		}
		for i := range pend {
			emitirCampanhaNoHub(hb, repo, pend[i].ID, upsertDaCampanha(&pend[i]))
		}
	}()
}
