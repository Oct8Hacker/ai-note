package health

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func okProbes() map[string]Probe {
	ok := func(context.Context) error { return nil }
	return map[string]Probe{"postgres": ok, "rabbitmq": ok, "redis": ok}
}

func TestCheckAllOK(t *testing.T) {
	c := &Checker{Probes: okProbes(), Version: "v1"}
	r := c.Check(context.Background(), false)
	require.Equal(t, "ok", r.Status)
	require.Equal(t, "v1", r.Version)
	for _, v := range r.Checks {
		require.Equal(t, "ok", v)
	}
	require.Len(t, r.Checks, 3)
}

func TestCheckOneDown(t *testing.T) {
	p := okProbes()
	p["rabbitmq"] = func(context.Context) error { return errors.New("connection refused") }
	r := (&Checker{Probes: p}).Check(context.Background(), false)
	require.Equal(t, "degraded", r.Status)
	require.Equal(t, "error: connection refused", r.Checks["rabbitmq"])
}

func TestProbeTimeout(t *testing.T) {
	p := map[string]Probe{"redis": func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }}
	start := time.Now()
	r := (&Checker{Probes: p}).Check(context.Background(), false)
	require.Less(t, time.Since(start), 3*time.Second)
	require.Equal(t, "degraded", r.Status)
	require.Contains(t, r.Checks["redis"], "error:")
}

func TestProbeIgnoringContextStillTimesOut(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	p := map[string]Probe{"redis": func(context.Context) error { <-block; return nil }}
	start := time.Now()
	r := (&Checker{Probes: p}).Check(context.Background(), false)
	require.Less(t, time.Since(start), 3*time.Second)
	require.Equal(t, "degraded", r.Status)
}

func TestWorkersAndDoctor(t *testing.T) {
	c := &Checker{
		Probes:       okProbes(),
		WorkersAlive: func(context.Context) (int, error) { return 2, nil },
		Provider429:  func(context.Context) (int64, error) { return 7, nil },
		Doctor:       func(context.Context) (map[string]any, error) { return map[string]any{"x": 1}, nil },
	}
	r := c.Check(context.Background(), false)
	require.Equal(t, 2, r.WorkersAlive)
	require.Equal(t, int64(7), r.Provider429Total)
	require.Nil(t, r.Doctor)
	r = c.Check(context.Background(), true)
	require.Equal(t, 1, r.Doctor["x"])
}

func TestSlowCallbacksDoNotMutateReport(t *testing.T) {
	c := &Checker{
		Probes: okProbes(),
		WorkersAlive: func(context.Context) (int, error) {
			time.Sleep(2500 * time.Millisecond)
			return 9, nil
		},
		Provider429: func(context.Context) (int64, error) {
			time.Sleep(2500 * time.Millisecond)
			return 9, nil
		},
		Doctor: func(context.Context) (map[string]any, error) {
			time.Sleep(2500 * time.Millisecond)
			return map[string]any{"late": true}, nil
		},
	}
	start := time.Now()
	r := c.Check(context.Background(), true)
	require.Less(t, time.Since(start), 8*time.Second)
	time.Sleep(1500 * time.Millisecond) // let the slow callbacks finish
	require.Equal(t, 0, r.WorkersAlive)
	require.Equal(t, int64(0), r.Provider429Total)
	require.Nil(t, r.Doctor)
}
