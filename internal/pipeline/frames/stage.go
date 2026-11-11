package frames

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"ainote/internal/pipeline"
	"golang.org/x/sync/errgroup"
)

const (
	pending  pipeline.FrameStatus = "pending"
	captured pipeline.FrameStatus = "captured"
	failed   pipeline.FrameStatus = "failed"
	omitted  pipeline.FrameStatus = "omitted"
)

// StreamResolver obtains a fresh URL for the source video.
type StreamResolver interface {
	StreamURL(context.Context, string) (string, error)
}

// Stage captures the selected visuals and verifies captions before publishing.
type Stage struct {
	YT                          StreamResolver
	FF                          FFmpeg
	Verifier                    Verifier
	Verify                      bool
	Concurrency                 int
	FrameTimeout                time.Duration
	SceneThreshold, MaxDriftSec float64
	Snap, PickSharpest          bool
}

// Name identifies the worker stage.
func (*Stage) Name() pipeline.Status { return pipeline.StatusExtractingFrames }

type frameRun struct {
	*Stage
	job    *pipeline.Job
	deps   pipeline.Deps
	source string
}

// Run resumes from individual frame checkpoints.
func (s *Stage) Run(ctx context.Context, job *pipeline.Job, deps pipeline.Deps) (pipeline.StageResult, error) {
	r := frameRun{Stage: s, job: job, deps: deps}
	return pipeline.StageResult{}, r.process(ctx)
}
func (r *frameRun) process(ctx context.Context) error {
	var syn pipeline.Synthesis
	ok, err := r.deps.Store.LoadArtifact(ctx, r.job.ID, "synthesis", &syn)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("missing synthesis checkpoint")
	}
	rows, err := r.deps.Store.ListFrames(ctx, r.job.ID)
	if err != nil {
		return err
	}
	existing := make(map[int]bool)
	for _, row := range rows {
		existing[row.Idx] = true
	}
	for i, v := range syn.Visuals {
		if existing[i] {
			continue
		}
		row := pipeline.Frame{JobID: r.job.ID, Idx: i, RequestedSec: v.TimestampSec, SectionIndex: v.SectionIndex, Caption: v.Caption, Reason: v.Reason, Status: pending}
		if err := r.deps.Store.UpsertFrame(ctx, row); err != nil {
			return err
		}
		rows = append(rows, row)
	}
	var todo []pipeline.Frame
	for _, row := range rows {
		if row.Status == pending && row.NotionFileUploadID == "" {
			todo = append(todo, row)
		}
	}
	if len(todo) > 0 {
		if err := r.resolve(ctx); err != nil {
			return err
		}
		if err := r.capturePending(ctx, todo); err != nil {
			return err
		}
		rows, err = r.deps.Store.ListFrames(ctx, r.job.ID)
		if err != nil {
			return err
		}
	}
	var check []pipeline.Frame
	for _, row := range rows {
		if row.NotionFileUploadID != "" || row.Status != captured {
			continue
		}
		if row.Verified || !r.Verify || r.Verifier == nil {
			if !row.Verified || strings.TrimSpace(row.Caption) == "" {
				if err := r.finish(ctx, row); err != nil {
					return err
				}
			}
			continue
		}
		check = append(check, row)
	}
	return r.verifyBatch(ctx, check, false)
}
func (r *frameRun) resolve(ctx context.Context) error {
	if r.source != "" {
		return nil
	}
	src, err := r.YT.StreamURL(ctx, r.job.VideoID)
	if err == nil {
		r.source = src
	}
	return err
}
func (r *frameRun) capturePending(ctx context.Context, rows []pipeline.Frame) error {
	group, gctx := errgroup.WithContext(ctx)
	group.SetLimit(max(1, r.Concurrency))
	var mu sync.Mutex
	done := 0
	for _, row := range rows {
		if gctx.Err() != nil {
			break
		}
		group.Go(func() error {
			if err := gctx.Err(); err != nil {
				return err
			}
			result, err := r.capture(gctx, row, false)
			if gctx.Err() != nil {
				return gctx.Err()
			}
			var throttle *pipeline.RateLimitedError
			if errors.As(err, &throttle) {
				return err
			}
			if err != nil {
				result.Status = failed
				result.WarningCode = pipeline.WarnFrameExtractionFailed
			}
			if err := r.deps.Store.UpsertFrame(gctx, result); err != nil {
				return err
			}
			mu.Lock()
			defer mu.Unlock()
			if result.Status == failed {
				r.deps.Warn(pipeline.Warning{Code: result.WarningCode, TimestampSec: &result.RequestedSec})
			}
			done++
			r.deps.Progress(100 * done / len(rows))
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return err
	}
	return ctx.Err()
}

func (r *frameRun) capture(ctx context.Context, row pipeline.Frame, retry bool) (pipeline.Frame, error) {
	if r.FrameTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.FrameTimeout)
		defer cancel()
	}
	if delay := r.deps.Faults.FrameDelay(ctx); delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return row, ctx.Err()
		case <-timer.C:
		}
	}
	if err := r.deps.Faults.Check(ctx, pipeline.FaultFrame); err != nil {
		return row, err
	}
	t := row.RequestedSec
	if retry {
		if row.CapturedSec == nil {
			return row, errors.New("missing capture time")
		}
		t = *row.CapturedSec + 2
	} else if r.Snap {
		if err := r.deps.Limiter.Acquire(ctx, pipeline.BucketYouTube, 1); err != nil {
			return row, err
		}
		changes, err := r.FF.SceneChanges(ctx, r.source, max(0, t-2), t+2, r.SceneThreshold)
		if err != nil {
			return row, err
		}
		t = ChooseTime(t, changes, r.MaxDriftSec)
	}
	offsets := []float64{0}
	if r.PickSharpest && !retry {
		offsets = append(offsets, -0.5, 0.5)
	}
	var tried []float64
	var lastErr error
	best := -1.
	var bestTime float64
	var bestJPEG []byte
	for _, offset := range offsets {
		at := ClampDrift(row.RequestedSec, t+offset, r.MaxDriftSec)
		if r.job.DurationSec > 0 {
			at = min(at, float64(r.job.DurationSec))
		}
		if slices.Contains(tried, at) {
			continue
		}
		tried = append(tried, at)
		if err := r.deps.Limiter.Acquire(ctx, pipeline.BucketYouTube, 1); err != nil {
			return row, err
		}
		data, err := r.FF.Capture(ctx, r.source, at)
		score := 0.
		if err == nil && len(offsets) > 1 {
			score, err = Sharpness(data)
		}
		if err != nil {
			var rate *pipeline.RateLimitedError
			if errors.As(err, &rate) || ctx.Err() != nil {
				return row, err
			}
			lastErr = err
			continue
		}
		if score > best {
			best, bestJPEG, bestTime = score, data, at
		}
	}
	if best < 0 {
		return row, lastErr
	}
	row.ImageJPEG = bestJPEG
	row.CapturedSec = &bestTime
	row.Status = captured
	row.Verified = false
	row.WarningCode = ""
	return row, nil
}

func (r *frameRun) finish(ctx context.Context, row pipeline.Frame) error {
	if strings.TrimSpace(row.Caption) == "" {
		row.Status = omitted
		row.WarningCode = pipeline.WarnFrameNotRelevant
	}
	row.Verified = true
	if row.Status == omitted {
		row.ImageJPEG = nil
	}
	if err := r.deps.Store.UpsertFrame(ctx, row); err != nil {
		return err
	}
	if row.WarningCode != "" {
		r.deps.Warn(pipeline.Warning{Code: row.WarningCode, TimestampSec: row.CapturedSec})
	}
	return nil
}
func (r *frameRun) verifyBatch(ctx context.Context, rows []pipeline.Frame, second bool) error {
	if len(rows) == 0 {
		return nil
	}
	inputs := make([]VerifyInput, 0, len(rows))
	for _, row := range rows {
		inputs = append(inputs, VerifyInput{Index: row.Idx, JPEG: row.ImageJPEG, Caption: row.Caption, Reason: row.Reason})
	}
	outs, err := r.Verifier.Verify(ctx, inputs)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	decisions := make(map[int]VerifyOutput, len(outs))
	for _, out := range outs {
		decisions[out.Index] = out
	}
	for _, row := range rows {
		if _, ok := decisions[row.Idx]; !ok {
			err = fmt.Errorf("missing verification for frame %d", row.Idx)
			break
		}
	}
	if err != nil {
		r.deps.Warn(pipeline.Warning{Code: pipeline.WarnFrameVerifySkipped})
		for _, row := range rows {
			if err := r.finish(ctx, row); err != nil {
				return err
			}
		}
		return nil
	}
	var retries []pipeline.Frame
	for _, row := range rows {
		out := decisions[row.Idx]
		switch {
		case out.ShowsVisual:
			if !out.MatchesCaption {
				row.Caption = out.RevisedCaption
			}
		case !second:
			if err := r.resolve(ctx); err != nil {
				return err
			}
			candidate, err := r.capture(ctx, row, true)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			var rate *pipeline.RateLimitedError
			if errors.As(err, &rate) {
				return err
			}
			if err == nil {
				retries = append(retries, candidate)
				continue
			}
			row.Status = omitted
			row.WarningCode = pipeline.WarnFrameNotRelevant
		default:
			row.Status = omitted
			row.WarningCode = pipeline.WarnFrameNotRelevant
		}
		if err := r.finish(ctx, row); err != nil {
			return err
		}
	}
	if len(retries) > 0 {
		return r.verifyBatch(ctx, retries, true)
	}
	return nil
}
