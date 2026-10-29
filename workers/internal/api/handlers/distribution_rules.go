package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"radiocheck/internal/acoes"
	"radiocheck/internal/auth"
	"radiocheck/internal/catalog"
)

type DistributionRulesHandler struct {
	Repo *catalog.DistributionRules
	// CampaignRepo: só usado por ListByCampaign pra scope check (404
	// anti-oracle quando viewer pede regras de campanha de outro cliente).
	// Nil-safe — quem só faz writes (admin/operator) pode passar nil.
	CampaignRepo *catalog.Campaigns
	// EmSegundoPlano roda a recategorização que não pode atrasar a resposta.
	// nil = goroutine (produção); os testes injetam um executor que espera.
	EmSegundoPlano func(func())
}

// servico monta as ações de regra sobre o repositório deste handler. A
// regra de negócio (validação, gravação, recategorização) mora em
// internal/acoes: o MCP chama as mesmas funções.
func (h *DistributionRulesHandler) servico() *acoes.Servico {
	return &acoes.Servico{Regras: h.Repo, EmSegundoPlano: h.EmSegundoPlano}
}

type rulePayload struct {
	TypeID      uuid.UUID   `json:"type_id"`
	StationIDs  []uuid.UUID `json:"station_ids"`
	MaterialIDs []uuid.UUID `json:"material_ids"`
	Name        string      `json:"name"`       // rótulo opcional do conjunto
	StartDate   string      `json:"start_date"` // YYYY-MM-DD
	EndDate     string      `json:"end_date"`
	WeekdayMask int16       `json:"weekday_mask"`
	TimeStart   string      `json:"time_start"` // HH:MM
	TimeEnd     string      `json:"time_end"`
	PlaysPerDay int16       `json:"plays_per_day"`
}

func (p *rulePayload) toEntrada() (acoes.EntradaRegra, error) {
	start, err := time.Parse("2006-01-02", p.StartDate)
	if err != nil {
		return acoes.EntradaRegra{}, err
	}
	end, err := time.Parse("2006-01-02", p.EndDate)
	if err != nil {
		return acoes.EntradaRegra{}, err
	}
	return acoes.EntradaRegra{
		TipoID:      p.TypeID,
		EmissoraIDs: p.StationIDs,
		MaterialIDs: p.MaterialIDs,
		Nome:        p.Name,
		Inicio:      start, Fim: end,
		MascaraDias: p.WeekdayMask,
		HoraInicio:  p.TimeStart, HoraFim: p.TimeEnd,
		InsercoesPorDia: p.PlaysPerDay,
	}, nil
}

func (h *DistributionRulesHandler) Create(w http.ResponseWriter, r *http.Request) {
	campaignID, err := uuid.Parse(chi.URLParam(r, "campaignID"))
	if err != nil {
		http.Error(w, "invalid campaignID", http.StatusBadRequest)
		return
	}
	var p rulePayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	in, err := p.toEntrada()
	if err != nil {
		http.Error(w, "invalid dates: "+err.Error(), http.StatusBadRequest)
		return
	}
	// Grava e recategoriza em segundo plano (best-effort) as detections que a
	// regra possa afetar.
	rule, err := h.servico().CriarRegra(r.Context(), campaignID, in)
	if err != nil {
		if !escreveRecusa(w, err) {
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}
	writeJSON(w, http.StatusCreated, rule)
}

func (h *DistributionRulesHandler) ListByCampaign(w http.ResponseWriter, r *http.Request) {
	campaignID, err := uuid.Parse(chi.URLParam(r, "campaignID"))
	if err != nil {
		http.Error(w, "invalid campaignID", http.StatusBadRequest)
		return
	}
	// Viewer scope: 404 quando a campanha não pertence ao cliente do JWT.
	if auth.ClientScopesFromContext(r.Context()) != nil && h.CampaignRepo != nil {
		camp, err := h.CampaignRepo.Get(r.Context(), campaignID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				http.Error(w, "not found", http.StatusNotFound)
			} else {
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
			return
		}
		if !auth.ScopeAllows(r.Context(), camp.ClientID) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
	}
	rules, err := h.Repo.ListByCampaign(r.Context(), campaignID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if rules == nil {
		rules = []catalog.DistributionRule{}
	}
	writeJSON(w, http.StatusOK, rules)
}

func (h *DistributionRulesHandler) Update(w http.ResponseWriter, r *http.Request) {
	campaignID, err := uuid.Parse(chi.URLParam(r, "campaignID"))
	if err != nil {
		http.Error(w, "invalid campaignID", http.StatusBadRequest)
		return
	}
	ruleID, err := uuid.Parse(chi.URLParam(r, "ruleID"))
	if err != nil {
		http.Error(w, "invalid ruleID", http.StatusBadRequest)
		return
	}
	var p rulePayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	in, err := p.toEntrada()
	if err != nil {
		http.Error(w, "invalid dates: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := h.servico().EditarRegra(r.Context(), campaignID, ruleID, in); err != nil {
		if !escreveRecusa(w, err) {
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *DistributionRulesHandler) Delete(w http.ResponseWriter, r *http.Request) {
	campaignID, err := uuid.Parse(chi.URLParam(r, "campaignID"))
	if err != nil {
		http.Error(w, "invalid campaignID", http.StatusBadRequest)
		return
	}
	ruleID, err := uuid.Parse(chi.URLParam(r, "ruleID"))
	if err != nil {
		http.Error(w, "invalid ruleID", http.StatusBadRequest)
		return
	}
	// Após o delete o escopo da regra se perde — a ação recategoriza a campanha toda.
	if err := h.servico().ApagarRegra(r.Context(), campaignID, ruleID); err != nil {
		if !escreveRecusa(w, err) {
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
