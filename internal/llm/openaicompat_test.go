package llm

import (
	"ainote/internal/pipeline"
	"ainote/internal/pipeline/pipelinetest"
	"context"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestProviderErrors(t *testing.T) {
	for _, tc := range []struct {
		status int
		code   string
	}{{429, "RATE_LIMITED"}, {500, "LLM_UNAVAILABLE"}, {401, "LLM_CONFIG_ERROR"}} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Retry-After", "12")
			w.WriteHeader(tc.status)
		}))
		calls := 0
		g := Guarded{Primary: NewOpenAICompat("test", srv.URL, "secret", "model", time.Second), Limiter: pipelinetest.NoopLimiter{}, Faults: pipelinetest.NoFaults{}, On429: func(context.Context) { calls++ }}
		_, err := g.GenerateJSON(context.Background(), JSONRequest{})
		srv.Close()
		require.Equal(t, tc.code, pipeline.ErrorCode(err))
		if tc.status == 429 {
			var rl *pipeline.RateLimitedError
			require.ErrorAs(t, err, &rl)
			require.Equal(t, 12*time.Second, rl.RetryAfter)
			require.Equal(t, 1, calls)
		}
	}
}
