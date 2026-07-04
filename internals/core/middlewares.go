package core

import (
	"context"
	"net/http"
	"strings"
)

type contextKey string

const UserContextKey contextKey = "user"

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

		claims, err := DecodeJWT(token)
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
