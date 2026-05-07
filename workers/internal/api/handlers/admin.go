package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"radiocheck/internal/auth"
)

// TieringRunner is the interface satisfied by *evidence.TieringJob; abstracted
// so the handler stays decoupled from the evidence package and easier to test.
type TieringRunner interface {
	Run(ctx context.Context) error
}

// CalibrationRunner is the interface satisfied by *calibration.Scheduler.
// Two operations are supported:
//   - RunOnce: scan eligible stations and recalibrate them all.
//   - RunOnceForStation: force-recalibrate a single station, ignoring the
//     7-day MinAge gate.
type CalibrationRunner interface {
	RunOnce(ctx context.Context) (int, error)
	RunOnceForStation(ctx context.Context, stationID uuid.UUID) error
}

// AdminHandler exposes operational endpoints meant for human operators
// holding an admin JWT. Today it covers manual triggers for the evidence
// tiering job (§11.4) and calibration scheduler (§9.4); future endpoints
// (manual backup kick, cache flush…) belong here too.
type AdminHandler struct {
	Tiering     TieringRunner
	Calibration CalibrationRunner
	Log         *zap.Logger
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

// runCalibrationRequest is the optional JSON body for RunCalibration. When
// station_id is supplied, the handler force-recalibrates that single station
// (bypassing the 7-day MinAge gate) — useful when an operator just changed
// a station's stream URL or audio processing chain. When the body is empty
// or station_id is absent, the handler triggers a full pass over all
// eligible stations.
type runCalibrationRequest struct {
	StationID string `json:"station_id,omitempty"`
}

// RunCalibration forces an immediate calibration pass. Mirrors RunTiering.
//
// Auth: requires JWT with role=admin (enforced by the router).
//
// Body (optional): {"station_id":"<uuid>"} to recalibrate one station.
//
// Response 200 (full pass):    {"status":"ok","stations":N,"duration_ms":...}
// Response 200 (single):       {"status":"ok","station_id":"<uuid>","duration_ms":...}
// Response 400 (bad uuid):     {"status":"error","error":"invalid station_id"}
// Response 500 (failure):      {"status":"error","error":"<reason>","duration_ms":...}
func (h *AdminHandler) RunCalibration(w http.ResponseWriter, r *http.Request) {
	if h.Calibration == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"status": "error",
			"error":  "calibration scheduler not configured",
		})
		return
	}

	// Body is optional; tolerate empty.
	var req runCalibrationRequest
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"status": "error",
				"error":  "invalid json body",
			})
			return
		}
	}

	// Cap the manual run; recalibrating one station is a single UPDATE,
	// but a full pass loops with semaphore-bounded concurrency.
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Minute)
	defer cancel()

	start := time.Now()

	if req.StationID != "" {
		stationID, err := uuid.Parse(req.StationID)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"status": "error",
				"error":  "invalid station_id",
			})
			return
		}
		if err := h.Calibration.RunOnceForStation(ctx, stationID); err != nil {
			h.adminLogFailure("calibration: station run failed",
				map[string]any{"station_id": stationID.String()}, err)
			writeJSON(w, http.StatusInternalServerError, map[string]any{
				"status":      "error",
				"error":       err.Error(),
				"station_id":  stationID.String(),
				"duration_ms": time.Since(start).Milliseconds(),
			})
			return
		}
		h.adminLogSuccess(r, "calibration: station ok",
			map[string]any{"station_id": stationID.String()}, start)
		writeJSON(w, http.StatusOK, map[string]any{
			"status":      "ok",
			"station_id":  stationID.String(),
			"duration_ms": time.Since(start).Milliseconds(),
		})
		return
	}

	count, err := h.Calibration.RunOnce(ctx)
	if err != nil {
		h.adminLogFailure("calibration: full pass failed", nil, err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"status":      "error",
			"error":       err.Error(),
			"duration_ms": time.Since(start).Milliseconds(),
		})
		return
	}
	h.adminLogSuccess(r, "calibration: full pass ok",
		map[string]any{"stations": count}, start)
	writeJSON(w, http.StatusOK, map[string]any{
		"status":      "ok",
		"stations":    count,
		"duration_ms": time.Since(start).Milliseconds(),
	})
}

func (h *AdminHandler) adminLogSuccess(r *http.Request, msg string, fields map[string]any, start time.Time) {
	if h.Log == nil {
		return
	}
	claims, _ := auth.ClaimsFromContext(r.Context())
	triggeredBy := ""
	if claims != nil {
		triggeredBy = claims.UserID.String()
	}
	zfields := []zap.Field{
		zap.String("triggered_by", triggeredBy),
		zap.Duration("duration", time.Since(start)),
	}
	for k, v := range fields {
		zfields = append(zfields, zap.Any(k, v))
	}
	h.Log.Info(msg, zfields...)
}

func (h *AdminHandler) adminLogFailure(msg string, fields map[string]any, err error) {
	if h.Log == nil {
		return
	}
	zfields := []zap.Field{zap.Error(err)}
	for k, v := range fields {
		zfields = append(zfields, zap.Any(k, v))
	}
	h.Log.Warn(msg, zfields...)
}
