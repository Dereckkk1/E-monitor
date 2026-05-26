package handlers

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"radiocheck/internal/auth"
	"radiocheck/internal/catalog"
)

// InsightsRepo é a dependência mínima do handler. Permite mockar
// catalog.Insights nos testes sem precisar de pool real.
type InsightsRepo interface {
	Compute(ctx context.Context, p catalog.InsightsParams) (*catalog.InsightsPayload, error)
}

type InsightsHandler struct {
	Repo InsightsRepo
}

func NewInsightsHandler(repo InsightsRepo) *InsightsHandler {
	return &InsightsHandler{Repo: repo}
}

// Get GET /insights
//
// Query params:
//   - client_id (uuid): obrigatório para admin; ignorado quando o JWT
//     traz scope de viewer (forçado pelo scope — anti-oracle).
//   - campaigns (csv de uuids): obrigatório, min 1, max 50.
//   - from, to (YYYY-MM-DD): opcional. Default = mês corrente.
//   - stations (csv de uuids): opcional. Vazio = todas.
//
// Resposta: catalog.InsightsPayload (JSON).
//
// Erros 403 quando uma das campanhas pedidas não pertence ao client_id
// resolvido (cross-client). Esse caminho é o que protege cliente B de
// ler dados do cliente A passando apenas o uuid.
func (h *InsightsHandler) Get(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	scope := auth.ClientScopeFromContext(r.Context())

	// client_id — forçado pelo JWT quando o usuário é viewer
	var clientID uuid.UUID
	if scope != nil {
		clientID = *scope
	} else {
		cid := q.Get("client_id")
		if cid == "" {
			http.Error(w, "client_id required", http.StatusBadRequest)
			return
		}
		parsed, err := uuid.Parse(cid)
		if err != nil {
			http.Error(w, "invalid client_id", http.StatusBadRequest)
			return
		}
		clientID = parsed
	}

	// campaigns
	camps, err := parseUUIDList(q.Get("campaigns"))
	if err != nil {
		http.Error(w, "invalid campaigns: "+err.Error(), http.StatusBadRequest)
		return
	}
	if len(camps) == 0 {
		http.Error(w, "campaigns required (csv of uuids, min 1)", http.StatusBadRequest)
		return
	}
	if len(camps) > 50 {
		http.Error(w, "campaigns max=50", http.StatusBadRequest)
		return
	}

	// stations (opcional)
	stations, err := parseUUIDList(q.Get("stations"))
	if err != nil {
		http.Error(w, "invalid stations: "+err.Error(), http.StatusBadRequest)
		return
	}
	if stations == nil {
		// pgx envia '{}' quando o slice é vazio. Garante array não-nil pra
		// que a SQL `$4::uuid[] = '{}'` funcione como flag de "sem filtro".
		stations = []uuid.UUID{}
	}

	// from / to — default = mês corrente
	now := time.Now().UTC()
	defaultFrom := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	defaultTo := defaultFrom.AddDate(0, 1, 0).Add(-time.Second)
	from := parseDateOr(q.Get("from"), defaultFrom)
	to := parseDateOr(q.Get("to"), defaultTo)
	if !to.After(from) {
		http.Error(w, "to must be after from", http.StatusBadRequest)
		return
	}

	out, err := h.Repo.Compute(r.Context(), catalog.InsightsParams{
		ClientID:    clientID,
		CampaignIDs: camps,
		From:        from,
		To:          to,
		StationIDs:  stations,
	})
	if err != nil {
		// Erros "cross-client" do repo viram 403 — não vazamos detalhe.
		if strings.Contains(err.Error(), "cross-client") {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// parseUUIDList aceita uma csv ("a,b,c") e devolve a lista parseada.
// String vazia → (nil, nil). Espaço em volta dos items é tolerado.
func parseUUIDList(s string) ([]uuid.UUID, error) {
	if s == "" {
		return nil, nil
	}
	parts := strings.Split(s, ",")
	out := make([]uuid.UUID, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		id, err := uuid.Parse(p)
		if err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, nil
}

func parseDateOr(s string, def time.Time) time.Time {
	if s == "" {
		return def
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return def
	}
	return t
}
