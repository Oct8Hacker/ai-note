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

func TestCancelJob(t *testing.T) {
	ctx := context.Background()
	r, id := newJobIn(t, pipeline.StatusSummarizing)
	_, outcome, err := r.Claim(ctx, id, "worker", time.Minute, nil)
	require.NoError(t, err)
	require.Equal(t, Claimed, outcome)
	_, err = r.ScheduleRetry(ctx, id, RetryFailure, "", []time.Duration{time.Hour}, 0, 3, "UPSTREAM_ERROR")
	require.NoError(t, err)
	before, err := r.GetJobView(ctx, id)
	require.NoError(t, err)
	require.True(t, before.CanCancel)
	require.NoError(t, r.CancelJob(ctx, id))
	require.NoError(t, r.CancelJob(ctx, id))
	require.ErrorIs(t, r.Checkpoints().MarkNotionCreateStarted(ctx, id), ErrIllegalTransition)
	view, err := r.GetJobView(ctx, id)
	require.NoError(t, err)
	require.Equal(t, pipeline.StatusCancelled, view.Status)
	require.False(t, view.CanCancel)
	require.Nil(t, view.Result)
	require.Nil(t, view.Waiting)
	require.Nil(t, view.Error)
	require.Equal(t, 1, count(t, r, `SELECT count(*) FROM job_events WHERE job_id=$1 AND from_status='SUMMARIZING' AND to_status='CANCELLED'`, id))
	require.Equal(t, 1, count(t, r, `SELECT count(*) FROM jobs WHERE id=$1 AND finished_at IS NOT NULL AND lease_owner IS NULL AND lease_until IS NULL AND next_attempt_at IS NULL`, id))
	require.ErrorIs(t, r.RenewLease(ctx, id, "worker", time.Minute), ErrLeaseLost)
	_, outcome, err = r.Claim(ctx, id, "other", time.Minute, nil)
	require.NoError(t, err)
	require.Equal(t, Terminal, outcome)
	_, err = r.ScheduleRetry(ctx, id, RetryFailure, "", nil, 0, 3, "")
	require.ErrorIs(t, err, ErrIllegalTransition)
	require.ErrorIs(t, r.Fail(ctx, id, pipeline.StatusDeadLettered, "X", ""), ErrIllegalTransition)
	_, err = r.CompleteStage(ctx, id, pipeline.StatusSummarizing, pipeline.StageResult{}, "worker")
	require.ErrorIs(t, err, ErrIllegalTransition)
	_, err = r.ResetForReplay(ctx, id, []byte("message"))
	require.ErrorIs(t, err, ErrNotDeadLettered)
	require.ErrorIs(t, r.CancelJob(ctx, uuid.New()), ErrNotFound)
}

func TestCancelEligibility(t *testing.T) {
	ctx := context.Background()
	r, id := newJobIn(t, pipeline.StatusPublishing)
	for _, status := range append(append([]pipeline.Status{}, nonTerminal...), terminalStatuses...) {
		_, err := r.pool.Exec(ctx, `UPDATE jobs SET status=$2 WHERE id=$1`, id, string(status))
		require.NoError(t, err)
		view, err := r.GetJobView(ctx, id)
		require.NoError(t, err)
		require.Equal(t, !status.IsTerminal(), view.CanCancel, status)
		err = r.CancelJob(ctx, id)
		if status.IsTerminal() && status != pipeline.StatusCancelled {
			require.ErrorIs(t, err, ErrCancelTooLate, status)
		} else {
			require.NoError(t, err, status)
		}
	}
	_, err := r.pool.Exec(ctx, `UPDATE jobs SET status='PUBLISHING' WHERE id=$1`, id)
	require.NoError(t, err)
	require.NoError(t, r.Checkpoints().MarkNotionCreateStarted(ctx, id))
	require.ErrorIs(t, r.CancelJob(ctx, id), ErrCancelTooLate)
	view, err := r.GetJobView(ctx, id)
	require.NoError(t, err)
	require.False(t, view.CanCancel)

	for _, page := range [][2]string{{"id", ""}, {"", "url"}} {
		_, err := r.pool.Exec(ctx, `UPDATE jobs SET notion_create_started_at=NULL, notion_page_id=$2, notion_page_url=$3 WHERE id=$1`, id, page[0], page[1])
		require.NoError(t, err)
		view, err := r.GetJobView(ctx, id)
		require.NoError(t, err)
		require.False(t, view.CanCancel)
		require.ErrorIs(t, r.CancelJob(ctx, id), ErrCancelTooLate)
	}
}

func TestCancelRacesStartOfPageCreation(t *testing.T) {
	ctx := context.Background()
	r, id := newJobIn(t, pipeline.StatusPublishing)
	start := make(chan struct{})
	marked := make(chan error, 1)
	go func() {
		<-start
		marked <- r.Checkpoints().MarkNotionCreateStarted(ctx, id)
	}()
	close(start)
	cancelErr := r.CancelJob(ctx, id)
	markErr := <-marked
	view, err := r.GetJobView(ctx, id)
	require.NoError(t, err)
	require.False(t, view.CanCancel)
	job, err := r.GetJob(ctx, id)
	require.NoError(t, err)
	if cancelErr == nil {
		require.ErrorIs(t, markErr, ErrIllegalTransition)
		require.Equal(t, pipeline.StatusCancelled, job.Status)
		require.Nil(t, job.NotionCreateStartedAt)
	} else {
		require.ErrorIs(t, cancelErr, ErrCancelTooLate)
		require.NoError(t, markErr)
		require.Equal(t, pipeline.StatusPublishing, job.Status)
		require.NotNil(t, job.NotionCreateStartedAt)
	}
}

func TestCancelledJobRejectsStaleCheckpoints(t *testing.T) {
	ctx := context.Background()
	r, id := newJobIn(t, pipeline.StatusPublishing)
	s := r.Checkpoints()
	require.NoError(t, s.SetVideoMeta(ctx, id, "Original", 60))
	require.NoError(t, s.SetNotionParent(ctx, id, "parent", "page", "title"))
	require.NoError(t, s.SaveArtifact(ctx, id, "chunk", "original"))
	frame := pipeline.Frame{JobID: id, Status: "pending", Caption: "original"}
	require.NoError(t, s.UpsertFrame(ctx, frame))
	require.NoError(t, r.CancelJob(ctx, id))

	require.ErrorIs(t, s.SetVideoMeta(ctx, id, "Late", 100), ErrIllegalTransition)
	require.ErrorIs(t, s.SetNotionParent(ctx, id, "late", "page", "title"), ErrIllegalTransition)
	require.ErrorIs(t, s.SetNotionPage(ctx, id, "late", "url"), ErrIllegalTransition)
	require.ErrorIs(t, s.SaveArtifact(ctx, id, "chunk", "late"), ErrIllegalTransition)
	frame.Caption = "late"
	require.ErrorIs(t, s.UpsertFrame(ctx, frame), ErrIllegalTransition)

	job, err := r.GetJob(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "Original", job.VideoTitle)
	require.Equal(t, 60, job.DurationSec)
	require.Equal(t, "parent", job.NotionResolvedParent)
	require.Empty(t, job.NotionPageID)
	var chunk string
	found, err := s.LoadArtifact(ctx, id, "chunk", &chunk)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "original", chunk)
	frames, err := s.ListFrames(ctx, id)
	require.NoError(t, err)
	require.Len(t, frames, 1)
	require.Equal(t, "original", frames[0].Caption)
}
