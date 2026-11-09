// Package outbox relays transactional-outbox rows to RabbitMQ.
package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"ainote/internal/jobs"
	"ainote/internal/queue"
)

const batchSize = 100

// Relay publishes unpublished outbox rows to jobs.work and marks them
// published. Delivery is at-least-once: a failure after a confirmed publish
// but before the mark commits republishes the row, so consumers must be
// idempotent.
type Relay struct {
	repo     *jobs.Repo
	pub      *queue.Publisher
	interval time.Duration
	log      *slog.Logger
	kick     chan struct{}
}

// NewRelay returns a Relay that also sweeps every interval.
func NewRelay(repo *jobs.Repo, pub *queue.Publisher, interval time.Duration, log *slog.Logger) *Relay {
	return &Relay{repo: repo, pub: pub, interval: interval, log: log, kick: make(chan struct{}, 1)}
}

// Kick requests a prompt batch. It never blocks.
func (r *Relay) Kick() {
	select {
	case r.kick <- struct{}{}:
	default:
	}
}

// Run publishes batches on Kick and every interval until ctx is done; it
// returns after the in-flight batch finishes.
func (r *Relay) Run(ctx context.Context) {
	t := time.NewTicker(r.interval)
	defer t.Stop()
	for {
		r.drain(ctx)
		select {
		case <-ctx.Done():
			return
		case <-r.kick:
		case <-t.C:
		}
	}
}

// drain runs batches until one comes back short or fails. After a failure it
// stops, leaving the retry to the next tick or kick.
func (r *Relay) drain(ctx context.Context) {
	for ctx.Err() == nil {
		n, err := r.repo.RelayBatch(ctx, batchSize, r.publish)
		if err != nil {
			if ctx.Err() == nil {
				r.log.Error("outbox relay batch failed", "published", n, "err", err)
			}
			return
		}
		if n < batchSize {
			return
		}
	}
}

func (r *Relay) publish(ctx context.Context, row jobs.OutboxRow) error {
	var m queue.Message
	if err := json.Unmarshal(row.Payload, &m); err != nil {
		// Never log the payload: it contains the sealed Notion token.
		r.log.Error("outbox payload undecodable, discarding", "outbox_id", row.ID, "job_id", row.JobID)
		return fmt.Errorf("%w: decode", jobs.ErrBadOutboxPayload)
	}
	return r.pub.PublishWork(ctx, m)
}
