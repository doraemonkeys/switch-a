package server

import (
	"net/http"

	"github.com/doraemonkeys/switch-a/internal/proxy"
)

// proxyRouteBoundary preserves proxy wire paths before ServeMux canonicalizes
// repeated slashes or literal dot segments. Canonical requests still flow
// through ServeMux so its registered method and fallback behavior remain the
// public routing contract for every other endpoint.
func proxyRouteBoundary(proxyHandler, mux http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !hasNonCanonicalPathSegments(r.URL.EscapedPath()) {
			mux.ServeHTTP(w, r)
			return
		}
		if _, ok := proxy.ResolveRequestURL(r.Method, r.URL); !ok {
			mux.ServeHTTP(w, r)
			return
		}
		proxyHandler.ServeHTTP(w, r)
	})
}
