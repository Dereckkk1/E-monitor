// reports.go — Relatórios consolidados de campanha (CSV + JSON pro PDF).
//
// Três fluxos cobertos:
//   - GET /reports/campaigns/{id}/consolidated.csv → CSV resumo por (material × emissora)
//   - GET /reports/campaigns/{id}/summary          → JSON com tudo pra montar o PDF no front
//   - O CSV detalhado já existe em /detections/export — não duplicamos.
//
// Acesso: viewer (escopo do próprio cliente) também pode baixar — os
// handlers verificam ClientScopesFromContext igual /detections/aggregate-by-material.

package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"radiocheck/internal/auth"
	"radiocheck/internal/catalog"
	"radiocheck/internal/reportcsv"
)

type ReportsHandler struct {
	Detections   *catalog.Detections
	CampaignRepo *catalog.Campaigns
	Pool         *pgxpool.Pool // used for the small client+campaign+counts compose
}

// fetchClientName devolve o nome do cliente da campanha (e CNPJ, se houver)
// pra ser usado no cabeçalho dos relatórios.
type clientHeader struct {
	ID   uuid.UUID
	Name string
	CNPJ *string
	// TargetLabel é o rótulo do público-alvo do cliente; nil = sem rótulo.
	TargetLabel *string
}

func (h *ReportsHandler) fetchClientByCampaign(r *http.Request, campaignID uuid.UUID) (*clientHeader, error) {
	var c clientHeader
	err := h.Pool.QueryRow(r.Context(), `
		SELECT cl.id, cl.name, cl.cnpj, cl.target_label
		FROM campaigns cmp
		JOIN clients   cl ON cl.id = cmp.client_id
		WHERE cmp.id = $1`, campaignID,
	).Scan(&c.ID, &c.Name, &c.CNPJ, &c.TargetLabel)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// parseFilter centraliza o parsing dos query params comuns aos dois endpoints:
// campaign_id (path), from/to (RFC3339, opcional). Garante o scope check do
// viewer antes de devolver o filter pronto pro repo.
func (h *ReportsHandler) parseFilter(w http.ResponseWriter, r *http.Request) (catalog.AggregateFilter, *catalog.Campaign, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid campaign id", http.StatusBadRequest)
		return catalog.AggregateFilter{}, nil, false
	}
	camp, err := h.CampaignRepo.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", http.StatusNotFound)
		} else {
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return catalog.AggregateFilter{}, nil, false
	}
	// Viewer scope: 404 (não 403) pra não vazar existência.
	if !auth.ScopeAllows(r.Context(), camp.ClientID) {
		http.Error(w, "not found", http.StatusNotFound)
		return catalog.AggregateFilter{}, nil, false
	}

	q := r.URL.Query()
	f := catalog.AggregateFilter{CampaignID: id}
	if v := q.Get("from"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			http.Error(w, "invalid from (use RFC3339)", http.StatusBadRequest)
			return catalog.AggregateFilter{}, nil, false
		}
		f.StartDate = &t
	}
	if v := q.Get("to"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			http.Error(w, "invalid to (use RFC3339)", http.StatusBadRequest)
			return catalog.AggregateFilter{}, nil, false
		}
		f.EndDate = &t
	}
	return f, camp, true
}

// slugify simplifica o nome da campanha pra entrar no filename do CSV/PDF
// sem caracteres que quebram cabeçalho HTTP ou filesystem.
func slugify(s string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevDash = false
		case r == ' ' || r == '-' || r == '_':
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "campanha"
	}
	if len(out) > 60 {
		out = out[:60]
	}
	return out
}

// Consolidated streams CSV with one row per (material × station). Inclui BOM
// UTF-8 e separador ';' pra abrir no Excel pt-BR exatamente como o detalhado.
func (h *ReportsHandler) Consolidated(w http.ResponseWriter, r *http.Request) {
	f, camp, ok := h.parseFilter(w, r)
	if !ok {
		return
	}

	rows, err := h.Detections.AggregateByMaterialStation(r.Context(), f)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// O rótulo do público-alvo entra no CABEÇALHO das colunas "no target".
	// Aqui é seguro: o CSV consolidado é sempre de UMA campanha, logo de um
	// único cliente (diferente do /detections/export, que é multi-campanha).
	client, err := h.fetchClientByCampaign(r, camp.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	targetSuffix := ""
	if client.TargetLabel != nil && strings.TrimSpace(*client.TargetLabel) != "" {
		targetSuffix = " (" + strings.TrimSpace(*client.TargetLabel) + ")"
	}

	stamp := time.Now().Format("20060102_150405")
	filename := fmt.Sprintf("relatorio-consolidado-%s-%s.csv", slugify(camp.Name), stamp)
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.WriteHeader(http.StatusOK)

	// A formatação mora em internal/reportcsv porque o bundle .zip do pós-venda
	// escreve o MESMO arquivo em memória. Erro aqui não vira 500: o header já
	// foi escrito, então só cortamos o stream.
	_ = reportcsv.WriteConsolidated(w, rows, targetSuffix)
}

// SummaryResponse é a payload do JSON usada pelo PDF builder no front.
type SummaryResponse struct {
	Campaign struct {
		ID        uuid.UUID `json:"id"`
		Name      string    `json:"name"`
		Status    string    `json:"status"`
		StartDate time.Time `json:"start_date"`
		EndDate   time.Time `json:"end_date"`
	} `json:"campaign"`
	Client struct {
		ID   uuid.UUID `json:"id"`
		Name string    `json:"name"`
		CNPJ *string   `json:"cnpj,omitempty"`
		// TargetLabel: rótulo do público-alvo, usado pelo PDF como sufixo
		// dos números "no target". Sem omitempty — `null` é significativo.
		TargetLabel *string `json:"target_label"`
	} `json:"client"`
	Period struct {
		From *time.Time `json:"from,omitempty"`
		To   *time.Time `json:"to,omitempty"`
	} `json:"period"`
	Totals struct {
		Detections        int `json:"detections"`
		DistinctMaterials int `json:"distinct_materials"`
		DistinctStations  int `json:"distinct_stations"`
		// Impactos = Σ (veiculações da emissora × PMM). ImpactosTarget usa o
		// PMM no target; StationsWithTarget > 0 é o gate de exibição no PDF.
		Impactos           int64 `json:"impactos"`
		ImpactosTarget     int64 `json:"impactos_target"`
		StationsWithTarget int   `json:"stations_with_target"`
	} `json:"totals"`
	ByMaterial        []catalog.MaterialAggregateRow `json:"by_material"`
	ByStation         []catalog.StationAggregateRow  `json:"by_station"`
	ByMaterialStation []catalog.MaterialStationRow   `json:"by_material_station"`
	GeneratedAt       time.Time                      `json:"generated_at"`
}

// Summary monta o JSON que o front transforma em PDF. Fica de propósito
// num único round-trip pra UX ser previsível ("Gerar PDF" → toast → download).
func (h *ReportsHandler) Summary(w http.ResponseWriter, r *http.Request) {
	f, camp, ok := h.parseFilter(w, r)
	if !ok {
		return
	}
	client, err := h.fetchClientByCampaign(r, camp.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "client not found", http.StatusNotFound)
		} else {
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}

	byMaterial, err := h.Detections.AggregateByMaterial(r.Context(), f)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	byStation, err := h.Detections.AggregateByStation(r.Context(), f)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	byMatSta, err := h.Detections.AggregateByMaterialStation(r.Context(), f)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	var resp SummaryResponse
	resp.Campaign.ID = camp.ID
	resp.Campaign.Name = camp.Name
	resp.Campaign.Status = camp.Status
	resp.Campaign.StartDate = camp.StartDate
	resp.Campaign.EndDate = camp.EndDate
	resp.Client.ID = client.ID
	resp.Client.Name = client.Name
	resp.Client.CNPJ = client.CNPJ
	resp.Client.TargetLabel = client.TargetLabel
	resp.Period.From = f.StartDate
	resp.Period.To = f.EndDate
	resp.Totals.Detections = byMaterial.TotalDetections
	resp.Totals.DistinctMaterials = byMaterial.DistinctMaterials
	resp.Totals.DistinctStations = len(byStation)
	// Impactos derivados de byStation (uma linha por emissora), não de
	// byMaterialStation — senão a mesma emissora entraria uma vez por material.
	//
	// math.Round, não truncamento: a coluna "Impactos" da tabela do PDF é
	// calculada no frontend com Math.round(pmm × count) (utils/pdfReport.js).
	// Truncar aqui faria o KPI do topo ficar ABAIXO da soma da própria coluna
	// no mesmo documento — stations.pmm é numeric(10,2), então o produto é
	// fracionário e a diferença chega a 1 por emissora.
	for _, s := range byStation {
		if s.StationPMM != nil {
			resp.Totals.Impactos += int64(math.Round(*s.StationPMM * float64(s.Count)))
		}
		if s.StationPMMTarget != nil {
			resp.Totals.ImpactosTarget += int64(*s.StationPMMTarget * s.Count)
			resp.Totals.StationsWithTarget++
		}
	}
	resp.ByMaterial = byMaterial.Data
	resp.ByStation = byStation
	resp.ByMaterialStation = byMatSta
	resp.GeneratedAt = time.Now().UTC()

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(resp)
}
