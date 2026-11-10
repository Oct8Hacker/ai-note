package ratelimit

import (
	"context"
	"errors"

	"github.com/redis/go-redis/v9"
)

const key429 = "metrics:provider_429_total"

// Incr429 counts one real provider 429. A failed increment is dropped: the
// counter is informational and must never fail the call that hit the 429.
func Incr429(ctx context.Context, rdb *redis.Client) {
	_ = rdb.Incr(ctx, key429).Err()
}

// Get429 returns the number of provider 429s recorded so far (0 if none).
func Get429(ctx context.Context, rdb *redis.Client) (int64, error) {
	n, err := rdb.Get(ctx, key429).Int64()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	return n, err
}
