package jobs

import (
	"testing"

	"ainote/internal/pipeline"

	"github.com/stretchr/testify/require"
)

func TestGlobalProgress(t *testing.T) {
	require.Equal(t, 0, GlobalProgress(pipeline.StatusQueued, 50))
	require.Equal(t, 5, GlobalProgress(pipeline.StatusTranscribing, 0))
	require.Equal(t, 20, GlobalProgress(pipeline.StatusTranscribing, 100))
	require.Equal(t, 20, GlobalProgress(pipeline.StatusSummarizing, 0))
	require.Equal(t, 50, GlobalProgress(pipeline.StatusSummarizing, 100))
	require.Equal(t, 65, GlobalProgress(pipeline.StatusExtractingFrames, 50))
	require.Equal(t, 80, GlobalProgress(pipeline.StatusPublishing, 0))
	require.Equal(t, 99, GlobalProgress(pipeline.StatusPublishing, 100))
	require.Equal(t, 99, GlobalProgress(pipeline.StatusPublishing, 500))
	require.Equal(t, 80, GlobalProgress(pipeline.StatusPublishing, -5))
	for _, s := range []pipeline.Status{pipeline.StatusCompleted, pipeline.StatusCompletedWithWarnings,
		pipeline.StatusFailed, pipeline.StatusDeadLettered} {
		require.Equal(t, 100, GlobalProgress(s, 0))
	}
}
