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

func (NoopLimiter) Acquire(context.Context, string, int) error { return nil }

// NoFaults injects nothing.
type NoFaults struct{}

func (NoFaults) Check(context.Context, pipeline.FaultPoint) error { return nil }
func (NoFaults) FrameDelay(context.Context) time.Duration         { return 0 }

// FakeStage is a scriptable pipeline.Stage. Use CallCount() to read Calls
// while other goroutines may be running the stage.
type FakeStage struct {
	Status  pipeline.Status
	Results []error // returned in order, then nil
	Hook    func(ctx context.Context) error
	Calls   int

	mu sync.Mutex
}

func (f *FakeStage) Name() pipeline.Status { return f.Status }

func (f *FakeStage) Run(ctx context.Context, _ *pipeline.Job, _ pipeline.Deps) (pipeline.StageResult, error) {
	if f.Hook != nil {
		if err := f.Hook(ctx); err != nil {
			return pipeline.StageResult{}, err
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var err error
	if f.Calls < len(f.Results) {
		err = f.Results[f.Calls]
	}
	f.Calls++
	return pipeline.StageResult{}, err
}

// CallCount is the concurrency-safe read of Calls.
func (f *FakeStage) CallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Calls
}

// TestDeps returns Deps wired with no-op fakes around store.
func TestDeps(store pipeline.CheckpointStore) pipeline.Deps {
	return pipeline.Deps{
		Store:    store,
		Limiter:  NoopLimiter{},
		Faults:   NoFaults{},
		Progress: func(int) {},
		Warn:     func(pipeline.Warning) {},
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}
