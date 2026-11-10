package frames

import (
	"errors"
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
func TestClassifyFFmpeg(t *testing.T) {
	for _, msg := range []string{"Server returned 429", "HTTP error 403 Forbidden"} {
		var rate *pipeline.RateLimitedError
		require.ErrorAs(t, classifyFFmpeg(msg, errors.New("exit 1")), &rate)
		require.Equal(t, "youtube", rate.Provider)
		require.Equal(t, 30*time.Second, rate.RetryAfter)
	}
	require.Nil(t, classifyFFmpeg("", nil))
	require.Equal(t, "FRAME_CAPTURE_FAILED", pipeline.ErrorCode(classifyFFmpeg("bad stream", errors.New("exit 1"))))
}
