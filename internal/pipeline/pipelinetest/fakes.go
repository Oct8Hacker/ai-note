package pipelinetest

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"time"

	"ainote/internal/pipeline"
)

// NoopLimiter never limits.
type NoopLimiter struct{}

// Acquire always succeeds immediately.
func (NoopLimiter) Acquire(context.Context, string, int) error { return nil }

// NoFaults injects nothing.
type NoFaults struct{}

// Check never injects a fault.
func (NoFaults) Check(context.Context, pipeline.FaultPoint) error { return nil }

// FrameDelay never delays frame capture.
func (NoFaults) FrameDelay(context.Context) time.Duration { return 0 }

// FakeStage is a scriptable pipeline.Stage. Use CallCount() to read Calls
// while other goroutines may be running the stage.
type FakeStage struct {
	Status  pipeline.Status
	Results []error // returned in order, then nil
	Hook    func(ctx context.Context) error
	Calls   int
	Result  pipeline.StageResult // returned when the call succeeds

	mu sync.Mutex
}

// Name returns the configured Status.
func (f *FakeStage) Name() pipeline.Status { return f.Status }

// Run counts the call, runs Hook, then returns the next scripted error or Result.
func (f *FakeStage) Run(ctx context.Context, _ *pipeline.Job, _ pipeline.Deps) (pipeline.StageResult, error) {
	f.mu.Lock()
	n := f.Calls
	f.Calls++
	f.mu.Unlock()
	if f.Hook != nil {
		if err := f.Hook(ctx); err != nil {
			return pipeline.StageResult{}, err
		}
	}
	if n < len(f.Results) && f.Results[n] != nil {
		return pipeline.StageResult{}, f.Results[n]
	}
	return f.Result, nil
}

// CallCount is the concurrency-safe read of Calls.
func (f *FakeStage) CallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Calls
}

// TestDeps returns Deps wired with no-op fakes around store. When store is a
// *MemStore, Warn and Progress are recorded on it.
func TestDeps(store pipeline.CheckpointStore) pipeline.Deps {
	d := pipeline.Deps{
		Store:    store,
		Limiter:  NoopLimiter{},
		Faults:   NoFaults{},
		Progress: func(int) {},
		Warn:     func(pipeline.Warning) {},
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if ms, ok := store.(*MemStore); ok {
		d.Progress = ms.recordProgress
		d.Warn = ms.AddWarning
	}
	return d
}
