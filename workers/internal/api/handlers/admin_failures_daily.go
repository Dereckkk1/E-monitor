package handlers

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"go.uber.org/zap"

	"radiocheck/internal/catalog"
)

// maxDailyRangeDays é o teto do range pedido de uma vez. 92 cobre "mês passado"
// e "últimos 90 dias" — os dois presets da UI — sem abrir espaço pra um SELECT
// de um ano inteiro sobre a daily_play_summary_for.
const maxDailyRangeDays = 92

// dailyHistoryDays espelha o limite de 90 dias do /admin/station-failures. Se o
// gráfico mostrasse um dia mais antigo que isso, clicar na barra levaria pra
// uma data que o próprio input de data da aba "Por emissora" recusa.
const dailyHistoryDays = 90

// FailuresDailyRepo abstrai o repo pra testar o handler sem DB.
type FailuresDailyRepo interface {
	ListDaily(ctx context.Context, from, to time.Time, minDownSeconds int) (*catalog.FailuresDailyResult, error)
}

// FailuresDailyHandler serve /v1/internal/admin/failures-daily — a série
// temporal por trás da aba "Por dia". Admin-only (montado no grupo admin do
// router.go). Doc: docs/features/admin-failures-daily.md.
type FailuresDailyHandler struct {
	Repo FailuresDailyRepo
	Log  *zap.Logger
}

func (h *FailuresDailyHandler) Get(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	now := time.Now().In(time.Local)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)

	// Default: os últimos 30 dias terminando hoje.
	to := today
	from := today.AddDate(0, 0, -29)

	if v := q.Get("to"); v != "" {
		d, err := time.ParseInLocation("2006-01-02", v, time.Local)
		if err != nil {
			http.Error(w, "invalid to: expected YYYY-MM-DD", http.StatusBadRequest)
			return
		}
		to = d
	}
	if v := q.Get("from"); v != "" {
		d, err := time.ParseInLocation("2006-01-02", v, time.Local)
		if err != nil {
			http.Error(w, "invalid from: expected YYYY-MM-DD", http.StatusBadRequest)
			return
		}
		from = d
	}

	if to.After(today) {
		// Pedir "até o fim do mês" quando o mês ainda está correndo é o caso
		// normal da UI, não erro — corta em hoje em vez de 400.
		to = today
	}
	if from.After(to) {
		http.Error(w, "invalid range: from after to", http.StatusBadRequest)
		return
	}
	if today.Sub(from) > dailyHistoryDays*24*time.Hour {
		http.Error(w, "invalid from: older than 90 days", http.StatusBadRequest)
		return
	}
	if to.Sub(from) > (maxDailyRangeDays-1)*24*time.Hour {
		http.Error(w, "invalid range: wider than 92 days", http.StatusBadRequest)
		return
	}

	minDown := 60
	if v := q.Get("min_down_seconds"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 && n <= 3600 {
			minDown = n
		}
	}

	if h.Repo == nil {
		writeJSON(w, http.StatusOK, &catalog.FailuresDailyResult{
			From: from.Format("2006-01-02"),
			To:   to.Format("2006-01-02"),
			Days: []catalog.DailyPoint{},
		})
		return
	}

	res, err := h.Repo.ListDaily(r.Context(), from, to, minDown)
	if err != nil {
		if h.Log != nil {
			h.Log.Error("failures_daily_query_failed",
				zap.String("from", from.Format("2006-01-02")),
				zap.String("to", to.Format("2006-01-02")),
				zap.Error(err))
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
