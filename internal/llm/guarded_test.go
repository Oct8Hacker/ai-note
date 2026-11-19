package llm

import (
	"ainote/internal/pipeline"
	"ainote/internal/pipeline/pipelinetest"
	"context"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

type meter struct {
	buckets []string
	counts  []int
}

func (m *meter) Acquire(_ context.Context, b string, n int) error {
	m.buckets = append(m.buckets, b)
	m.counts = append(m.counts, n)
	return nil
}

type stub struct {
	Provider
	err   error
	calls int
}

func (p *stub) GenerateJSON(context.Context, JSONRequest) (JSONResponse, error) {
	p.calls++
	return JSONResponse{}, p.err
}

type inject struct{ pipelinetest.NoFaults }

func (inject) Check(context.Context, pipeline.FaultPoint) error {
	return &pipeline.RateLimitedError{Provider: "llm", RetryAfter: time.Second}
}
func TestGuardedFallbackAndBuckets(t *testing.T) {
	for _, code := range []string{"LLM_UNAVAILABLE", "RATE_LIMITED", "INJECTED"} {
		p := &stub{err: &pipeline.RetryableError{Code: code}}
		f := &stub{}
		m := &meter{}
		g := Guarded{Primary: p, Fallback: f, Limiter: m, Faults: pipelinetest.NoFaults{}, TokenChunkSize: 3}
		if code == "RATE_LIMITED" {
			p.err = &pipeline.RateLimitedError{Provider: "llm"}
		}
		if code == "INJECTED" {
			g.Faults = inject{}
		}
		_, err := g.GenerateJSON(context.Background(), JSONRequest{MaxOutputTokens: 7})
		switch code {
		case "LLM_UNAVAILABLE":
			require.NoError(t, err)
			require.Equal(t, 2, p.calls)
			require.Equal(t, 1, f.calls)
			require.Equal(t, []string{pipeline.BucketLLMRequests, pipeline.BucketLLMTokens, pipeline.BucketLLMTokens, pipeline.BucketLLMTokens, pipeline.BucketLLMDaily}, m.buckets[:5])
			require.Equal(t, []int{1, 3, 3, 1, 1}, m.counts[:5])
			require.Equal(t, pipeline.BucketLLMFallbackRequests, m.buckets[len(m.buckets)-1])
		case "RATE_LIMITED":
			require.Error(t, err)
			require.Equal(t, 1, p.calls)
			require.Zero(t, f.calls)
		case "INJECTED":
			require.Error(t, err)
			require.Zero(t, p.calls)
			require.Empty(t, m.buckets)
		}
	}
}

// throttled rate-limits one bucket and records every bucket asked for.
type throttled struct {
	meter
	bucket string
}

func (m *throttled) Acquire(ctx context.Context, b string, n int) error {
	_ = m.meter.Acquire(ctx, b, n)
	if b == m.bucket {
		return &pipeline.RateLimitedError{Provider: "llm", RetryAfter: time.Minute}
	}
	return nil
}

// A per-minute wait that ends in a rate-limit retry must not spend the daily
// budget: that unit buys no call, and a burst of such retries used to exhaust
// LLM_RPD while only a few real calls were made.
func TestRateLimitedReserveSpendsNoDailyBudget(t *testing.T) {
	for _, bucket := range []string{pipeline.BucketLLMRequests, pipeline.BucketLLMTokens} {
		p := &stub{}
		m := &throttled{bucket: bucket}
		g := Guarded{Primary: p, Limiter: m, Faults: pipelinetest.NoFaults{}}
		_, err := g.GenerateJSON(context.Background(), JSONRequest{MaxOutputTokens: 7})
		require.Equal(t, "RATE_LIMITED", pipeline.ErrorCode(err), bucket)
		require.Zero(t, p.calls, bucket)
		require.NotContains(t, m.buckets, pipeline.BucketLLMDaily, bucket)
	}
}
