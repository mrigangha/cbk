package core

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"time"

	"github.com/mrigangha/cbk/internals/ai/cloud"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

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
	r.Get("/users/{userID}", func(w http.ResponseWriter, r *http.Request) {
		userID := chi.URLParam(r, "userID")
		json.NewEncoder(w).Encode(map[string]string{"user_id": userID})
	})

	// Sub-router grouping
	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/status", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"status": "OK"}`))
		})
	})
	r.With(AuthMiddleware).Get("/me", func(w http.ResponseWriter, r *http.Request) {
		user, ok := r.Context().Value(UserContextKey).(map[string]any)
		if !ok {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		json.NewEncoder(w).Encode(user)
	})

	r.Route("/auth", func(r chi.Router) {
		r.Post("/login", func(w http.ResponseWriter, r *http.Request) {
			token := NewJWT("john@example.com")

			json.NewEncoder(w).Encode(map[string]string{
				"token": token,
				"type":  "Bearer",
			})
		})
	})
	api.db = db
	api.router = r
	return &api
}

func (a *Api) Health(w http.ResponseWriter, r *http.Request) {
	p := cloud.NewProvider(os.Getenv("GEMINI_API_KEY"), "gemini-3.5-flash", "https://generativelanguage.googleapis.com/v1beta")
	res, err := p.GenerateText("Say hello")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Write([]byte(res))
}

func (a *Api) Router() *chi.Mux {
	return a.router
}

func (a *Api) Cleanup() {
	a.db.Close()
}
