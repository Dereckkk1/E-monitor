package api

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/trace"
	"radiocheck/internal/api/handlers"
	"radiocheck/internal/auth"
	"radiocheck/internal/reqmetrics"
)

// Deps groups all handlers and middleware required by the API router.
//
// External (`/v1`) routes are protected by API key (§13.1).
// Internal (`/v1/internal`) routes require a JWT. Role requirements vary:
//   - viewer: read-only endpoints (campaigns, detections, materials list)
//   - operator: writes and admin reads
//   - admin: mutations, lifecycle ops, user management
//
// /health and /auth/login remain public (no JWT required).
//
// StreamHealth (master) and APIKeys/Auth (fase2) coexist in this router.
type Deps struct {
	Stations              *handlers.StationsHandler
	Clients               *handlers.ClientsHandler
	Campaigns             *handlers.CampaignsHandler
	Commercials           *handlers.CommercialsHandler
	Detections            *handlers.DetectionsHandler
	Health                *handlers.HealthHandler
	StreamHealth          *handlers.StreamHealthHandler
	Auth                  *handlers.AuthHandler
	APIKey                *auth.APIKeyMiddleware
	APIKeys               *handlers.APIKeysHandler
	Admin                 *handlers.AdminHandler
	SystemHealth          *handlers.SystemHealthHandler
	AdminMonitoring       *handlers.AdminMonitoringHandler
	StationFailures       *handlers.StationFailuresHandler
	CampaignFailures      *handlers.CampaignFailuresHandler
	Webhooks              *handlers.WebhooksHandler
	MaterialTypes         *handlers.MaterialTypesHandler
	Materials             *handlers.MaterialsHandler
	CampaignMaterials     *handlers.CampaignMaterialsHandler
	DistributionRules     *handlers.DistributionRulesHandler
	DistributionOverrides *handlers.DistributionOverridesHandler
	Pricing               *handlers.PricingHandler
	Users                 *handlers.UsersHandler
	Me                    *handlers.MeHandler
	Reports               *handlers.ReportsHandler
	Notifications         *handlers.NotificationsHandler
	Insights              *handlers.InsightsHandler

	// Reqmetrics writer and block-list. Quando ambos são nil, o router não
	// instala telemetria nem enforcement — útil em testes que não querem
	// inicializar o pgxpool.
	Metrics   *reqmetrics.Writer
	BlockList *reqmetrics.BlockList
}

func NewRouter(d Deps) http.Handler {
	r := chi.NewRouter()
	// We deliberately wrap the *router* (not the per-route handlers) with
	// otelhttp at the bottom of this function so chi.RouteContext is populated
	// by the time the span name formatter runs.
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))
	r.Use(corsMiddleware)
	r.Use(otelRoutePatternMiddleware)

	// IP block enforcement — roda cedo no pipeline para cortar requests
	// banidos antes de qualquer handler/SQL. Exempta rotas de admin para o
	// operador conseguir se desbloquear (ver reqmetrics/blocked.go).
	if d.BlockList != nil {
		r.Use(reqmetrics.BlockMiddleware(d.BlockList))
	}

	// Request telemetry — captura status/duration/route/IP/user e submete
	// async ao writer. Roda como último middleware antes dos handlers para
	// ler o pattern resolvido pelo chi.
	if d.Metrics != nil {
		r.Use(reqmetrics.Middleware(d.Metrics, 2000))
	}

	// External client API — protected by API key (§13.1).
	if d.APIKey != nil {
		r.Route("/v1", func(r chi.Router) {
			r.Group(func(r chi.Router) {
				r.Use(d.APIKey.Middleware)
				r.Route("/detections", func(r chi.Router) {
					r.Get("/", d.Detections.List)
					r.Get("/{id}", d.Detections.Get)
					r.Get("/{id}/evidence", d.Detections.Evidence)
				})
			})
		})
	}

	// Prometheus metrics — public, no auth required (§15.1).
	r.Handle("/metrics", promhttp.Handler())

	r.Route("/v1/internal", func(r chi.Router) {
		// Public: health check and login do not require JWT.
		r.Get("/health", d.Health.Check)
		if d.Auth != nil {
			r.Post("/auth/login", d.Auth.Login)
		}

		// Protected: all other internal routes require a valid JWT.
		r.Group(func(r chi.Router) {
			r.Use(auth.RequireJWT)

			// ── Subgrupo A — viewer-friendly reads ────────────────────────────
			// Chi accumulates middlewares — inner groups do NOT override an outer
			// RequireRole gate. To allow viewer access, these reads live in their
			// own top-level group with RequireRole(admin/operator/viewer), NOT
			// nested inside the admin/operator group below.
			r.Group(func(r chi.Router) {
				r.Use(auth.RequireRole("admin", "operator", "viewer"))

				// Self-service /auth/me* — any authenticated user can manage their
				// own profile. Role, client_id, is_active are NOT mutable via /me.
				if d.Me != nil {
					r.Get("/auth/me", d.Me.Get)
					r.Patch("/auth/me", d.Me.Patch)
					r.Post("/auth/me/password", d.Me.ChangePassword)
				}

				// Campaign reads — literal prefixes BEFORE /{id} so chi resolves
				// /campaigns/financials correctly and doesn't try to parse
				// "financials" as a UUID.
				r.Get("/campaigns", d.Campaigns.List)
				r.Get("/campaigns/financials", d.Campaigns.Financials)
				r.Get("/campaigns/{id}", d.Campaigns.Get)

				// Daily summary — feeds the /detections UI.
				r.Get("/campaigns/{campaignID}/daily-summary", d.Detections.DailySummary)

				// Detection reads — static prefixes BEFORE /{id} so chi doesn't
				// try to parse "aggregate-by-material" as a UUID.
				r.Get("/detections", d.Detections.List)
				r.Get("/detections/aggregate-by-material", d.Detections.AggregateByMaterial)
				r.Get("/detections/{id}", d.Detections.Get)
				r.Get("/detections/{id}/evidence", d.Detections.Evidence)
				r.Get("/detections/{id}/evidence/url", d.Detections.EvidenceURL)

				// Per-client material library list — handler enforces cross-client
				// isolation via client_id from JWT claims.
				r.Get("/clients/{clientID}/materials", d.Materials.ListByClient)

				// Relatórios consolidados de campanha — CSV resumo + JSON pra PDF.
				// Viewer scope checado dentro do handler (mesmo padrão do
				// /detections/aggregate-by-material). O CSV detalhado continua
				// em /detections/export e é admin-only.
				if d.Reports != nil {
					r.Get("/reports/campaigns/{id}/consolidated.csv", d.Reports.Consolidated)
					r.Get("/reports/campaigns/{id}/summary", d.Reports.Summary)
				}

				// Insights dashboard — admin vê tudo; cliente fica restrito ao
				// próprio scope via auth.ClientScopeFromContext (anti-oracle dentro
				// do handler+repo).
				if d.Insights != nil {
					r.Get("/insights", d.Insights.Get)
				}

				// Web Vitals telemetry — qualquer usuário autenticado posta
				// LCP/INP/CLS/FCP/TTFB do seu navegador. Painel admin agrega.
				if d.AdminMonitoring != nil {
					r.Post("/web-vitals", d.AdminMonitoring.PostVitals)
				}

				// Lookups + leituras necessárias para a página /detections do
				// cliente. Todas GET-only. Writes desses recursos continuam
				// admin/operator-only no subgrupo abaixo. Notas:
				//   - /stations e /stations/{id}: emissoras são broadcasters
				//     "públicos" (sem proprietário). Cliente precisa pra
				//     renderizar nome/cidade nas células da grade.
				//   - /material-types: registry global, sem client_id.
				//   - /campaigns/{campaignID}/{materials|distribution-rules|pricing}:
				//     fazem scope-check no handler (404 anti-oracle) via
				//     auth.ClientScopeFromContext, mesmo padrão de
				//     /campaigns/{id} e /campaigns/{campaignID}/daily-summary.
				r.Get("/stations", d.Stations.List)
				r.Get("/stations/{id}", d.Stations.Get)
				r.Get("/material-types", d.MaterialTypes.List)
				r.Get("/campaigns/{campaignID}/materials", d.CampaignMaterials.ListByCampaign)
				r.Get("/campaigns/{campaignID}/distribution-rules", d.DistributionRules.ListByCampaign)
				if d.Pricing != nil {
					r.Get("/campaigns/{campaignID}/pricing", d.Pricing.ListByCampaign)
				}
			})

			// ── Subgrupo B — admin/operator (writes + admin reads) ────────────
			r.Group(func(r chi.Router) {
				r.Use(auth.RequireRole("admin", "operator"))

				// /stations: GET (List, Get) ficam no subgrupo A (viewer-friendly)
				// porque a página /detections do cliente precisa do catálogo
				// pra renderizar nome/cidade nas células. Writes + GetThreshold
				// continuam admin/operator-only aqui.
				r.Post("/stations", d.Stations.Create)
				r.Put("/stations/{id}", d.Stations.Update)
				r.Get("/stations/{id}/threshold", d.Stations.GetThreshold)
				r.Route("/clients", func(r chi.Router) {
					r.Get("/", d.Clients.List)
					r.Post("/", d.Clients.Create)
					r.Put("/{id}", d.Clients.Update)
					r.Delete("/{id}", d.Clients.Delete)
				})
				if d.APIKeys != nil {
					r.Get("/clients/{clientID}/api-keys", d.APIKeys.List)
					r.Post("/clients/{clientID}/api-keys", d.APIKeys.Create)
					r.Delete("/clients/{clientID}/api-keys/{keyID}", d.APIKeys.Revoke)
				}
				if d.Webhooks != nil {
					// Webhook config + observability + test dispatcher (§13.1.4).
					//
					// Reads (operator+admin): exposing config and delivery
					// history is fine for any authenticated internal user.
					r.Get("/clients/{id}/webhook", d.Webhooks.GetConfig)
					r.Get("/clients/{id}/webhook-deliveries", d.Webhooks.ListDeliveries)
					// Mutations (admin-only): until tenancy is implemented
					// (see follow-ups F-XX), restrict mutations to admins so a
					// regular operator cannot rotate another client's webhook
					// secret or fire a test POST to an attacker-controlled URL.
					r.Group(func(r chi.Router) {
						r.Use(auth.RequireRole("admin"))
						r.Patch("/clients/{id}/webhook", d.Webhooks.PatchConfig)
						r.Post("/clients/{id}/webhook-test", d.Webhooks.SendTest)
					})
				}

				// Campaign writes — reads are in subgrupo A (viewer-friendly).
				// Lifecycle mutations (cancel/start/pause) are admin-only (§18.2.1):
				// until tenancy is wired (follow-ups F-XX), cancellation requires
				// admin so operators can't terminate arbitrary campaigns. /start and
				// /pause also act on global supervisor state.
				r.Post("/campaigns", d.Campaigns.Create)
				// Edita o trio básico (name, start_date, end_date) — usado
				// pelo Step 1 do wizard em modo edit. client_id continua imutável.
				r.Put("/campaigns/{id}", d.Campaigns.Update)
				r.Put("/campaigns/{id}/stations", d.Campaigns.UpdateStations)
				r.Delete("/campaigns/{id}", d.Campaigns.Delete)
				r.Group(func(r chi.Router) {
					r.Use(auth.RequireRole("admin"))
					r.Post("/campaigns/{id}/cancel", d.Campaigns.Cancel)
					r.Put("/campaigns/{id}/start", d.Campaigns.Start)
					r.Put("/campaigns/{id}/pause", d.Campaigns.Pause)
				})

				r.Route("/commercials", func(r chi.Router) {
					r.Get("/", d.Commercials.List)
					r.Post("/", d.Commercials.Upload)
					r.Get("/{id}", d.Commercials.Get)
					r.Get("/{id}/audio", d.Commercials.Audio)
					r.Put("/{id}/stations", d.Commercials.UpdateStations)
					r.Delete("/{id}", d.Commercials.Delete)
				})

				// Material types — global registry (Tasks 13-19).
				// GET (List) ficou no subgrupo A (viewer-friendly): registry
				// global sem client_id, e a página /detections do cliente
				// precisa pra renderizar cor/nome dos tipos. Writes seguem
				// admin/operator-only aqui.
				r.Post("/material-types", d.MaterialTypes.Create)
				r.Put("/material-types/{id}", d.MaterialTypes.Update)
				r.Delete("/material-types/{id}", d.MaterialTypes.Delete)

				// Materials — per-client library (writes and individual reads).
				// ListByClient (GET /clients/{clientID}/materials) lives in subgrupo A.
				r.Route("/materials", func(r chi.Router) {
					r.Post("/", d.Materials.Upload)
					r.Get("/{id}", d.Materials.Get)
					r.Get("/{id}/audio", d.Materials.Audio)
					r.Post("/{id}/similarity/acknowledge", d.Materials.Acknowledge)
					r.Patch("/{id}/type", d.Materials.UpdateType)
					r.Patch("/{id}/script", d.Materials.UpdateScript)
					r.Delete("/{id}", d.Materials.Delete)
				})

				// Campaign ↔ Materials link.
				// GET (ListByCampaign) ficou no subgrupo A com scope-check no
				// handler. Writes seguem admin/operator-only aqui.
				r.Route("/campaigns/{campaignID}/materials", func(r chi.Router) {
					r.Post("/", d.CampaignMaterials.Link)
					r.Put("/{materialID}/stations", d.CampaignMaterials.UpdateStations)
					r.Delete("/{materialID}", d.CampaignMaterials.Unlink)
				})

				// Distribution rules.
				// GET (ListByCampaign) ficou no subgrupo A com scope-check.
				// Writes seguem admin/operator-only.
				r.Route("/campaigns/{campaignID}/distribution-rules", func(r chi.Router) {
					r.Post("/", d.DistributionRules.Create)
					r.Put("/{ruleID}", d.DistributionRules.Update)
					r.Delete("/{ruleID}", d.DistributionRules.Delete)
				})

				// Distribution overrides.
				r.Route("/campaigns/{campaignID}/distribution-overrides", func(r chi.Router) {
					r.Get("/", d.DistributionOverrides.ListByDateRange)
					r.Put("/", d.DistributionOverrides.Upsert)
					r.Delete("/", d.DistributionOverrides.Delete)
				})

				// Pricing por (campanha × emissora) — alimenta o Step 5 do
				// wizard e os valores do resumo em /detections + CPM em
				// /campaigns. Mode: consolidated | per_insertion. Validação
				// forte no repo, retorna 422 em payload inválido.
				// GET (ListByCampaign) ficou no subgrupo A com scope-check.
				// Writes seguem admin/operator-only.
				if d.Pricing != nil {
					r.Route("/campaigns/{campaignID}/pricing", func(r chi.Router) {
						r.Put("/{stationID}", d.Pricing.Upsert)
						r.Delete("/{stationID}", d.Pricing.Delete)
					})
				}

				// Detection writes — reads live in subgrupo A (viewer-friendly).
				//
				// Admin-only soft-delete ("desconsiderar veiculação"). Reverter
				// é a operação simétrica via /restore. Veiculação fica zerada
				// nos agregados (daily_play_summary filtra ignored_at IS NULL)
				// mas a evidência e o registro continuam intactos.
				//
				// Admin-only manual entry ("Adicionar veiculação manualmente"):
				// veiculações retroativas. A linha entra em daily_play_summary
				// igual à automática — o categorizer roda pra decidir
				// in_slot/out_slot/out_date/orphan.
				r.Group(func(r chi.Router) {
					r.Use(auth.RequireRole("admin"))
					r.Post("/detections/manual", d.Detections.CreateManual)
					r.Post("/detections/{id}/ignore", d.Detections.Ignore)
					r.Post("/detections/{id}/restore", d.Detections.Restore)
					// CSV export do relatório data/hora — streaming. Prefixo
					// estático /export não colide com /{id} porque ambos estão
					// neste grupo e chi resolve por especificidade.
					r.Get("/detections/export", d.Detections.Export)
				})

				r.Route("/stream-health", func(r chi.Router) {
					r.Get("/", d.StreamHealth.List)
					r.Get("/{stationId}", d.StreamHealth.Detail)
				})
				r.Get("/workers", d.Health.WorkerStatus)

				// Admin-only operational endpoints (§11.4 / §14.4 / §9.4).
				if d.Admin != nil {
					r.Group(func(r chi.Router) {
						r.Use(auth.RequireRole("admin"))
						r.Post("/admin/evidence/tiering/run", d.Admin.RunTiering)
						r.Post("/admin/stations/{id}/threshold/refresh", d.Admin.RefreshThreshold)
						r.Post("/admin/calibration/run", d.Admin.RunCalibration)
					})
				}

				// Admin system-health dashboard endpoint. Single GET that pings
				// every dependency (infra + observability) in parallel and rolls
				// up an "attention" list of currently-broken things. See
				// handlers/system_health.go.
				if d.SystemHealth != nil {
					r.Group(func(r chi.Router) {
						r.Use(auth.RequireRole("admin"))
						r.Get("/admin/system-health", d.SystemHealth.Get)
					})
				}

				// /admin/monitoring — painel de telemetria HTTP, identidades,
				// vitals e bloqueio de IP. Admin-only por completo.
				// Documentado em docs/features/admin-monitoring.md.
				if d.AdminMonitoring != nil {
					r.Group(func(r chi.Router) {
						r.Use(auth.RequireRole("admin"))
						r.Get("/admin/monitoring/overview", d.AdminMonitoring.Overview)
						r.Get("/admin/monitoring/routes", d.AdminMonitoring.Routes)
						r.Get("/admin/monitoring/errors", d.AdminMonitoring.Errors)
						r.Get("/admin/monitoring/slow", d.AdminMonitoring.Slow)
						r.Get("/admin/monitoring/timeline", d.AdminMonitoring.Timeline)
						r.Get("/admin/monitoring/vitals", d.AdminMonitoring.Vitals)
						r.Get("/admin/monitoring/top-actors", d.AdminMonitoring.TopActors)
						r.Get("/admin/monitoring/actor-detail", d.AdminMonitoring.ActorDetail)
						r.Get("/admin/monitoring/blocked-ips", d.AdminMonitoring.BlockedIPs)
						r.Post("/admin/monitoring/block-ip", d.AdminMonitoring.BlockIP)
						r.Delete("/admin/monitoring/block-ip/{ip}", d.AdminMonitoring.UnblockIP)
						r.Post("/admin/monitoring/block-user/{userId}", d.AdminMonitoring.BlockUser)
					})
				}

				// /admin/station-failures — cruzamento de quedas de stream com
				// déficits de campanha por dia. Lista cada emissora que falhou
				// + as campanhas com slots perdidos. Documentado em
				// docs/features/admin-station-failures.md.
				if d.StationFailures != nil {
					r.Group(func(r chi.Router) {
						r.Use(auth.RequireRole("admin"))
						r.Get("/admin/station-failures", d.StationFailures.Get)
					})
				}

				// /admin/campaign-failures — same intent as station-failures but
				// pivoted by campaign. Two list modes (date / historical) plus a
				// drill-in by campaign id. Docs em docs/features/admin-campaign-failures.md.
				if d.CampaignFailures != nil {
					r.Group(func(r chi.Router) {
						r.Use(auth.RequireRole("admin"))
						r.Get("/admin/campaign-failures", d.CampaignFailures.GetList)
						r.Get("/admin/campaign-failures/{id}", d.CampaignFailures.GetByID)
					})
				}

				// /admin/notifications — sininho do dashboard admin.
				// Spec: docs/superpowers/specs/2026-05-25-admin-notifications-and-failure-filter-design.md
				if d.Notifications != nil {
					r.Group(func(r chi.Router) {
						r.Use(auth.RequireRole("admin"))
						r.Get("/admin/notifications", d.Notifications.List)
						r.Post("/admin/notifications/mark-read", d.Notifications.MarkRead)
						r.Post("/admin/notifications/mark-all-read", d.Notifications.MarkAllRead)
					})
				}
			}) // end admin/operator group

			// ── Subgrupo C — admin-only: user management CRUD ────────────────
			// New endpoints for managing platform users. Nil-guarded so the
			// handler can be omitted in test harnesses without panicking.
			if d.Users != nil {
				r.Group(func(r chi.Router) {
					r.Use(auth.RequireRole("admin"))
					r.Route("/admin/users", func(r chi.Router) {
						r.Get("/", d.Users.List)
						r.Post("/", d.Users.Create)
						r.Get("/{id}", d.Users.Get)
						r.Patch("/{id}", d.Users.Patch)
						r.Delete("/{id}", d.Users.Delete)
						r.Post("/{id}/password", d.Users.ResetPassword)
					})
				})
			}
		}) // end RequireJWT group
	})

	// Wrap the whole router with OpenTelemetry's HTTP instrumentation. The
	// otelhttp handler reads any inbound traceparent header, starts a server
	// span, and terminates it when the response is flushed. Outbound calls
	// made with otelhttp.NewTransport (e.g. webhook deliveries) become
	// children of the active span.
	//
	// Span names default to "<method> <url-path>". A chi middleware at the
	// top of the stack rewrites them to the matched route pattern (e.g.
	// "GET /v1/internal/clients/{clientID}/api-keys") once chi resolves it,
	// so dashboards don't blow up cardinality with raw IDs.
	return otelhttp.NewHandler(r, "radiocheck-api",
		otelhttp.WithSpanNameFormatter(func(_ string, req *http.Request) string {
			return req.Method + " " + req.URL.Path
		}),
	)
}

// otelRoutePatternMiddleware rewrites the active span name to the matched
// chi route pattern after the inner handler returns.
func otelRoutePatternMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
		if rctx := chi.RouteContext(r.Context()); rctx != nil && rctx.RoutePattern() != "" {
			trace.SpanFromContext(r.Context()).SetName(r.Method + " " + rctx.RoutePattern())
		}
	})
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == "OPTIONS" {
			w.WriteHeader(204)
			return
		}
		next.ServeHTTP(w, r)
	})
}
