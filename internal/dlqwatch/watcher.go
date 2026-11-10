// Package dlqwatch consumes the jobs.dlq.audit queue and makes sure every job
// the broker dead-lettered (for example on the delivery limit) ends up in the
// DEAD_LETTERED status.
package dlqwatch

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"ainote/internal/jobs"
	"ainote/internal/pipeline"
	"ainote/internal/queue"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
)

const (
	// CodeDeliveryLimit is the job error code recorded for broker dead-lettering.
	CodeDeliveryLimit = "DELIVERY_LIMIT_EXCEEDED"
	errorBackoff      = 2 * time.Second
)

// store is the slice of *jobs.Repo the watcher uses.
type store interface {
	GetJobView(ctx context.Context, id uuid.UUID) (jobs.JobView, error)
	LastReplayAt(ctx context.Context, id uuid.UUID) (time.Time, bool, error)
	Fail(ctx context.Context, id uuid.UUID, terminal pipeline.Status, code, msg string) error
}

// Watch consumes jobs.dlq.audit until ctx is done. For each message whose job
// is not yet terminal it records DEAD_LETTERED / DELIVERY_LIMIT_EXCEEDED, then
// acks. Messages for unknown or already-terminal jobs are acked untouched.
// Database errors requeue the message after a short, ctx-aware pause. The
// consumer itself drops undecodable bodies. The inspection copy in jobs.dlq is
// never touched.
func Watch(ctx context.Context, conn *queue.Conn, repo *jobs.Repo, log *slog.Logger) error {
	w := &watcher{repo: repo, log: log, backoff: errorBackoff, sleep: sleepCtx}
	return queue.Consume(ctx, conn, queue.QueueDLQAudit, 1, w.handle)
}

type watcher struct {
	repo    store
	log     *slog.Logger
	backoff time.Duration
	sleep   func(ctx context.Context, d time.Duration)
}

func sleepCtx(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

func (w *watcher) handle(ctx context.Context, d queue.Delivery) {
	id := d.Msg.JobID
	v, err := w.repo.GetJobView(ctx, id)
	switch {
	case errors.Is(err, jobs.ErrNotFound):
		w.log.Warn("dead-lettered message for unknown job", "job_id", id)
		w.ack(d)
		return
	case err != nil:
		w.retryLater(ctx, d, id, err)
		return
	}
	if v.Status.IsTerminal() {
		w.ack(d)
		return
	}
	// A replay newer than this dead-letter makes the audit copy stale: acting
	// on it would undo the replay.
	if dlAt, ok := deadLetteredAt(d); ok {
		rAt, replayed, err := w.repo.LastReplayAt(ctx, id)
		if err != nil {
			w.retryLater(ctx, d, id, err)
			return
		}
		if replayed && rAt.After(dlAt) {
			w.log.Info("ignoring stale dead-letter copy of replayed job", "job_id", id)
			w.ack(d)
			return
		}
	}
	err = w.repo.Fail(ctx, id, pipeline.StatusDeadLettered, CodeDeliveryLimit,
		"processing kept crashing on this video (broker delivery limit exceeded)")
	switch {
	case err == nil:
		w.log.Warn("job dead-lettered by broker", "job_id", id, "was", v.Status)
		w.ack(d)
	case errors.Is(err, jobs.ErrIllegalTransition):
		w.ack(d) // reached a terminal state between the read and the write
	default:
		w.retryLater(ctx, d, id, err)
	}
}

// deadLetteredAt returns the latest broker dead-lettering time recorded in the
// x-death header; ok is false when the header is absent or unreadable.
func deadLetteredAt(d queue.Delivery) (time.Time, bool) {
	deaths, _ := d.Raw.Headers["x-death"].([]any)
	var latest time.Time
	for _, e := range deaths {
		tbl, _ := e.(amqp.Table)
		if t, ok := tbl["time"].(time.Time); ok && t.After(latest) {
			latest = t
		}
	}
	return latest, !latest.IsZero()
}

func (w *watcher) ack(d queue.Delivery) {
	if err := d.Ack(); err != nil {
		w.log.Warn("ack failed", "err", err)
	}
}

func (w *watcher) retryLater(ctx context.Context, d queue.Delivery, id uuid.UUID, cause error) {
	w.log.Error("dlq watcher: database error, requeueing", "job_id", id, "err", cause)
	w.sleep(ctx, w.backoff)
	if err := d.NackRequeue(); err != nil {
		w.log.Warn("nack failed", "err", err)
	}
}
