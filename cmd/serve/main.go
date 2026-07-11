package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"strings"

	"github.com/joho/godotenv"
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

	err := godotenv.Load()
	if err != nil {
		log.Fatal("Error loading .env file")
	}
	api := core.NewApi()
	defer api.Cleanup()

	err = http.ListenAndServe(":3000", api.Router())
	if err != nil {
		log.Fatal(err)
	}
}
