package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"radiocheck/internal/acoes"
	"radiocheck/internal/catalog"
)

// OverrideRepo é a fatia de catalog.DistributionOverrides que o handler usa —
// interface p/ permitir mock no teste sem DB.
type OverrideRepo interface {
	Upsert(ctx context.Context, in catalog.UpsertOverrideInput) error
	Delete(ctx context.Context, campaignID, typeID, stationID uuid.UUID, forDate time.Time) error
	ListByCampaignAndDateRange(ctx context.Context, campaignID uuid.UUID, from, to time.Time) ([]catalog.DistributionOverride, error)
}

// OverrideRecategorizer redispara a recat de uma célula após mudança de override.
// Satisfeita por *catalog.DistributionRules.
type OverrideRecategorizer interface {
	RecategorizeForOverride(ctx context.Context, campaignID, typeID, stationID uuid.UUID, forDate time.Time) error
}

type DistributionOverridesHandler struct {
	Repo  OverrideRepo
	Recat OverrideRecategorizer
}

// servico monta as ações de ajuste sobre o repositório e o recategorizador deste
// handler (nil-safe: sem Recat o ajuste é gravado e a recategorização é
// pulada). A regra de negócio mora em internal/acoes; o MCP chama as mesmas
// funções.
func (h *DistributionOverridesHandler) servico() *acoes.Servico {
	s := &acoes.Servico{}
	if h.Repo != nil {
		s.Ajustes = h.Repo
	}
	if h.Recat != nil {
		s.RecatCelula = h.Recat
	}
	return s
}

type overridePayload struct {
	TypeID        uuid.UUID `json:"type_id"`
	StationID     uuid.UUID `json:"station_id"`
	ForDate       string    `json:"for_date"` // YYYY-MM-DD
	PlaysExpected int16     `json:"plays_expected"`
	TimeStart     string    `json:"time_start"` // "HH:MM"
	TimeEnd       string    `json:"time_end"`   // "HH:MM"
	Reason        *string   `json:"reason,omitempty"`
}

func (h *DistributionOverridesHandler) Upsert(w http.ResponseWriter, r *http.Request) {
	campaignID, err := uuid.Parse(chi.URLParam(r, "campaignID"))
	if err != nil {
		http.Error(w, "invalid campaignID", http.StatusBadRequest)
		return
	}
	var p overridePayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	date, err := time.Parse("2006-01-02", p.ForDate)
	if err != nil {
		http.Error(w, "for_date must be YYYY-MM-DD", http.StatusBadRequest)
		return
	}
	// Grava e recategoriza a célula afetada.
	//
	// A recategorização é SÍNCRONA (NÃO goroutine): o frontend refetcha
	// detections/daily-summary no onSuccess deste 204, então a recat precisa ter
	// commitado ANTES da resposta — senão o refetch pinta a categoria velha
	// (race). Escopo de célula = poucas linhas, latência desprezível. Best-effort:
	// o override já foi gravado, então erro na recat não falha o request.
	if err := h.servico().DefinirAjuste(r.Context(), acoes.EntradaAjuste{
		CampanhaID: campaignID, TipoID: p.TypeID, EmissoraID: p.StationID,
		Dia: date, Insercoes: p.PlaysExpected,
		HoraInicio: p.TimeStart, HoraFim: p.TimeEnd,
		Motivo: p.Reason,
	}); err != nil {
		if !escreveRecusa(w, err) {
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *DistributionOverridesHandler) Delete(w http.ResponseWriter, r *http.Request) {
	campaignID, err := uuid.Parse(chi.URLParam(r, "campaignID"))
	if err != nil {
		http.Error(w, "invalid campaignID", http.StatusBadRequest)
		return
	}
	var p overridePayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	date, err := time.Parse("2006-01-02", p.ForDate)
	if err != nil {
		http.Error(w, "for_date must be YYYY-MM-DD", http.StatusBadRequest)
		return
	}
	// Mesma recategorização síncrona do Upsert (a célula reverte para a regra).
	if err := h.servico().ApagarAjuste(r.Context(), campaignID, p.TypeID, p.StationID, date); err != nil {
		if !escreveRecusa(w, err) {
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *DistributionOverridesHandler) ListByDateRange(w http.ResponseWriter, r *http.Request) {
	campaignID, err := uuid.Parse(chi.URLParam(r, "campaignID"))
	if err != nil {
		http.Error(w, "invalid campaignID", http.StatusBadRequest)
		return
	}
	fromStr := r.URL.Query().Get("from")
	toStr := r.URL.Query().Get("to")
	from, err := time.Parse("2006-01-02", fromStr)
	if err != nil {
		http.Error(w, "from must be YYYY-MM-DD", http.StatusBadRequest)
		return
	}
	to, err := time.Parse("2006-01-02", toStr)
	if err != nil {
		http.Error(w, "to must be YYYY-MM-DD", http.StatusBadRequest)
		return
	}
	overrides, err := h.Repo.ListByCampaignAndDateRange(r.Context(), campaignID, from, to)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if overrides == nil {
		overrides = []catalog.DistributionOverride{}
	}
	writeJSON(w, http.StatusOK, overrides)
}
