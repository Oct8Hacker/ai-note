package faults

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"ainote/internal/api"
	"ainote/internal/config"
	"ainote/internal/pipeline"
)

var (
	_ api.FaultAdmin  = (*Faults)(nil)
	_ pipeline.Faults = (*Faults)(nil)
)

func newFaults(t *testing.T, d config.FaultDefaults, rnd func() float64) (*Faults, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	if rnd == nil {
		rnd = func() float64 { return 0.5 }
	}
	return New(rdb, d, rnd), mr
}

func TestNoFaultsByDefault(t *testing.T) {
	f, _ := newFaults(t, config.FaultDefaults{}, nil)
	for _, p := range []pipeline.FaultPoint{pipeline.FaultLLM, pipeline.FaultNotion, pipeline.FaultYouTube, pipeline.FaultFrame} {
		if err := f.Check(context.Background(), p); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
	}
	if d := f.FrameDelay(context.Background()); d != 0 {
		t.Fatalf("delay %v", d)
	}
}

func TestRateOne(t *testing.T) {
	f, _ := newFaults(t, config.FaultDefaults{}, nil)
	ctx := context.Background()
	if err := f.Set(ctx, map[string]string{"llm_429_rate": "1"}); err != nil {
		t.Fatal(err)
	}
	var rl *pipeline.RateLimitedError
	err := f.Check(ctx, pipeline.FaultLLM)
	if !errors.As(err, &rl) || rl.Provider != "llm" || rl.RetryAfter != 5*time.Second {
		t.Fatalf("got %v", err)
	}
	if err := f.Check(ctx, pipeline.FaultNotion); err != nil {
		t.Fatalf("notion: %v", err)
	}
}

func TestEnvDefaultsApply(t *testing.T) {
	f, _ := newFaults(t, config.FaultDefaults{YouTube429Rate: 1, StageDelayMs: 250}, nil)
	var rl *pipeline.RateLimitedError
	if err := f.Check(context.Background(), pipeline.FaultYouTube); !errors.As(err, &rl) || rl.Provider != "youtube" {
		t.Fatalf("got %v", err)
	}
	if d := f.FrameDelay(context.Background()); d != 250*time.Millisecond {
		t.Fatalf("delay %v", d)
	}
}

func TestFrameFailAll(t *testing.T) {
	f, _ := newFaults(t, config.FaultDefaults{}, nil)
	ctx := context.Background()
	if err := f.Set(ctx, map[string]string{"frame_fail_all": "true"}); err != nil {
		t.Fatal(err)
	}
	if err := f.Check(ctx, pipeline.FaultFrame); !errors.Is(err, pipeline.ErrInjectedFrameFailure) {
		t.Fatalf("got %v", err)
	}
}

func TestFrameFailRate(t *testing.T) {
	f, _ := newFaults(t, config.FaultDefaults{FrameFailRate: 0.5}, func() float64 { return 0.4 })
	if err := f.Check(context.Background(), pipeline.FaultFrame); !errors.Is(err, pipeline.ErrInjectedFrameFailure) {
		t.Fatalf("got %v", err)
	}
	f2, _ := newFaults(t, config.FaultDefaults{FrameFailRate: 0.5}, func() float64 { return 0.6 })
	if err := f2.Check(context.Background(), pipeline.FaultFrame); err != nil {
		t.Fatalf("got %v", err)
	}
}

func TestRuntimeOverride(t *testing.T) {
	f, _ := newFaults(t, config.FaultDefaults{LLM429Rate: 1}, nil)
	ctx := context.Background()
	if f.Check(ctx, pipeline.FaultLLM) == nil {
		t.Fatal("default should inject")
	}
	if err := f.Set(ctx, map[string]string{"llm_429_rate": "0", "stage_delay_ms": "100"}); err != nil {
		t.Fatal(err)
	}
	if err := f.Check(ctx, pipeline.FaultLLM); err != nil {
		t.Fatalf("override not visible immediately: %v", err)
	}
	if d := f.FrameDelay(ctx); d != 100*time.Millisecond {
		t.Fatalf("delay %v", d)
	}
	got, err := f.Get(ctx)
	if err != nil || got["llm_429_rate"] != "0" || got["stage_delay_ms"] != "100" {
		t.Fatalf("get %v %v", got, err)
	}
}

func TestOtherProcessSeesOverrideWithinCache(t *testing.T) {
	f, mr := newFaults(t, config.FaultDefaults{}, nil)
	ctx := context.Background()
	if f.Check(ctx, pipeline.FaultLLM) != nil {
		t.Fatal("unexpected")
	}
	mr.HSet("faults", "llm_429_rate", "1")
	time.Sleep(1100 * time.Millisecond)
	if f.Check(ctx, pipeline.FaultLLM) == nil {
		t.Fatal("override from another process not seen after cache period")
	}
}

func TestSetRejectsInvalid(t *testing.T) {
	f, mr := newFaults(t, config.FaultDefaults{}, nil)
	ctx := context.Background()
	bad := []map[string]string{
		{"bogus": "1"},
		{"llm_429_rate": "1.5"},
		{"llm_429_rate": "-0.1"},
		{"llm_429_rate": "abc"},
		{"frame_fail_all": "maybe"},
		{"stage_delay_ms": "60001"},
		{"stage_delay_ms": "-1"},
		{"stage_delay_ms": "1.5"},
		{"llm_429_rate": "0.5", "bogus": "1"},
	}
	for _, m := range bad {
		err := f.Set(ctx, m)
		if err == nil || !errors.Is(err, ErrInvalid) {
			t.Errorf("%v: got %v", m, err)
		}
	}
	if mr.Exists("faults") {
		t.Fatal("invalid Set must not write anything")
	}
}

func TestFailOpenOnRedisError(t *testing.T) {
	f, mr := newFaults(t, config.FaultDefaults{LLM429Rate: 1}, nil)
	mr.Close()
	if err := f.Check(context.Background(), pipeline.FaultLLM); err != nil {
		t.Fatalf("must fail open, got %v", err)
	}
	if d := f.FrameDelay(context.Background()); d != 0 {
		t.Fatalf("delay %v", d)
	}
}

func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
	return &buf
}

func TestLogsWhenEnabled(t *testing.T) {
	buf := captureLog(t)
	f, _ := newFaults(t, config.FaultDefaults{LLM429Rate: 0.5}, nil)
	if !strings.Contains(buf.String(), "fault injection active") {
		t.Fatalf("no startup log: %q", buf.String())
	}
	buf.Reset()
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		_ = f.Check(ctx, pipeline.FaultLLM)
	}
	if buf.Len() != 0 {
		t.Fatalf("logged on Check: %q", buf.String())
	}
	if err := f.Set(ctx, map[string]string{"frame_fail_all": "true"}); err != nil {
		t.Fatal(err)
	}
	_ = f.Check(ctx, pipeline.FaultFrame)
	if !strings.Contains(buf.String(), "fault injection active") || !strings.Contains(buf.String(), "frame_fail_all=true") {
		t.Fatalf("no change log: %q", buf.String())
	}
}

func TestNoLogWhenAllZero(t *testing.T) {
	buf := captureLog(t)
	f, _ := newFaults(t, config.FaultDefaults{}, nil)
	_ = f.Check(context.Background(), pipeline.FaultLLM)
	if buf.Len() != 0 {
		t.Fatalf("unexpected log: %q", buf.String())
	}
}
