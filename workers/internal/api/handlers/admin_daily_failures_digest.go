package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"radiocheck/internal/catalog"
)

// ─── Response types (forma leve do digest) ───────────────────────────

type digestCampaign struct {
	ID             uuid.UUID `json:"id"`
	Name           string    `json:"name"`
	ClientName     string    `json:"client_name"`
	ClientLogoURL  string    `json:"client_logo_url"`
	StationsFailed int       `json:"stations_failed"`
}

type digestSummary struct {
	Campaigns int `json:"campaigns"`
	Stations  int `json:"stations"`
}

type digestResponse struct {
	Date      string           `json:"date"`
	Seen      bool             `json:"seen"`
	Summary   digestSummary    `json:"summary"`
	Campaigns []digestCampaign `json:"campaigns"`
}

// digestFromDaily reduz o DailyResult completo (do catalog) para a forma
// leve da modal: por campanha só nome/cliente/contagem-de-emissoras.
// Sempre devolve Campaigns como slice não-nil (JSON "[]", nunca null).
func digestFromDaily(res *catalog.DailyResult, seen bool) digestResponse {
	out := digestResponse{
		Date:      res.Date,
		Seen:      seen,
		Summary:   digestSummary{Campaigns: res.Summary.Campaigns, Stations: res.Summary.Stations},
		Campaigns: []digestCampaign{},
	}
	for _, c := range res.Campaigns {
		out.Campaigns = append(out.Campaigns, digestCampaign{
			ID:             c.Campaign.ID,
			Name:           c.Campaign.Name,
			ClientName:     c.Campaign.ClientName,
			ClientLogoURL:  c.Campaign.ClientLogoURL,
			StationsFailed: len(c.Stations),
		})
	}
	return out
}

// ─── Chave de "visto" (reusa notification_reads) ─────────────────────

const digestKeyPrefix = "daily_failures_digest:"

func digestKey(dayStr string) string { return digestKeyPrefix + dayStr }

// yesterdayLocal devolve "ontem" no fuso local, igual ao default de
// CampaignFailuresHandler.GetList. Centralizado pra GET e Ack usarem a
// mesma data.
func yesterdayLocal() time.Time { return time.Now().AddDate(0, 0, -1) }

// ─── Repos (interfaces estreitas pra testar sem DB) ──────────────────

// DigestCampaignsRepo é satisfeito por *catalog.CampaignFailures.
type DigestCampaignsRepo interface {
	ListForDate(ctx context.Context, day time.Time) (*catalog.DailyResult, error)
}

// DigestSeenRepo é satisfeito por *catalog.Notifications (após Task 3
// adicionar HasRead).
type DigestSeenRepo interface {
	HasRead(ctx context.Context, userID uuid.UUID, key string) (bool, error)
	MarkRead(ctx context.Context, userID uuid.UUID, keys []string) (int, error)
}

// DailyFailuresDigestHandler powers /v1/internal/admin/daily-failures-digest.
// Auth: admin-only (montado no grupo admin do router, junto do sininho).
type DailyFailuresDigestHandler struct {
	Campaigns DigestCampaignsRepo
	Seen      DigestSeenRepo
	Log       *zap.Logger
}

func (h *DailyFailuresDigestHandler) logErr(msg string, err error) {
	if h.Log != nil {
		h.Log.Error(msg, zap.Error(err))
	}
}

// Get serve GET /admin/daily-failures-digest.
// Calcula "ontem", busca as falhas do dia e o flag "seen" do usuário atual.
func (h *DailyFailuresDigestHandler) Get(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFromReq(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	day := yesterdayLocal()
	dayStr := day.Format("2006-01-02")

	if h.Campaigns == nil {
		// Test harness sem DB — devolve vazio em vez de panicar.
		writeJSON(w, http.StatusOK, digestResponse{Date: dayStr, Campaigns: []digestCampaign{}})
		return
	}

	res, err := h.Campaigns.ListForDate(r.Context(), day)
	if err != nil {
		h.logErr("daily_failures_digest_list_failed", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	seen := false
	if h.Seen != nil {
		s, err := h.Seen.HasRead(r.Context(), userID, digestKey(dayStr))
		if err != nil {
			h.logErr("daily_failures_digest_hasread_failed", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		seen = s
	}

	writeJSON(w, http.StatusOK, digestFromDaily(res, seen))
}

// Ack serve POST /admin/daily-failures-digest/ack.
// Sem body. Servidor deriva "ontem" e marca a chave como lida pro usuário.
func (h *DailyFailuresDigestHandler) Ack(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFromReq(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	dayStr := yesterdayLocal().Format("2006-01-02")

	if h.Seen != nil {
		if _, err := h.Seen.MarkRead(r.Context(), userID, []string{digestKey(dayStr)}); err != nil {
			h.logErr("daily_failures_digest_ack_failed", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]bool{"acked": true})
}
