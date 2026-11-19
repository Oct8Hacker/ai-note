package frames

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"ainote/internal/pipeline"
	"github.com/stretchr/testify/require"
)

func TestChooseTime(t *testing.T) {
	for _, tc := range []struct {
		changes     []float64
		drift, want float64
	}{
		{[]float64{98.7, 104}, 3, 99.2}, {[]float64{90}, 3, 100}, {nil, 3, 100}, {[]float64{98.7}, 0.4, 100}, {[]float64{98, 100}, 3, 100.5},
	} {
		require.InDelta(t, tc.want, ChooseTime(100, tc.changes, tc.drift), 0.0001)
	}
}
func TestClampDrift(t *testing.T) {
	require.Equal(t, 97., ClampDrift(100, 94, 3))
	require.Equal(t, 103., ClampDrift(100, 104, 3))
	require.Equal(t, 101., ClampDrift(100, 101, 3))
	require.Zero(t, ClampDrift(1, -1, 3))
}

type argsRunner struct{ calls [][]string }

func (r *argsRunner) Run(_ context.Context, name string, args ...string) ([]byte, []byte, error) {
	r.calls = append(r.calls, append([]string{name}, args...))
	return []byte("jpeg"), nil, nil
}

// YouTube's video servers refuse ffmpeg's default "Lavf" agent for some
// videos, so every stream read sends a browser User-Agent before -i.
func TestFFmpegSendsBrowserUserAgent(t *testing.T) {
	runner := &argsRunner{}
	ff := NewFFmpeg(runner)
	_, err := ff.Capture(context.Background(), "https://stream", 12)
	require.NoError(t, err)
	_, err = ff.SceneChanges(context.Background(), "https://stream", 10, 14, 0.3)
	require.NoError(t, err)
	require.Len(t, runner.calls, 2)
	for _, args := range runner.calls {
		ua := slices.Index(args, "-user_agent")
		require.GreaterOrEqual(t, ua, 0, "missing -user_agent in %v", args)
		require.Equal(t, streamUserAgent, args[ua+1])
		require.Less(t, ua, slices.Index(args, "-i"), "-user_agent must come before -i")
	}

	// -user_agent is an HTTP input option; ffmpeg rejects it for a local file.
	runner.calls = nil
	_, err = ff.Capture(context.Background(), "/tmp/video.mp4", 12)
	require.NoError(t, err)
	require.NotContains(t, runner.calls[0], "-user_agent")
}

func TestClassifyFFmpeg(t *testing.T) {
	var rate *pipeline.RateLimitedError
	require.ErrorAs(t, classifyFFmpeg("Server returned 429", errors.New("exit 1")), &rate)
	require.Equal(t, "youtube", rate.Provider)
	require.Equal(t, 30*time.Second, rate.RetryAfter)

	// 403 is a refused URL, not throttling: the stage retries with a fresh URL.
	forbidden := classifyFFmpeg("Server returned 403 Forbidden (access denied)", errors.New("exit 1"))
	require.ErrorIs(t, forbidden, errStreamForbidden)
	require.False(t, errors.As(forbidden, &rate), "a 403 must not be treated as a rate limit")
	require.Nil(t, classifyFFmpeg("", nil))
	require.Equal(t, "FRAME_CAPTURE_FAILED", pipeline.ErrorCode(classifyFFmpeg("bad stream", errors.New("exit 1"))))
}
