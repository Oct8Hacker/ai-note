package llm

import (
	"ainote/internal/pipeline"
	"context"
	"errors"
)

// Guarded enforces provider quotas and permits one fallback call after two primary outages.
type Guarded struct {
	Primary, Fallback Provider
	Limiter           pipeline.Limiter
	Faults            pipeline.Faults
	On429             func(context.Context)
	TokenChunkSize    int
}

// Name identifies the primary provider.
func (g *Guarded) Name() string { return g.Primary.Name() }

// SupportsImages reports the primary adapter's image capability.
func (g *Guarded) SupportsImages() bool { return g.Primary.SupportsImages() }

// Ping checks the primary provider without generating tokens.
func (g *Guarded) Ping(ctx context.Context) error { return g.Primary.Ping(ctx) }

// reserve takes the per-minute request and token budgets first and the daily
// budget last. A per-minute wait can end in a rate-limit retry, and a daily
// unit taken before it would be spent without buying a call.
func (g *Guarded) reserve(ctx context.Context, tokens int) error {
	if err := g.Limiter.Acquire(ctx, pipeline.BucketLLMRequests, 1); err != nil {
		return err
	}
	for tokens > 0 {
		chunk := tokens
		if g.TokenChunkSize > 0 {
			chunk = min(tokens, g.TokenChunkSize)
		}
		if err := g.Limiter.Acquire(ctx, pipeline.BucketLLMTokens, chunk); err != nil {
			return err
		}
		tokens -= chunk
	}
	return g.Limiter.Acquire(ctx, pipeline.BucketLLMDaily, 1)
}
func (g *Guarded) call(ctx context.Context, p Provider, r JSONRequest) (JSONResponse, error) {
	out, err := p.GenerateJSON(ctx, r)
	var rate *pipeline.RateLimitedError
	if g.On429 != nil && errors.As(err, &rate) {
		g.On429(ctx)
	}
	return out, err
}

// GenerateJSON reserves quota for each attempt and fails over only on repeated unavailability.
func (g *Guarded) GenerateJSON(ctx context.Context, r JSONRequest) (JSONResponse, error) {
	if err := g.Faults.Check(ctx, pipeline.FaultLLM); err != nil {
		return JSONResponse{}, err
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := g.reserve(ctx, EstimateTokens(r)); err != nil {
			return JSONResponse{}, err
		}
		out, err := g.call(ctx, g.Primary, r)
		var retry *pipeline.RetryableError
		if g.Fallback == nil || !errors.As(err, &retry) || retry.Code != "LLM_UNAVAILABLE" {
			return out, err
		}
	}
	if err := g.Limiter.Acquire(ctx, pipeline.BucketLLMFallbackRequests, 1); err != nil {
		return JSONResponse{}, err
	}
	return g.call(ctx, g.Fallback, r)
}
