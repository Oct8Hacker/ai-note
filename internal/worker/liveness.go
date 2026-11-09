// Package worker holds worker-process concerns shared by the worker binary.
package worker

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

const heartbeatPrefix = "worker:alive:"

// HeartbeatKey is the Redis key a worker refreshes while alive.
func HeartbeatKey(workerID string) string { return heartbeatPrefix + workerID }

// StartHeartbeat sets the worker's key (synchronously, best effort) and then
// refreshes it every interval in a background goroutine. When ctx is done the
// key is deleted. A SIGKILLed worker leaves the key to expire after ttl.
func StartHeartbeat(ctx context.Context, rdb *redis.Client, workerID string, interval, ttl time.Duration) {
	key := HeartbeatKey(workerID)
	_ = rdb.Set(ctx, key, "1", ttl).Err()
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				dctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				_ = rdb.Del(dctx, key).Err()
				cancel()
				return
			case <-t.C:
				_ = rdb.Set(ctx, key, "1", ttl).Err()
			}
		}
	}()
}

// IsAlive reports whether the worker's heartbeat key exists.
func IsAlive(ctx context.Context, rdb *redis.Client, workerID string) (bool, error) {
	n, err := rdb.Exists(ctx, HeartbeatKey(workerID)).Result()
	return n > 0, err
}

// CountAlive counts live workers using SCAN (never KEYS).
func CountAlive(ctx context.Context, rdb *redis.Client) (int, error) {
	var (
		cursor uint64
		n      int
	)
	for {
		keys, next, err := rdb.Scan(ctx, cursor, heartbeatPrefix+"*", 100).Result()
		if err != nil {
			return 0, err
		}
		n += len(keys)
		if next == 0 {
			return n, nil
		}
		cursor = next
	}
}
