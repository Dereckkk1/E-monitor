package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"radiocheck/internal/auth"
	"radiocheck/internal/reqmetrics"
)

// AdminMonitoringHandler espelha os endpoints do /admin/monitoring do
// E-radios. Dois data stores: system_metrics (1 row por request HTTP) e
// blocked_ips (IPs banidos). Web Vitals tem sua própria tabela.
//
// Todos os reads filtram por janela rolling (range=1h|24h|7d|30d). O range
// é validado no helper sinceFromRange para evitar SQL injection — apesar de
// não construirmos SQL com strings, mantemos a validação como defesa em
// profundidade caso o filtro vá pra LIMIT/ORDER algum dia.
type AdminMonitoringHandler struct {
	DB     *pgxpool.Pool
	Block  *reqmetrics.BlockList
	Log    *zap.Logger
}

// rangeWindow converte "1h"/"24h"/"7d"/"30d" em duração. Fallback é 24h —
// igual ao backend de origem.
func rangeWindow(r string) time.Duration {
	switch r {
	case "1h":
		return time.Hour
	case "24h":
		return 24 * time.Hour
	case "7d":
		return 7 * 24 * time.Hour
	case "30d":
		return 30 * 24 * time.Hour
	default:
		return 24 * time.Hour
	}
}

func sinceFromRange(r string) time.Time {
	return time.Now().Add(-rangeWindow(r))
}

// hideLocalhostFilter retorna um fragmento SQL e args quando o operador
// pediu para esconder requests internos. NULL-safe: o filtro só vê ip = ''
// como localhost depois de coalescer.
func hideLocalhostClause(hide bool) (string, []any) {
	if !hide {
		return "", nil
	}
	// IPs locais comuns no Docker (loopback v4/v6, IPv4-mapped v6) + IP do
	// gateway docker padrão. Conservador: não excluir nada falsamente.
	return `AND ip NOT IN ('127.0.0.1', '::1', 'localhost', '')`, nil
}

// queryHideLocalhost lê o flag ?hideLocalhost=true|false. Default true,
// igual ao painel original — operador normalmente quer ver só o tráfego real.
func queryHideLocalhost(r *http.Request) bool {
	v := r.URL.Query().Get("hideLocalhost")
	if v == "" {
		return true
	}
	return v == "true" || v == "1"
}

// ─── GET /admin/monitoring/overview ──────────────────────────────────────────

type periodSummary struct {
	Range          string  `json:"range"`
	Since          string  `json:"since"`
	TotalRequests  int     `json:"totalRequests"`
	TotalErrors    int     `json:"totalErrors"`
	TotalSlow      int     `json:"totalSlow"`
	ErrorRate      string  `json:"errorRate"`
	AvgDuration    int     `json:"avgDuration"`
}

type serverSummary struct {
	Uptime struct {
		Human string `json:"human"`
	} `json:"uptime"`
	Memory struct {
		HeapUsedMB  int `json:"heapUsedMB"`
		HeapTotalMB int `json:"heapTotalMB"`
		RSSMB       int `json:"rssMB"`
	} `json:"memory"`
	Node string `json:"node"`
}

type overviewResponse struct {
	Server serverSummary `json:"server"`
	Period periodSummary `json:"period"`
}

// procStart é o instante em que esse processo subiu — usado para calcular
// uptime humano no overview, equivalente a process.uptime() do Node.
var procStart = time.Now()

func (h *AdminMonitoringHandler) Overview(w http.ResponseWriter, r *http.Request) {
	rng := r.URL.Query().Get("range")
	since := sinceFromRange(rng)
	hide := queryHideLocalhost(r)
	hideSQL, _ := hideLocalhostClause(hide)

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	var (
		total, errors, slow int
		avg                 float64
	)

	q := fmt.Sprintf(`
		SELECT
			COUNT(*) FILTER (WHERE TRUE),
			COUNT(*) FILTER (WHERE is_error),
			COUNT(*) FILTER (WHERE is_slow),
			COALESCE(AVG(duration_ms), 0)
		FROM system_metrics
		WHERE ts >= $1 %s
	`, hideSQL)

	if err := h.DB.QueryRow(ctx, q, since).Scan(&total, &errors, &slow, &avg); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "overview query failed"})
		return
	}

	rate := "0.00%"
	if total > 0 {
		rate = fmt.Sprintf("%.2f%%", float64(errors)*100/float64(total))
	}

	resp := overviewResponse{
		Period: periodSummary{
			Range:         normalizeRange(rng),
			Since:         since.UTC().Format(time.RFC3339),
			TotalRequests: total,
			TotalErrors:   errors,
			TotalSlow:     slow,
			ErrorRate:     rate,
			AvgDuration:   int(avg + 0.5),
		},
	}
	resp.Server.Uptime.Human = humanDuration(time.Since(procStart))
	// memory: lazy approximation via runtime/metrics. Mantemos leve — só
	// preenche o que conseguimos sem nova dependência. O painel mostra
	// 0/Node "go" quando não tem dado, sem quebrar a UI.
	resp.Server.Memory.HeapUsedMB, resp.Server.Memory.HeapTotalMB, resp.Server.Memory.RSSMB = readMemoryMB()
	resp.Server.Node = goVersion()

	writeJSON(w, http.StatusOK, resp)
}

// ─── GET /admin/monitoring/routes ────────────────────────────────────────────

type routeRow struct {
	Route       string `json:"route"`
	Count       int    `json:"count"`
	P50         int    `json:"p50"`
	P95         int    `json:"p95"`
	P99         int    `json:"p99"`
	Avg         int    `json:"avg"`
	Max         int    `json:"max"`
	ErrorCount  int    `json:"errorCount"`
	SlowCount   int    `json:"slowCount"`
	ErrorRate   string `json:"errorRate"`
	Health      string `json:"health"`
}

func (h *AdminMonitoringHandler) Routes(w http.ResponseWriter, r *http.Request) {
	rng := r.URL.Query().Get("range")
	since := sinceFromRange(rng)
	hide := queryHideLocalhost(r)
	hideSQL, _ := hideLocalhostClause(hide)

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	// percentile_disc do Postgres é exato e barato para até ~50k rows por
	// grupo. Acima disso, considerar amostragem (próximo follow-up).
	q := fmt.Sprintf(`
		SELECT
			route,
			COUNT(*) AS cnt,
			percentile_disc(0.50) WITHIN GROUP (ORDER BY duration_ms) AS p50,
			percentile_disc(0.95) WITHIN GROUP (ORDER BY duration_ms) AS p95,
			percentile_disc(0.99) WITHIN GROUP (ORDER BY duration_ms) AS p99,
			AVG(duration_ms)::int                                       AS avg,
			MAX(duration_ms)                                            AS mx,
			SUM(CASE WHEN is_error THEN 1 ELSE 0 END)                   AS err,
			SUM(CASE WHEN is_slow  THEN 1 ELSE 0 END)                   AS slo
		FROM system_metrics
		WHERE ts >= $1 %s
		GROUP BY route
		ORDER BY cnt DESC
	`, hideSQL)

	rows, err := h.DB.Query(ctx, q, since)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "routes query failed"})
		return
	}
	defer rows.Close()

	out := make([]routeRow, 0)
	for rows.Next() {
		var rr routeRow
		var p50, p95, p99 *int
		if err := rows.Scan(&rr.Route, &rr.Count, &p50, &p95, &p99,
			&rr.Avg, &rr.Max, &rr.ErrorCount, &rr.SlowCount); err != nil {
			continue
		}
		if p50 != nil {
			rr.P50 = *p50
		}
		if p95 != nil {
			rr.P95 = *p95
		}
		if p99 != nil {
			rr.P99 = *p99
		}
		// Error rate como string formatada (compat com o componente do frontend).
		erRate := 0.0
		if rr.Count > 0 {
			erRate = float64(rr.ErrorCount) * 100 / float64(rr.Count)
		}
		rr.ErrorRate = fmt.Sprintf("%.2f%%", erRate)
		rr.Health = healthBucket(rr.P95, erRate)
		out = append(out, rr)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"range":       normalizeRange(rng),
		"totalRoutes": len(out),
		"routes":      out,
	})
}

func healthBucket(p95 int, errRate float64) string {
	switch {
	case p95 >= 2000 || errRate >= 5:
		return "critical"
	case p95 >= 500 || errRate >= 1:
		return "warning"
	default:
		return "good"
	}
}

// ─── GET /admin/monitoring/errors ────────────────────────────────────────────

type errorRow struct {
	Route          string    `json:"route"`
	StatusCode     int       `json:"statusCode"`
	Count          int       `json:"count"`
	LastOccurrence time.Time `json:"lastOccurrence"`
	AvgDuration    int       `json:"avgDuration"`
}

func (h *AdminMonitoringHandler) Errors(w http.ResponseWriter, r *http.Request) {
	rng := r.URL.Query().Get("range")
	since := sinceFromRange(rng)
	hide := queryHideLocalhost(r)
	hideSQL, _ := hideLocalhostClause(hide)

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	q := fmt.Sprintf(`
		SELECT route, status_code, COUNT(*) AS cnt,
		       MAX(ts) AS last_occ,
		       AVG(duration_ms)::int AS avg_dur
		FROM system_metrics
		WHERE ts >= $1 AND is_error %s
		GROUP BY route, status_code
		ORDER BY cnt DESC
		LIMIT 50
	`, hideSQL)

	rows, err := h.DB.Query(ctx, q, since)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "errors query failed"})
		return
	}
	defer rows.Close()

	total := 0
	out := make([]errorRow, 0)
	for rows.Next() {
		var er errorRow
		if err := rows.Scan(&er.Route, &er.StatusCode, &er.Count, &er.LastOccurrence, &er.AvgDuration); err != nil {
			continue
		}
		total += er.Count
		out = append(out, er)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"range":       normalizeRange(rng),
		"totalErrors": total,
		"errors":      out,
	})
}

// ─── GET /admin/monitoring/slow ──────────────────────────────────────────────

type slowRow struct {
	Route      string    `json:"route"`
	Method     string    `json:"method"`
	StatusCode int       `json:"statusCode"`
	Duration   int       `json:"duration"`
	Timestamp  time.Time `json:"timestamp"`
}

func (h *AdminMonitoringHandler) Slow(w http.ResponseWriter, r *http.Request) {
	rng := r.URL.Query().Get("range")
	since := sinceFromRange(rng)
	hide := queryHideLocalhost(r)
	hideSQL, _ := hideLocalhostClause(hide)

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	q := fmt.Sprintf(`
		SELECT route, method, status_code, duration_ms, ts
		FROM system_metrics
		WHERE ts >= $1 AND is_slow %s
		ORDER BY duration_ms DESC
		LIMIT 100
	`, hideSQL)

	rows, err := h.DB.Query(ctx, q, since)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "slow query failed"})
		return
	}
	defer rows.Close()

	out := make([]slowRow, 0)
	for rows.Next() {
		var sr slowRow
		if err := rows.Scan(&sr.Route, &sr.Method, &sr.StatusCode, &sr.Duration, &sr.Timestamp); err != nil {
			continue
		}
		out = append(out, sr)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"range":     normalizeRange(rng),
		"totalSlow": len(out),
		"requests":  out,
	})
}

// ─── GET /admin/monitoring/timeline ──────────────────────────────────────────

type timelineRow struct {
	Period        string `json:"period"`
	TotalRequests int    `json:"totalRequests"`
	TotalErrors   int    `json:"totalErrors"`
	TotalSlow     int    `json:"totalSlow"`
	AvgDuration   int    `json:"avgDuration"`
}

func (h *AdminMonitoringHandler) Timeline(w http.ResponseWriter, r *http.Request) {
	rng := r.URL.Query().Get("range")
	since := sinceFromRange(rng)
	hide := queryHideLocalhost(r)
	hideSQL, _ := hideLocalhostClause(hide)

	// Bucket por dia para ranges longos, por hora para curtos — espelhando
	// o painel original.
	groupByDay := rng == "7d" || rng == "30d"
	fmtMask := "YYYY-MM-DD\"T\"HH24:00"
	if groupByDay {
		fmtMask = "YYYY-MM-DD"
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	q := fmt.Sprintf(`
		SELECT
			to_char(ts, '%s')                              AS period,
			COUNT(*)                                       AS total,
			SUM(CASE WHEN is_error THEN 1 ELSE 0 END)::int AS errs,
			SUM(CASE WHEN is_slow  THEN 1 ELSE 0 END)::int AS slo,
			AVG(duration_ms)::int                          AS avg_d
		FROM system_metrics
		WHERE ts >= $1 %s
		GROUP BY period
		ORDER BY period ASC
	`, fmtMask, hideSQL)

	rows, err := h.DB.Query(ctx, q, since)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "timeline query failed"})
		return
	}
	defer rows.Close()

	out := make([]timelineRow, 0)
	for rows.Next() {
		var tr timelineRow
		if err := rows.Scan(&tr.Period, &tr.TotalRequests, &tr.TotalErrors, &tr.TotalSlow, &tr.AvgDuration); err != nil {
			continue
		}
		out = append(out, tr)
	}

	groupLabel := "hour"
	if groupByDay {
		groupLabel = "day"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"range":     normalizeRange(rng),
		"groupedBy": groupLabel,
		"timeline":  out,
	})
}

// ─── GET /admin/monitoring/top-actors ────────────────────────────────────────

type actorRow struct {
	IP               string     `json:"ip"`
	UserID           *uuid.UUID `json:"userId"`
	UserEmail        *string    `json:"userEmail"`
	TotalRequests    int        `json:"totalRequests"`
	UniqueRouteCount int        `json:"uniqueRouteCount"`
	ErrorCount       int        `json:"errorCount"`
	SlowCount        int        `json:"slowCount"`
	NotFoundCount    int        `json:"notFoundCount"`
	FirstSeen        time.Time  `json:"firstSeen"`
	LastSeen         time.Time  `json:"lastSeen"`
	RiskLevel        string     `json:"riskLevel"`
	IsIPBlocked      bool       `json:"isIPBlocked"`
	BlockedReason    *string    `json:"blockedReason"`
	BlockedAt        *time.Time `json:"blockedAt"`
}

// riskLevel: scoring igual ao backend Node — favorece sinais de bot
// (rota_diversa, 404s, anônimo) sobre volume puro.
func riskLevel(reqs, uniqueRoutes, notFound int, authed bool) string {
	score := 0

	routeRatio := 0.0
	notFoundRate := 0.0
	if reqs > 0 {
		routeRatio = float64(uniqueRoutes) / float64(reqs)
		notFoundRate = float64(notFound) / float64(reqs)
	}

	switch {
	case notFoundRate >= 0.5:
		score += 40
	case notFoundRate >= 0.2:
		score += 20
	case notFoundRate >= 0.1:
		score += 10
	}

	if reqs >= 5 {
		switch {
		case routeRatio >= 0.8:
			score += 35
		case routeRatio >= 0.5:
			score += 15
		}
	}

	if !authed {
		score += 15
	}

	switch {
	case reqs >= 500:
		score += 10
	case reqs >= 200:
		score += 5
	}

	switch {
	case score >= 60:
		return "critical"
	case score >= 35:
		return "high"
	case score >= 15:
		return "medium"
	default:
		return "low"
	}
}

func (h *AdminMonitoringHandler) TopActors(w http.ResponseWriter, r *http.Request) {
	rng := r.URL.Query().Get("range")
	since := sinceFromRange(rng)
	hide := queryHideLocalhost(r)
	hideSQL, _ := hideLocalhostClause(hide)

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	q := fmt.Sprintf(`
		SELECT
			ip,
			user_id,
			COUNT(*)                                       AS total,
			COUNT(DISTINCT route)                          AS routes,
			SUM(CASE WHEN is_error THEN 1 ELSE 0 END)::int AS errs,
			SUM(CASE WHEN is_slow  THEN 1 ELSE 0 END)::int AS slo,
			SUM(CASE WHEN status_code = 404 THEN 1 ELSE 0 END)::int AS nf,
			MIN(ts) AS first_seen,
			MAX(ts) AS last_seen
		FROM system_metrics
		WHERE ts >= $1 %s
		GROUP BY ip, user_id
		ORDER BY total DESC
		LIMIT 300
	`, hideSQL)

	rows, err := h.DB.Query(ctx, q, since)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "actors query failed"})
		return
	}

	type raw struct {
		IP         string
		UserID     *uuid.UUID
		Total, Routes, Errs, Slo, NF int
		FirstSeen, LastSeen          time.Time
	}
	rawActors := make([]raw, 0, 100)
	userIDs := map[uuid.UUID]struct{}{}
	for rows.Next() {
		var rr raw
		if err := rows.Scan(&rr.IP, &rr.UserID, &rr.Total, &rr.Routes,
			&rr.Errs, &rr.Slo, &rr.NF, &rr.FirstSeen, &rr.LastSeen); err != nil {
			continue
		}
		if rr.UserID != nil {
			userIDs[*rr.UserID] = struct{}{}
		}
		rawActors = append(rawActors, rr)
	}
	rows.Close()

	// Lookup email para todos os user_ids encontrados num único query.
	emails := map[uuid.UUID]string{}
	if len(userIDs) > 0 {
		ids := make([]uuid.UUID, 0, len(userIDs))
		for id := range userIDs {
			ids = append(ids, id)
		}
		em, _ := h.DB.Query(ctx, `SELECT id, email FROM users WHERE id = ANY($1)`, ids)
		for em.Next() {
			var id uuid.UUID
			var email string
			if err := em.Scan(&id, &email); err == nil {
				emails[id] = email
			}
		}
		em.Close()
	}

	// Lookup blocked map.
	blocked := map[string]struct {
		Reason    string
		BlockedAt time.Time
	}{}
	if rows2, err := h.DB.Query(ctx, `SELECT ip, COALESCE(reason,''), blocked_at FROM blocked_ips`); err == nil {
		for rows2.Next() {
			var ip, reason string
			var at time.Time
			if err := rows2.Scan(&ip, &reason, &at); err == nil {
				blocked[ip] = struct {
					Reason    string
					BlockedAt time.Time
				}{reason, at}
			}
		}
		rows2.Close()
	}

	out := make([]actorRow, 0, len(rawActors))
	for _, rr := range rawActors {
		var email *string
		if rr.UserID != nil {
			if e, ok := emails[*rr.UserID]; ok {
				e := e
				email = &e
			}
		}
		ar := actorRow{
			IP:               rr.IP,
			UserID:           rr.UserID,
			UserEmail:        email,
			TotalRequests:    rr.Total,
			UniqueRouteCount: rr.Routes,
			ErrorCount:       rr.Errs,
			SlowCount:        rr.Slo,
			NotFoundCount:    rr.NF,
			FirstSeen:        rr.FirstSeen,
			LastSeen:         rr.LastSeen,
			RiskLevel:        riskLevel(rr.Total, rr.Routes, rr.NF, rr.UserID != nil),
		}
		if b, ok := blocked[rr.IP]; ok {
			ar.IsIPBlocked = true
			reason := b.Reason
			ar.BlockedReason = &reason
			at := b.BlockedAt
			ar.BlockedAt = &at
		}
		out = append(out, ar)
	}

	// Estabiliza ordenação (count desc igual ao DB, mas com tie-break por IP
	// para snapshot determinístico em testes).
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].TotalRequests != out[j].TotalRequests {
			return out[i].TotalRequests > out[j].TotalRequests
		}
		return out[i].IP < out[j].IP
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"range":       normalizeRange(rng),
		"totalActors": len(out),
		"actors":      out,
	})
}

// ─── GET /admin/monitoring/actor-detail ──────────────────────────────────────

type actorDetailRequest struct {
	IP     string
	UserID *uuid.UUID
	Range  string
}

type actorTimelineRow struct {
	Period string `json:"_id"`
	Count  int    `json:"count"`
	Errors int    `json:"errors"`
}

type actorRouteCount struct {
	Route string `json:"route"`
	Count int    `json:"count"`
}

type actorRequestRow struct {
	Route      string    `json:"route"`
	Method     string    `json:"method"`
	StatusCode int       `json:"statusCode"`
	Duration   int       `json:"duration"`
	Timestamp  time.Time `json:"timestamp"`
	IP         string    `json:"ip"`
	UserID     *uuid.UUID `json:"userId"`
	UserEmail  *string    `json:"userEmail"`
}

func (h *AdminMonitoringHandler) ActorDetail(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	rng := q.Get("range")
	since := sinceFromRange(rng)

	req := actorDetailRequest{IP: q.Get("ip"), Range: normalizeRange(rng)}
	if v := q.Get("userId"); v != "" {
		if id, err := uuid.Parse(v); err == nil {
			req.UserID = &id
		}
	}
	if req.IP == "" && req.UserID == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "ip or userId required"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	// Where clause dinâmica (mas com bind args), nunca com concatenação de
	// valores do usuário no SQL.
	where := []string{"ts >= $1"}
	args := []any{since}
	if req.IP != "" {
		where = append(where, fmt.Sprintf("ip = $%d", len(args)+1))
		args = append(args, req.IP)
	}
	if req.UserID != nil {
		where = append(where, fmt.Sprintf("user_id = $%d", len(args)+1))
		args = append(args, *req.UserID)
	}
	whereSQL := "WHERE " + strings.Join(where, " AND ")

	// Requests detalhados (até 300).
	requests := make([]actorRequestRow, 0, 100)
	if rows, err := h.DB.Query(ctx, `
		SELECT route, method, status_code, duration_ms, ts, ip, user_id, user_email
		FROM system_metrics
		`+whereSQL+`
		ORDER BY ts DESC
		LIMIT 300
	`, args...); err == nil {
		for rows.Next() {
			var rr actorRequestRow
			var email *string
			if err := rows.Scan(&rr.Route, &rr.Method, &rr.StatusCode, &rr.Duration,
				&rr.Timestamp, &rr.IP, &rr.UserID, &email); err == nil {
				rr.UserEmail = email
				requests = append(requests, rr)
			}
		}
		rows.Close()
	}

	// Timeline por hora.
	timeline := make([]actorTimelineRow, 0, 24)
	if rows, err := h.DB.Query(ctx, `
		SELECT
			to_char(ts, 'YYYY-MM-DD"T"HH24:00') AS period,
			COUNT(*)::int                       AS cnt,
			SUM(CASE WHEN is_error THEN 1 ELSE 0 END)::int AS errs
		FROM system_metrics
		`+whereSQL+`
		GROUP BY period
		ORDER BY period ASC
	`, args...); err == nil {
		for rows.Next() {
			var tr actorTimelineRow
			if err := rows.Scan(&tr.Period, &tr.Count, &tr.Errors); err == nil {
				timeline = append(timeline, tr)
			}
		}
		rows.Close()
	}

	// Top rotas — derivado client-side do array requests para evitar
	// segundo aggregate. 300 reqs * map look-up é trivial.
	routeMap := map[string]int{}
	for _, rq := range requests {
		routeMap[rq.Route]++
	}
	type kv struct {
		Route string
		Count int
	}
	pairs := make([]kv, 0, len(routeMap))
	for k, v := range routeMap {
		pairs = append(pairs, kv{k, v})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].Count > pairs[j].Count })
	topRoutes := make([]actorRouteCount, 0, 10)
	for i, p := range pairs {
		if i >= 10 {
			break
		}
		topRoutes = append(topRoutes, actorRouteCount{p.Route, p.Count})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"range":     req.Range,
		"requests":  requests,
		"timeline":  timeline,
		"topRoutes": topRoutes,
	})
}

// ─── /admin/monitoring/blocked-ips ───────────────────────────────────────────

type blockedRow struct {
	IP             string    `json:"ip"`
	Reason         *string   `json:"reason"`
	BlockedAt      time.Time `json:"blockedAt"`
	BlockedByID    *string   `json:"blockedById"`
	BlockedByEmail *string   `json:"blockedByEmail"`
}

func (h *AdminMonitoringHandler) BlockedIPs(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	rows, err := h.DB.Query(ctx, `
		SELECT ip, reason, blocked_at, blocked_by_id::text, blocked_by_email
		FROM blocked_ips
		ORDER BY blocked_at DESC
	`)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "blocked-ips query failed"})
		return
	}
	defer rows.Close()

	out := make([]blockedRow, 0)
	for rows.Next() {
		var br blockedRow
		if err := rows.Scan(&br.IP, &br.Reason, &br.BlockedAt, &br.BlockedByID, &br.BlockedByEmail); err == nil {
			out = append(out, br)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"blockedIPs": out})
}

// ─── POST /admin/monitoring/block-ip ─────────────────────────────────────────

type blockIPRequest struct {
	IP     string `json:"ip"`
	Reason string `json:"reason"`
}

func (h *AdminMonitoringHandler) BlockIP(w http.ResponseWriter, r *http.Request) {
	var req blockIPRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid body"})
		return
	}
	req.IP = strings.TrimSpace(req.IP)
	if req.IP == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "ip is required"})
		return
	}
	if req.Reason == "" {
		req.Reason = "Bloqueado manualmente pelo painel admin"
	}

	claims, _ := auth.ClaimsFromContext(r.Context())
	var blockedByID *uuid.UUID
	var blockedByEmail *string
	if claims != nil {
		uid := claims.UserID
		blockedByID = &uid
		// Lookup email para registro.
		if h.DB != nil {
			var email string
			if err := h.DB.QueryRow(r.Context(), `SELECT email FROM users WHERE id = $1`, uid).Scan(&email); err == nil {
				blockedByEmail = &email
			}
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	_, err := h.DB.Exec(ctx, `
		INSERT INTO blocked_ips (ip, reason, blocked_at, blocked_by_id, blocked_by_email)
		VALUES ($1, $2, NOW(), $3, $4)
		ON CONFLICT (ip) DO UPDATE
		SET reason = EXCLUDED.reason,
		    blocked_at = NOW(),
		    blocked_by_id = EXCLUDED.blocked_by_id,
		    blocked_by_email = EXCLUDED.blocked_by_email
	`, req.IP, req.Reason, blockedByID, blockedByEmail)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "block-ip insert failed"})
		return
	}

	if h.Block != nil {
		h.Block.Add(req.IP)
	}
	if h.Log != nil {
		h.Log.Info("admin: ip blocked", zap.String("ip", req.IP), zap.String("reason", req.Reason))
	}

	writeJSON(w, http.StatusOK, map[string]any{"message": "IP bloqueado com sucesso", "ip": req.IP})
}

// ─── DELETE /admin/monitoring/block-ip/{ip} ──────────────────────────────────

func (h *AdminMonitoringHandler) UnblockIP(w http.ResponseWriter, r *http.Request) {
	ip := chi.URLParam(r, "ip")
	if ip == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "ip is required"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	if _, err := h.DB.Exec(ctx, `DELETE FROM blocked_ips WHERE ip = $1`, ip); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "unblock-ip failed"})
		return
	}
	if h.Block != nil {
		h.Block.Remove(ip)
	}
	if h.Log != nil {
		h.Log.Info("admin: ip unblocked", zap.String("ip", ip))
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": "IP desbloqueado com sucesso", "ip": ip})
}

// ─── POST /admin/monitoring/block-user/{userId} ──────────────────────────────
//
// "Bloquear" um usuário no Radiocheck = `is_active = false`. O JWT continua
// válido até expirar (8h), mas o middleware RequireJWT NÃO checa is_active
// hoje — ele só valida assinatura. Para fechar a janela na hora, ver
// follow-up "JWT revogação imediata". Por ora, marcar inativo já barra novos
// logins e segue o mesmo padrão do /admin/users (DELETE /admin/users/{id}).

type blockUserResponse struct {
	Message string `json:"message"`
	UserID  string `json:"userId"`
	Email   string `json:"email"`
}

func (h *AdminMonitoringHandler) BlockUser(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "userId")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid user id"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	var email string
	if err := h.DB.QueryRow(ctx, `
		UPDATE users SET is_active = false, updated_at = NOW()
		WHERE id = $1 RETURNING email
	`, id).Scan(&email); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "user not found"})
		return
	}

	if h.Log != nil {
		h.Log.Info("admin: user blocked", zap.String("user_id", id.String()), zap.String("email", email))
	}
	writeJSON(w, http.StatusOK, blockUserResponse{
		Message: "Usuário bloqueado com sucesso",
		UserID:  id.String(),
		Email:   email,
	})
}

// ─── GET /admin/monitoring/vitals ────────────────────────────────────────────

type vitalRow struct {
	Name        string  `json:"name"`
	Page        string  `json:"page"`
	Count       int     `json:"count"`
	Avg         float64 `json:"avg"`
	P75         float64 `json:"p75"`
	GoodPercent string  `json:"goodPercent"`
	PoorPercent string  `json:"poorPercent"`
	GoodCount   int     `json:"goodCount"`
	PoorCount   int     `json:"poorCount"`
	NeedsImprovementCount int `json:"needsImprovementCount"`
}

func (h *AdminMonitoringHandler) Vitals(w http.ResponseWriter, r *http.Request) {
	rng := r.URL.Query().Get("range")
	since := sinceFromRange(rng)
	hide := queryHideLocalhost(r)
	hideSQL := ""
	if hide {
		hideSQL = "AND page NOT LIKE '%localhost%'"
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	q := fmt.Sprintf(`
		SELECT
			name, page,
			COUNT(*)                                                            AS cnt,
			AVG(value)                                                          AS avg_v,
			percentile_disc(0.75) WITHIN GROUP (ORDER BY value)                 AS p75,
			SUM(CASE WHEN rating = 'good'              THEN 1 ELSE 0 END)::int  AS good_n,
			SUM(CASE WHEN rating = 'poor'              THEN 1 ELSE 0 END)::int  AS poor_n,
			SUM(CASE WHEN rating = 'needs-improvement' THEN 1 ELSE 0 END)::int  AS ni_n
		FROM web_vitals
		WHERE ts >= $1 %s
		GROUP BY name, page
		ORDER BY name ASC, cnt DESC
	`, hideSQL)

	rows, err := h.DB.Query(ctx, q, since)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "vitals query failed"})
		return
	}
	defer rows.Close()

	out := make([]vitalRow, 0)
	for rows.Next() {
		var vr vitalRow
		var p75 *float64
		if err := rows.Scan(&vr.Name, &vr.Page, &vr.Count, &vr.Avg, &p75,
			&vr.GoodCount, &vr.PoorCount, &vr.NeedsImprovementCount); err != nil {
			continue
		}
		if p75 != nil {
			vr.P75 = *p75
		}
		total := vr.GoodCount + vr.PoorCount + vr.NeedsImprovementCount
		if total > 0 {
			vr.GoodPercent = fmt.Sprintf("%.1f%%", float64(vr.GoodCount)*100/float64(total))
			vr.PoorPercent = fmt.Sprintf("%.1f%%", float64(vr.PoorCount)*100/float64(total))
		} else {
			vr.GoodPercent = "0%"
			vr.PoorPercent = "0%"
		}
		out = append(out, vr)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"range":  normalizeRange(rng),
		"vitals": out,
	})
}

// ─── POST /v1/internal/web-vitals ────────────────────────────────────────────
//
// Endpoint que o frontend bate ao reportar um vital (LCP/INP/CLS/...). Body:
// {name, value, rating, page}. user_id/email vêm do JWT (ou null para
// anonimato). IP é sempre capturado para correlação.

type vitalsRequest struct {
	Name   string  `json:"name"`
	Value  float64 `json:"value"`
	Rating string  `json:"rating"`
	Page   string  `json:"page"`
}

func (h *AdminMonitoringHandler) PostVitals(w http.ResponseWriter, r *http.Request) {
	var req vitalsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid body"})
		return
	}
	// Validação leve — só os 6 nomes canônicos e os 3 ratings.
	switch req.Name {
	case "LCP", "FID", "INP", "CLS", "FCP", "TTFB":
	default:
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "unknown metric name"})
		return
	}
	switch req.Rating {
	case "good", "needs-improvement", "poor":
	default:
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid rating"})
		return
	}

	var userID *uuid.UUID
	var userEmail *string
	if claims, ok := auth.ClaimsFromContext(r.Context()); ok && claims != nil {
		uid := claims.UserID
		userID = &uid
		// Email não está no JWT — lookup leve.
		if h.DB != nil {
			var em string
			if err := h.DB.QueryRow(r.Context(), `SELECT email FROM users WHERE id = $1`, uid).Scan(&em); err == nil {
				userEmail = &em
			}
		}
	}

	ip := requestIP(r)

	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	_, err := h.DB.Exec(ctx, `
		INSERT INTO web_vitals (name, value, rating, page, ip, user_id, user_email)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, req.Name, req.Value, req.Rating, req.Page, ip, userID, userEmail)
	if err != nil {
		// Não interrompemos o frontend por falha no log — fire and forget.
		w.WriteHeader(http.StatusAccepted)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

// normalizeRange força um valor canônico no JSON de resposta — vazio vira 24h
// para não confundir o frontend que itera sobre o eco do range.
func normalizeRange(r string) string {
	switch r {
	case "1h", "24h", "7d", "30d":
		return r
	default:
		return "24h"
	}
}

// requestIP é versão local de reqmetrics.clientIP (não exportada lá). Repete
// para não acoplar — o cust é trivial.
func requestIP(r *http.Request) string {
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i > 0 && i < len(host)-1 {
		if strings.HasPrefix(host, "[") {
			if j := strings.Index(host, "]"); j > 0 {
				host = host[1:j]
			}
		} else {
			host = host[:i]
		}
	}
	return host
}

// humanDuration formata uma duração para o "uptime" do overview.
func humanDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	}
	days := int(d.Hours()) / 24
	return fmt.Sprintf("%dd %dh", days, int(d.Hours())%24)
}
