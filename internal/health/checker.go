// Package health aggregates dependency probes into a /healthz report.
package health

import (
	"context"
	"sync"
	"time"
)

const probeTimeout = 2 * time.Second

// Probe checks one dependency; nil means healthy.
type Probe func(ctx context.Context) error

// Report is the /healthz response body.
type Report struct {
	Status           string            `json:"status"`
	Checks           map[string]string `json:"checks"`
	WorkersAlive     int               `json:"workers_alive"`
	Provider429Total int64             `json:"provider_429_total"`
	Version          string            `json:"version"`
	Doctor           map[string]any    `json:"doctor,omitempty"`
}

// Checker runs probes and optional informational callbacks.
type Checker struct {
	Probes       map[string]Probe
	WorkersAlive func(ctx context.Context) (int, error)
	Provider429  func(ctx context.Context) (int64, error)
	Doctor       func(ctx context.Context) (map[string]any, error)
	Version      string
}

// callWithTimeout runs fn with a 2s deadline and returns its result over a
// buffered channel, so a late fn can never touch caller state. It returns even
// if fn ignores ctx.
func callWithTimeout[T any](ctx context.Context, fn func(context.Context) (T, error)) (T, error) {
	type res struct {
		v   T
		err error
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	ch := make(chan res, 1)
	go func() {
		v, err := fn(ctx)
		ch <- res{v, err}
	}()
	select {
	case r := <-ch:
		return r.v, r.err
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	}
}

// Check runs all probes concurrently. Status is "ok" only if every probe passes.
func (c *Checker) Check(ctx context.Context, deep bool) Report {
	r := Report{Status: "ok", Checks: make(map[string]string, len(c.Probes)), Version: c.Version}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for name, p := range c.Probes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := callWithTimeout(ctx, func(ctx context.Context) (struct{}, error) { return struct{}{}, p(ctx) })
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				r.Checks[name] = "error: " + err.Error()
				r.Status = "degraded"
			} else {
				r.Checks[name] = "ok"
			}
		}()
	}
	wg.Wait()

	if c.WorkersAlive != nil {
		if n, err := callWithTimeout(ctx, c.WorkersAlive); err == nil {
			r.WorkersAlive = n
		}
	}
	if c.Provider429 != nil {
		if n, err := callWithTimeout(ctx, c.Provider429); err == nil {
			r.Provider429Total = n
		}
	}
	if deep && c.Doctor != nil {
		if d, err := callWithTimeout(ctx, c.Doctor); err == nil {
			r.Doctor = d
		}
	}
	return r
}
