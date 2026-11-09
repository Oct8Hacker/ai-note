//go:build integration

package jobs

import (
	"context"
	"testing"
	"time"

	"ainote/internal/pipeline"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// newJobIn creates a job and forces it into status (test setup only).
func newJobIn(t *testing.T, status pipeline.Status) (*Repo, uuid.UUID) {
	t.Helper()
	r, pool := newTestRepo(t)
	id := uuid.New()
	_, _, err := r.CreateJob(context.Background(), NewJob{ID: id, VideoID: "v", YouTubeURL: "https://youtu.be/v"}, []byte("x"))
	require.NoError(t, err)
	_, err = pool.Exec(context.Background(), `UPDATE jobs SET status=$2 WHERE id=$1`, id, string(status))
	require.NoError(t, err)
	return r, id
}

func count(t *testing.T, r *Repo, q string, args ...any) int {
	t.Helper()
	var n int
	require.NoError(t, r.pool.QueryRow(context.Background(), q, args...).Scan(&n))
	return n
}

func TestCompleteStageAtomic(t *testing.T) {
	ctx := context.Background()
	r, id := newJobIn(t, pipeline.StatusTranscribing)
	title, dur := "T", 120
	next, err := r.CompleteStage(ctx, id, pipeline.StatusTranscribing, pipeline.StageResult{
		ArtifactKind: "transcript", Artifact: pipeline.Transcript{Language: "en"},
		VideoTitle: &title, DurationSec: &dur}, "w1")
	require.NoError(t, err)
	require.Equal(t, pipeline.StatusSummarizing, next)
	j, err := r.GetJob(ctx, id)
	require.NoError(t, err)
	require.Equal(t, pipeline.StatusSummarizing, j.Status)
	require.Equal(t, "T", j.VideoTitle)
	require.Equal(t, 120, j.DurationSec)
	v, err := r.GetJobView(ctx, id)
	require.NoError(t, err)
	require.Equal(t, 20, v.Progress)
	require.Equal(t, 1, count(t, r, `SELECT count(*) FROM job_artifacts WHERE job_id=$1 AND kind='transcript'`, id))
	require.Equal(t, 1, count(t, r, `SELECT count(*) FROM job_events WHERE job_id=$1 AND type='transition' AND to_status='SUMMARIZING'`, id))

	// stale cur: rejected and artifact unchanged
	_, err = r.CompleteStage(ctx, id, pipeline.StatusTranscribing, pipeline.StageResult{
		ArtifactKind: "transcript", Artifact: pipeline.Transcript{Language: "fr"}}, "w1")
	require.ErrorIs(t, err, ErrIllegalTransition)
	var lang string
	require.NoError(t, r.pool.QueryRow(ctx, `SELECT data->>'language' FROM job_artifacts WHERE job_id=$1 AND kind='transcript'`, id).Scan(&lang))
	require.Equal(t, "en", lang)
	// a brand-new artifact kind from a stale call must not be saved either
	_, err = r.CompleteStage(ctx, id, pipeline.StatusTranscribing, pipeline.StageResult{
		ArtifactKind: "synthesis", Artifact: map[string]any{}}, "w1")
	require.ErrorIs(t, err, ErrIllegalTransition)
	require.Equal(t, 0, count(t, r, `SELECT count(*) FROM job_artifacts WHERE job_id=$1 AND kind='synthesis'`, id))
}

func TestPublishCompletesWithWarnings(t *testing.T) {
	ctx := context.Background()
	r, id := newJobIn(t, pipeline.StatusPublishing)
	ts := 12.5
	require.NoError(t, r.AddWarning(ctx, id, pipeline.Warning{Code: pipeline.WarnFrameNotRelevant, TimestampSec: &ts}))
	n := 3
	next, err := r.CompleteStage(ctx, id, pipeline.StatusPublishing, pipeline.StageResult{
		FramesRequested: &n, FramesPublished: &n, PageURL: "https://notion.so/p"}, "w")
	require.NoError(t, err)
	require.Equal(t, pipeline.StatusCompletedWithWarnings, next)
	v, err := r.GetJobView(ctx, id)
	require.NoError(t, err)
	require.Equal(t, 100, v.Progress)
	require.Equal(t, "https://notion.so/p", v.Result.NotionPageURL)
	require.Equal(t, 3, v.Result.FramesPublished)
	require.Equal(t, 1, count(t, r, `SELECT count(*) FROM jobs WHERE id=$1 AND finished_at IS NOT NULL`, id))
}

func TestPublishCompletesClean(t *testing.T) {
	ctx := context.Background()
	r, id := newJobIn(t, pipeline.StatusPublishing)
	next, err := r.CompleteStage(ctx, id, pipeline.StatusPublishing, pipeline.StageResult{}, "w")
	require.NoError(t, err)
	require.Equal(t, pipeline.StatusCompleted, next)
	v, _ := r.GetJobView(ctx, id)
	require.Equal(t, 100, v.Progress)
}

func TestTransition(t *testing.T) {
	ctx := context.Background()
	r, id := newJobIn(t, pipeline.StatusQueued)
	require.ErrorIs(t, r.Transition(ctx, id, pipeline.StatusPublishing, "w", nil), ErrIllegalTransition)
	require.NoError(t, r.Transition(ctx, id, pipeline.StatusTranscribing, "w", map[string]string{"a": "b"}))
	require.Equal(t, 1, count(t, r, `SELECT count(*) FROM job_events WHERE job_id=$1 AND from_status='QUEUED' AND to_status='TRANSCRIBING' AND worker_id='w'`, id))
	require.NoError(t, r.Transition(ctx, id, pipeline.StatusDeadLettered, "w", nil))
	require.ErrorIs(t, r.Transition(ctx, id, pipeline.StatusFailed, "w", nil), ErrIllegalTransition)
}

func TestProgressMonotonic(t *testing.T) {
	ctx := context.Background()
	r, id := newJobIn(t, pipeline.StatusExtractingFrames)
	require.NoError(t, r.SetProgress(ctx, id, pipeline.StatusExtractingFrames, 33))
	v, _ := r.GetJobView(ctx, id)
	require.Equal(t, 59, v.Progress)
	require.NoError(t, r.SetProgress(ctx, id, pipeline.StatusExtractingFrames, 10))
	v, _ = r.GetJobView(ctx, id)
	require.Equal(t, 59, v.Progress)
}

func TestAddWarningDedupes(t *testing.T) {
	ctx := context.Background()
	r, id := newJobIn(t, pipeline.StatusExtractingFrames)
	a, b := 1.5, 2.5
	for i := 0; i < 3; i++ {
		require.NoError(t, r.AddWarning(ctx, id, pipeline.Warning{Code: "X", TimestampSec: &a}))
	}
	require.NoError(t, r.AddWarning(ctx, id, pipeline.Warning{Code: "X", TimestampSec: &b}))
	require.NoError(t, r.AddWarning(ctx, id, pipeline.Warning{Code: "X"}))
	require.NoError(t, r.AddWarning(ctx, id, pipeline.Warning{Code: "X"}))
	w, err := r.Checkpoints().Warnings(ctx, id)
	require.NoError(t, err)
	require.Len(t, w, 3)
}

func TestScheduleRetryBudget(t *testing.T) {
	ctx := context.Background()
	r, id := newJobIn(t, pipeline.StatusSummarizing)
	delays := []time.Duration{10 * time.Second, 30 * time.Second, 90 * time.Second, 270 * time.Second}
	for i, w := range delays {
		d, err := r.ScheduleRetry(ctx, id, RetryFailure, "", delays, 0, 5, "UPSTREAM_ERROR")
		require.NoError(t, err)
		require.False(t, d.Exhausted)
		require.Equal(t, i+1, d.N)
		require.Equal(t, w, d.Delay)
	}
	require.Equal(t, 1, count(t, r, `SELECT count(*) FROM jobs WHERE id=$1 AND next_attempt_at > now() AND waiting_reason='RETRYING'`, id))
	// independent rate-limit and deferral counters
	d, err := r.ScheduleRetry(ctx, id, RetryRateLimit, "llm", delays, 45*time.Second, 5, "")
	require.NoError(t, err)
	require.False(t, d.Exhausted)
	require.Equal(t, 1, d.N)
	require.Equal(t, 45*time.Second, d.Delay)
	require.Equal(t, 1, count(t, r, `SELECT count(*) FROM jobs WHERE id=$1 AND waiting_reason='RATE_LIMITED' AND waiting_provider='llm'`, id))
	d, err = r.ScheduleRetry(ctx, id, RetryDefer, "", []time.Duration{time.Second}, 0, 5, "")
	require.NoError(t, err)
	require.Equal(t, 1, d.N)
	require.Equal(t, 1, count(t, r, `SELECT count(*) FROM jobs WHERE id=$1 AND waiting_reason='BUSY'`, id))

	d, err = r.ScheduleRetry(ctx, id, RetryFailure, "", delays, 0, 5, "UPSTREAM_ERROR")
	require.NoError(t, err)
	require.True(t, d.Exhausted)
	require.Equal(t, 5, d.N)
	require.Equal(t, 1, count(t, r, `SELECT count(*) FROM job_events WHERE job_id=$1 AND type='retry_exhausted'`, id))
	require.Equal(t, 6, count(t, r, `SELECT count(*) FROM job_events WHERE job_id=$1 AND type='retry_scheduled'`, id))
	j, _ := r.GetJob(ctx, id)
	require.Equal(t, pipeline.StatusSummarizing, j.Status)
	require.Equal(t, 5, j.Attempts)
}

func TestFailFromTerminalRejected(t *testing.T) {
	ctx := context.Background()
	r, id := newJobIn(t, pipeline.StatusSummarizing)
	require.Error(t, r.Fail(ctx, id, pipeline.StatusCompleted, "X", "m"))
	require.NoError(t, r.Fail(ctx, id, pipeline.StatusFailed, "VIDEO_UNAVAILABLE", "gone"))
	v, err := r.GetJobView(ctx, id)
	require.NoError(t, err)
	require.Equal(t, pipeline.StatusFailed, v.Status)
	require.Equal(t, 100, v.Progress)
	require.Equal(t, "VIDEO_UNAVAILABLE", v.Error.Code)
	require.ErrorIs(t, r.Fail(ctx, id, pipeline.StatusDeadLettered, "X", "m"), ErrIllegalTransition)
	require.ErrorIs(t, r.Fail(ctx, id, pipeline.StatusFailed, "X", "m"), ErrIllegalTransition)
}
