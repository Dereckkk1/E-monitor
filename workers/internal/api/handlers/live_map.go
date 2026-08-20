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
	Coverage(ctx context.Context, campaignIDs []uuid.UUID, scopes []uuid.UUID, opts catalog.LiveMapOpts) (catalog.LiveCoverageResult, error)
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
	campaignIDs, opts, ok := parseLiveMapQuery(w, r)
	if !ok {
		return
	}
	out, err := h.Repo.Get(r.Context(), campaignIDs, auth.ClientScopesFromContext(r.Context()), opts)
	if err != nil {
		writeLiveMapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// GetCoverage GET /live-map/coverage?campaigns=UUID[,UUID...] — os municípios
// ao alcance estimado das emissoras-alvo, para o mapa desenhar o raio de
// cobertura e as cidades dentro dele.
//
// Endereço próprio, e não um campo a mais no /live-map, porque a resposta é
// ESTÁTICA: cobertura só muda quando a Anatel republica o plano. O /live-map
// faz polling de 20s; carregar isto junto retransmitiria dezenas de KB
// imutáveis a cada ciclo, em toda sessão aberta o dia inteiro. O frontend
// busca uma vez por seleção de campanha e cacheia.
//
// Mesmos parâmetros, mesmo recorte por cliente e mesmo 404 anti-oracle do Get.
func (h *LiveMapHandler) GetCoverage(w http.ResponseWriter, r *http.Request) {
	campaignIDs, opts, ok := parseLiveMapQuery(w, r)
	if !ok {
		return
	}
	out, err := h.Repo.Coverage(r.Context(), campaignIDs, auth.ClientScopesFromContext(r.Context()), opts)
	if err != nil {
		writeLiveMapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// parseLiveMapQuery lê os parâmetros comuns a /live-map e /live-map/coverage.
// Escreve a resposta de erro e devolve ok=false quando algo não valida — o
// chamador só precisa retornar.
func parseLiveMapQuery(w http.ResponseWriter, r *http.Request) ([]uuid.UUID, catalog.LiveMapOpts, bool) {
	q := r.URL.Query()
	var opts catalog.LiveMapOpts

	campaignIDs, err := parseUUIDList(q.Get("campaigns"))
	if err != nil {
		http.Error(w, "invalid campaigns: "+err.Error(), http.StatusBadRequest)
		return nil, opts, false
	}
	if cid := q.Get("campaign_id"); cid != "" {
		campaignID, err := uuid.Parse(cid)
		if err != nil {
			http.Error(w, "invalid campaign_id", http.StatusBadRequest)
			return nil, opts, false
		}
		campaignIDs = append(campaignIDs, campaignID)
	}
	if len(campaignIDs) == 0 {
		http.Error(w, "campaigns required", http.StatusBadRequest)
		return nil, opts, false
	}
	if len(campaignIDs) > liveMapMaxCampaigns {
		http.Error(w, "campaigns max=200", http.StatusBadRequest)
		return nil, opts, false
	}
	opts.IncludeTerminal = q.Get("include_terminal") == "1"
	return campaignIDs, opts, true
}

// writeLiveMapErr mantém o 404 anti-oracle idêntico nos dois endpoints: se um
// deles vazasse 403 ou 500 onde o outro dá 404, a diferença já revelaria a
// existência da campanha.
func writeLiveMapErr(w http.ResponseWriter, err error) {
	if errors.Is(err, catalog.ErrCampaignNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	http.Error(w, "internal error", http.StatusInternalServerError)
}
