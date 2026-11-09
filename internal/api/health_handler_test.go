package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"ainote/internal/health"

	"github.com/stretchr/testify/require"
)

func checker(probeErr error) *health.Checker {
	return &health.Checker{
		Probes: map[string]health.Probe{"postgres": func(context.Context) error { return probeErr }},
		Doctor: func(context.Context) (map[string]any, error) { return map[string]any{"a": "b"}, nil },
	}
}

func TestHandler503(t *testing.T) {
	rec := httptest.NewRecorder()
	HealthHandler(checker(errors.New("down")))(rec, httptest.NewRequest("GET", "/healthz", nil))
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Contains(t, rec.Body.String(), `"status":"degraded"`)
}

func TestHandler200(t *testing.T) {
	rec := httptest.NewRecorder()
	HealthHandler(checker(nil))(rec, httptest.NewRequest("GET", "/healthz", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"status":"ok"`)
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
}

func TestDeepOnlyWhenAsked(t *testing.T) {
	rec := httptest.NewRecorder()
	HealthHandler(checker(nil))(rec, httptest.NewRequest("GET", "/healthz", nil))
	require.NotContains(t, rec.Body.String(), "doctor")
	rec = httptest.NewRecorder()
	HealthHandler(checker(nil))(rec, httptest.NewRequest("GET", "/healthz?deep=1", nil))
	require.Contains(t, rec.Body.String(), `"doctor"`)
}
