//go:build integration

package worker

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func newRedis(t *testing.T) *redis.Client {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{Addr: redisAddr})
	t.Cleanup(func() { _ = rdb.Close() })
	require.NoError(t, rdb.FlushAll(context.Background()).Err())
	return rdb
}

func TestHeartbeatVisibleAndDeletedOnCancel(t *testing.T) {
	rdb := newRedis(t)
	ctx, cancel := context.WithCancel(context.Background())
	StartHeartbeat(ctx, rdb, "w1", 100*time.Millisecond, time.Second)
	ok, err := IsAlive(context.Background(), rdb, "w1")
	require.NoError(t, err)
	require.True(t, ok)
	n, err := CountAlive(context.Background(), rdb)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	cancel()
	require.Eventually(t, func() bool {
		ok, _ := IsAlive(context.Background(), rdb, "w1")
		return !ok
	}, 3*time.Second, 50*time.Millisecond)
}

func TestHeartbeatExpires(t *testing.T) {
	rdb := newRedis(t)
	require.NoError(t, rdb.Set(context.Background(), HeartbeatKey("w2"), "1", time.Second).Err())
	ok, err := IsAlive(context.Background(), rdb, "w2")
	require.NoError(t, err)
	require.True(t, ok)
	time.Sleep(1500 * time.Millisecond)
	ok, err = IsAlive(context.Background(), rdb, "w2")
	require.NoError(t, err)
	require.False(t, ok)
}
