package queue

import (
	"testing"
	"time"
)

func TestRetryQueueName(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{10 * time.Second, "jobs.retry.10s"},
		{90 * time.Second, "jobs.retry.90s"},
		{time.Minute + 30*time.Second, "jobs.retry.90s"},
		{270 * time.Second, "jobs.retry.270s"},
	}
	for _, c := range cases {
		if got := RetryQueueName(c.d); got != c.want {
			t.Errorf("RetryQueueName(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

func TestPickRetryDelay(t *testing.T) {
	cfg := []time.Duration{10 * time.Second, 30 * time.Second, 90 * time.Second, 270 * time.Second}
	cases := []struct{ want, expect time.Duration }{
		{0, 10 * time.Second},
		{10 * time.Second, 10 * time.Second},
		{25 * time.Second, 30 * time.Second},
		{5 * time.Minute, 270 * time.Second},
	}
	for _, c := range cases {
		if got := PickRetryDelay(cfg, c.want); got != c.expect {
			t.Errorf("PickRetryDelay(%v) = %v, want %v", c.want, got, c.expect)
		}
	}
}
