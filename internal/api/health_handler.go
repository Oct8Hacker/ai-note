// Package api holds the gateway HTTP handlers.
package api

import (
	"encoding/json"
	"net/http"

	"ainote/internal/health"
)

// HealthHandler serves the health report: 200 if ok, 503 if degraded.
// ?deep=1 includes the doctor section.
func HealthHandler(c *health.Checker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rep := c.Check(r.Context(), r.URL.Query().Get("deep") == "1")
		w.Header().Set("Content-Type", "application/json")
		if rep.Status != "ok" {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(w).Encode(rep)
	}
}
