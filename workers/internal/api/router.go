package api

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"radiocheck/internal/api/handlers"
)

type Deps struct {
	Stations    *handlers.StationsHandler
	Clients     *handlers.ClientsHandler
	Campaigns   *handlers.CampaignsHandler
	Commercials *handlers.CommercialsHandler
	Detections  *handlers.DetectionsHandler
	Health      *handlers.HealthHandler
}

func NewRouter(d Deps) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))
	r.Use(corsMiddleware)

	r.Route("/v1/internal", func(r chi.Router) {
		r.Get("/health", d.Health.Check)

		r.Route("/stations", func(r chi.Router) {
			r.Get("/", d.Stations.List)
			r.Post("/", d.Stations.Create)
			r.Get("/{id}", d.Stations.Get)
		})
		r.Route("/clients", func(r chi.Router) {
			r.Get("/", d.Clients.List)
			r.Post("/", d.Clients.Create)
		})
		r.Route("/campaigns", func(r chi.Router) {
			r.Get("/", d.Campaigns.List)
			r.Post("/", d.Campaigns.Create)
			r.Get("/{id}", d.Campaigns.Get)
			r.Put("/{id}/start", d.Campaigns.Start)
			r.Put("/{id}/pause", d.Campaigns.Pause)
		})
		r.Route("/commercials", func(r chi.Router) {
			r.Post("/", d.Commercials.Upload)
			r.Get("/{id}", d.Commercials.Get)
		})
		r.Route("/detections", func(r chi.Router) {
			r.Get("/", d.Detections.List)
			r.Get("/{id}", d.Detections.Get)
			r.Get("/{id}/evidence", d.Detections.Evidence)
		})
	})

	return r
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
