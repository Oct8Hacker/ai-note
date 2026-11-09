//go:build integration

package jobs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func seedOutbox(t *testing.T, r *Repo, n int) []uuid.UUID {
	t.Helper()
	ids := make([]uuid.UUID, n)
	for i := range ids {
		ids[i] = uuid.New()
		_, created, err := r.CreateJob(context.Background(), NewJob{ID: ids[i], VideoID: "v", YouTubeURL: "u"}, []byte(`{}`))
		require.NoError(t, err)
		require.True(t, created)
	}
	return ids
}

func TestRelayBatchMarkFailureKeepsEarlierMarks(t *testing.T) {
	r, pool := newTestRepo(t)
	ids := seedOutbox(t, r, 5)
	ctx := context.Background()

	var failID int64
	require.NoError(t, pool.QueryRow(ctx, `SELECT id FROM outbox WHERE job_id=$1`, ids[2]).Scan(&failID))
	markFault = func(id int64) error {
		if id == failID {
			return errors.New("injected mark failure")
		}
		return nil
	}
	t.Cleanup(func() { markFault = nil })

	var published []uuid.UUID
	n, err := r.RelayBatch(ctx, 100, func(_ context.Context, row OutboxRow) error {
		published = append(published, row.JobID)
		return nil
	})
	require.Error(t, err)
	require.Equal(t, 2, n)
	require.Equal(t, ids[:3], published) // third was published but its mark failed

	markFault = nil
	published = nil
	n, err = r.RelayBatch(ctx, 100, func(_ context.Context, row OutboxRow) error {
		published = append(published, row.JobID)
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, 3, n)
	require.Equal(t, ids[2:], published, "only rows from the failed mark onward are republished")
}

func TestRelayBatchSlowPublishStillCommits(t *testing.T) {
	r, pool := newTestRepo(t)
	ids := seedOutbox(t, r, 2)
	old := finishTimeout
	finishTimeout = 200 * time.Millisecond
	t.Cleanup(func() { finishTimeout = old })

	n, err := r.RelayBatch(context.Background(), 100, func(_ context.Context, _ OutboxRow) error {
		time.Sleep(300 * time.Millisecond)
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, 2, n)
	var cnt int
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT count(*) FROM outbox WHERE published_at IS NOT NULL AND job_id = ANY($1)`, ids).Scan(&cnt))
	require.Equal(t, 2, cnt)
}
