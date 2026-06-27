package appsplatform

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

// ctxKey is the unexported context key type for values the platform stores.
type ctxKey int

const ctxApp ctxKey = iota

// AdminAuthFunc lets a product reuse ITS OWN session auth for the management
// API. Given the request it returns the caller's account id, whether the caller
// is an admin (can manage every app, not just their own), and ok=false to reject
// (the platform then responds 401). The platform never sees the product's
// session mechanism — only this adapter.
type AdminAuthFunc func(r *http.Request) (ownerID string, isAdmin bool, ok bool)

// TokenAuth is the runtime Bearer-token middleware. It expects an
// "Authorization: Bearer <app-token>" header, looks the app up by the token's
// sha256 HASH in the registry (constant-time; the plaintext is never stored),
// and on success stashes the app in the request context. On any miss it responds
// 401; a registry error responds 503.
func TokenAuth(reg Registry, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r)
		if token == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "app token required"})
			return
		}
		app, err := reg.GetByTokenHash(HashToken(token))
		if err != nil || app == nil {
			if err != nil && !errors.Is(err, ErrNotFound) {
				writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "app registry unavailable"})
				return
			}
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid app token"})
			return
		}
		ctx := context.WithValue(r.Context(), ctxApp, app)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// AppFromContext returns the authenticated app set by TokenAuth.
func AppFromContext(ctx context.Context) (*App, bool) {
	a, ok := ctx.Value(ctxApp).(*App)
	return a, ok
}

// bearerToken extracts a Bearer token from the Authorization header only. Unlike
// a session, the runtime API does not accept a cookie.
func bearerToken(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
	}
	return ""
}
