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
)

// Deps groups all handlers and middleware required by the API router.
//
// External (`/v1`) routes are protected by API key (§13.1).
// Internal (`/v1/internal`) routes require a JWT with role admin/operator,
// except for /health and /auth/login which remain public.
//
// StreamHealth (master) and APIKeys/Auth (fase2) coexist in this router.
type Deps struct {
	Stations             *handlers.StationsHandler
	Clients              *handlers.ClientsHandler
	Campaigns            *handlers.CampaignsHandler
	Commercials          *handlers.CommercialsHandler
	Detections           *handlers.DetectionsHandler
	Health               *handlers.HealthHandler
	StreamHealth         *handlers.StreamHealthHandler
	Auth                 *handlers.AuthHandler
	APIKey               *auth.APIKeyMiddleware
	APIKeys              *handlers.APIKeysHandler
	Admin                *handlers.AdminHandler
	Webhooks             *handlers.WebhooksHandler
	MaterialTypes        *handlers.MaterialTypesHandler
	Materials            *handlers.MaterialsHandler
	CampaignMaterials    *handlers.CampaignMaterialsHandler
	DistributionRules    *handlers.DistributionRulesHandler
	DistributionOverrides *handlers.DistributionOverridesHandler
	Pricing              *handlers.PricingHandler
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

		// Protected: all other internal routes require a valid JWT
		// with role "admin" or "operator".
		r.Group(func(r chi.Router) {
			r.Use(auth.RequireJWT)
			r.Use(auth.RequireRole("admin", "operator"))

			r.Route("/stations", func(r chi.Router) {
				r.Get("/", d.Stations.List)
				r.Post("/", d.Stations.Create)
				r.Get("/{id}", d.Stations.Get)
				r.Put("/{id}", d.Stations.Update)
				r.Get("/{id}/threshold", d.Stations.GetThreshold)
			})
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
			r.Route("/campaigns", func(r chi.Router) {
				r.Get("/", d.Campaigns.List)
				r.Post("/", d.Campaigns.Create)
				// Literal antes do param pra chi resolver corretamente:
				// /campaigns/financials → Financials, /campaigns/{id} → Get.
				r.Get("/financials", d.Campaigns.Financials)
				r.Get("/{id}", d.Campaigns.Get)
				// Edita o trio básico (name, start_date, end_date) — usado
				// pelo Step 1 do wizard em modo edit. client_id continua
				// imutável.
				r.Put("/{id}", d.Campaigns.Update)
				// Lifecycle (§18.2.1): /cancel is the only manual
				// transition. Until tenancy is wired (follow-ups F-XX),
				// cancellation requires admin so operators can't terminate
				// arbitrary campaigns. /start and /pause already act on
				// global supervisor state and are also admin-gated.
				r.Group(func(r chi.Router) {
					r.Use(auth.RequireRole("admin"))
					r.Post("/{id}/cancel", d.Campaigns.Cancel)
					r.Put("/{id}/start", d.Campaigns.Start)
					r.Put("/{id}/pause", d.Campaigns.Pause)
				})
				r.Put("/{id}/stations", d.Campaigns.UpdateStations)
				r.Delete("/{id}", d.Campaigns.Delete)
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
			r.Route("/material-types", func(r chi.Router) {
				r.Get("/", d.MaterialTypes.List)
				r.Post("/", d.MaterialTypes.Create)
				r.Put("/{id}", d.MaterialTypes.Update)
				r.Delete("/{id}", d.MaterialTypes.Delete)
			})

			// Materials — per-client library.
			r.Get("/clients/{clientID}/materials", d.Materials.ListByClient)
			r.Route("/materials", func(r chi.Router) {
				r.Post("/", d.Materials.Upload)
				r.Get("/{id}", d.Materials.Get)
				r.Patch("/{id}/type", d.Materials.UpdateType)
				r.Delete("/{id}", d.Materials.Delete)
			})

			// Campaign ↔ Materials link.
			r.Route("/campaigns/{campaignID}/materials", func(r chi.Router) {
				r.Post("/", d.CampaignMaterials.Link)
				r.Get("/", d.CampaignMaterials.ListByCampaign)
				r.Put("/{materialID}/stations", d.CampaignMaterials.UpdateStations)
				r.Delete("/{materialID}", d.CampaignMaterials.Unlink)
			})

			// Distribution rules.
			r.Route("/campaigns/{campaignID}/distribution-rules", func(r chi.Router) {
				r.Get("/", d.DistributionRules.ListByCampaign)
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
			if d.Pricing != nil {
				r.Route("/campaigns/{campaignID}/pricing", func(r chi.Router) {
					r.Get("/", d.Pricing.ListByCampaign)
					r.Put("/{stationID}", d.Pricing.Upsert)
					r.Delete("/{stationID}", d.Pricing.Delete)
				})
			}

			// Daily summary — feeds the /detections UI.
			r.Get("/campaigns/{campaignID}/daily-summary", d.Detections.DailySummary)

			r.Route("/detections", func(r chi.Router) {
				r.Get("/", d.Detections.List)
				// Static prefixes BEFORE /{id} so chi doesn't try to parse
				// "aggregate-by-material" as a UUID. Same reason for /export
				// inside the admin group below.
				r.Get("/aggregate-by-material", d.Detections.AggregateByMaterial)
				r.Get("/{id}", d.Detections.Get)
				r.Get("/{id}/evidence", d.Detections.Evidence)
				r.Get("/{id}/evidence/url", d.Detections.EvidenceURL)
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
					r.Post("/manual", d.Detections.CreateManual)
					r.Post("/{id}/ignore", d.Detections.Ignore)
					r.Post("/{id}/restore", d.Detections.Restore)
				})
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
		})
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
