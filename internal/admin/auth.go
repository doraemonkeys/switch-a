// Package admin provides the management API handlers and middleware.
package admin

import (
	"net/http"
	"strings"
)

// AuthMiddleware provides authentication for admin API routes.
type AuthMiddleware struct {
	token string
}

// NewAuthMiddleware creates a new auth middleware with the given admin token.
func NewAuthMiddleware(token string) *AuthMiddleware {
	return &AuthMiddleware{token: token}
}

// Wrap returns a new handler that checks for valid authentication.
// The token must be provided as "Authorization: Bearer <token>".
func (m *AuthMiddleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			writeError(w, http.StatusUnauthorized, ErrCodeUnauthorized, "Missing Authorization header")
			return
		}

		// Extract Bearer token
		const prefix = "Bearer "
		if !strings.HasPrefix(authHeader, prefix) {
			writeError(w, http.StatusUnauthorized, ErrCodeUnauthorized, "Invalid Authorization header format")
			return
		}

		token := strings.TrimPrefix(authHeader, prefix)
		if token != m.token {
			writeError(w, http.StatusUnauthorized, ErrCodeUnauthorized, "Invalid token")
			return
		}

		next.ServeHTTP(w, r)
	})
}

// WrapFunc wraps an http.HandlerFunc with authentication.
func (m *AuthMiddleware) WrapFunc(next http.HandlerFunc) http.Handler {
	return m.Wrap(next)
}
