//go:build integration

package jobs

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"ainote/internal/pipeline"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func replaceDB(u, name string) string {
	i := strings.Index(u, "/postgres?")
	return u[:i] + "/" + name + u[i+len("/postgres"):]
}

func TestMigrateIdempotent(t *testing.T) {
	url := newTestDB(t)
	require.NoError(t, Migrate(context.Background(), url))
	require.NoError(t, Migrate(context.Background(), url))
}

func TestCreateJobWritesOutbox(t *testing.T) {
	ctx := context.Background()
	r, pool := newTestRepo(t)
	id := uuid.New()
	job, created, err := r.CreateJob(ctx, NewJob{ID: id, VideoID: "abc", YouTubeURL: "https://youtu.be/abc", NotionParentID: "p"}, []byte("payload"))
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, id, job.ID)
	require.Equal(t, pipeline.StatusQueued, job.Status)

	var status string
	var progress int
	require.NoError(t, pool.QueryRow(ctx, "SELECT status, progress FROM jobs WHERE id=$1", id).Scan(&status, &progress))
	require.Equal(t, "QUEUED", status)
	require.Equal(t, 0, progress)

	var n int
	var payload []byte
	require.NoError(t, pool.QueryRow(ctx, "SELECT count(*), (array_agg(payload))[1] FROM outbox WHERE job_id=$1 AND published_at IS NULL", id).Scan(&n, &payload))
	require.Equal(t, 1, n)
	require.Equal(t, []byte("payload"), payload)
}

func TestCreateJobIdempotencyKey(t *testing.T) {
	ctx := context.Background()
	r, pool := newTestRepo(t)
	first, c1, err := r.CreateJob(ctx, NewJob{ID: uuid.New(), VideoID: "v", YouTubeURL: "u", IdempotencyKey: "k"}, []byte("a"))
	require.NoError(t, err)
	require.True(t, c1)
	second, c2, err := r.CreateJob(ctx, NewJob{ID: uuid.New(), VideoID: "v", YouTubeURL: "u", IdempotencyKey: "k"}, []byte("b"))
	require.NoError(t, err)
	require.False(t, c2)
	require.Equal(t, first.ID, second.ID)
	var n int
	require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM outbox").Scan(&n))
	require.Equal(t, 1, n)
}

func TestTwoJobsSameVideo(t *testing.T) {
	ctx := context.Background()
	r, _ := newTestRepo(t)
	a, _, err := r.CreateJob(ctx, NewJob{ID: uuid.New(), VideoID: "v", YouTubeURL: "u", IdempotencyKey: "k1"}, []byte("a"))
	require.NoError(t, err)
	b, _, err := r.CreateJob(ctx, NewJob{ID: uuid.New(), VideoID: "v", YouTubeURL: "u", IdempotencyKey: "k2"}, []byte("b"))
	require.NoError(t, err)
	require.NotEqual(t, a.ID, b.ID)
}

func TestGetJobViewNotFound(t *testing.T) {
	r, _ := newTestRepo(t)
	_, err := r.GetJobView(context.Background(), uuid.New())
	require.ErrorIs(t, err, ErrNotFound)
	_, err = r.GetJob(context.Background(), uuid.New())
	require.ErrorIs(t, err, ErrNotFound)
}

func TestGetJobViewIsReadOnly(t *testing.T) {
	ctx := context.Background()
	r, pool := newTestRepo(t)
	id := uuid.New()
	_, _, err := r.CreateJob(ctx, NewJob{ID: id, VideoID: "v", YouTubeURL: "u"}, []byte("a"))
	require.NoError(t, err)
	v1, err := r.GetJobView(ctx, id)
	require.NoError(t, err)
	time.Sleep(20 * time.Millisecond)
	v2, err := r.GetJobView(ctx, id)
	require.NoError(t, err)
	require.True(t, v1.UpdatedAt.Equal(v2.UpdatedAt))
	var n int
	require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM jobs WHERE id=$1 AND updated_at=$2", id, v1.UpdatedAt).Scan(&n))
	require.Equal(t, 1, n)
}

func TestWarningsNeverNull(t *testing.T) {
	ctx := context.Background()
	r, _ := newTestRepo(t)
	id := uuid.New()
	_, _, err := r.CreateJob(ctx, NewJob{ID: id, VideoID: "v", YouTubeURL: "u"}, []byte("a"))
	require.NoError(t, err)
	v, err := r.GetJobView(ctx, id)
	require.NoError(t, err)
	require.NotNil(t, v.Warnings)
	b, err := json.Marshal(v)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	require.Equal(t, []any{}, m["warnings"])
	require.Nil(t, m["video_title"])
	require.Nil(t, m["duration_sec"])
	require.Contains(t, m, "created_at")
	require.Contains(t, m, "updated_at")
	require.Nil(t, m["error"])
	require.Nil(t, m["result"])
	require.Nil(t, m["waiting"])
}

func TestMigrateCancelled(t *testing.T) {
	url := newTestDB(t)

	// Already-cancelled context: must fail with context.Canceled.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, Migrate(ctx, url), context.Canceled)

	// Cancel shortly after starting: either it finished first (nil) or it
	// reports cancellation; never anything else.
	ctx2, cancel2 := context.WithCancel(context.Background())
	time.AfterFunc(time.Millisecond, cancel2)
	if err := Migrate(ctx2, url); err != nil {
		require.ErrorIs(t, err, context.Canceled)
	}

	// No dirty state or held lock left behind.
	require.NoError(t, Migrate(context.Background(), url))
}
