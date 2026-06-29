package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/mrigangha/cbk/internals/core"
)

type contextKey string

const UserContextKey contextKey = "user"

func GetIP(r *http.Request) string {
	// X-Forwarded-For
	xff := r.Header.Get("X-Forwarded-For")
	if xff != "" {
		ips := strings.Split(xff, ",")
		return strings.TrimSpace(ips[0])
	}

	// X-Real-IP
	xrip := r.Header.Get("X-Real-IP")
	if xrip != "" {
		return xrip
	}

	// Fallback
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}

	return ip
}

func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			http.Error(w, "Authorization header missing", http.StatusUnauthorized)
			return
		}

		// 2. Check and strip the 'Bearer ' prefix
		const prefix = "Bearer "
		if !strings.HasPrefix(authHeader, prefix) {
			http.Error(w, "Invalid token format", http.StatusUnauthorized)
			return
		}
		token := strings.TrimPrefix(authHeader, prefix)

		// 3. Process/validate your token here
		fmt.Println("Extracted Token:", token)
		claims, err := core.DecodeJWT(token)
		if err != nil {
			http.Error(w, "Invalid token format", http.StatusUnauthorized)
			return
		}

		user := map[string]any{
			"email": claims["email"],
		}

		ctx := context.WithValue(r.Context(), UserContextKey, user)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func main() {
	r := chi.NewRouter()

	// Middleware for logging, recovery, and timeouts
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))
	// Basic route
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Welcome"))
	})

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
			token := core.NewJWT("john@example.com")

			json.NewEncoder(w).Encode(map[string]string{
				"token": token,
			})
		})
	})

	http.ListenAndServe(":3000", r)
}
