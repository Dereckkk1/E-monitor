package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
	"radiocheck/internal/auth"
	"radiocheck/internal/catalog"
	"radiocheck/internal/hub"
)

// validStatuses lists the four lifecycle states accepted by the ?status= filter.
var validStatuses = map[string]struct{}{
	"programada": {},
	"ativa":      {},
	"concluida":  {},
	"cancelada":  {},
}

type CampaignsHandler struct {
	Repo       *catalog.Campaigns
	Supervisor CampaignSupervisor
	Log        *zap.Logger // optional; used to surface Pause/Start/Reload failures
	// Hub é opcional: nil ou não configurado significa "esta instalação não
	// avisa o hub", e a criação de campanha segue igual. É o mesmo portão que
	// o SSO já usa, e é o que faz dev e teste não baterem em produção.
	Hub *hub.Client
}

// CampaignSupervisor is the subset of supervisor.Supervisor used by API handlers.
type CampaignSupervisor interface {
	Start(campaignID uuid.UUID) error
	Pause(campaignID uuid.UUID) error
	Reload(campaignID uuid.UUID) error
	// UpdateStations replaces target_stations on a campaign and reconciles
	// running workers without bouncing the campaign through 'cancelada' as
	// the legacy Pause+Start dance did.
	UpdateStations(campaignID uuid.UUID, newStations []uuid.UUID) error
	StopWorkersForCampaign(campaignID uuid.UUID)
}

func (h *CampaignsHandler) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	scope := auth.ClientScopesFromContext(r.Context())

	// Paged mode kicks in as soon as ?page or ?page_size shows up; legacy
	// callers (DetectionsPage, AirtimeReportPage, wizard layout etc.) keep
	// hitting the unpaged path so they can still build dropdowns from the
	// full catalog.
	if q.Get("page") != "" || q.Get("page_size") != "" || q.Get("q") != "" || q.Get("competence") != "" || q.Get("id") != "" {
		page, _ := strconv.Atoi(q.Get("page"))
		if page < 1 {
			page = 1
		}
		size, _ := strconv.Atoi(q.Get("page_size"))
		if size < 1 {
			size = 20
		}
		if size > 200 {
			size = 200
		}
		var campIDPtr *uuid.UUID
		if idStr := q.Get("id"); idStr != "" {
			cid, err := uuid.Parse(idStr)
			if err != nil {
				http.Error(w, "invalid id", 400)
				return
			}
			campIDPtr = &cid
		}
		items, total, err := h.Repo.ListPaged(r.Context(), q.Get("q"), q.Get("competence"), scope, campIDPtr, page, size)
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
		return
	}

	// ?status=programada,ativa,concluida,cancelada — CSV, optional.
	var statuses []string
	if raw := q.Get("status"); raw != "" {
		for _, s := range strings.Split(raw, ",") {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			if _, ok := validStatuses[s]; !ok {
				http.Error(w, "invalid status filter: "+s, 400)
				return
			}
			statuses = append(statuses, s)
		}
	}
	items, err := h.Repo.ListFiltered(r.Context(), statuses, scope)
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	writeJSON(w, 200, map[string]any{"data": items})
}

func (h *CampaignsHandler) Create(w http.ResponseWriter, r *http.Request) {
	var in catalog.CreateCampaignInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "invalid request", 400)
		return
	}
	if in.Name == "" || in.ClientID == uuid.Nil {
		http.Error(w, "name and client_id are required", 400)
		return
	}

	// A barreira do §4.4. O mesmo helper serve o `Update` — ver o comentário
	// dele para o porquê de ser compartilhado.
	canonico, ok := h.conferirCodigoOuRecusar(w, r, in.ClientID, in.HubCode)
	if !ok {
		return
	}
	in.HubCode = canonico

	out, err := h.Repo.Create(r.Context(), in)
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	avisarHubDaCampanha(h.Hub, h.Repo, out)
	writeJSON(w, 201, out)
}

/*
conferirCodigoOuRecusar é a barreira de verdade do §4.4 da spec, compartilhada
pelo `Create` e pelo `Update`.

Devolve o código na forma CANÔNICA e `ok` verdadeiro quando dá para seguir.
Quando devolve `ok` falso, a resposta de erro JÁ FOI ESCRITA — quem chama só
precisa voltar.

⚠️ COMPARTILHADA de propósito, e isso é conserto de um buraco real. O plano
desta série conferia só no `Create`; com isso a barreira virava opcional em duas
requisições:

	POST /campaigns      {"hub_code": ""}                   → 201 (a decisão 6 permite)
	PUT  /campaigns/{id} {"hub_code": "<de outro cliente>"} → gravava, e emitia

E era pior que pular a conferência: o `AtualizarHubCode` ZERA o
`hub_notified_at`, então a campanha entrava na fila do job do §8 e passava a
bater no hub de 15 em 15 minutos com o código do cliente errado, para sempre.

⚠️ TRÊS desfechos, e eles não são o mesmo:
  - o hub diz que o código é de OUTRO cliente → 422. É erro de quem digitou, e a
    mensagem diz de quem é o código para a pessoa conseguir achar o certo;
  - o hub diz 404 → 422, idem;
  - o hub NÃO respondeu (mudo, 401, 429, 5xx) → SEGUE. Decisão 2 da spec: uma
    queda do hub não pode parar o cadastro de campanha no E-monitor. O
    `hub_notified_at` fica nulo e o job de reemissão leva a campanha quando o hub
    voltar. Recusar aqui seria o contrário do que a decisão 2 pede.

⚠️ E o que volta é CANÔNICO. Sem isto, `EH-7K4M2X` e `eh7k4m2x` viram códigos
diferentes aqui enquanto para o hub são o mesmo, e a §10 exige a forma canônica
na coluna. Vale inclusive quando o hub não respondeu — aí a normalização é
local, que é o que `hub.NormalizaCodigo` faz sem tocar a rede.
*/
func (h *CampaignsHandler) conferirCodigoOuRecusar(
	w http.ResponseWriter, r *http.Request, clientID uuid.UUID, bruto string,
) (canonico string, ok bool) {
	canonico = hub.NormalizaCodigo(bruto)
	if canonico == "" {
		if strings.TrimSpace(bruto) != "" {
			/* Veio alguma coisa, e não é código. Recusar daqui evita gravar
			   lixo na coluna que o job vai ler de 15 em 15 minutos para
			   sempre. */
			http.Error(w, "código do hub inválido", http.StatusUnprocessableEntity)
			return "", false
		}
		// Sem código nenhum: a decisão 6 permite (campanha antiga, ou criada
		// por carga). Quem exige o campo é a tela, na criação.
		return "", true
	}
	if !h.Hub.Configured() {
		// Sem hub não há com quem conferir, e a normalização local já entrega o
		// que a §10 exige na coluna.
		return canonico, true
	}

	/* ⚠️ Deadline PRÓPRIO, e curto. O `hub.New` fixa `http.Client{Timeout: 10s}`
	   e esses 10s não podem ser mexidos — é o mesmo cliente do `Exchange`, que
	   roda dentro de um login. Aqui quem espera é uma pessoa no fim de um wizard
	   inteiro preenchido, e um hub que aceita a conexão e não responde gastava
	   os 10s para devolver o MESMO 201 que devolveria em 1s: medido em
	   2026-09-21. O deadline cai no `default:` abaixo e o comportamento fica
	   idêntico, só que em 2 segundos. */
	ctx, cancel := context.WithTimeout(r.Context(), prazoDeConferencia)
	defer cancel()

	doHub, err := h.Hub.ConferirCodigo(ctx, canonico)
	var he *hub.Error
	switch {
	case err == nil:
		if !clienteDoHubConfere(doHub.Cliente.IDNaPlataforma, clientID) {
			http.Error(w, fmt.Sprintf(
				"esse código é da campanha %q, de outro cliente (%s)",
				doHub.Nome, doHub.Cliente.Nome), http.StatusUnprocessableEntity)
			return "", false
		}
	case errors.As(err, &he) && he.Status == http.StatusNotFound:
		http.Error(w, "código do hub não encontrado", http.StatusUnprocessableEntity)
		return "", false
	default:
		// Não deu para conferir. Loga e segue — ver a decisão 2 acima.
		if h.Log != nil {
			h.Log.Warn("nao deu para conferir o codigo do hub",
				zap.String("hub_code", canonico), zap.Error(err))
		}
	}
	return canonico, true
}

/*
clienteDoHubConfere diz se a ponte que o hub devolveu aponta para ESTE cliente.

⚠️ Compara UUID com UUID, e não string com string. Deste lado o valor é sempre
`uuid.String()` — minúsculo, canônico. No hub, `Client.emonitorClientId` é
`String` com `trim` e mais nada, digitado no formulário de `/admin/clientes`. Um
UUID colado em MAIÚSCULAS (que é exatamente como o Compass o mostra) fazia TODA
campanha daquele cliente levar 422 dizendo "é de outro cliente (Y)" — onde Y é o
nome do PRÓPRIO cliente. Falha fechada, com a mensagem mais confusa possível.

⚠️ E "não é UUID" é ponte AUSENTE, não divergência — o mesmo tratamento que a
§6.1 da spec dá ao cliente do hub sem `emonitorClientId`. Lixo naquele campo não
é afirmação de que a campanha é de outro cliente; é afirmação de que ninguém
ligou os dois cadastros direito. Recusar aqui barraria o cliente inteiro por um
erro de digitação do outro lado.
*/
func clienteDoHubConfere(idNaPlataforma string, local uuid.UUID) bool {
	doHub, err := uuid.Parse(strings.TrimSpace(idNaPlataforma))
	if err != nil {
		return true
	}
	return doHub == local
}

func (h *CampaignsHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}
	out, err := h.Repo.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", 404)
		} else {
			http.Error(w, "internal error", 500)
		}
		return
	}
	// Viewer scope: hide cross-client campaigns as 404 (anti-oracle).
	if !auth.ScopeAllows(r.Context(), out.ClientID) {
		http.Error(w, "not found", 404)
		return
	}
	writeJSON(w, 200, out)
}

// maxFinancialsIDs caps ?ids= so a caller can't turn the recorte into a
// full scan by pasting thousands of uuids. 200 = o teto de page_size de
// /campaigns, então nenhuma tela legítima esbarra nele.
const maxFinancialsIDs = 200

// Financials returns the per-campaign aggregate of investimento + bonificação +
// total inserções, usado pelo badge de CPM em /campaigns. Calculado em uma query
// só (CTE) pra evitar N+1 chamadas no frontend. O CPM é derivado no frontend a
// partir de DUAS parcelas: (total_invested + total_bonus_value) ÷ total_audience
// × 1000 — ver o comentário de catalog.CampaignFinancials pro porquê.
//
// ?ids=<uuid>,<uuid>,… recorta o agregado às campanhas pedidas — a PÁGINA
// atual da listagem, tipicamente 12. É o filtro que faz a rota ser barata
// (ver FinancialsByCampaign: só ele atravessa a view daily_play_summary).
// Sem o parâmetro o comportamento é o antigo: todas as campanhas do escopo.
//
// Viewer scope: filtra pela carteira de clientes do JWT para evitar vazamento
// cross-client. O ?ids= é INTERSEÇÃO com a carteira, nunca um bypass — pedir
// o id de uma campanha de outro cliente devolve zero linhas.
func (h *CampaignsHandler) Financials(w http.ResponseWriter, r *http.Request) {
	scope := auth.ClientScopesFromContext(r.Context())

	var campaignIDs []uuid.UUID
	if raw := r.URL.Query().Get("ids"); raw != "" {
		parts := strings.Split(raw, ",")
		if len(parts) > maxFinancialsIDs {
			http.Error(w, "too many ids", http.StatusBadRequest)
			return
		}
		// Slice não-nil mesmo se todos os pedaços forem vazios: "?ids=" com
		// lixo é "nenhuma campanha", não "todas".
		campaignIDs = make([]uuid.UUID, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			id, err := uuid.Parse(p)
			if err != nil {
				http.Error(w, "invalid ids", http.StatusBadRequest)
				return
			}
			campaignIDs = append(campaignIDs, id)
		}
	}

	out, err := h.Repo.FinancialsByCampaign(r.Context(), scope, campaignIDs, todaySaoPaulo())
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// Update edits the basic data trio (name, start_date, end_date) of an existing
// campaign — what the wizard's Step 1 surfaces in edit mode. client_id stays
// locked because Step 3 is hydrated against the client's material library.
//
// Returns:
//   - 200 + updated campaign on success.
//   - 400 on invalid JSON, missing required fields, or end_date <= start_date.
//   - 404 if the id does not exist.
func (h *CampaignsHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}
	var in struct {
		Name      string    `json:"name"`
		StartDate time.Time `json:"start_date"`
		EndDate   time.Time `json:"end_date"`
		/* ⚠️ PONTEIRO, e o ponteiro é o desenho: `nil` = o corpo não falou do
		   código (não mexe), `""` = mandou vazio (apaga). Sem ele, todo PUT do
		   wizard que editasse só a data apagaria o código — e apagar o código
		   congela a coleta da proposta no hub (§6.5). Um campo ausente e um
		   campo vazio querem dizer coisas opostas aqui. */
		HubCode *string `json:"hub_code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "invalid request", 400)
		return
	}
	if strings.TrimSpace(in.Name) == "" {
		http.Error(w, "name is required", 400)
		return
	}
	if in.StartDate.IsZero() || in.EndDate.IsZero() {
		http.Error(w, "start_date and end_date are required", 400)
		return
	}
	if in.EndDate.Before(in.StartDate) {
		http.Error(w, "end_date must be on or after start_date", 400)
		return
	}

	/* ⚠️ A conferência vem ANTES de qualquer escrita, e é só por isso que este
	   `Get` existe.

	   Conferindo depois do `UpdateBasic` — que é como o plano escreveu esta task
	   —, um PUT que muda o nome E traz um código de outro cliente responderia
	   422 com o nome JÁ GRAVADO. Resposta de erro com escrita parcial é defeito
	   que só aparece meses depois, quando alguém repara que o nome mudou numa
	   edição que "deu erro". */
	var canonico string
	var codigoMudou bool
	if in.HubCode != nil {
		atual, err := h.Repo.Get(r.Context(), id)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				http.Error(w, "not found", 404)
			} else {
				http.Error(w, "internal error", 500)
			}
			return
		}
		var ok bool
		canonico, ok = h.conferirCodigoOuRecusar(w, r, atual.ClientID, *in.HubCode)
		if !ok {
			return
		}
		codigoMudou = deref(atual.HubCode) != canonico
	}

	out, err := h.Repo.UpdateBasic(r.Context(), id, catalog.UpdateBasicInput{
		Name:      strings.TrimSpace(in.Name),
		StartDate: in.StartDate,
		EndDate:   in.EndDate,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", 404)
		} else {
			http.Error(w, "internal error", 500)
		}
		return
	}

	/* ⚠️ Só emite quando o código MUDOU. O nome da campanha daqui vira o nome da
	   proposta no hub e é atualizado na varredura de métricas — emitir a cada
	   renomeação seria uma chamada de rede por tecla salva, sem nada de novo do
	   outro lado.

	   E apagar TEM de emitir: é o evento sem código que faz o hub pôr
	   `coletaAtiva = false` na proposta (§6.5). Sem ele, a proposta seguiria
	   coletando para sempre, amarrada a uma campanha que não aponta mais para
	   ela. */
	if codigoMudou {
		// O `AtualizarHubCode` já zera o `hub_notified_at` sozinho, e tem teste
		// para isso — não refaça essa parte aqui.
		if err := h.Repo.AtualizarHubCode(r.Context(), id, canonico); err != nil {
			http.Error(w, "internal error", 500)
			return
		}
		out.HubCode = ptrOuNil(canonico)
		out.HubNotifiedAt = nil
		avisarHubDaCampanha(h.Hub, h.Repo, out)
	}
	writeJSON(w, 200, out)
}

// ptrOuNil devolve nil para string vazia — é o par do `NULLIF` que o
// `AtualizarHubCode` usa, para a resposta do handler dizer o mesmo que a coluna
// passou a guardar.
func ptrOuNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// UpdateFixedCPM seta (ou limpa, quando fixed_cpm = null) o CPM fixo da
// campanha. Usado pelo Step 6 do wizard (pricing): quando preenchido,
// sobrescreve o CPM derivado nas telas /campaigns, /insights e dashboard.
// Body: { "fixed_cpm": <number> | null }.
func (h *CampaignsHandler) UpdateFixedCPM(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}
	var in struct {
		FixedCPM *float64 `json:"fixed_cpm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "invalid request", 400)
		return
	}
	if in.FixedCPM != nil && *in.FixedCPM < 0 {
		http.Error(w, "fixed_cpm must be >= 0", 400)
		return
	}
	out, err := h.Repo.UpdateFixedCPM(r.Context(), id, in.FixedCPM)
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

func (h *CampaignsHandler) Start(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}
	if h.Supervisor == nil {
		http.Error(w, "supervisor not configured", 503)
		return
	}
	if err := h.Supervisor.Start(id); err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	w.WriteHeader(204)
}

func (h *CampaignsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}
	// Guard (audit C1): recusa (409) ANTES de pausar workers se apagar esta
	// campanha destruiria projeções fan-out de OUTRAS campanhas via CASCADE.
	// Checar antes do Pause evita deixar uma campanha viva pausada num delete
	// que vai falhar.
	if n, cerr := h.Repo.CountForeignProjections(r.Context(), id); cerr == nil && n > 0 {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":               "campaign_has_foreign_projections",
			"foreign_projections": n,
		})
		return
	}
	if h.Supervisor != nil {
		if perr := h.Supervisor.Pause(id); perr != nil && h.Log != nil {
			h.Log.Error("campaigns.Delete: supervisor pause failed",
				zap.String("campaign_id", id.String()),
				zap.Error(perr),
			)
		}
	}
	if err := h.Repo.Delete(r.Context(), id); err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			http.Error(w, "not found", 404)
		case errors.Is(err, catalog.ErrCampaignHasForeignProjections):
			// Defense-in-depth: o pre-check acima normalmente já pegou isto.
			writeJSON(w, http.StatusConflict, map[string]any{
				"error": "campaign_has_foreign_projections",
			})
		default:
			http.Error(w, "internal error", 500)
		}
		return
	}
	w.WriteHeader(204)
}

// Cancel transitions a campaign from programada/ativa to cancelada (§18.2.1).
// Returns:
//   - 204 on success.
//   - 404 when the id does not exist.
//   - 409 when the campaign is already in a terminal state (concluida/cancelada).
//
// Workers, if any, are stopped after the DB transition succeeds.
func (h *CampaignsHandler) Cancel(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}
	changed, prev, err := h.Repo.CancelCampaign(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", 404)
			return
		}
		http.Error(w, "internal error", 500)
		return
	}
	if !changed {
		writeJSON(w, 409, map[string]any{
			"error":          "campaign already in terminal state",
			"current_status": prev,
		})
		return
	}
	// Stop workers for stations that no longer have any active campaign.
	// CancelCampaign already flipped the status, so we go straight to the
	// worker-stop path without an extra DB round-trip via Pause().
	if h.Supervisor != nil {
		h.Supervisor.StopWorkersForCampaign(id)
	}
	w.WriteHeader(204)
}

// Pause is deprecated as of 2026-05-07 (§18.2.1). The 'paused' state was
// collapsed into 'cancelada' under the new lifecycle model, and silently
// aliasing /pause → cancel was a contract break. This handler now returns
// HTTP 410 Gone so callers fail loud and migrate to POST /campaigns/{id}/cancel.
//
// Deprecated: use Cancel via POST /campaigns/{id}/cancel.
func (h *CampaignsHandler) Pause(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusGone, map[string]any{
		"error":   "endpoint deprecated",
		"message": "use POST /campaigns/{id}/cancel instead",
		"since":   "2026-05-07",
	})
}

// UpdateStations replaces the target_stations list for a campaign.
//
// When a supervisor is wired, the DB write and the per-station worker
// reconciliation happen inside Supervisor.UpdateStations as a single path —
// the campaign never transitions through 'cancelada' (which is what the
// previous Pause+Start dance did). When no supervisor is wired (test mode),
// we fall back to a plain DB update.
func (h *CampaignsHandler) UpdateStations(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}
	var in struct {
		TargetStations []uuid.UUID `json:"target_stations"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "invalid request", 400)
		return
	}
	if in.TargetStations == nil {
		in.TargetStations = []uuid.UUID{}
	}

	if h.Supervisor != nil {
		if err := h.Supervisor.UpdateStations(id, in.TargetStations); err != nil {
			if h.Log != nil {
				h.Log.Error("campaigns.UpdateStations: supervisor failed",
					zap.String("campaign_id", id.String()),
					zap.Error(err),
				)
			}
			if strings.Contains(err.Error(), "campaign not found") {
				http.Error(w, "not found", 404)
				return
			}
			http.Error(w, "internal error", 500)
			return
		}
		w.WriteHeader(204)
		return
	}

	// No supervisor (test setup) — plain DB update.
	if _, err := h.Repo.Get(r.Context(), id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", 404)
		} else {
			http.Error(w, "internal error", 500)
		}
		return
	}
	if err := h.Repo.UpdateTargetStations(r.Context(), id, in.TargetStations); err != nil {
		http.Error(w, "internal error", 500)
		return
	}

	w.WriteHeader(204)
}

// avisarHubDaCampanha manda `campanha.upsert` FORA do caminho da resposta.
//
// # Por que goroutine, e por que context.Background
//
// A criação da campanha NÃO PODE depender do hub estar de pé. E o `r.Context()`
// morre quando a resposta é escrita: usá-lo cancelaria a emissão no exato
// instante em que ela começa — defeito intermitente e silencioso, a pior
// combinação possível.
//
// # O preço desta escolha, escrito para quem for mexer
//
// Evento perdido é campanha que nunca chega ao hub, e não há nada em tela
// nenhuma dizendo que falta alguma. Quem conserta isso é o retrato periódico
// do §5 do desenho, que não está construído. Até lá, isto é atraso invisível,
// não erro visível.
func avisarHubDaCampanha(h *hub.Client, repo *catalog.Campaigns, c *catalog.Campaign) {
	if h == nil || !h.Configured() || c == nil {
		return
	}
	// Os campos são copiados AQUI, síncrono, de propósito: a goroutine não
	// pode ler `c` depois que o handler seguiu adiante e talvez o alterou.
	dados := hub.CampanhaUpsert{
		IDNaPlataforma:        c.ID.String(),
		IDClienteNaPlataforma: c.ClientID.String(),
		/* ⚠️ SEM esta linha, nada da série funciona. O hub casa a campanha pelo
		   código; um `campanha.upsert` sem ele é ignorado com
		   `platform.campanha.ignorada` motivo `sem-codigo` (§6.1) — sem erro,
		   sem retentativa, e sem nada em tela nenhuma denunciando. A campanha
		   nasceria certa aqui e nunca chegaria lá.

		   Vazio é significativo e não é omissão: é o que o hub lê como "o
		   código foi apagado", e faz ele congelar a coleta da proposta em vez
		   de ignorar o evento (§6.5). */
		HubCode: deref(c.HubCode),
	}
	id := c.ID
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := h.Emitir(ctx, "campanha.upsert", dados); err != nil {
			// Log e mais nada: não há a quem devolver o erro, e repetir aqui
			// seria inventar uma fila sem durabilidade.
			//
			// `zap.L()` e não `log.Printf`, pelo mesmo motivo que o
			// `recat_failures.go` — o outro fire-and-forget deste pacote — já
			// documenta: os handlers não carregam logger próprio, e o log
			// estruturado é o que o resto da casa consulta. Só o
			// `campaign_id` e o erro; o payload NÃO vai para o log.
			zap.L().Error("hub: campanha.upsert falhou",
				zap.String("campaign_id", id.String()), zap.Error(err))
			return
		}
		/* A confirmação é o que TIRA a campanha da fila do job de reemissão.
		   Sem ela, uma campanha entregue com sucesso continuaria sendo
		   reenviada de 15 em 15 minutos para sempre.

		   ⚠️ Só depois do sucesso. Marcar um envio que falhou transformaria
		   "tentou e não deu" em "já avisou", e a campanha sairia da fila sem
		   nunca ter chegado — fila que esquece é pior que fila que repete.

		   ⚠️ E com o `ctx` desta goroutine, que nasce de `context.Background()`:
		   o `r.Context()` do handler já morreu quando a resposta foi escrita. */
		if repo != nil {
			if err := repo.MarcarHubNotificada(ctx, id); err != nil {
				zap.L().Warn("hub: entregou a campanha mas nao marcou hub_notified_at",
					zap.String("campaign_id", id.String()), zap.Error(err))
			}
		}
	}()
}
