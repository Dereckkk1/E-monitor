package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
)

type HealthHandler struct {
	DB   *pgxpool.Pool
	NATS *nats.Conn
}

func (h *HealthHandler) Check(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	status := "ok"
	deps := map[string]string{}

	if err := h.DB.Ping(ctx); err != nil {
		status = "degraded"
		deps["postgres"] = "down: " + err.Error()
	} else {
		deps["postgres"] = "ok"
	}

	if h.NATS == nil || !h.NATS.IsConnected() {
		status = "degraded"
		deps["nats"] = "down"
	} else {
		deps["nats"] = "ok"
	}

	writeJSON(w, 200, map[string]any{
		"status": status,
		"deps":   deps,
	})
}
