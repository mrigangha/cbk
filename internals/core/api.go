package core

import (
	"database/sql"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/mrigangha/cbk/internals/tools"
	_ "modernc.org/sqlite"
)

type Api struct {
	db          *sql.DB
	router      *chi.Mux
	toolHandler *tools.ToolHandler
}

func NewApi() *Api {
	api := Api{}
	api.toolHandler = tools.NewToolHandler()
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
	// Middleware for logging and recovery.
	// NOTE: no global Timeout — agent runs and SSE streams are long-lived;
	// the client aborting the request is the cancellation signal.
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
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
		r.Post("/logout", api.Logout)
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

	r.With(AuthMiddleware).Post("/agent/chat", api.RunAgentChat)
	// Streaming variant: server-sent events with live progress.
	r.With(AuthMiddleware).Post("/agent/chat/stream", api.RunAgentChatStream)
	// Deprecated alias kept for older clients.
	r.With(AuthMiddleware).Post("/test", api.RunAgentChat)

	r.Group(func(r chi.Router) {
		r.Use(AuthMiddleware)

		r.Get("/agent/sessions", api.ListAgentSessions)
		r.Post("/agent/sessions", api.CreateAgentSession)
		r.Get("/agent/sessions/{sessionID}/messages", api.GetAgentSessionMessages)
		r.Delete("/agent/sessions/{sessionID}", api.DeleteAgentSession)
	})

	r.Route("/goals", func(r chi.Router) {
		r.Use(AuthMiddleware)

		r.Post("/", api.CreateGoal)
		r.Get("/", api.ListGoals)
		r.Get("/{goalID}", api.GetGoal)
		r.Patch("/{goalID}", api.UpdateGoal)
		r.Delete("/{goalID}", api.DeleteGoal)
		r.Post("/{goalID}/pause", api.PauseGoal)
		r.Post("/{goalID}/activate", api.ActivateGoal)
		r.Post("/{goalID}/complete", api.CompleteGoal)
	})

	r.Route("/analytics", func(r chi.Router) {
		r.Use(AuthMiddleware)

		r.Get("/overview", api.AnalyticsOverview)
		r.Get("/campaigns", api.AnalyticsCampaigns)
		r.Get("/adsets", api.AnalyticsAdSets)
		r.Get("/ads", api.AnalyticsAds)
		r.Get("/trends", api.AnalyticsTrends)
		r.Get("/anomalies", api.AnalyticsAnomalies)
		r.Get("/compare", api.AnalyticsCompare)
		r.Get("/recommendations", api.AnalyticsRecommendations)
	})

	// Optimization proposals: generate → review → approve/reject.
	// Execution against the Meta API is intentionally NOT wired yet;
	// approved actions wait for the executor milestone.
	r.Route("/optimization", func(r chi.Router) {
		r.Use(AuthMiddleware)

		r.Post("/generate", api.GenerateOptimizationActions)
		r.Get("/actions", api.ListOptimizationActions)
		r.Post("/actions/{actionID}/approve", api.ApproveOptimizationAction)
		r.Post("/actions/{actionID}/reject", api.RejectOptimizationAction)
		r.Post("/actions/{actionID}/execute", api.ExecuteOptimizationAction)
	})
	r.Post("/auth/meta/callback", api.MetaCallback)
	r.Get("/auth/meta/credential", api.GetMetaCredential)
	r.With(AuthMiddleware).Delete("/auth/meta/accounts", api.DeleteConnectedMetaAccounts)
	r.With(AuthMiddleware).Get("/meta/rate-limit/status", api.MetaRateLimitStatus)
	r.With(AuthMiddleware).Get("/meta/campaigns", api.GetCampaigns)
	r.With(AuthMiddleware).Get("/meta/campaign/details", api.GetCampaignDetails)
	r.With(AuthMiddleware).Get("/meta/campaign/insights", api.GetCampaignInsights)
	r.With(AuthMiddleware).Get("/meta/adsets", api.GetAdSets)
	r.With(AuthMiddleware).Get("/meta/ads", api.GetAds)

	r.Group(func(r chi.Router) {
		r.Use(AuthMiddleware)

		r.Post("/providers", api.CreateProvider)
		r.Get("/providers", api.ListProviders)
		r.Delete("/providers/{providerID}", api.DeleteProvider)
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
