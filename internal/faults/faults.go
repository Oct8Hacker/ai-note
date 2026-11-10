// Package faults is the runtime fault-injection switchboard (SRS 9.6). Defaults
// come from env (config.FaultDefaults); overrides live in the Redis hash
// "faults" so an admin can flip a failure on during a live demo without a restart.
package faults

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"ainote/internal/config"
	"ainote/internal/pipeline"
)

const (
	hashKey    = "faults"
	cacheTTL   = time.Second
	retryAfter = 5 * time.Second
	maxDelayMs = 60000

	keyLLM       = "llm_429_rate"
	keyNotion    = "notion_429_rate"
	keyYouTube   = "youtube_429_rate"
	keyFrameRate = "frame_fail_rate"
	keyFrameAll  = "frame_fail_all"
	keyDelay     = "stage_delay_ms"
)

// ErrInvalid is matched (errors.Is) by every validation failure from Set.
var ErrInvalid = errors.New("invalid fault flags")

type invalidError struct{ msg string }

func (e invalidError) Error() string { return "invalid fault flags: " + e.msg }

// Is makes errors.Is(err, ErrInvalid) true.
func (e invalidError) Is(target error) bool { return target == ErrInvalid }

// Invalid marks this as a client error for callers (the API layer) that cannot
// import this package.
func (e invalidError) Invalid() bool { return true }

func invalidf(format string, a ...any) error { return invalidError{msg: fmt.Sprintf(format, a...)} }

type effective struct {
	llm, notion, youtube, frameRate float64
	frameAll                        bool
	delayMs                         int
}

func (e effective) active() bool {
	return e.llm != 0 || e.notion != 0 || e.youtube != 0 || e.frameRate != 0 || e.frameAll || e.delayMs != 0
}

func (e effective) asMap() map[string]string {
	return map[string]string{
		keyLLM:       formatRate(e.llm),
		keyNotion:    formatRate(e.notion),
		keyYouTube:   formatRate(e.youtube),
		keyFrameRate: formatRate(e.frameRate),
		keyFrameAll:  strconv.FormatBool(e.frameAll),
		keyDelay:     strconv.Itoa(e.delayMs),
	}
}

func formatRate(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// Faults implements pipeline.Faults and api.FaultAdmin on top of Redis.
type Faults struct {
	rdb      *redis.Client
	defaults effective
	rnd      func() float64

	mu         sync.Mutex
	cached     effective
	until      time.Time
	logged     effective
	loggedOnce bool
}

// New builds a Faults. rnd returns a value in [0,1) (rand.Float64 in production).
// It logs once immediately when the env defaults already enable a fault.
func New(rdb *redis.Client, d config.FaultDefaults, rnd func() float64) *Faults {
	f := &Faults{
		rdb: rdb,
		rnd: rnd,
		defaults: effective{
			llm: d.LLM429Rate, notion: d.Notion429Rate, youtube: d.YouTube429Rate,
			frameRate: d.FrameFailRate, frameAll: d.FrameFailAll, delayMs: d.StageDelayMs,
		},
	}
	f.defaults.clamp()
	f.cached = f.defaults
	f.noteChange(f.defaults)
	return f
}

func (e *effective) clamp() {
	for _, p := range []*float64{&e.llm, &e.notion, &e.youtube, &e.frameRate} {
		*p = min(max(*p, 0), 1)
	}
	e.delayMs = min(max(e.delayMs, 0), maxDelayMs)
}

// noteChange logs the active values when they differ from what was last logged.
// Callers hold f.mu (or are the constructor). Never called per Check.
func (f *Faults) noteChange(e effective) {
	if f.loggedOnce && e == f.logged {
		return
	}
	wasActive := f.loggedOnce && f.logged.active()
	f.logged, f.loggedOnce = e, true
	switch {
	case e.active():
		slog.Warn("fault injection active",
			keyLLM, e.llm, keyNotion, e.notion, keyYouTube, e.youtube,
			keyFrameRate, e.frameRate, keyFrameAll, e.frameAll, keyDelay, e.delayMs)
	case wasActive:
		slog.Info("fault injection cleared")
	}
}

// load reads the hash and merges it over the env defaults.
func (f *Faults) load(ctx context.Context) (effective, error) {
	m, err := f.rdb.HGetAll(ctx, hashKey).Result()
	if err != nil {
		return effective{}, err
	}
	e := f.defaults
	for k, v := range m {
		switch k {
		case keyLLM:
			e.llm = parseRate(v, e.llm)
		case keyNotion:
			e.notion = parseRate(v, e.notion)
		case keyYouTube:
			e.youtube = parseRate(v, e.youtube)
		case keyFrameRate:
			e.frameRate = parseRate(v, e.frameRate)
		case keyFrameAll:
			if b, err := strconv.ParseBool(v); err == nil {
				e.frameAll = b
			}
		case keyDelay:
			if n, err := strconv.Atoi(v); err == nil && n >= 0 && n <= maxDelayMs {
				e.delayMs = n
			}
		}
	}
	return e, nil
}

func parseRate(s string, fallback float64) float64 {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || v < 0 || v > 1 {
		return fallback
	}
	return v
}

// current returns the cached effective config, refreshing it at most once per
// cacheTTL. Fault injection must never break real work, so a Redis error fails
// OPEN: no fault is injected for that period, and the warning is logged at most
// once per period because the failed result is cached too.
func (f *Faults) current(ctx context.Context) effective {
	f.mu.Lock()
	defer f.mu.Unlock()
	if time.Now().Before(f.until) {
		return f.cached
	}
	e, err := f.load(ctx)
	if err != nil {
		slog.Warn("fault injection: redis read failed, injecting nothing", "err", err)
		e = effective{}
	}
	f.cached, f.until = e, time.Now().Add(cacheTTL)
	f.noteChange(e)
	return e
}

// Check returns the injected failure for point p, or nil.
func (f *Faults) Check(ctx context.Context, p pipeline.FaultPoint) error {
	e := f.current(ctx)
	var rate float64
	switch p {
	case pipeline.FaultLLM:
		rate = e.llm
	case pipeline.FaultNotion:
		rate = e.notion
	case pipeline.FaultYouTube:
		rate = e.youtube
	case pipeline.FaultFrame:
		if e.frameAll || (e.frameRate > 0 && f.rnd() < e.frameRate) {
			return pipeline.ErrInjectedFrameFailure
		}
		return nil
	default:
		return nil
	}
	if rate > 0 && f.rnd() < rate {
		return &pipeline.RateLimitedError{Provider: string(p), RetryAfter: retryAfter}
	}
	return nil
}

// FrameDelay is the artificial per-stage delay (stage_delay_ms).
func (f *Faults) FrameDelay(ctx context.Context) time.Duration {
	return time.Duration(f.current(ctx).delayMs) * time.Millisecond
}

// Get returns the effective flags (env defaults overlaid with Redis overrides),
// read fresh from Redis.
func (f *Faults) Get(ctx context.Context) (map[string]string, error) {
	e, err := f.load(ctx)
	if err != nil {
		return nil, fmt.Errorf("faults get: %w", err)
	}
	return e.asMap(), nil
}

// Set validates m (unknown keys and out-of-range values are rejected with
// ErrInvalid, nothing is written) and stores it as overrides. The local cache is
// dropped so this process sees the change at once; others within cacheTTL.
func (f *Faults) Set(ctx context.Context, m map[string]string) error {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	vals := make(map[string]any, len(m))
	for _, k := range keys {
		v := m[k]
		switch k {
		case keyLLM, keyNotion, keyYouTube, keyFrameRate:
			r, err := strconv.ParseFloat(v, 64)
			if err != nil || math.IsNaN(r) || r < 0 || r > 1 {
				return invalidf("%s must be a number in [0,1]", k)
			}
		case keyFrameAll:
			if _, err := strconv.ParseBool(v); err != nil {
				return invalidf("%s must be a bool", k)
			}
		case keyDelay:
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 || n > maxDelayMs {
				return invalidf("%s must be an integer in [0,%d]", k, maxDelayMs)
			}
		default:
			return invalidf("unknown flag %q", k)
		}
		vals[k] = v
	}
	if len(vals) == 0 {
		return nil
	}
	if err := f.rdb.HSet(ctx, hashKey, vals).Err(); err != nil {
		return fmt.Errorf("faults set: %w", err)
	}
	f.mu.Lock()
	f.until = time.Time{}
	f.mu.Unlock()
	return nil
}
