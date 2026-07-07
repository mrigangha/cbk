package core

import (
	"database/sql"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	_ "modernc.org/sqlite"
)

type Api struct {
	db     *sql.DB
	router *chi.Mux
}

func NewApi() *Api {
	api := Api{}
	db, err := sql.Open("sqlite", "app.db")
	if err != nil {
		panic(err)
	}

	r := chi.NewRouter()
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins: []string{
			"http://localhost:5173", // SvelteKit dev server
		},

		AllowedMethods: []string{
			"GET",
			"POST",
			"PUT",
			"DELETE",
			"OPTIONS",
		},

		AllowedHeaders: []string{
			"Accept",
			"Authorization",
			"Content-Type",
		},

		ExposedHeaders: []string{
			"Set-Cookie",
		},

		AllowCredentials: true,

		MaxAge: 300,
	}))
	// Middleware for logging, recovery, and timeouts
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))
	// Basic route
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Welcome"))
	})
	r.Get("/health", api.Health)

	// URL parameter extraction
	r.Route("/auth", func(r chi.Router) {
		r.Post("/register", api.Register)
		r.Post("/login", api.Login)
		r.Post("/refresh", api.Refresh)
		r.With(AuthMiddleware).Get("/meta/accounts", api.GetConnectedMetaAccounts)
	})

	r.Group(func(r chi.Router) {
		r.Use(AuthMiddleware)

		r.Get("/me", api.Me)
	})
	// Sub-router grouping
	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/status", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"status": "OK"}`))
		})
	})

	r.Post("/auth/meta/callback", api.MetaCallback)
	r.Get("/auth/meta/credential", api.GetMetaCredential)
	r.With(AuthMiddleware).Delete("/auth/meta/accounts", api.DeleteConnectedMetaAccounts)
	r.With(AuthMiddleware).Get("/meta/campaigns", api.GetCampaigns)
	r.With(AuthMiddleware).Get("/meta/campaign/details", api.GetCampaignDetails)

	r.Group(func(r chi.Router) {
		r.Use(AuthMiddleware)

		r.Post("/providers", api.CreateProvider)
	})
	r.Group(func(r chi.Router) {
		r.Use(AuthMiddleware)

		r.Post("/ai/generate", api.Generate)
	})

	api.db = db
	api.router = r
	return &api
}

func (a *Api) Health(w http.ResponseWriter, r *http.Request) {

	w.Write([]byte("OK"))
}

func (a *Api) Router() *chi.Mux {
	return a.router
}

func (a *Api) Cleanup() {
	a.db.Close()
}
