// Package ratelimit provides the Redis-backed limiter behind pipeline.Limiter:
// token buckets shared by every worker, plus a daily request cap.
package ratelimit

import (
	"context"
	_ "embed"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"ainote/internal/config"
	"ainote/internal/pipeline"
)

//go:embed bucket.lua
var bucketLua string

var bucketScript = redis.NewScript(bucketLua)

// BucketSpec describes one token bucket.
type BucketSpec struct {
	Capacity     float64
	RefillPerSec float64
}

// Limiter implements pipeline.Limiter on top of Redis.
type Limiter struct {
	rdb            *redis.Client
	buckets        map[string]BucketSpec
	notionTemplate BucketSpec
	daily          map[string]DailySpec
	maxWait        time.Duration
	now            func() time.Time
}

var _ pipeline.Limiter = (*Limiter)(nil)

// New builds a Limiter. Buckets named "notion:*" use notionTemplate, one
// independent bucket per name. now is used for the daily cap's calendar day;
// token buckets read the Redis server clock so all clients agree.
func New(rdb *redis.Client, buckets map[string]BucketSpec, notionTemplate BucketSpec,
	daily map[string]DailySpec, maxWait time.Duration, now func() time.Time) *Limiter {
	return &Limiter{rdb: rdb, buckets: buckets, notionTemplate: notionTemplate, daily: daily, maxWait: maxWait, now: now}
}

// providerOf maps a bucket name to the provider reported in RateLimitedError.
func providerOf(bucket string) string {
	prefix, _, _ := strings.Cut(bucket, ":")
	if prefix == "llm-fallback" {
		return "llm"
	}
	return prefix
}

func (l *Limiter) spec(bucket string) (BucketSpec, bool) {
	if s, ok := l.buckets[bucket]; ok {
		return s, true
	}
	if strings.HasPrefix(bucket, "notion:") {
		return l.notionTemplate, true
	}
	return BucketSpec{}, false
}

// unavailable converts a Redis failure into the fail-closed error (FR-RL-5),
// unless the caller's own context ended, which is reported as is.
func (l *Limiter) unavailable(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return &pipeline.RetryableError{Code: "LIMITER_UNAVAILABLE", Err: err}
}

// Take atomically refills the bucket and takes n tokens. It returns 0 when the
// tokens were taken, otherwise how long to wait before they will be available
// (nothing is taken then).
func (l *Limiter) Take(ctx context.Context, bucket string, n int) (time.Duration, error) {
	spec, ok := l.spec(bucket)
	if !ok {
		return 0, &pipeline.FatalError{Code: "LIMITER_UNKNOWN_BUCKET", Msg: bucket}
	}
	res, err := bucketScript.Run(ctx, l.rdb, []string{"rl:bucket:" + bucket}, spec.Capacity, spec.RefillPerSec, n).Int64()
	if err != nil {
		return 0, l.unavailable(ctx, err)
	}
	return time.Duration(res) * time.Microsecond, nil
}

// Acquire takes n tokens from bucket, blocking up to maxWait in total. If the
// wait would exceed maxWait it returns *pipeline.RateLimitedError instead.
func (l *Limiter) Acquire(ctx context.Context, bucket string, n int) error {
	if d, ok := l.daily[bucket]; ok {
		return l.acquireDaily(ctx, bucket, d, n)
	}
	var waited time.Duration
	for {
		wait, err := l.Take(ctx, bucket, n)
		if err != nil {
			return err
		}
		if wait == 0 {
			return nil
		}
		if waited+wait > l.maxWait {
			return &pipeline.RateLimitedError{Provider: providerOf(bucket), RetryAfter: wait}
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		waited += wait
	}
}

// FromWorkerConfig derives the limiter settings from the worker config.
// Bucket capacity is ten seconds of refill (RPM/6, TPM/6), at least one request.
func FromWorkerConfig(cfg config.Worker) (map[string]BucketSpec, BucketSpec, map[string]DailySpec) {
	rpm := float64(cfg.LLMRPM)
	req := BucketSpec{Capacity: max(1, rpm/6), RefillPerSec: rpm / 60}
	tpm := float64(cfg.LLMTPM)
	buckets := map[string]BucketSpec{
		pipeline.BucketLLMRequests:         req,
		pipeline.BucketLLMFallbackRequests: req,
		pipeline.BucketLLMTokens:           {Capacity: tpm / 6, RefillPerSec: tpm / 60},
		// One second of refill (at least 1), so a frame's scene check and
		// sharpness candidates (4 stream reads) can start together.
		pipeline.BucketYouTube: {Capacity: max(1, cfg.YouTubeRPS), RefillPerSec: cfg.YouTubeRPS},
	}
	notion := BucketSpec{Capacity: 3, RefillPerSec: cfg.NotionRPS}
	daily := map[string]DailySpec{}
	if cfg.LLMRPD > 0 {
		loc, err := time.LoadLocation(cfg.LLMRPDResetTZ)
		if err != nil { // config validation already rejects this; UTC is a safe fallback
			loc = time.UTC
		}
		daily[pipeline.BucketLLMDaily] = DailySpec{Limit: cfg.LLMRPD, Loc: loc}
	}
	return buckets, notion, daily
}
