// post_sale.go — rotas admin do Pós-venda.
//
// O gating de role mora no router (RequireRole("admin")), como no resto do
// repo; aqui tratamos parsing, tradução de erro e serialização.
//
// Ver docs/features/post-sale.md.
package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"radiocheck/internal/auth"
	"radiocheck/internal/postsale"
)

type PostSaleHandler struct {
	svc *postsale.Service
	log *zap.Logger
}

func NewPostSaleHandler(svc *postsale.Service, log *zap.Logger) *PostSaleHandler {
	if log == nil {
		log = zap.NewNop()
	}
	return &PostSaleHandler{svc: svc, log: log}
}

// fail traduz os erros do serviço pro HTTP. Mantém 404/409 fora do balde 500
// pra que a UI consiga reagir (o wizard mostra mensagem própria em cada um).
func (h *PostSaleHandler) fail(w http.ResponseWriter, err error, op string) {
	switch {
	case errors.Is(err, postsale.ErrNotFound):
		http.Error(w, "not_found", http.StatusNotFound)
	case errors.Is(err, postsale.ErrAlreadySent):
		http.Error(w, "already_sent", http.StatusConflict)
	default:
		h.log.Error("postsale: "+op, zap.Error(err))
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

func pathUUID(w http.ResponseWriter, r *http.Request, key string) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, key))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return uuid.Nil, false
	}
	return id, true
}

// List devolve a listagem de /admin/pos-venda.
func (h *PostSaleHandler) List(w http.ResponseWriter, r *http.Request) {
	items, err := h.svc.Repo().List(r.Context())
	if err != nil {
		h.fail(w, err, "list")
		return
	}
	writeJSON(w, http.StatusOK, items)
}

// Get devolve um relatório com blocos e destinatários.
func (h *PostSaleHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	rep, err := h.svc.Repo().Get(r.Context(), id)
	if err != nil {
		h.fail(w, err, "get")
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

type createPostSaleRequest struct {
	ClientID     uuid.UUID `json:"client_id"`
	Title        string    `json:"title"`
	IntroMessage string    `json:"intro_message"`
}

// Create abre o rascunho (passo 1 do wizard).
func (h *PostSaleHandler) Create(w http.ResponseWriter, r *http.Request) {
	var in createPostSaleRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if in.ClientID == uuid.Nil {
		http.Error(w, "client_id is required", http.StatusBadRequest)
		return
	}
	if in.Title == "" {
		http.Error(w, "title is required", http.StatusBadRequest)
		return
	}

	var by *uuid.UUID
	if claims, ok := auth.ClaimsFromContext(r.Context()); ok {
		id := claims.UserID
		by = &id
	}
	rep, err := h.svc.Repo().CreateDraft(r.Context(), postsale.CreateDraftInput{
		ClientID:     in.ClientID,
		Title:        in.Title,
		IntroMessage: in.IntroMessage,
		CreatedBy:    by,
	})
	if err != nil {
		h.fail(w, err, "create")
		return
	}
	writeJSON(w, http.StatusCreated, rep)
}

type updatePostSaleRequest struct {
	Title        *string `json:"title"`
	IntroMessage *string `json:"intro_message"`
	// ClientID permite trocar o cliente do rascunho. Sem isso, voltar ao passo 1
	// e escolher outro cliente mantinha o rascunho no cliente antigo em silêncio,
	// e o preview quebrava depois (campanha de um cliente, relatório de outro).
	ClientID *uuid.UUID `json:"client_id"`
	Blocks   []struct {
		CampaignID   uuid.UUID             `json:"campaign_id"`
		PeriodFrom   string                `json:"period_from"` // YYYY-MM-DD
		PeriodTo     string                `json:"period_to"`
		Position     int                   `json:"position"`
		CheckingText string                `json:"checking_text"`
		CheckingRows []postsale.StationRow `json:"checking_rows"`
		// CheckingEdited: o front manda true a partir do momento em que o admin
		// mexe nas linhas. false = "derive do banco" (ver migration 0059).
		CheckingEdited bool                  `json:"checking_edited"`
		KPIOverrides   postsale.KPIOverrides `json:"kpi_overrides"`
	} `json:"blocks"`
}

// Update grava o que o admin editou nos passos 2 e 3. Só em draft — depois de
// enviado, o que o cliente leu não muda (o serviço devolve 404 nesse caso).
func (h *PostSaleHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	var in updatePostSaleRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	repo := h.svc.Repo()
	current, err := repo.Get(r.Context(), id)
	if err != nil {
		h.fail(w, err, "update: get")
		return
	}

	// Trocar de cliente descarta os blocos: campanha de outro cliente no mesmo
	// relatório é justamente o que o buildBlock recusa.
	if in.ClientID != nil && *in.ClientID != current.ClientID {
		if err := repo.UpdateClient(r.Context(), id, *in.ClientID); err != nil {
			h.fail(w, err, "update: client")
			return
		}
		if err := repo.ReplaceBlocks(r.Context(), id, nil); err != nil {
			h.fail(w, err, "update: reset blocks")
			return
		}
	}

	if in.Title != nil || in.IntroMessage != nil {
		title := current.Title
		if in.Title != nil {
			title = *in.Title
		}
		intro := current.IntroMessage
		if in.IntroMessage != nil {
			intro = *in.IntroMessage
		}
		if err := repo.UpdateContent(r.Context(), id, title, intro); err != nil {
			h.fail(w, err, "update: content")
			return
		}
	}

	if in.Blocks != nil {
		blocks := make([]postsale.BlockRow, 0, len(in.Blocks))
		for _, b := range in.Blocks {
			from, err := time.Parse("2006-01-02", b.PeriodFrom)
			if err != nil {
				http.Error(w, "invalid period_from (use YYYY-MM-DD)", http.StatusBadRequest)
				return
			}
			to, err := time.Parse("2006-01-02", b.PeriodTo)
			if err != nil {
				http.Error(w, "invalid period_to (use YYYY-MM-DD)", http.StatusBadRequest)
				return
			}
			if to.Before(from) {
				http.Error(w, "period_to must be >= period_from", http.StatusBadRequest)
				return
			}
			blocks = append(blocks, postsale.BlockRow{
				CampaignID:     b.CampaignID,
				From:           from,
				To:             to,
				Position:       b.Position,
				CheckingText:   b.CheckingText,
				CheckingRows:   b.CheckingRows,
				CheckingEdited: b.CheckingEdited,
				KPIOverrides:   b.KPIOverrides,
			})
		}
		if err := repo.ReplaceBlocks(r.Context(), id, blocks); err != nil {
			h.fail(w, err, "update: blocks")
			return
		}
	}

	rep, err := repo.Get(r.Context(), id)
	if err != nil {
		h.fail(w, err, "update: reload")
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

// Preview calcula o documento ao vivo, com a MESMA função que o publish congela.
// É o que garante que o admin aprova exatamente o que o cliente vai ver.
func (h *PostSaleHandler) Preview(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	payload, err := h.svc.Preview(r.Context(), id)
	if err != nil {
		h.fail(w, err, "preview")
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

// Recipients lista quem receberá o pós-venda (usuários ativos do cliente).
// O wizard mostra isso já no passo 1 pra transparência do disparo.
func (h *PostSaleHandler) Recipients(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	rep, err := h.svc.Repo().Get(r.Context(), id)
	if err != nil {
		h.fail(w, err, "recipients: get")
		return
	}
	people, err := h.svc.Repo().ActiveClientUsers(r.Context(), rep.ClientID)
	if err != nil {
		h.fail(w, err, "recipients")
		return
	}
	out := make([]map[string]string, 0, len(people))
	for _, p := range people {
		out = append(out, map[string]string{"email": p.Email, "name": p.Name})
	}
	writeJSON(w, http.StatusOK, out)
}

// maxCaptureBytes limita o upload das capturas. Dois PNGs de página inteira em
// scale 2 dão ~2-4 MB cada; 12 MB cobre com folga sem virar vetor de abuso.
const maxCaptureBytes = 12 << 20

// UploadAssets recebe os PNGs que o browser do admin capturou (/live-map e
// /insights renderizados offscreen). Ficam em memória no serviço até o publish
// montar o zip.
func (h *PostSaleHandler) UploadAssets(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	campaignID, err := uuid.Parse(r.URL.Query().Get("campaign_id"))
	if err != nil {
		http.Error(w, "invalid campaign_id", http.StatusBadRequest)
		return
	}
	if err := r.ParseMultipartForm(maxCaptureBytes); err != nil {
		http.Error(w, "invalid multipart form", http.StatusBadRequest)
		return
	}
	mapPNG, err := readUpload(r, "map_png")
	if err != nil {
		http.Error(w, "map_png is required", http.StatusBadRequest)
		return
	}
	insightsPNG, err := readUpload(r, "insights_png")
	if err != nil {
		http.Error(w, "insights_png is required", http.StatusBadRequest)
		return
	}
	if err := h.svc.SetAssetsBytes(r.Context(), id, campaignID, mapPNG, insightsPNG); err != nil {
		h.fail(w, err, "upload assets")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func readUpload(r *http.Request, field string) ([]byte, error) {
	f, _, err := r.FormFile(field)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, maxCaptureBytes))
}

// Publish congela e dispara. 409 quando já enviado: republicar mudaria o que o
// cliente já leu.
func (h *PostSaleHandler) Publish(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	res, err := h.svc.Publish(r.Context(), id)
	if err != nil {
		h.fail(w, err, "publish")
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// Resend reenvia o email de um destinatário. Mesmo token: reenvio é "o email
// não chegou", não "quero link novo".
func (h *PostSaleHandler) Resend(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	var in struct {
		RecipientID uuid.UUID `json:"recipient_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.RecipientID == uuid.Nil {
		http.Error(w, "recipient_id is required", http.StatusBadRequest)
		return
	}
	if err := h.svc.Resend(r.Context(), id, in.RecipientID); err != nil {
		h.fail(w, err, "resend")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// RevokeRecipient mata um link vazado sem derrubar os dos outros.
func (h *PostSaleHandler) RevokeRecipient(w http.ResponseWriter, r *http.Request) {
	rid, ok := pathUUID(w, r, "rid")
	if !ok {
		return
	}
	var by uuid.UUID
	if claims, ok := auth.ClaimsFromContext(r.Context()); ok {
		by = claims.UserID
	}
	if err := h.svc.Repo().RevokeRecipient(r.Context(), rid, by); err != nil {
		h.fail(w, err, "revoke")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
