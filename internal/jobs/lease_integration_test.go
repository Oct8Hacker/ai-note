//go:build integration

package jobs

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ainote/internal/pipeline"

	"github.com/stretchr/testify/require"
)

func alive(v bool) func(string) (bool, error) {
	return func(string) (bool, error) { return v, nil }
}

func leaseOf(t *testing.T, r *Repo, id any) (owner *string) {
	t.Helper()
	require.NoError(t, r.pool.QueryRow(context.Background(), `SELECT lease_owner FROM jobs WHERE id=$1`, id).Scan(&owner))
	return owner
}

func TestClaimFresh(t *testing.T) {
	r, id := newJobIn(t, pipeline.StatusQueued)
	j, out, err := r.Claim(context.Background(), id, "w1", time.Minute, alive(true))
	require.NoError(t, err)
	require.Equal(t, Claimed, out)
	require.Equal(t, id, j.ID)
	require.Equal(t, "w1", *leaseOf(t, r, id))
}

func TestClaimOwnJobRenews(t *testing.T) {
	r, id := newJobIn(t, pipeline.StatusQueued)
	ctx := context.Background()
	_, out, err := r.Claim(ctx, id, "w1", time.Minute, alive(true))
	require.NoError(t, err)
	require.Equal(t, Claimed, out)
	_, out, err = r.Claim(ctx, id, "w1", time.Minute, alive(true))
	require.NoError(t, err)
	require.Equal(t, Claimed, out)
}

func TestClaimBusyLiveOwner(t *testing.T) {
	r, id := newJobIn(t, pipeline.StatusQueued)
	ctx := context.Background()
	_, _, err := r.Claim(ctx, id, "w1", time.Minute, alive(true))
	require.NoError(t, err)
	_, out, err := r.Claim(ctx, id, "w2", time.Minute, alive(true))
	require.NoError(t, err)
	require.Equal(t, Busy, out)
	require.Equal(t, "w1", *leaseOf(t, r, id))
}

func TestClaimDeadOwnerImmediately(t *testing.T) {
	r, id := newJobIn(t, pipeline.StatusQueued)
	ctx := context.Background()
	_, _, err := r.Claim(ctx, id, "w1", time.Minute, alive(true))
	require.NoError(t, err)
	_, out, err := r.Claim(ctx, id, "w2", time.Minute, alive(false))
	require.NoError(t, err)
	require.Equal(t, Claimed, out)
	require.Equal(t, "w2", *leaseOf(t, r, id))
}

func TestClaimExpiredLease(t *testing.T) {
	r, id := newJobIn(t, pipeline.StatusQueued)
	ctx := context.Background()
	_, _, err := r.Claim(ctx, id, "w1", time.Minute, alive(true))
	require.NoError(t, err)
	_, err = r.pool.Exec(ctx, `UPDATE jobs SET lease_until = now() - interval '1 second' WHERE id=$1`, id)
	require.NoError(t, err)
	_, out, err := r.Claim(ctx, id, "w2", time.Minute, alive(true))
	require.NoError(t, err)
	require.Equal(t, Claimed, out)
}

func TestClaimTerminalAndNotFound(t *testing.T) {
	r, id := newJobIn(t, pipeline.StatusCompleted)
	_, out, err := r.Claim(context.Background(), id, "w1", time.Minute, alive(true))
	require.NoError(t, err)
	require.Equal(t, Terminal, out)
	_, out, err = r.Claim(context.Background(), [16]byte{9}, "w1", time.Minute, alive(true))
	require.NoError(t, err)
	require.Equal(t, NotFound, out)
}

func TestClaimRace(t *testing.T) {
	r, id := newJobIn(t, pipeline.StatusQueued)
	var wins atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, out, err := r.Claim(context.Background(), id, string(rune('a'+i)), time.Minute, alive(true))
			if err == nil && out == Claimed {
				wins.Add(1)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	require.EqualValues(t, 1, wins.Load())
}

func TestRenewAndReleaseLease(t *testing.T) {
	r, id := newJobIn(t, pipeline.StatusQueued)
	ctx := context.Background()
	_, _, err := r.Claim(ctx, id, "w1", time.Minute, alive(true))
	require.NoError(t, err)
	require.NoError(t, r.RenewLease(ctx, id, "w1", time.Minute))
	require.ErrorIs(t, r.RenewLease(ctx, id, "w2", time.Minute), ErrLeaseLost)
	require.NoError(t, r.ReleaseLease(ctx, id, "w2")) // no-op
	require.Equal(t, "w1", *leaseOf(t, r, id))
	require.NoError(t, r.ReleaseLease(ctx, id, "w1"))
	require.Nil(t, leaseOf(t, r, id))
	require.ErrorIs(t, r.RenewLease(ctx, id, "w1", time.Minute), ErrLeaseLost)
}

func TestClaimRenewBetweenReadAndWrite(t *testing.T) {
	r, id := newJobIn(t, pipeline.StatusQueued)
	ctx := context.Background()
	_, _, err := r.Claim(ctx, id, "w1", time.Minute, alive(true))
	require.NoError(t, err)
	_, err = r.pool.Exec(ctx, `UPDATE jobs SET lease_until = now() - interval '1 second' WHERE id=$1`, id)
	require.NoError(t, err)
	claimAfterRead = func() { require.NoError(t, r.RenewLease(ctx, id, "w1", time.Minute)) }
	t.Cleanup(func() { claimAfterRead = nil })
	_, out, err := r.Claim(ctx, id, "w2", time.Minute, alive(true))
	require.NoError(t, err)
	require.Equal(t, Busy, out)
	require.Equal(t, "w1", *leaseOf(t, r, id))
}
