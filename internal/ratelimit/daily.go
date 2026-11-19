package ratelimit

import (
	"context"
	"time"

	"ainote/internal/pipeline"
)

// DailySpec is a per-day request cap that resets at local midnight in Loc.
type DailySpec struct {
	Limit int
	Loc   *time.Location
}

// NextReset returns the next local midnight in loc after now.
func NextReset(now time.Time, loc *time.Location) time.Time {
	t := now.In(loc)
	return time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, loc)
}

// acquireDaily counts n requests against today's counter. Over the limit it
// returns RateLimitedError at once (a daily cap is never worth blocking for).
func (l *Limiter) acquireDaily(ctx context.Context, name string, spec DailySpec, n int) error {
	now := l.now()
	reset := NextReset(now, spec.Loc)
	key := "rl:" + name + ":" + now.In(spec.Loc).Format("2006-01-02")

	pipe := l.rdb.TxPipeline()
	incr := pipe.IncrBy(ctx, key, int64(n))
	pipe.ExpireAt(ctx, key, reset)
	if _, err := pipe.Exec(ctx); err != nil {
		return l.unavailable(ctx, err)
	}
	if incr.Val() > int64(spec.Limit) {
		return &pipeline.RateLimitedError{Provider: providerOf(name), RetryAfter: reset.Sub(now)}
	}
	return nil
}
