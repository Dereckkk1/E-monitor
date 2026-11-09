package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"radiocheck/internal/acoes"
	"radiocheck/internal/auth"
	"radiocheck/internal/mcp/tokens"
)

// PendentesHTTP serve a fila de aprovação das escritas críticas do MCP
// (/v1/internal/mcp/pendentes, sob JWT + RequireRole admin). O pedido é sempre
// do dono: o de outra pessoa é indistinguível de um que não existe.
type PendentesHTTP struct {
	Tokens     *tokens.Store
	Pendentes  *Pendentes
	Aprovacoes *Aprovacoes // nil se o índice das escritas falhou no boot
	Log        *zap.Logger
}

const (
	tetoListaPendentesHTTP = 50
	prazoReler422          = 5 * time.Second
	// tetoCorpoAprovar: o corpo é {"tentativa": N}.
	tetoCorpoAprovar = 1 << 10
)

// pendenteDTO é a vitrine do pedido: nunca args, resultado, token nem usuário.
type pendenteDTO struct {
	ID      uuid.UUID `json:"id"`
	Tool    string    `json:"tool"`
	Motivo  string    `json:"motivo"`
	Preview previa    `json:"preview"`
	Estado  string    `json:"estado"`
	// Tentativa é a versão do pedido (conta as aprovações): quem aprova a devolve no corpo, e a
	// aprovação só vale se for a vigente. É o que impede de aprovar um resumo que a pessoa nunca leu.
	Tentativa   int     `json:"tentativa"`
	CriadaEm    string  `json:"criada_em"`
	ExpiraEm    string  `json:"expira_em"`
	AprovadaEm  *string `json:"aprovada_em"`
	ExecutadaEm *string `json:"executada_em"`
	RecusadaEm  *string `json:"recusada_em"`
	Erro        *string `json:"erro"`
	// Ressalvas são os avisos da execução ("o email não saiu: a pessoa não tem a senha"), gravados
	// ao fechar o pedido: quem recarrega /pendentes?id= os lê de novo. Sempre um array.
	Ressalvas []string `json:"ressalvas"`
	// Campanha é a que os args citam (campanha_id), com o nome atual: a tela agrupa e aprova em lote
	// por ela. null sem campanha, com campanha apagada e nas respostas de aprovar/recusar (a tela
	// mantém o que já tinha).
	Campanha *campanhaDoPedido `json:"campanha"`
}

type campanhaDoPedido struct {
	ID   uuid.UUID `json:"id"`
	Nome string    `json:"nome"`
}

// ressalvasOuVazio garante o array (nunca null) no JSON.
func ressalvasOuVazio(rs []string) []string {
	if rs == nil {
		return []string{}
	}
	return rs
}

func pendenteParaDTO(p Pendente) pendenteDTO {
	return pendenteDTO{
		ID: p.ID, Tool: p.Tool, Motivo: p.Motivo, Preview: p.Preview, Estado: p.Estado, Tentativa: p.Tentativa,
		CriadaEm: instante(p.CriadaEm), ExpiraEm: instante(p.ExpiraEm),
		AprovadaEm: instanteOpc(p.AprovadaEm), ExecutadaEm: instanteOpc(p.ExecutadaEm),
		RecusadaEm: instanteOpc(p.RecusadaEm), Erro: p.Erro, Ressalvas: ressalvasOuVazio(p.Ressalvas),
	}
}

// erroMensagemJSON é o erro com código estável e texto para a pessoa.
func erroMensagemJSON(w http.ResponseWriter, status int, codigo, mensagem string) {
	escreverJSON(w, status, map[string]string{"error": codigo, "mensagem": mensagem})
}

func naoEncontradoPendente(w http.ResponseWriter) {
	erroMensagemJSON(w, http.StatusNotFound, "nao_encontrado", "Pedido não encontrado.")
}

func textoEstadoPendente(estado string) string {
	switch estado {
	case "executada":
		return "Este pedido já foi executado."
	case "recusada":
		return "Este pedido já foi recusado."
	case "aprovada":
		return "Este pedido já está em execução."
	case "falhou":
		return "Este pedido foi interrompido; confira se a ação aconteceu."
	case "expirada":
		return "Este pedido expirou. Peça de novo à IA."
	}
	return "Este pedido já está " + estado + "."
}

// ator relê o admin no banco (o JWT carrega o role por até 8 h). Devolve false
// depois de já ter respondido.
func (h *PendentesHTTP) ator(w http.ResponseWriter, r *http.Request) (*Principal, bool) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	dono, err := h.Tokens.DonoAdminAtivo(r.Context(), claims.UserID)
	if err != nil {
		if errors.Is(err, tokens.ErrNaoEncontrado) {
			erroMensagemJSON(w, http.StatusForbidden, "proibido", "Só administrador ativo aprova pedidos do MCP.")
			return nil, false
		}
		h.Log.Error("mcp: conferir admin ativo", zap.Error(err))
		erroMensagemJSON(w, http.StatusInternalServerError, "interno", "Erro interno.")
		return nil, false
	}
	return &Principal{UserID: dono.ID, Email: dono.Email, Nome: dono.Nome}, true
}

func (h *PendentesHTTP) Listar(w http.ResponseWriter, r *http.Request) {
	p, ok := h.ator(w, r)
	if !ok {
		return
	}
	itens, _, err := h.Pendentes.Listar(r.Context(), p.UserID, []string{"pendente", "aprovada"}, tetoListaPendentesHTTP, 0)
	if err != nil {
		h.Log.Error("mcp: listar pendentes", zap.Error(err))
		erroMensagemJSON(w, http.StatusInternalServerError, "interno", "Erro interno.")
		return
	}
	dtos := make([]pendenteDTO, 0, len(itens))
	for _, it := range itens {
		dtos = append(dtos, pendenteParaDTO(it))
	}
	// destaque: o pedido do link ?id=. De outra pessoa ou malformado vira null,
	// com o mesmo 200, para a listagem não servir de oráculo.
	var destaque *pendenteDTO
	var destaquePend *Pendente
	if id, err := uuid.Parse(r.URL.Query().Get("id")); err == nil {
		got, err := h.Pendentes.Obter(r.Context(), id, p.UserID)
		switch {
		case err == nil:
			d := pendenteParaDTO(got)
			destaque = &d
			destaquePend = &got
		case errors.Is(err, ErrPendenteNaoAchada):
		default:
			h.Log.Error("mcp: obter pendente em destaque", zap.Error(err))
			erroMensagemJSON(w, http.StatusInternalServerError, "interno", "Erro interno.")
			return
		}
	}
	alvos := make([]*pendenteDTO, 0, len(dtos)+1)
	pedidos := make([]Pendente, 0, len(itens)+1)
	for i := range dtos {
		alvos = append(alvos, &dtos[i])
		pedidos = append(pedidos, itens[i])
	}
	if destaque != nil {
		alvos = append(alvos, destaque)
		pedidos = append(pedidos, *destaquePend)
	}
	h.preencherCampanhas(r.Context(), pedidos, alvos)
	escreverJSON(w, http.StatusOK, map[string]any{"items": dtos, "destaque": destaque})
}

// preencherCampanhas põe em cada DTO (alvos[i] é o de pedidos[i]) a campanha que os args citam. É
// enfeite de agrupamento: se a leitura dos nomes falhar, a lista sai sem as campanhas em vez de falhar.
func (h *PendentesHTTP) preencherCampanhas(ctx context.Context, pedidos []Pendente, alvos []*pendenteDTO) {
	citadas := make([]*uuid.UUID, len(pedidos))
	var ids []uuid.UUID
	for i, p := range pedidos {
		if c := campanhaDosArgs(p.Args); c != nil {
			citadas[i] = c
			ids = append(ids, *c)
		}
	}
	if len(ids) == 0 {
		return
	}
	nomes, err := h.Pendentes.NomesDeCampanhas(ctx, ids)
	if err != nil {
		h.Log.Warn("mcp: nomes das campanhas dos pedidos", zap.Error(err))
		return
	}
	for i, c := range citadas {
		if c == nil {
			continue
		}
		if nome, ok := nomes[*c]; ok {
			alvos[i].Campanha = &campanhaDoPedido{ID: *c, Nome: nome}
		}
	}
}

// preparar valida o admin, o id e o executor; devolve false depois de responder.
func (h *PendentesHTTP) preparar(w http.ResponseWriter, r *http.Request) (*Principal, uuid.UUID, bool) {
	if h.Aprovacoes == nil {
		erroMensagemJSON(w, http.StatusServiceUnavailable, "indisponivel", "Aprovação de pedidos indisponível no momento.")
		return nil, uuid.Nil, false
	}
	p, ok := h.ator(w, r)
	if !ok {
		return nil, uuid.Nil, false
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		naoEncontradoPendente(w)
		return nil, uuid.Nil, false
	}
	return p, id, true
}

// responderErroPendente traduz os erros comuns do store e do executor. Na recusa de negócio (422) o
// corpo leva também o `pendente` como está agora — o mesmo DTO do GET, com o preview e as ressalvas
// já atualizados (preview_desatualizado grava o resumo novo antes de responder): quem aprova lê o que
// vale agora sem recarregar. É aditivo (`error` e `mensagem` seguem iguais) e best-effort: se a
// releitura falhar, o 422 sai sem ele.
func (h *PendentesHTTP) responderErroPendente(w http.ResponseWriter, r *http.Request, id, userID uuid.UUID, err error) {
	var est ErrEstadoPendente
	var rec *acoes.Recusa
	switch {
	case errors.Is(err, ErrPendenteNaoAchada):
		naoEncontradoPendente(w)
	case errors.Is(err, ErrPendenteMudou):
		// O resumo (ou o estado) mudou desde que a tela abriu: ela relê antes de a pessoa decidir de novo.
		erroMensagemJSON(w, http.StatusConflict, "pendente_mudou", msgPendenteMudou)
	case errors.Is(err, ErrCredencialRevogada):
		// O pedido acabou de virar `expirada` (a conexão que o fez foi revogada): é o mesmo 409 de um
		// pedido que já estava expirado, com o motivo no texto.
		erroMensagemJSON(w, http.StatusConflict, "pendente_expirada", msgCredencialRevogada)
	case errors.As(err, &est):
		texto := est.Motivo
		if texto == "" {
			texto = textoEstadoPendente(est.Estado)
		}
		erroMensagemJSON(w, http.StatusConflict, "pendente_"+est.Estado, texto)
	case errors.As(err, &rec):
		corpo := map[string]any{"error": rec.Codigo, "mensagem": rec.Mensagem}
		// A releitura não herda o cancelamento do pedido: o 422 pode estar saindo depois de o
		// cliente (ou o middleware de timeout) ter desistido, e é ela que leva o resumo novo.
		rctx, cancela := context.WithTimeout(context.WithoutCancel(r.Context()), prazoReler422)
		got, gerr := h.Pendentes.Obter(rctx, id, userID)
		cancela()
		if gerr == nil {
			corpo["pendente"] = pendenteParaDTO(got)
		} else {
			h.Log.Warn("mcp: reler pendente para o 422", zap.Error(gerr))
		}
		escreverJSON(w, http.StatusUnprocessableEntity, corpo)
	case errors.Is(err, ErrConferenciaFalhou):
		erroMensagemJSON(w, http.StatusServiceUnavailable, "conferencia_falhou", msgConferenciaFalhou)
	case errors.Is(err, ErrExecucaoInterrompida):
		erroMensagemJSON(w, http.StatusInternalServerError, "interrompida", mensagemDaInterrupcao(err))
	default:
		h.Log.Error("mcp: pedido pendente", zap.Error(err))
		erroMensagemJSON(w, http.StatusInternalServerError, "interno", "Erro interno.")
	}
}

// corpoAprovar é o que a tela manda ao aprovar: a versão (tentativa) do pedido que a pessoa está lendo.
type corpoAprovar struct {
	Tentativa *int `json:"tentativa"`
}

func (h *PendentesHTTP) Aprovar(w http.ResponseWriter, r *http.Request) {
	ator, id, ok := h.preparar(w, r)
	if !ok {
		return
	}
	// Sem a versão não há como saber o que a pessoa leu: 400, e não "aprova a vigente". O único
	// cliente é a tela /pendentes, que sempre a manda.
	var c corpoAprovar
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, tetoCorpoAprovar)).Decode(&c); err != nil || c.Tentativa == nil || *c.Tentativa < 0 {
		erroMensagemJSON(w, http.StatusBadRequest, "entrada_invalida", "Falta a versão do pedido (tentativa): recarregue a página e aprove de novo.")
		return
	}
	out, err := h.Aprovacoes.Aprovar(r.Context(), id, ator, *c.Tentativa)
	if err != nil {
		h.responderErroPendente(w, r, id, ator.UserID, err)
		return
	}
	// ResultadoJSON já vem serializado (nil vira null): nada a falhar aqui.
	// Aprovado.Ressalvas nunca é nil: o array vai sempre na resposta, vazio ou não.
	escreverJSON(w, http.StatusOK, map[string]any{"pendente": pendenteParaDTO(out.Pendente), "resultado": out.ResultadoJSON, "ressalvas": out.Ressalvas})
}

func (h *PendentesHTTP) Recusar(w http.ResponseWriter, r *http.Request) {
	ator, id, ok := h.preparar(w, r)
	if !ok {
		return
	}
	pend, err := h.Aprovacoes.Recusar(r.Context(), id, ator)
	if err != nil {
		h.responderErroPendente(w, r, id, ator.UserID, err)
		return
	}
	escreverJSON(w, http.StatusOK, map[string]any{"pendente": pendenteParaDTO(pend)})
}
