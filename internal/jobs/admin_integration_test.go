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

func TestListDeadLettered(t *testing.T) {
	ctx := context.Background()
	r, id := newJobIn(t, pipeline.StatusSummarizing)
	other := uuid.New()
	_, _, err := r.CreateJob(ctx, NewJob{ID: other, VideoID: "o", YouTubeURL: "https://youtu.be/o"}, []byte("x"))
	require.NoError(t, err)

	require.NoError(t, r.Fail(ctx, id, pipeline.StatusDeadLettered, "RETRIES_EXHAUSTED", "boom"))

	got, err := r.ListDeadLettered(ctx, 10)
	require.NoError(t, err)
	require.Len(t, got, 1)
	e := got[0]
	require.Equal(t, id, e.JobID)
	require.Equal(t, pipeline.StatusDeadLettered, e.Status)
	require.NotNil(t, e.Error)
	require.Equal(t, "RETRIES_EXHAUSTED", e.Error.Code)
	require.NotEmpty(t, e.Events)
	last := e.Events[len(e.Events)-1]
	require.Equal(t, "transition", last.Type)
	require.Equal(t, "SUMMARIZING", *last.FromStatus)
	require.Equal(t, "DEAD_LETTERED", *last.ToStatus)
	require.JSONEq(t, `{"code":"RETRIES_EXHAUSTED","message":"boom"}`, string(last.Detail))
}

func TestResetForReplay(t *testing.T) {
	ctx := context.Background()
	r, id := newJobIn(t, pipeline.StatusSummarizing)
	_, err := r.pool.Exec(ctx, `UPDATE jobs SET attempts=3, rate_limit_retries=2, deferrals=4, lease_owner='w1',
		lease_until=now()+interval '1 minute' WHERE id=$1`, id)
	require.NoError(t, err)
	require.NoError(t, r.Fail(ctx, id, pipeline.StatusDeadLettered, "RETRIES_EXHAUSTED", "boom"))

	job, err := r.ResetForReplay(ctx, id, []byte("replay-payload"))
	require.NoError(t, err)
	require.Equal(t, pipeline.StatusSummarizing, job.Status)
	require.Equal(t, 0, job.Attempts)

	v, err := r.GetJobView(ctx, id)
	require.NoError(t, err)
	require.Equal(t, pipeline.StatusSummarizing, v.Status)
	require.False(t, v.Status.IsTerminal())
	require.Nil(t, v.Error)
	require.Equal(t, 20, v.Progress)

	var rl, def int
	var fin *time.Time
	var owner *string
	require.NoError(t, r.pool.QueryRow(ctx, `SELECT rate_limit_retries, deferrals, finished_at, lease_owner FROM jobs WHERE id=$1`, id).
		Scan(&rl, &def, &fin, &owner))
	require.Equal(t, 0, rl)
	require.Equal(t, 0, def)
	require.Nil(t, fin)
	require.Nil(t, owner)

	require.Equal(t, 1, count(t, r, `SELECT count(*) FROM outbox WHERE job_id=$1 AND published_at IS NULL AND payload='\x7265706c61792d7061796c6f6164'::bytea`, id))
	require.Equal(t, 1, count(t, r, `SELECT count(*) FROM job_events WHERE job_id=$1 AND type='replay'`, id))

	// A second replay is refused: the job is no longer dead-lettered.
	_, err = r.ResetForReplay(ctx, id, []byte("again"))
	require.ErrorIs(t, err, ErrNotDeadLettered)
	_, err = r.ResetForReplay(ctx, uuid.New(), []byte("x"))
	require.ErrorIs(t, err, ErrNotFound)
}

func TestResetForReplayDefaultsToQueued(t *testing.T) {
	ctx := context.Background()
	r, id := newJobIn(t, pipeline.StatusDeadLettered) // no transition event
	job, err := r.ResetForReplay(ctx, id, []byte("p"))
	require.NoError(t, err)
	require.Equal(t, pipeline.StatusQueued, job.Status)
}

func insertFrame(t *testing.T, r *Repo, id uuid.UUID, idx int) {
	t.Helper()
	_, err := r.pool.Exec(context.Background(), `INSERT INTO job_frames
		(job_id, idx, requested_sec, captured_sec, section_index, caption, reason, status, verified, image_jpeg)
		VALUES ($1,$2,1,1,0,'cap','why','captured',true,'\xffd8ffe0')`, id, idx)
	require.NoError(t, err)
}

func TestPurgeFrameImages(t *testing.T) {
	ctx := context.Background()
	r, pool := newTestRepo(t)
	mk := func(status string, finishedAgo *time.Duration) uuid.UUID {
		id := uuid.New()
		_, _, err := r.CreateJob(ctx, NewJob{ID: id, VideoID: "v", YouTubeURL: "https://youtu.be/v"}, []byte("x"))
		require.NoError(t, err)
		if finishedAgo != nil {
			_, err = pool.Exec(ctx, `UPDATE jobs SET status=$2, finished_at=now()-make_interval(secs=>$3) WHERE id=$1`,
				id, status, finishedAgo.Seconds())
		} else {
			_, err = pool.Exec(ctx, `UPDATE jobs SET status=$2 WHERE id=$1`, id, status)
		}
		require.NoError(t, err)
		insertFrame(t, r, id, 0)
		insertFrame(t, r, id, 1)
		return id
	}
	old, recent := 25*time.Hour, time.Hour
	oldDone := mk("COMPLETED", &old)
	oldFailed := mk("FAILED", &old)
	recentDone := mk("COMPLETED", &recent)
	running := mk("SUMMARIZING", nil)

	n, err := r.PurgeFrameImages(ctx, 24*time.Hour)
	require.NoError(t, err)
	require.EqualValues(t, 4, n)

	withBytes := func(id uuid.UUID) int {
		return count(t, r, `SELECT count(*) FROM job_frames WHERE job_id=$1 AND image_jpeg IS NOT NULL`, id)
	}
	require.Equal(t, 0, withBytes(oldDone))
	require.Equal(t, 0, withBytes(oldFailed))
	require.Equal(t, 2, withBytes(recentDone))
	require.Equal(t, 2, withBytes(running))
	// metadata kept
	require.Equal(t, 2, count(t, r, `SELECT count(*) FROM job_frames WHERE job_id=$1 AND caption='cap' AND status='captured'`, oldDone))

	// idempotent
	n, err = r.PurgeFrameImages(ctx, 24*time.Hour)
	require.NoError(t, err)
	require.EqualValues(t, 0, n)
}

func TestReplayRecapturesPurgedFrames(t *testing.T) {
	ctx := context.Background()
	r, id := newJobIn(t, pipeline.StatusPublishing)
	insertFrame(t, r, id, 0)
	insertFrame(t, r, id, 1)
	insertFrame(t, r, id, 2)
	// Frame 2 already reached Notion: its bytes are no longer needed.
	_, err := r.pool.Exec(ctx, `UPDATE job_frames SET notion_file_upload_id='up_2', notion_uploaded_at=now() WHERE job_id=$1 AND idx=2`, id)
	require.NoError(t, err)

	require.NoError(t, r.Fail(ctx, id, pipeline.StatusDeadLettered, "RETRIES_EXHAUSTED", "notion limit"))
	_, err = r.pool.Exec(ctx, `UPDATE jobs SET finished_at = now() - interval '25 hours' WHERE id=$1`, id)
	require.NoError(t, err)
	n, err := r.PurgeFrameImages(ctx, 24*time.Hour)
	require.NoError(t, err)
	require.EqualValues(t, 3, n)

	job, err := r.ResetForReplay(ctx, id, []byte("p"))
	require.NoError(t, err)
	require.Equal(t, pipeline.StatusExtractingFrames, job.Status, "purged frames force a re-run of frame extraction")

	status := func(idx int) string {
		var s string
		require.NoError(t, r.pool.QueryRow(ctx, `SELECT status FROM job_frames WHERE job_id=$1 AND idx=$2`, id, idx).Scan(&s))
		return s
	}
	require.Equal(t, "pending", status(0))
	require.Equal(t, "pending", status(1))
	require.Equal(t, "captured", status(2), "uploaded frame is left alone")
	require.Equal(t, 0, count(t, r, `SELECT count(*) FROM job_frames WHERE job_id=$1 AND idx<2 AND (captured_sec IS NOT NULL OR verified)`, id))
	require.Equal(t, 3, count(t, r, `SELECT count(*) FROM job_frames WHERE job_id=$1 AND caption='cap'`, id), "metadata kept")
}

func TestReplayKeepsStageWhenFramesIntact(t *testing.T) {
	ctx := context.Background()
	r, id := newJobIn(t, pipeline.StatusPublishing)
	insertFrame(t, r, id, 0)
	require.NoError(t, r.Fail(ctx, id, pipeline.StatusDeadLettered, "RETRIES_EXHAUSTED", "x"))
	job, err := r.ResetForReplay(ctx, id, []byte("p"))
	require.NoError(t, err)
	require.Equal(t, pipeline.StatusPublishing, job.Status)
	require.Equal(t, 1, count(t, r, `SELECT count(*) FROM job_frames WHERE job_id=$1 AND status='captured' AND image_jpeg IS NOT NULL`, id))
}

func TestLastReplayAt(t *testing.T) {
	ctx := context.Background()
	r, id := newJobIn(t, pipeline.StatusSummarizing)
	_, ok, err := r.LastReplayAt(ctx, id)
	require.NoError(t, err)
	require.False(t, ok)
	require.NoError(t, r.Fail(ctx, id, pipeline.StatusDeadLettered, "RETRIES_EXHAUSTED", "x"))
	_, err = r.ResetForReplay(ctx, id, []byte("p"))
	require.NoError(t, err)
	at, ok, err := r.LastReplayAt(ctx, id)
	require.NoError(t, err)
	require.True(t, ok)
	require.WithinDuration(t, time.Now(), at, time.Minute)
}
