package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"radiocheck/internal/auth"
)

// TieringRunner is the interface satisfied by *evidence.TieringJob; abstracted
// so the handler stays decoupled from the evidence package and easier to test.
type TieringRunner interface {
	Run(ctx context.Context) error
}

// ThresholdRefresher is the slice of *supervisor.Supervisor needed by the
// threshold refresh endpoint. Kept narrow so the handler stays decoupled
// from the supervisor package and easy to fake in tests.
type ThresholdRefresher interface {
	RefreshThreshold(stationID uuid.UUID) error
}

// AdminHandler exposes operational endpoints meant for human operators
// holding an admin JWT. Today it covers manual triggers for the evidence
// tiering job (§11.4) and the dynamic-threshold refresh (§9.4); future
// endpoints (manual backup kick, cache flush…) belong here too.
type AdminHandler struct {
	Tiering   TieringRunner
	Threshold ThresholdRefresher
	Log       *zap.Logger
}

// RunTiering forces an immediate evidence tiering pass. Useful for ops when
// they need to free hot storage outside the 03:00 BR window. Idempotent.
//
// Auth: requires JWT with role=admin.
//
// Response (200):
//
//	{"status":"ok","duration_ms":1234}
//
// Response (500):
//
//	{"status":"error","error":"<reason>"}
func (h *AdminHandler) RunTiering(w http.ResponseWriter, r *http.Request) {
	if h.Tiering == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"status": "error",
			"error":  "tiering job not configured",
		})
		return
	}

	// Cap the manual run so a hung S3 bucket can't pin the request forever.
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Minute)
	defer cancel()

	start := time.Now()
	if err := h.Tiering.Run(ctx); err != nil {
		if h.Log != nil {
			h.Log.Warn("admin: tiering run failed", zap.Error(err))
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"status":      "error",
			"error":       err.Error(),
			"duration_ms": time.Since(start).Milliseconds(),
		})
		return
	}

	claims, _ := auth.ClaimsFromContext(r.Context())
	triggeredBy := ""
	if claims != nil {
		triggeredBy = claims.UserID.String()
	}
	if h.Log != nil {
		h.Log.Info("admin: tiering run ok",
			zap.String("triggered_by", triggeredBy),
			zap.Duration("duration", time.Since(start)),
		)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":      "ok",
		"duration_ms": time.Since(start).Milliseconds(),
	})
}

// RefreshThreshold forces the running worker for the given station to re-read
// station_thresholds immediately, bypassing the 5-min refresh tick. Useful
// after a manual SQL tweak or right after the calibration job finishes so ops
// can validate the new value without waiting.
//
// Auth: requires JWT with role=admin (mounted under the admin group).
//
// Path: POST /v1/internal/admin/stations/{id}/threshold/refresh
//
// Responses:
//
//	202 {"status":"queued"}                — refresh signal accepted
//	400 {"status":"error","error":"..."}   — invalid station id
//	404 {"status":"error","error":"..."}   — no worker running for station
//	503 {"status":"error","error":"..."}   — supervisor not wired
func (h *AdminHandler) RefreshThreshold(w http.ResponseWriter, r *http.Request) {
	if h.Threshold == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"status": "error",
			"error":  "threshold refresher not configured",
		})
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"status": "error",
			"error":  "invalid station id",
		})
		return
	}
	if err := h.Threshold.RefreshThreshold(id); err != nil {
		// The only error the supervisor returns today is "no worker running".
		if h.Log != nil {
			h.Log.Info("admin: threshold refresh refused",
				zap.String("station_id", id.String()),
				zap.Error(err))
		}
		writeJSON(w, http.StatusNotFound, map[string]any{
			"status": "error",
			"error":  err.Error(),
		})
		return
	}
	claims, _ := auth.ClaimsFromContext(r.Context())
	triggeredBy := ""
	if claims != nil {
		triggeredBy = claims.UserID.String()
	}
	if h.Log != nil {
		h.Log.Info("admin: threshold refresh queued",
			zap.String("station_id", id.String()),
			zap.String("triggered_by", triggeredBy))
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"status": "queued",
	})
}
