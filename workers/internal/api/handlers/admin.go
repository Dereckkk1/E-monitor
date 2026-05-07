package handlers

import (
	"context"
	"net/http"
	"time"

	"go.uber.org/zap"

	"radiocheck/internal/auth"
)

// TieringRunner is the interface satisfied by *evidence.TieringJob; abstracted
// so the handler stays decoupled from the evidence package and easier to test.
type TieringRunner interface {
	Run(ctx context.Context) error
}

// AdminHandler exposes operational endpoints meant for human operators
// holding an admin JWT. Today it covers manual triggers for the evidence
// tiering job (§11.4); future endpoints (manual backup kick, cache flush…)
// belong here too.
type AdminHandler struct {
	Tiering TieringRunner
	Log     *zap.Logger
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
