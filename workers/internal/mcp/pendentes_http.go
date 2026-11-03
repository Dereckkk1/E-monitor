package mcp

import (
	"errors"
	"net/http"

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

const tetoListaPendentesHTTP = 50

// pendenteDTO é a vitrine do pedido: nunca args, resultado, token nem usuário.
type pendenteDTO struct {
	ID          uuid.UUID `json:"id"`
	Tool        string    `json:"tool"`
	Motivo      string    `json:"motivo"`
	Preview     previa    `json:"preview"`
	Estado      string    `json:"estado"`
	CriadaEm    string    `json:"criada_em"`
	ExpiraEm    string    `json:"expira_em"`
	AprovadaEm  *string   `json:"aprovada_em"`
	ExecutadaEm *string   `json:"executada_em"`
	RecusadaEm  *string   `json:"recusada_em"`
	Erro        *string   `json:"erro"`
}

func pendenteParaDTO(p Pendente) pendenteDTO {
	return pendenteDTO{
		ID: p.ID, Tool: p.Tool, Motivo: p.Motivo, Preview: p.Preview, Estado: p.Estado,
		CriadaEm: instante(p.CriadaEm), ExpiraEm: instante(p.ExpiraEm),
		AprovadaEm: instanteOpc(p.AprovadaEm), ExecutadaEm: instanteOpc(p.ExecutadaEm),
		RecusadaEm: instanteOpc(p.RecusadaEm), Erro: p.Erro,
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
		return "Este pedido expirou (24 h). Peça de novo à IA."
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
	if id, err := uuid.Parse(r.URL.Query().Get("id")); err == nil {
		got, err := h.Pendentes.Obter(r.Context(), id, p.UserID)
		switch {
		case err == nil:
			d := pendenteParaDTO(got)
			destaque = &d
		case errors.Is(err, ErrPendenteNaoAchada):
		default:
			h.Log.Error("mcp: obter pendente em destaque", zap.Error(err))
			erroMensagemJSON(w, http.StatusInternalServerError, "interno", "Erro interno.")
			return
		}
	}
	escreverJSON(w, http.StatusOK, map[string]any{"items": dtos, "destaque": destaque})
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

// responderErroPendente traduz os erros comuns do store e do executor.
func (h *PendentesHTTP) responderErroPendente(w http.ResponseWriter, err error) {
	var est ErrEstadoPendente
	var rec *acoes.Recusa
	switch {
	case errors.Is(err, ErrPendenteNaoAchada):
		naoEncontradoPendente(w)
	case errors.As(err, &est):
		erroMensagemJSON(w, http.StatusConflict, "pendente_"+est.Estado, textoEstadoPendente(est.Estado))
	case errors.As(err, &rec):
		erroMensagemJSON(w, http.StatusUnprocessableEntity, rec.Codigo, rec.Mensagem)
	case errors.Is(err, ErrConferenciaFalhou):
		erroMensagemJSON(w, http.StatusServiceUnavailable, "conferencia_falhou", msgConferenciaFalhou)
	case errors.Is(err, ErrExecucaoInterrompida):
		erroMensagemJSON(w, http.StatusInternalServerError, "interrompida", msgInterrompida)
	default:
		h.Log.Error("mcp: pedido pendente", zap.Error(err))
		erroMensagemJSON(w, http.StatusInternalServerError, "interno", "Erro interno.")
	}
}

func (h *PendentesHTTP) Aprovar(w http.ResponseWriter, r *http.Request) {
	ator, id, ok := h.preparar(w, r)
	if !ok {
		return
	}
	out, err := h.Aprovacoes.Aprovar(r.Context(), id, ator)
	if err != nil {
		h.responderErroPendente(w, err)
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
		h.responderErroPendente(w, err)
		return
	}
	escreverJSON(w, http.StatusOK, map[string]any{"pendente": pendenteParaDTO(pend)})
}
