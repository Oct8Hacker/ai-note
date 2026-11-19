//go:build integration

package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"ainote/internal/pipeline"
)

var redisAddr string

// TestMain starts one Redis container shared by every test in the package.
func TestMain(m *testing.M) { os.Exit(runMain(m)) }

func runMain(m *testing.M) int {
	ctx := context.Background()
	rds, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "redis:7-alpine",
			ExposedPorts: []string{"6379/tcp"},
			WaitingFor:   wait.ForLog("Ready to accept connections").WithStartupTimeout(60 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "start redis:", err)
		return 1
	}
	defer func() { _ = testcontainers.TerminateContainer(rds) }()
	host, _ := rds.Host(ctx)
	port, err := rds.MappedPort(ctx, "6379/tcp")
	if err != nil {
		fmt.Fprintln(os.Stderr, "redis port:", err)
		return 1
	}
	redisAddr = fmt.Sprintf("%s:%s", host, port.Port())
	return m.Run()
}

func newRedis(t *testing.T) *redis.Client {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{Addr: redisAddr})
	t.Cleanup(func() { _ = rdb.Close() })
	require.NoError(t, rdb.FlushAll(context.Background()).Err())
	return rdb
}

func TestBucketRate(t *testing.T) {
	rdb := newRedis(t)
	l := New(rdb, map[string]BucketSpec{"llm:requests": {Capacity: 2, RefillPerSec: 1}},
		BucketSpec{}, nil, 10*time.Second, time.Now)
	ctx := context.Background()

	start := time.Now()
	require.NoError(t, l.Acquire(ctx, "llm:requests", 1))
	require.NoError(t, l.Acquire(ctx, "llm:requests", 1))
	require.Less(t, time.Since(start), 300*time.Millisecond, "first two takes are immediate")

	require.NoError(t, l.Acquire(ctx, "llm:requests", 1))
	el := time.Since(start)
	require.GreaterOrEqual(t, el, 900*time.Millisecond)
	require.Less(t, el, 1800*time.Millisecond)
}

func TestSharedAcrossClients(t *testing.T) {
	rdb := newRedis(t)
	rdb2 := redis.NewClient(&redis.Options{Addr: redisAddr})
	t.Cleanup(func() { _ = rdb2.Close() })
	specs := map[string]BucketSpec{"youtube": {Capacity: 1, RefillPerSec: 10}}
	l1 := New(rdb, specs, BucketSpec{}, nil, 30*time.Second, time.Now)
	l2 := New(rdb2, specs, BucketSpec{}, nil, 30*time.Second, time.Now)

	var wg sync.WaitGroup
	errs := make(chan error, 20)
	start := time.Now()
	for i := 0; i < 20; i++ {
		l := l1
		if i%2 == 1 {
			l = l2
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- l.Acquire(context.Background(), "youtube", 1)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.GreaterOrEqual(t, time.Since(start), 1900*time.Millisecond, "combined rate must not exceed 10/s")
}

func TestMaxWaitReturnsRateLimited(t *testing.T) {
	rdb := newRedis(t)
	l := New(rdb, map[string]BucketSpec{"llm:requests": {Capacity: 1, RefillPerSec: 0.01}},
		BucketSpec{}, nil, time.Second, time.Now)
	ctx := context.Background()
	require.NoError(t, l.Acquire(ctx, "llm:requests", 1))

	err := l.Acquire(ctx, "llm:requests", 1)
	var rl *pipeline.RateLimitedError
	require.ErrorAs(t, err, &rl)
	require.Equal(t, "llm", rl.Provider)
	require.Greater(t, rl.RetryAfter, time.Second)
}

func TestDailyCap(t *testing.T) {
	rdb := newRedis(t)
	la, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)
	// Redis expires keys against its real clock. Start tomorrow at 22:00
	// so the simulated midnight reset stays in the future throughout the test.
	year, month, day := time.Now().In(la).AddDate(0, 0, 1).Date()
	now := time.Date(year, month, day, 22, 0, 0, 0, la)
	l := New(rdb, nil, BucketSpec{}, map[string]DailySpec{"llm:daily": {Limit: 3, Loc: la}},
		time.Second, func() time.Time { return now })
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		require.NoError(t, l.Acquire(ctx, "llm:daily", 1))
	}
	err = l.Acquire(ctx, "llm:daily", 1)
	var rl *pipeline.RateLimitedError
	require.ErrorAs(t, err, &rl)
	require.Equal(t, "llm", rl.Provider)
	require.Equal(t, 2*time.Hour, rl.RetryAfter)

	// The next day starts a fresh counter.
	now = now.Add(3 * time.Hour)
	require.NoError(t, l.Acquire(ctx, "llm:daily", 1))
}

func TestNotionTemplate(t *testing.T) {
	rdb := newRedis(t)
	l := New(rdb, nil, BucketSpec{Capacity: 1, RefillPerSec: 0.01}, nil, time.Second, time.Now)
	ctx := context.Background()
	a, b := pipeline.NotionBucket("a"), pipeline.NotionBucket("b")

	require.NoError(t, l.Acquire(ctx, a, 1))
	require.NoError(t, l.Acquire(ctx, b, 1), "b has its own bucket")

	var rl *pipeline.RateLimitedError
	require.ErrorAs(t, l.Acquire(ctx, a, 1), &rl)
	require.Equal(t, "notion", rl.Provider)
}

func TestFailsClosedWhenRedisDown(t *testing.T) {
	dead := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 200 * time.Millisecond, MaxRetries: -1})
	t.Cleanup(func() { _ = dead.Close() })
	l := New(dead, map[string]BucketSpec{"youtube": {Capacity: 1, RefillPerSec: 1}},
		BucketSpec{}, map[string]DailySpec{"llm:daily": {Limit: 1, Loc: time.UTC}}, time.Second, time.Now)

	for _, b := range []string{"youtube", "llm:daily"} {
		err := l.Acquire(context.Background(), b, 1)
		var re *pipeline.RetryableError
		require.ErrorAs(t, err, &re, b)
		require.Equal(t, "LIMITER_UNAVAILABLE", re.Code)
	}
}

func TestAcquireCtxCancelled(t *testing.T) {
	rdb := newRedis(t)
	l := New(rdb, map[string]BucketSpec{"youtube": {Capacity: 1, RefillPerSec: 0.5}},
		BucketSpec{}, nil, 30*time.Second, time.Now)
	require.NoError(t, l.Acquire(context.Background(), "youtube", 1))
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	err := l.Acquire(ctx, "youtube", 1)
	require.True(t, errors.Is(err, context.DeadlineExceeded), "got %v", err)
}

func TestMetrics429(t *testing.T) {
	rdb := newRedis(t)
	ctx := context.Background()
	n, err := Get429(ctx, rdb)
	require.NoError(t, err)
	require.Zero(t, n)
	Incr429(ctx, rdb)
	Incr429(ctx, rdb)
	n, err = Get429(ctx, rdb)
	require.NoError(t, err)
	require.EqualValues(t, 2, n)
}
