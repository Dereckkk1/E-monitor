package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"radiocheck/internal/acoes"
	"radiocheck/internal/auth"
	"radiocheck/internal/catalog"
	"radiocheck/internal/hub"
)

type ClientsHandler struct {
	Repo *catalog.Clients
	// Hub é opcional: nil ou não configurado significa "esta instalação não
	// avisa o hub", e a criação de cliente segue igual. É o mesmo portão que o
	// SSO já usa, e é o que faz dev e teste não baterem em produção.
	Hub *hub.Client
}

// List supports two modes:
//   - Legacy/unpaged (no ?page or ?page_size): returns the full list, ordered
//     by name. Compatible with callers that need the entire catalog (e.g. the
//     campaign dropdown's client map).
//   - Paged (?page + ?page_size): returns {data, total, total_pages, page,
//     page_size}. Optional ?q filters by name/city/state/cnpj/email/contact.
func (h *ClientsHandler) List(w http.ResponseWriter, r *http.Request) {
	// Viewer scope: cliente enxerga a própria carteira (1 item no caso comum,
	// N no caso de agência). Mesmo envelope {data: [...]} da lista global — a
	// UI trata igual, sem gates de role, e usa o tamanho da lista pra decidir
	// se mostra o seletor de cliente. Paginação não se aplica.
	if scopes := auth.ClientScopesFromContext(r.Context()); scopes != nil {
		items, err := h.Repo.ListByIDs(r.Context(), scopes)
		if err != nil {
			http.Error(w, "internal error", 500)
			return
		}
		writeJSON(w, 200, map[string]any{"data": items})
		return
	}

	q := r.URL.Query()
	pageStr := q.Get("page")
	sizeStr := q.Get("page_size")
	if pageStr == "" && sizeStr == "" && q.Get("q") == "" {
		items, err := h.Repo.List(r.Context())
		if err != nil {
			http.Error(w, "internal error", 500)
			return
		}
		writeJSON(w, 200, map[string]any{"data": items})
		return
	}

	page, _ := strconv.Atoi(pageStr)
	if page < 1 {
		page = 1
	}
	size, _ := strconv.Atoi(sizeStr)
	if size < 1 {
		size = 20
	}
	if size > 200 {
		size = 200
	}

	// include_inactive=1|true: management page's "mostrar inativos" toggle.
	// Default false hides deactivated clients from the list.
	includeInactive := q.Get("include_inactive") == "1" || q.Get("include_inactive") == "true"
	items, total, err := h.Repo.ListPaged(r.Context(), q.Get("q"), page, size, includeInactive)
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	totalPages := (total + size - 1) / size
	if totalPages < 1 {
		totalPages = 1
	}
	writeJSON(w, 200, map[string]any{
		"data":        items,
		"total":       total,
		"total_pages": totalPages,
		"page":        page,
		"page_size":   size,
	})
}

// servico monta as ações de cliente sobre o repositório e o hub deste handler. A
// regra de criação (validação, conferência do CNPJ, gravação, aviso ao hub) mora
// em internal/acoes: o MCP chama as mesmas funções. O hub só entra quando
// existe (um *hub.Client nil dentro da interface passaria no `!= nil`).
func (h *ClientsHandler) servico() *acoes.Servico {
	s := &acoes.Servico{Clientes: h.Repo}
	if h.Hub != nil {
		s.Hub = h.Hub
	}
	return s
}

func (h *ClientsHandler) Create(w http.ResponseWriter, r *http.Request) {
	var in catalog.CreateClientInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "invalid request", 400)
		return
	}
	out, err := h.servico().CriarCliente(r.Context(), in)
	if err != nil {
		if !escreveRecusa(w, err) {
			http.Error(w, "internal error", 500)
		}
		return
	}
	writeJSON(w, 201, out)
}

func (h *ClientsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}
	if err := h.Repo.Delete(r.Context(), id); err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			http.Error(w, "not found", 404)
		case errors.Is(err, catalog.ErrClientHasDependents):
			// Hard-delete refused by a foreign key. Tell the operator exactly
			// what's blocking so the UI can offer the reversible "deactivate"
			// path instead of a silent 500. Counts are best-effort: if the
			// breakdown query fails we still return 409 (the conflict is real).
			counts, cerr := h.Repo.CountDependents(r.Context(), id)
			if cerr != nil {
				counts = catalog.DependentCounts{}
			}
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":     "client_has_dependents",
				"campaigns": counts.Campaigns,
				"materials": counts.Materials,
				"users":     counts.Users,
			})
		default:
			http.Error(w, "internal error", 500)
		}
		return
	}
	w.WriteHeader(204)
}

// Deactivate flips is_active=false (reversible "Desativar"). The client stays
// in the DB with all its campaigns/materials/users intact, disappears from the
// management list by default, and its users can no longer log in.
func (h *ClientsHandler) Deactivate(w http.ResponseWriter, r *http.Request) {
	h.setActive(w, r, false)
}

// Activate flips is_active=true (reactivate a previously deactivated client).
func (h *ClientsHandler) Activate(w http.ResponseWriter, r *http.Request) {
	h.setActive(w, r, true)
}

func (h *ClientsHandler) setActive(w http.ResponseWriter, r *http.Request, active bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}
	out, err := h.Repo.SetActive(r.Context(), id, active)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", 404)
		} else {
			http.Error(w, "internal error", 500)
		}
		return
	}
	writeJSON(w, 200, out)
}

func (h *ClientsHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}
	var in catalog.UpdateClientInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "invalid request", 400)
		return
	}
	if in.Name == "" {
		http.Error(w, "name is required", 400)
		return
	}
	if !acoes.NormalizarRotuloPublico(&in.TargetLabel) {
		http.Error(w, "target_label must be at most 200 characters", 400)
		return
	}
	/* O CNPJ só é conferido quando os DÍGITOS mudam: duplicata que já existe
	   (A e B com os mesmos 14 dígitos) não pode travar a edição de B. */
	atual, err := h.Repo.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", 404)
		} else {
			http.Error(w, "internal error", 500)
		}
		return
	}
	if acoes.DigitosDoCNPJ(atual.CNPJ) != acoes.DigitosDoCNPJ(in.CNPJ) {
		if err := h.servico().ChecarCNPJ(r.Context(), in.CNPJ, id); err != nil {
			if !escreveRecusa(w, err) {
				http.Error(w, "internal error", 500)
			}
			return
		}
	}
	out, err := h.Repo.Update(r.Context(), id, in)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", 404)
		} else {
			http.Error(w, "internal error", 500)
		}
		return
	}
	writeJSON(w, 200, out)
}
