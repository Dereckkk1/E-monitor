package handlers

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

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
}

// Get responde o que a §4.3 da spec precisa para as três caras da tela: 200 (✓
// ou ✗, a comparação de cliente é da tela, que sabe qual cliente está
// escolhido), 404 (✗ vermelho, o único caso em que o problema é do que a pessoa
// digitou) e qualquer outra coisa (⚠ âmbar, "não deu para conferir agora").
func (h *HubCodesHandler) Get(w http.ResponseWriter, r *http.Request) {
	if h.Hub == nil || !h.Hub.Configured() {
		http.Error(w, "integração com a Central de Clientes não está configurada",
			http.StatusServiceUnavailable)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), prazoDeConferencia)
	defer cancel()

	doHub, err := h.Hub.ConferirCodigo(ctx, chi.URLParam(r, "code"))
	if err != nil {
		/* O status vem do `ConferirCodigo` e desce CRU, porque o status É a
		   tela. Ele já traduziu o eixo que importa — "é problema do código que
		   a pessoa digitou (404) × é problema nosso (503)" —, e reescrevê-lo
		   aqui só poderia estragar essa tradução. */
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
	writeJSON(w, http.StatusOK, map[string]any{
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
	})
}
