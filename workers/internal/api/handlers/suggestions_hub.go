package handlers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"radiocheck/internal/auth"
	"radiocheck/internal/hub"
	"radiocheck/internal/users"
)

/*
SuggestionsHubHandler serve `/v1/internal/suggestions*` desde 2026-10-26: a fila
MORA no hub (spec do hub 2026-10-26 §4). As rotas e o JSON são os mesmos que a
tela já consumia; o que mudou é a fonte — a porta `/api/platform/sugestoes`.

O `SuggestionsHandler` de antes (Postgres) continua no código e testado, mas saiu
do router: as tabelas `suggestions*` param de receber sugestão nova e ficam como
arquivo, de onde o `import-suggestions-hub` as leva para o hub.

⚠️ Toda persona aqui é AUTOR. A triagem é só no hub, e a pessoa gestora vê na
tela daqui o aviso "A fila agora vive no hub" (`FilaNoHub.jsx`). Por isso PATCH,
summary e a URL pré-assinada respondem 410 (`Gone`).

⚠️ Sem hub configurado (sem HUB_URL/HUB_PLATFORM_KEY), toda rota responde 503
pela `ErrNotConfigured` — de propósito: gravar no Postgres em silêncio quando a
variável some faria a fila voltar a ter duas fontes sem ninguém ver.
*/

// autorLoader é o pedaço do users.Repo que este handler usa: o e-mail e o nome
// de quem está logado (o JWT daqui não carrega e-mail). Interface para o teste
// não precisar de Postgres.
type autorLoader interface {
	Get(ctx context.Context, id uuid.UUID) (*users.User, error)
}

type SuggestionsHubHandler struct {
	Hub      *hub.Client
	Users    autorLoader
	naoLidas *cacheNaoLidas
}

func NewSuggestionsHubHandler(h *hub.Client, u autorLoader) *SuggestionsHubHandler {
	return &SuggestionsHubHandler{Hub: h, Users: u, naoLidas: novoCacheNaoLidas(30 * time.Second)}
}

// O id do hub é um ObjectId. Conferir aqui faz o malformado nem sair de casa e
// mantém o `Content-Disposition` do proxy livre de texto vindo da URL.
var idDoHub = regexp.MustCompile(`^[a-f0-9]{24}$`)

// A chave de idempotência que o front gera ao abrir o formulário (o formato é o
// que o hub aceita). Sem chave válida, uma nova: o envio funciona, só não é
// reenviável sem duplicar.
var chaveDeEnvioValida = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)

func chaveDeEnvio(v string) string {
	if chaveDeEnvioValida.MatchString(v) {
		return v
	}
	return uuid.NewString()
}

const triagemNoHub = "A triagem agora é feita na Central de Clientes (hub)."

func erroJSON(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func escreveErroDoHub(w http.ResponseWriter, err error) {
	if e, ok := hub.AsError(err); ok {
		erroJSON(w, e.Status, e.Message)
		return
	}
	erroJSON(w, http.StatusInternalServerError, "erro interno")
}

// autorOu resolve quem está logado ou responde o erro. 401 só sem sessão: um 401
// por falha do banco jogaria a pessoa para o login (interceptor do axios).
func (h *SuggestionsHubHandler) autorOu(w http.ResponseWriter, r *http.Request) (hub.Autor, bool) {
	claims, ok := auth.ClaimsFromContext(r.Context())
	if !ok {
		erroJSON(w, http.StatusUnauthorized, "unauthorized")
		return hub.Autor{}, false
	}
	u, err := h.Users.Get(r.Context(), claims.UserID)
	if err != nil || u == nil || strings.TrimSpace(u.Email) == "" {
		erroJSON(w, http.StatusInternalServerError, "erro interno")
		return hub.Autor{}, false
	}
	return hub.Autor{Email: u.Email, Nome: u.Name}, true
}

func idDaRota(w http.ResponseWriter, r *http.Request, nome string) (string, bool) {
	id := chi.URLParam(r, nome)
	if !idDoHub.MatchString(id) {
		erroJSON(w, http.StatusNotFound, "sugestão não encontrada")
		return "", false
	}
	return id, true
}

// Create — POST /suggestions. Os anexos já subiram (POST /suggestions/attachments)
// e chegam por `attachment_ids`.
func (h *SuggestionsHubHandler) Create(w http.ResponseWriter, r *http.Request) {
	a, ok := h.autorOu(w, r)
	if !ok {
		return
	}
	var body struct {
		Title             string   `json:"title"`
		Description       string   `json:"description"`
		Type              string   `json:"type"`
		TargetScreen      *string  `json:"target_screen"`
		RequesterPriority string   `json:"requester_priority"`
		AttachmentIDs     []string `json:"attachment_ids"`
		ClientKey         string   `json:"client_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		erroJSON(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	errs := map[string]string{}
	if strings.TrimSpace(body.Title) == "" {
		errs["title"] = "título é obrigatório"
	}
	if strings.TrimSpace(body.Description) == "" {
		errs["description"] = "descrição é obrigatória"
	}
	if !suggestionTypes[body.Type] {
		errs["type"] = "tipo inválido"
	}
	if !suggestionReqPriorities[body.RequesterPriority] {
		errs["requester_priority"] = "prioridade inválida"
	}
	if len(errs) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"errors": errs})
		return
	}
	var tela *string
	if body.TargetScreen != nil && strings.TrimSpace(*body.TargetScreen) != "" {
		t := strings.TrimSpace(*body.TargetScreen)
		tela = &t
	}
	s, err := h.Hub.CriarSugestao(r.Context(), a, hub.NovaSugestaoHub{
		ChaveEnvio:    chaveDeEnvio(body.ClientKey),
		Tipo:          hub.TipoParaHub(body.Type),
		Titulo:        strings.TrimSpace(body.Title),
		Descricao:     strings.TrimSpace(body.Description),
		TelaAlvo:      tela,
		UrgenciaAutor: body.RequesterPriority,
		AnexoIDs:      body.AttachmentIDs,
	})
	if err != nil {
		escreveErroDoHub(w, err)
		return
	}
	h.naoLidas.esquece(a.Email)
	writeJSON(w, http.StatusCreated, detalheDoHub(s))
}

// List — GET /suggestions. As do autor, de todas as origens (spec do hub §3.4).
func (h *SuggestionsHubHandler) List(w http.ResponseWriter, r *http.Request) {
	a, ok := h.autorOu(w, r)
	if !ok {
		return
	}
	status := r.URL.Query().Get("status")
	if !suggestionStatuses[status] && status != "retirada" {
		status = "" // fora do enum é descartado, nunca repassado
	}
	itens, err := h.Hub.ListarSugestoes(r.Context(), a, status)
	if err != nil {
		escreveErroDoHub(w, err)
		return
	}
	out := make([]sugestaoHubDTO, 0, len(itens))
	for _, it := range itens {
		out = append(out, linhaDoHub(it))
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}

// Get — GET /suggestions/{id}. O hub dá 404 se não for do autor. Não marca
// como lida (a tela chama /read ao abrir).
func (h *SuggestionsHubHandler) Get(w http.ResponseWriter, r *http.Request) {
	a, ok := h.autorOu(w, r)
	if !ok {
		return
	}
	id, ok := idDaRota(w, r, "id")
	if !ok {
		return
	}
	s, err := h.Hub.DetalheSugestao(r.Context(), a, id)
	if err != nil {
		escreveErroDoHub(w, err)
		return
	}
	writeJSON(w, http.StatusOK, detalheDoHub(s))
}

// AddComment — POST /suggestions/{id}/comments. Texto, anexos (já subidos) ou
// os dois; vazio dos dois é 422.
func (h *SuggestionsHubHandler) AddComment(w http.ResponseWriter, r *http.Request) {
	a, ok := h.autorOu(w, r)
	if !ok {
		return
	}
	id, ok := idDaRota(w, r, "id")
	if !ok {
		return
	}
	var body struct {
		Body          string   `json:"body"`
		AttachmentIDs []string `json:"attachment_ids"`
		ClientKey     string   `json:"client_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		erroJSON(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	corpo := strings.TrimSpace(body.Body)
	if corpo == "" && len(body.AttachmentIDs) == 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"errors": map[string]string{"body": "mensagem vazia"}})
		return
	}
	s, err := h.Hub.ComentarSugestao(r.Context(), a, id, hub.NovoComentarioHub{
		ChaveEnvio: chaveDeEnvio(body.ClientKey), Corpo: corpo, AnexoIDs: body.AttachmentIDs,
	})
	if err != nil {
		escreveErroDoHub(w, err)
		return
	}
	h.naoLidas.esquece(a.Email)
	writeJSON(w, http.StatusCreated, detalheDoHub(s))
}

// imagemDoFormulario lê o campo `file` com as MESMAS regras de antes (imagem,
// 10 MB). O hub aceita mais tipos; esta tela continua só com imagem.
func imagemDoFormulario(w http.ResponseWriter, r *http.Request) (nome, mime string, dados []byte, ok bool) {
	r.Body = http.MaxBytesReader(w, r.Body, suggestionAttachmentMaxBytes+(1<<20))
	if err := r.ParseMultipartForm(4 << 20); err != nil {
		erroJSON(w, http.StatusBadRequest, "envio inválido (limite 10MB)")
		return "", "", nil, false
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		erroJSON(w, http.StatusBadRequest, "campo 'file' obrigatório")
		return "", "", nil, false
	}
	defer file.Close()
	mime = strings.ToLower(strings.TrimSpace(header.Header.Get("Content-Type")))
	if _, aceito := suggestionImageMIME[mime]; !aceito {
		erroJSON(w, http.StatusUnsupportedMediaType, "formato não suportado (use png, jpeg, webp ou gif)")
		return "", "", nil, false
	}
	dados, err = io.ReadAll(io.LimitReader(file, suggestionAttachmentMaxBytes+1))
	if err != nil {
		erroJSON(w, http.StatusBadRequest, "não consegui ler o arquivo")
		return "", "", nil, false
	}
	if len(dados) > suggestionAttachmentMaxBytes {
		erroJSON(w, http.StatusRequestEntityTooLarge, "imagem acima de 10MB")
		return "", "", nil, false
	}
	return header.Filename, mime, dados, true
}

// StageAttachment — POST /suggestions/attachments. Sobe a imagem "sem dono" no
// hub e devolve `{id}`; a criação ou o comentário a amarram por
// `attachment_ids`. É o desenho que mata o defeito nº 1 do módulo antigo
// (upload falha depois de criar → reenviar duplica).
func (h *SuggestionsHubHandler) StageAttachment(w http.ResponseWriter, r *http.Request) {
	a, ok := h.autorOu(w, r)
	if !ok {
		return
	}
	nome, mime, dados, ok := imagemDoFormulario(w, r)
	if !ok {
		return
	}
	id, err := h.Hub.SubirAnexoSugestao(r.Context(), a, nome, mime, dados)
	if err != nil {
		escreveErroDoHub(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": id})
}

// UploadAttachment — POST /suggestions/{id}/attachments, a rota de antes. A
// tela nova não a chama mais; continua viva para quem a chamar (spec do hub
// §4.3): no hub, anexo sempre pertence a uma fala, então a imagem vira uma fala
// só com ela.
func (h *SuggestionsHubHandler) UploadAttachment(w http.ResponseWriter, r *http.Request) {
	a, ok := h.autorOu(w, r)
	if !ok {
		return
	}
	id, ok := idDaRota(w, r, "id")
	if !ok {
		return
	}
	nome, mime, dados, ok := imagemDoFormulario(w, r)
	if !ok {
		return
	}
	anexo, err := h.Hub.SubirAnexoSugestao(r.Context(), a, nome, mime, dados)
	if err != nil {
		escreveErroDoHub(w, err)
		return
	}
	if _, err := h.Hub.ComentarSugestao(r.Context(), a, id, hub.NovoComentarioHub{
		ChaveEnvio: uuid.NewString(), AnexoIDs: []string{anexo},
	}); err != nil {
		escreveErroDoHub(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": anexo})
}

// ProxyAttachment — GET /suggestions/attachments/{aid}. Os BYTES passam pela API
// (com JWT), nunca a URL assinada: é o incidente de 2026-07-13 (host do bucket
// inalcançável pelo navegador) e o motivo do proxy do áudio de evidência.
func (h *SuggestionsHubHandler) ProxyAttachment(w http.ResponseWriter, r *http.Request) {
	a, ok := h.autorOu(w, r)
	if !ok {
		return
	}
	aid, ok := idDaRota(w, r, "aid")
	if !ok {
		return
	}
	corpo, ct, err := h.Hub.AbrirAnexoSugestao(r.Context(), a, aid)
	if err != nil {
		escreveErroDoHub(w, err)
		return
	}
	defer corpo.Close()
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Disposition", `inline; filename="`+aid+`"`)
	w.Header().Set("Cache-Control", "private, max-age=300")
	_, _ = io.Copy(w, corpo) // streaming; cliente que desconecta no meio não tem o que receber
}

// MarkRead — POST /suggestions/{id}/read.
func (h *SuggestionsHubHandler) MarkRead(w http.ResponseWriter, r *http.Request) {
	a, ok := h.autorOu(w, r)
	if !ok {
		return
	}
	id, ok := idDaRota(w, r, "id")
	if !ok {
		return
	}
	if _, err := h.Hub.MarcarSugestaoLida(r.Context(), a, id); err != nil {
		escreveErroDoHub(w, err)
		return
	}
	h.naoLidas.esquece(a.Email)
	w.WriteHeader(http.StatusNoContent)
}

// UnreadCount — GET /suggestions/unread-count, com o cache de 30 s. Erro do hub
// é erro: devolver `{count:0}` apagaria o badge de quem tem novidade (zero não
// é ausência). O react-query mantém o último valor bom.
func (h *SuggestionsHubHandler) UnreadCount(w http.ResponseWriter, r *http.Request) {
	a, ok := h.autorOu(w, r)
	if !ok {
		return
	}
	if n, ok := h.naoLidas.pega(a.Email); ok {
		writeJSON(w, http.StatusOK, map[string]int{"count": n})
		return
	}
	n, err := h.Hub.SugestoesNaoLidas(r.Context(), a)
	if err != nil {
		escreveErroDoHub(w, err)
		return
	}
	h.naoLidas.guarda(a.Email, n)
	writeJSON(w, http.StatusOK, map[string]int{"count": n})
}

// Gone — PATCH /suggestions/{id}, GET /suggestions/summary e
// GET /suggestions/attachments/{aid}/url. A triagem e os KPIs da Central de
// Comando moram no hub; a URL pré-assinada nunca devia ir ao navegador.
func (h *SuggestionsHubHandler) Gone(w http.ResponseWriter, _ *http.Request) {
	erroJSON(w, http.StatusGone, triagemNoHub)
}
