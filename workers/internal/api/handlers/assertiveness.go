package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"

	"radiocheck/internal/assertiveness"
)

// AssertivenessRepo é a dependência mínima do handler (mockável em teste).
type AssertivenessRepo interface {
	Get(ctx context.Context, now time.Time, f assertiveness.Filter) (assertiveness.Result, error)
}

// AssertivenessHandler serve GET /assertiveness — o card da Visão Gerencial.
// Admin/operator-only (gating no router, mesmo grupo do /management-overview).
type AssertivenessHandler struct {
	Repo  AssertivenessRepo
	NowFn func() time.Time
}

func NewAssertivenessHandler(repo AssertivenessRepo) *AssertivenessHandler {
	return &AssertivenessHandler{Repo: repo, NowFn: time.Now}
}

// Get aceita os MESMOS filtros de escopo do /management-overview (client_id,
// campaigns) — mas de propósito NÃO aceita from/to.
//
// A janela é sempre o mês fechado anterior. O mês em curso mentiria: no dia 3
// quase nenhuma manual daquele mês foi digitada (a emissora manda o comprovante
// depois), então o percentual nasce perto de 100% e desaba no fim do mês. Um
// número que só fica correto no último dia do mês não serve num painel.
func (h *AssertivenessHandler) Get(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	var f assertiveness.Filter
	if cid := q.Get("client_id"); cid != "" {
		parsed, err := uuid.Parse(cid)
		if err != nil {
			http.Error(w, "invalid client_id", http.StatusBadRequest)
			return
		}
		f.ClientID = &parsed
	}

	camps, err := parseUUIDList(q.Get("campaigns"))
	if err != nil {
		http.Error(w, "invalid campaigns: "+err.Error(), http.StatusBadRequest)
		return
	}
	if len(camps) > 200 {
		http.Error(w, "campaigns max=200", http.StatusBadRequest)
		return
	}
	f.CampaignIDs = camps

	now := time.Now()
	if h.NowFn != nil {
		now = h.NowFn()
	}
	res, err := h.Repo.Get(r.Context(), now, f)
	if err != nil {
		http.Error(w, "failed to load assertiveness", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
