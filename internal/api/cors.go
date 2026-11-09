package api

import (
	"net/http"
	"slices"
)

// withCORS echoes the request Origin only when it is in the allow-list and
// answers preflight requests itself.
func withCORS(allowed []string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		ok := origin != "" && slices.Contains(allowed, origin)
		if ok {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Add("Vary", "Origin")
		}
		if r.Method == http.MethodOptions {
			if ok {
				h := w.Header()
				h.Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
				h.Set("Access-Control-Allow-Headers", "Content-Type, Idempotency-Key, Authorization")
				h.Set("Access-Control-Max-Age", "600")
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
