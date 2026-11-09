package jobs

import (
	"testing"
	"time"

	"ainote/internal/pipeline"

	"github.com/stretchr/testify/require"
)

func allows(from, to pipeline.Status) bool {
	for _, f := range AllowedFrom(to) {
		if f == from {
			return true
		}
	}
	return false
}

func TestAllowedTransitions(t *testing.T) {
	ok := [][2]pipeline.Status{
		{pipeline.StatusQueued, pipeline.StatusTranscribing},
		{pipeline.StatusTranscribing, pipeline.StatusSummarizing},
		{pipeline.StatusSummarizing, pipeline.StatusExtractingFrames},
		{pipeline.StatusExtractingFrames, pipeline.StatusPublishing},
		{pipeline.StatusPublishing, pipeline.StatusCompleted},
		{pipeline.StatusPublishing, pipeline.StatusCompletedWithWarnings},
	}
	for _, p := range ok {
		require.Truef(t, allows(p[0], p[1]), "%s -> %s", p[0], p[1])
	}
	for _, from := range []pipeline.Status{pipeline.StatusQueued, pipeline.StatusTranscribing,
		pipeline.StatusSummarizing, pipeline.StatusExtractingFrames, pipeline.StatusPublishing} {
		require.True(t, allows(from, pipeline.StatusFailed))
		require.True(t, allows(from, pipeline.StatusDeadLettered))
	}
	bad := [][2]pipeline.Status{
		{pipeline.StatusCompleted, pipeline.StatusSummarizing},
		{pipeline.StatusSummarizing, pipeline.StatusTranscribing},
		{pipeline.StatusQueued, pipeline.StatusPublishing},
		{pipeline.StatusCompleted, pipeline.StatusFailed},
		{pipeline.StatusFailed, pipeline.StatusDeadLettered},
		{pipeline.StatusTranscribing, pipeline.StatusQueued},
	}
	for _, p := range bad {
		require.Falsef(t, allows(p[0], p[1]), "%s -> %s", p[0], p[1])
	}
}

func TestRetryDelay(t *testing.T) {
	d := []time.Duration{10 * time.Second, 30 * time.Second, 90 * time.Second, 270 * time.Second}
	require.Equal(t, 10*time.Second, RetryDelay(d, 1, 0))
	require.Equal(t, 270*time.Second, RetryDelay(d, 4, 0))
	require.Equal(t, 270*time.Second, RetryDelay(d, 7, 0))
	require.Equal(t, 45*time.Second, RetryDelay(d, 1, 45*time.Second))
	require.Equal(t, time.Duration(0), RetryDelay(nil, 3, 0))
}
