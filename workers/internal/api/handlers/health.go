package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"

	"radiocheck/internal/supervisor"
)

// workerLister is satisfied by *supervisor.Supervisor.
type workerLister interface {
	WorkerStatuses() []supervisor.WorkerStatus
}

type HealthHandler struct {
	DB         *pgxpool.Pool
	NATS       *nats.Conn
	Sup        workerLister
	clapClient *http.Client
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

// WorkerStatus returns a JSON snapshot of all running workers plus CLAP verifier reachability.
func (h *HealthHandler) WorkerStatus(w http.ResponseWriter, r *http.Request) {
	var statuses []supervisor.WorkerStatus
	if h.Sup != nil {
		statuses = h.Sup.WorkerStatuses()
	}
	if statuses == nil {
		statuses = []supervisor.WorkerStatus{}
	}

	clapOK := false
	clapURL := os.Getenv("CLAP_VERIFIER_URL")
	if clapURL != "" {
		if h.clapClient == nil {
			h.clapClient = &http.Client{Timeout: 2 * time.Second}
		}
		resp, err := h.clapClient.Get(clapURL + "/health")
		clapOK = err == nil && resp != nil && resp.StatusCode == 200
		if resp != nil {
			resp.Body.Close()
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"workers":       statuses,
		"clap_verifier": clapOK,
	})
}
