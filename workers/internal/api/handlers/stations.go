package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"radiocheck/internal/auth"
	"radiocheck/internal/catalog"
	"radiocheck/internal/probe"
)

type StationsHandler struct {
	Repo *catalog.Stations
	// Workers expõe o snapshot de workers vivos pro caminho híbrido do teste
	// "worker". Satisfeito por *supervisor.Supervisor. nil em testes de
	// validação (o caminho live é pulado → cai em efêmero).
	Workers workerLister
	// Limiter limita probes concorrentes que spawnam ffmpeg/ffprobe. nil → o
	// handler cria um default lazy (capacidade probeConcurrency).
	Limiter *probe.Limiter
}

const probeConcurrency = 8

func (h *StationsHandler) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	in := catalog.ListInput{
		Q:     q.Get("q"),
		Band:  q.Get("band"),
		State: q.Get("state"),
		City:  q.Get("city"),
	}
	if raw := q.Get("station_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			http.Error(w, "invalid station_id", 400)
			return
		}
		in.StationID = &id
	}
	// ids: conjunto fechado de emissoras (csv de uuids). Quem já tem os uuids
	// e só quer os rótulos — o seletor do /insights parte de
	// campaigns.target_stations. Sem isto o caller cairia na paginação padrão
	// de 20 e receberia, em silêncio, emissoras que não pediu.
	//
	// Não atravessa tenant (diferente de contracted_by logo abaixo): /stations
	// já é legível por qualquer usuário autenticado — emissora é dado de
	// catálogo, não de cliente. O teto de 500 é anti-abuso: mantém a query e o
	// payload limitados sem atrapalhar nenhum uso real (a maior campanha em
	// prod tem dezenas de emissoras).
	if raw := q.Get("ids"); raw != "" {
		parts := strings.Split(raw, ",")
		if len(parts) > 500 {
			http.Error(w, "ids max=500", 400)
			return
		}
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			id, err := uuid.Parse(part)
			if err != nil {
				http.Error(w, "invalid ids", 400)
				return
			}
			in.IDs = append(in.IDs, id)
		}
	}
	// contracted_by: emissoras que ESTES clientes têm contratadas agora. Lista
	// separada por vírgula porque usuário de agência tem carteira.
	//
	// Este é o único filtro de /stations que atravessa tenant, então cada id
	// passa por ScopeAllows: cliente só enxerga a própria carteira, e pedir a
	// de outro responde 404 (anti-oracle, mesmo padrão de /campaigns/{id} e
	// /detections). Admin/operator não tem escopo e escolhe livre.
	if raw := q.Get("contracted_by"); raw != "" {
		for _, part := range strings.Split(raw, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			id, err := uuid.Parse(part)
			if err != nil {
				http.Error(w, "invalid contracted_by", 400)
				return
			}
			if !auth.ScopeAllows(r.Context(), id) {
				http.Error(w, "not found", 404)
				return
			}
			in.ContractedBy = append(in.ContractedBy, id)
		}
	}
	if p, _ := strconv.Atoi(q.Get("page")); p > 0 {
		in.Page = p
	}
	// Cap was 2000 originally — bumped to 10000 in 2026-05-15 because the
	// Audiency import populates ~4-5k Brazilian stations with monitoring_status
	// default 'paused', which sort below 'active'/'calibrating' in the List
	// ordering. Campaigns referencing target_stations beyond the 2000 window
	// got their stations invisibly dropped from the wizard's allStations
	// pre-fetch — making them un-removable from the campaign UI.
	if l, _ := strconv.Atoi(q.Get("limit")); l > 0 && l <= 10000 {
		in.Limit = l
	}

	out, err := h.Repo.List(r.Context(), in)
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	writeJSON(w, 200, out)
}

// Suggest alimenta o dropdown de busca de /stations: emissoras, cidades e UF
// agrupadas. Abaixo de catalog.SuggestMinChars devolve os três grupos vazios
// (200, não 400) — o campo chama a cada tecla e um 4xx só polui o console.
func (h *StationsHandler) Suggest(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	out, err := h.Repo.Suggest(r.Context(), catalog.SuggestInput{
		Q:    q.Get("q"),
		Band: q.Get("band"),
	})
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	writeJSON(w, 200, out)
}

func (h *StationsHandler) Create(w http.ResponseWriter, r *http.Request) {
	var in catalog.CreateStationInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if in.Name == "" || in.Band == "" || in.StreamURL == "" {
		http.Error(w, "name, band, and stream_url are required", 400)
		return
	}
	if in.Band != "AM" && in.Band != "FM" {
		http.Error(w, "band must be AM or FM", 400)
		return
	}
	out, err := h.Repo.Create(r.Context(), in)
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	writeJSON(w, 201, out)
}

func (h *StationsHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}
	st, err := h.Repo.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", 404)
		} else {
			http.Error(w, "internal error", 500)
		}
		return
	}
	writeJSON(w, 200, st)
}

func (h *StationsHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}
	var in catalog.UpdateStationInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if in.Name == "" || in.Band == "" || in.StreamURL == "" {
		http.Error(w, "name, band, and stream_url are required", 400)
		return
	}
	if in.Band != "AM" && in.Band != "FM" {
		http.Error(w, "band must be AM or FM", 400)
		return
	}
	st, err := h.Repo.Update(r.Context(), id, in)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", 404)
		} else {
			http.Error(w, "internal error", 500)
		}
		return
	}
	writeJSON(w, 200, st)
}

// GetThreshold returns the calibration status (min_hashes, days elapsed, mode)
// for a station — surface the fase2 calibration job state for operators.
func (h *StationsHandler) GetThreshold(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid station id", http.StatusBadRequest)
		return
	}
	status, err := h.Repo.GetCalibrationStatus(r.Context(), id)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(status)
}

// UpdateStreamURL troca SOMENTE a stream_url da emissora. Caminho dedicado e
// seguro usado pela etapa Conexão do wizard — ver catalog.UpdateStreamURL.
func (h *StationsHandler) UpdateStreamURL(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}
	var in struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if in.URL == "" {
		http.Error(w, "url is required", 400)
		return
	}
	if !validStreamScheme(in.URL) {
		http.Error(w, "url must be http or https", 400)
		return
	}
	st, err := h.Repo.UpdateStreamURL(r.Context(), id, in.URL)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", 404)
		} else {
			http.Error(w, "internal error", 500)
		}
		return
	}
	writeJSON(w, 200, st)
}

// connectionTestRequest é o body de POST /stations/{id}/connection-test.
type connectionTestRequest struct {
	Tests []string `json:"tests"` // subset de ping|stream|worker; vazio = todos
	URL   string   `json:"url"`   // override opcional
}

// ConnectionTest roda os diagnósticos pedidos (ping/stream/worker) na URL salva
// ou no override. Efêmero: nunca sobe worker permanente. Ver
// docs/features/campaign-connection-step.md.
func (h *StationsHandler) ConnectionTest(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}
	var req connectionTestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	st, err := h.Repo.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", 404)
		} else {
			http.Error(w, "internal error", 500)
		}
		return
	}

	savedURL := st.StreamURL
	targetURL := savedURL
	if req.URL != "" {
		targetURL = req.URL
	}

	if h.Limiter == nil {
		h.Limiter = probe.NewLimiter(probeConcurrency)
	}

	want := func(name string) bool {
		if len(req.Tests) == 0 {
			return true
		}
		for _, t := range req.Tests {
			if t == name {
				return true
			}
		}
		return false
	}

	results := map[string]probe.TestResult{
		"ping":   {Status: probe.StatusSkipped},
		"stream": {Status: probe.StatusSkipped},
		"worker": {Status: probe.StatusSkipped},
	}

	if want("ping") {
		results["ping"] = probe.Ping(r.Context(), targetURL)
	}
	if want("stream") {
		results["stream"] = probe.ProbeStream(r.Context(), h.Limiter, targetURL)
	}
	if want("worker") {
		results["worker"] = h.workerTest(r, id, targetURL, savedURL)
	}

	writeJSON(w, 200, map[string]any{
		"station_id": id.String(),
		"tested_url": targetURL,
		"results":    results,
	})
}

// workerTest implementa o caminho híbrido: status ao vivo se há worker fresco na
// URL salva; senão probe efêmero.
func (h *StationsHandler) workerTest(r *http.Request, id uuid.UUID, targetURL, savedURL string) probe.TestResult {
	var live *probe.LiveWorker
	if h.Workers != nil {
		for _, ws := range h.Workers.WorkerStatuses() {
			if ws.StationID == id.String() {
				live = &probe.LiveWorker{Active: ws.Active, LastPCMAt: ws.LastPCMAt}
				break
			}
		}
	}
	source, useLive := probe.PickWorkerSource(targetURL, savedURL, live)
	if useLive {
		return probe.TestResult{
			Status:    probe.StatusOK,
			Source:    source,
			LastPCMAt: live.LastPCMAt.Format(time.RFC3339),
		}
	}
	return probe.ProbeIngest(r.Context(), h.Limiter, targetURL, 5*time.Second)
}

// validStreamScheme aceita só http/https (mitiga SSRF no override/salvar).
func validStreamScheme(raw string) bool {
	return len(raw) >= 7 && (raw[:7] == "http://" || (len(raw) >= 8 && raw[:8] == "https://"))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
