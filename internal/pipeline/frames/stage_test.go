package frames

import (
	"ainote/internal/pipeline"
	"ainote/internal/pipeline/pipelinetest"
	"bytes"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"image"
	"image/jpeg"
	"math"
	"sync"
	"testing"
	"testing/quick"
	"time"
)

type testStream struct{ calls int }

type trackedCapture struct {
	data  []byte
	calls int
	cuts  []float64
}

func (f *trackedCapture) Capture(context.Context, string, float64) ([]byte, error) {
	f.calls++
	return f.data, nil
}
func (f *trackedCapture) SceneChanges(context.Context, string, float64, float64, float64) ([]float64, error) {
	f.calls++
	return f.cuts, nil
}

type frameLimiter struct{ calls int }

func (l *frameLimiter) Acquire(_ context.Context, b string, n int) error {
	if b != pipeline.BucketYouTube || n != 1 {
		return errors.New("unexpected bucket or cost")
	}
	l.calls++
	return nil
}
func TestDriftNeverExceeded(t *testing.T) {
	h := fixture(t)
	var jpegData bytes.Buffer
	require.NoError(t, jpeg.Encode(&jpegData, image.NewGray(image.Rect(0, 0, 4, 4)), nil))
	ff := &trackedCapture{data: jpegData.Bytes()}
	meter := &frameLimiter{}
	h.s.FF = ff
	h.s.Snap = true
	h.s.PickSharpest = true
	h.deps.Limiter = meter
	r := frameRun{Stage: &h.s, job: &h.job, deps: h.deps, source: "stream"}
	require.NoError(t, quick.Check(func(request uint16, offsets []int8) bool {
		t0 := float64(request%1000) / 10
		ff.cuts = nil
		for _, delta := range offsets {
			ff.cuts = append(ff.cuts, t0+float64(delta)/20)
		}
		row, err := r.capture(context.Background(), pipeline.Frame{RequestedSec: t0}, false)
		return err == nil && math.Abs(*row.CapturedSec-t0) <= 3 && ff.calls == meter.calls
	}, nil))
}
func TestThrottleDuringVerificationRetry(t *testing.T) {
	h := fixture(t)
	h.s.Verify = true
	h.s.Verifier = checkFunc(func(_ context.Context, in []VerifyInput) ([]VerifyOutput, error) {
		var out []VerifyOutput
		for _, v := range in {
			out = append(out, VerifyOutput{Index: v.Index, ShowsVisual: v.Index != 1, MatchesCaption: true})
		}
		return out, nil
	})
	rate := &pipeline.RateLimitedError{Provider: "youtube"}
	h.ff.fail[22] = rate
	require.ErrorIs(t, h.run(), rate)
	row := h.rows(t)[1]
	require.Equal(t, 20., *row.CapturedSec)
	require.False(t, row.Verified)
	h.ff.times = nil
	delete(h.ff.fail, 22)
	require.NoError(t, h.run())
	require.Equal(t, []float64{22}, h.ff.times)
}

func (r *testStream) StreamURL(context.Context, string) (string, error) {
	r.calls++
	return "stream", nil
}

type captureFake struct {
	sync.Mutex
	times []float64
	fail  map[float64]error
}

func (f *captureFake) Capture(_ context.Context, _ string, t float64) ([]byte, error) {
	f.Lock()
	defer f.Unlock()
	f.times = append(f.times, t)
	return []byte("capture"), f.fail[t]
}
func (*captureFake) SceneChanges(context.Context, string, float64, float64, float64) ([]float64, error) {
	return nil, nil
}

type checkFunc func(context.Context, []VerifyInput) ([]VerifyOutput, error)

func (f checkFunc) Verify(c context.Context, in []VerifyInput) ([]VerifyOutput, error) {
	return f(c, in)
}

type faultFrame struct{ pipelinetest.NoFaults }

func (faultFrame) Check(context.Context, pipeline.FaultPoint) error {
	return pipeline.ErrInjectedFrameFailure
}

type harness struct {
	s     Stage
	ff    *captureFake
	store *pipelinetest.MemStore
	deps  pipeline.Deps
	job   pipeline.Job
}

func fixture(t *testing.T) *harness {
	t.Helper()
	store := pipelinetest.NewMemStore()
	ff := &captureFake{fail: map[float64]error{}}
	h := &harness{ff: ff, store: store, deps: pipelinetest.TestDeps(store), job: pipeline.Job{DurationSec: 100}, s: Stage{YT: &testStream{}, FF: ff, Concurrency: 1, FrameTimeout: time.Second, MaxDriftSec: 3}}
	syn := pipeline.Synthesis{}
	for _, sec := range []float64{10, 20, 30} {
		syn.Visuals = append(syn.Visuals, pipeline.Visual{TimestampSec: sec, Caption: "slide", Reason: "diagram"})
	}
	require.NoError(t, store.SaveArtifact(context.Background(), h.job.ID, "synthesis", syn))
	return h
}
func (h *harness) run() error { _, err := h.s.Run(context.Background(), &h.job, h.deps); return err }
func (h *harness) rows(t *testing.T) []pipeline.Frame {
	rows, err := h.store.ListFrames(context.Background(), h.job.ID)
	require.NoError(t, err)
	return rows
}
func TestReplayAndRateLimit(t *testing.T) {
	h := fixture(t)
	rate := &pipeline.RateLimitedError{Provider: "youtube"}
	h.ff.fail[20] = rate
	require.ErrorIs(t, h.run(), rate)
	require.Equal(t, pipeline.FrameStatus("captured"), h.rows(t)[0].Status)
	row := h.rows(t)[0]
	row.NotionFileUploadID = "uploaded"
	row.Verified = false
	require.NoError(t, h.store.UpsertFrame(context.Background(), row))
	delete(h.ff.fail, 20)
	h.ff.times = nil
	require.NoError(t, h.run())
	require.Equal(t, []float64{20, 30}, h.ff.times)
	require.Equal(t, row, h.rows(t)[0])
	for _, f := range h.rows(t) {
		require.Equal(t, pipeline.FrameStatus("captured"), f.Status)
		require.InDelta(t, f.RequestedSec, *f.CapturedSec, 3)
	}
}

// slowLimiter hands out each token after wait, like a busy shared YouTube bucket.
type slowLimiter struct{ wait time.Duration }

func (l slowLimiter) Acquire(ctx context.Context, _ string, _ int) error {
	select {
	case <-time.After(l.wait):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// hangingFF never finishes a capture until its context ends, like a stuck ffmpeg.
type hangingFF struct{}

func (hangingFF) Capture(ctx context.Context, _ string, _ float64) ([]byte, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func (hangingFF) SceneChanges(context.Context, string, float64, float64, float64) ([]float64, error) {
	return nil, nil
}

// FRAME_TIMEOUT bounds ffmpeg work only. Waiting for rate-limit tokens must not
// count: with several jobs sharing the YouTube bucket, frames timed out while
// queueing and were published as missing images.
func TestFrameTimeoutCountsOnlyFFmpeg(t *testing.T) {
	var jpegData bytes.Buffer // sharpness scoring needs a real JPEG
	require.NoError(t, jpeg.Encode(&jpegData, image.NewGray(image.Rect(0, 0, 4, 4)), nil))
	h := fixture(t)
	h.s.FF = &trackedCapture{data: jpegData.Bytes()}
	h.s.Snap, h.s.PickSharpest = true, true   // 4 tokens per frame
	h.s.FrameTimeout = 100 * time.Millisecond // less than the 4 x 40 ms of waiting
	h.deps.Limiter = slowLimiter{wait: 40 * time.Millisecond}
	require.NoError(t, h.run())
	for _, row := range h.rows(t) {
		require.Equal(t, captured, row.Status, "frame %d failed while waiting for tokens", row.Idx)
	}

	h = fixture(t)
	h.s.FF = hangingFF{}
	h.s.FrameTimeout = 50 * time.Millisecond
	require.NoError(t, h.run())
	for _, row := range h.rows(t) {
		require.Equal(t, failed, row.Status, "a hung ffmpeg call must still time out")
		require.Equal(t, pipeline.WarnFrameExtractionFailed, row.WarningCode)
	}
}

func TestCaptureFailures(t *testing.T) {
	for _, all := range []bool{false, true} {
		h := fixture(t)
		h.ff.fail[20] = errors.New("bad frame")
		if all {
			h.deps.Faults = faultFrame{}
		}
		require.NoError(t, h.run())
		for i, row := range h.rows(t) {
			if all || i == 1 {
				require.Equal(t, pipeline.FrameStatus("failed"), row.Status)
				require.Equal(t, pipeline.WarnFrameExtractionFailed, row.WarningCode)
			} else {
				require.Equal(t, pipeline.FrameStatus("captured"), row.Status)
			}
		}
		ws, err := h.store.Warnings(context.Background(), h.job.ID)
		require.NoError(t, err)
		require.NotEmpty(t, ws)
		require.NotNil(t, ws[0].TimestampSec)
	}
}
func TestVerifyBatches(t *testing.T) {
	h := fixture(t)
	h.s.Verify = true
	var batches [][]int
	h.s.Verifier = checkFunc(func(_ context.Context, in []VerifyInput) ([]VerifyOutput, error) {
		var ids []int
		var out []VerifyOutput
		for _, v := range in {
			ids = append(ids, v.Index)
			out = append(out, VerifyOutput{Index: v.Index, ShowsVisual: v.Index != 1, MatchesCaption: v.Index == 2, RevisedCaption: "new caption"})
		}
		batches = append(batches, ids)
		return out, nil
	})
	require.NoError(t, h.run())
	rows := h.rows(t)
	require.Equal(t, [][]int{{0, 1, 2}, {1}}, batches)
	require.Equal(t, "new caption", rows[0].Caption)
	require.Equal(t, pipeline.FrameStatus("omitted"), rows[1].Status)
	require.Equal(t, pipeline.WarnFrameNotRelevant, rows[1].WarningCode)
	require.Equal(t, 22., *rows[1].CapturedSec)
	require.Equal(t, "slide", rows[2].Caption)
	require.NoError(t, h.run())
	require.Len(t, batches, 2)
}
func TestVerifyFailsOpen(t *testing.T) {
	h := fixture(t)
	h.s.Verify = true
	h.s.Verifier = checkFunc(func(context.Context, []VerifyInput) ([]VerifyOutput, error) { return nil, errors.New("offline") })
	require.NoError(t, h.run())
	for _, row := range h.rows(t) {
		require.True(t, row.Verified)
		require.Equal(t, "slide", row.Caption)
	}
	ws, _ := h.store.Warnings(context.Background(), h.job.ID)
	require.Equal(t, pipeline.WarnFrameVerifySkipped, ws[0].Code)
}
