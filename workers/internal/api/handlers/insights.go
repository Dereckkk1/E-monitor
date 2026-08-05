package handlers

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"radiocheck/internal/auth"
	"radiocheck/internal/catalog"
)

// InsightsRepo é a dependência mínima do handler. Permite mockar
// catalog.Insights nos testes sem precisar de pool real.
type InsightsRepo interface {
	Compute(ctx context.Context, p catalog.InsightsParams) (*catalog.InsightsPayload, error)
}

type InsightsHandler struct {
	Repo InsightsRepo
}

func NewInsightsHandler(repo InsightsRepo) *InsightsHandler {
	return &InsightsHandler{Repo: repo}
}

// Get GET /insights
//
// Query params:
//   - client_id (uuid): obrigatório para admin e para viewer com carteira de
//     mais de um cliente; ignorado quando a carteira tem 1 cliente só (forçado
//     pelo escopo — anti-oracle).
//   - campaigns (csv de uuids): obrigatório, min 1, max 50.
//   - from, to (YYYY-MM-DD): opcional. Default = mês corrente.
//   - stations (csv de uuids): opcional. Vazio = todas.
//
// Resposta: catalog.InsightsPayload (JSON).
//
// Erros 403 quando uma das campanhas pedidas não pertence ao client_id
// resolvido (cross-client). Esse caminho é o que protege cliente B de
// ler dados do cliente A passando apenas o uuid.
//
// A tela é estruturalmente POR CLIENTE (catalog.InsightsParams.ClientID é um
// uuid, não lista): target PMM, CPM, investido e valor consolidado só têm
// significado comercial dentro de um cliente. Logo o usuário-agência escolhe
// UM cliente da carteira, igual o admin faz hoje. Ver spec §6.7 de
// docs/superpowers/specs/2026-08-04-multi-client-user-design.md.
func (h *InsightsHandler) Get(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	scopes := auth.ClientScopesFromContext(r.Context())

	// client_id — forçado pelo JWT quando a carteira tem um cliente só.
	var clientID uuid.UUID
	if len(scopes) == 1 {
		clientID = scopes[0]
	} else {
		cid := q.Get("client_id")
		if cid == "" {
			http.Error(w, "client_id required", http.StatusBadRequest)
			return
		}
		parsed, err := uuid.Parse(cid)
		if err != nil {
			http.Error(w, "invalid client_id", http.StatusBadRequest)
			return
		}
		// scopes == nil (admin) passa direto; viewer-agência precisa pedir um
		// cliente da própria carteira.
		if !auth.ScopeAllows(r.Context(), parsed) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		clientID = parsed
	}

	// campaigns
	camps, err := parseUUIDList(q.Get("campaigns"))
	if err != nil {
		http.Error(w, "invalid campaigns: "+err.Error(), http.StatusBadRequest)
		return
	}
	if len(camps) == 0 {
		http.Error(w, "campaigns required (csv of uuids, min 1)", http.StatusBadRequest)
		return
	}
	if len(camps) > 50 {
		http.Error(w, "campaigns max=50", http.StatusBadRequest)
		return
	}

	// stations (opcional)
	stations, err := parseUUIDList(q.Get("stations"))
	if err != nil {
		http.Error(w, "invalid stations: "+err.Error(), http.StatusBadRequest)
		return
	}
	if stations == nil {
		// pgx envia '{}' quando o slice é vazio. Garante array não-nil pra
		// que a SQL `$4::uuid[] = '{}'` funcione como flag de "sem filtro".
		stations = []uuid.UUID{}
	}

	// from / to — default = mês corrente
	now := time.Now().UTC()
	defaultFrom := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	defaultTo := defaultFrom.AddDate(0, 1, 0).Add(-time.Second)
	from := parseDateOr(q.Get("from"), defaultFrom)
	to := parseDateOr(q.Get("to"), defaultTo)
	if to.Before(from) {
		http.Error(w, "to must be on or after from", http.StatusBadRequest)
		return
	}

	out, err := h.Repo.Compute(r.Context(), catalog.InsightsParams{
		ClientID:    clientID,
		CampaignIDs: camps,
		From:        from,
		To:          to,
		StationIDs:  stations,
		// "Hoje" em America/Sao_Paulo: o valor consolidado acumula por mês
		// (ciclos mensais iniciados até hoje).
		Today: todaySaoPaulo(),
	})
	if err != nil {
		// Erros "cross-client" do repo viram 403 — não vazamos detalhe.
		if strings.Contains(err.Error(), "cross-client") {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// parseUUIDList aceita uma csv ("a,b,c") e devolve a lista parseada.
// String vazia → (nil, nil). Espaço em volta dos items é tolerado.
func parseUUIDList(s string) ([]uuid.UUID, error) {
	if s == "" {
		return nil, nil
	}
	parts := strings.Split(s, ",")
	out := make([]uuid.UUID, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		id, err := uuid.Parse(p)
		if err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, nil
}

// spLocation é America/Sao_Paulo, resolvido uma vez no init (tzdata está na
// imagem — ver workers.Dockerfile). Fallback pra UTC-3 fixo caso falte tzdata
// (Brasil não observa DST desde 2019).
var spLocation = func() *time.Location {
	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		return time.FixedZone("BRT", -3*3600)
	}
	return loc
}()

// todaySaoPaulo devolve o dia corrente em America/Sao_Paulo como date-only
// (meia-noite UTC daquele dia calendário). Usado pra acumular o consolidado
// por mês em /insights e /campaigns.
func todaySaoPaulo() time.Time {
	n := time.Now().In(spLocation)
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC)
}

func parseDateOr(s string, def time.Time) time.Time {
	if s == "" {
		return def
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return def
	}
	return t
}
