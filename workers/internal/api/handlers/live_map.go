package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"radiocheck/internal/auth"
	"radiocheck/internal/catalog"
)

// liveMapMaxCampaigns é o teto de campanhas por requisição. Bate com o
// pageSize=200 que a tela usa pra listar campanhas — é o máximo que o seletor
// consegue oferecer — e evita URL/plano de query absurdos.
const liveMapMaxCampaigns = 200

// LiveMapRepo é a dependência mínima do handler. Mockável nos testes sem pool
// real (mesmo padrão de InsightsHandler).
type LiveMapRepo interface {
	Get(ctx context.Context, campaignIDs []uuid.UUID, scopes []uuid.UUID, opts catalog.LiveMapOpts) (catalog.LiveMapResult, error)
}

type LiveMapHandler struct {
	Repo LiveMapRepo
}

func NewLiveMapHandler(repo LiveMapRepo) *LiveMapHandler {
	return &LiveMapHandler{Repo: repo}
}

// Get GET /live-map?campaigns=UUID[,UUID...] — emissoras-alvo das campanhas
// (com coordenada, unidas sem repetir) + as veiculações delas. Viewer fica
// restrito às campanhas da própria carteira via auth.ClientScopesFromContext
// (404 anti-oracle quando alguma campanha é de outro cliente).
//
// `campaigns` (csv) é a forma multi, mesmo nome/formato de /insights e
// /management. `campaign_id` (uuid único) continua aceito — é o que o
// pós-venda manda, e ele fecha o documento de UMA campanha.
//
// include_terminal=1 pede o mapa mesmo de campanha cancelada. Quem usa é o
// pós-venda, que é documento histórico — a tela ao vivo não manda o parâmetro
// e continua vendo 404. O recorte por cliente vale igual nos dois casos.
func (h *LiveMapHandler) Get(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	campaignIDs, err := parseUUIDList(q.Get("campaigns"))
	if err != nil {
		http.Error(w, "invalid campaigns: "+err.Error(), http.StatusBadRequest)
		return
	}
	if cid := q.Get("campaign_id"); cid != "" {
		campaignID, err := uuid.Parse(cid)
		if err != nil {
			http.Error(w, "invalid campaign_id", http.StatusBadRequest)
			return
		}
		campaignIDs = append(campaignIDs, campaignID)
	}
	if len(campaignIDs) == 0 {
		http.Error(w, "campaigns required", http.StatusBadRequest)
		return
	}
	if len(campaignIDs) > liveMapMaxCampaigns {
		http.Error(w, "campaigns max=200", http.StatusBadRequest)
		return
	}

	scope := auth.ClientScopesFromContext(r.Context())
	opts := catalog.LiveMapOpts{
		IncludeTerminal: q.Get("include_terminal") == "1",
	}
	out, err := h.Repo.Get(r.Context(), campaignIDs, scope, opts)
	if err != nil {
		if errors.Is(err, catalog.ErrCampaignNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
