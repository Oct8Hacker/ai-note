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

// runWithTimeout runs fn with a 2s deadline; it returns even if fn ignores ctx.
func runWithTimeout(ctx context.Context, fn func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- fn(ctx) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
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
			err := runWithTimeout(ctx, p)
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
		_ = runWithTimeout(ctx, func(ctx context.Context) error {
			n, err := c.WorkersAlive(ctx)
			if err == nil {
				r.WorkersAlive = n
			}
			return err
		})
	}
	if c.Provider429 != nil {
		_ = runWithTimeout(ctx, func(ctx context.Context) error {
			n, err := c.Provider429(ctx)
			if err == nil {
				r.Provider429Total = n
			}
			return err
		})
	}
	if deep && c.Doctor != nil {
		_ = runWithTimeout(ctx, func(ctx context.Context) error {
			d, err := c.Doctor(ctx)
			if err == nil {
				r.Doctor = d
			}
			return err
		})
	}
	return r
}
