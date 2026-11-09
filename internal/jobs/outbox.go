package jobs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// OutboxRow is an unpublished outbox entry.
type OutboxRow struct {
	ID      int64
	JobID   uuid.UUID
	Payload []byte
}

// ErrBadOutboxPayload is returned (wrapped) by a RelayBatch callback when a
// row's payload can never be published; the row is then marked published with
// its payload cleared and an 'outbox_bad_payload' job event is recorded.
var ErrBadOutboxPayload = errors.New("bad outbox payload")

// finishTimeout bounds the marking/commit work that must complete even when
// the caller's context has been cancelled mid-batch.
const finishTimeout = 10 * time.Second

// RelayBatch processes up to limit unpublished outbox rows, oldest first, in
// one transaction. Rows are selected FOR UPDATE SKIP LOCKED, so concurrent
// relays never take the same row; the locks are held until the batch commits.
// For each row, fn is called (it should publish with confirms); on success the
// row is marked published (published_at=now(), payload=NULL) within the same
// transaction. If fn fails, the batch stops, rows already handled are
// committed, and the error is returned along with the count handled. n is the
// number of rows marked published.
func (r *Repo) RelayBatch(ctx context.Context, limit int, fn func(ctx context.Context, row OutboxRow) error) (n int, err error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	// Marking and commit use a detached context so already-published rows are
	// recorded even if ctx is cancelled during the batch.
	dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), finishTimeout)
	defer cancel()
	defer func() { _ = tx.Rollback(dctx) }()

	rows, err := tx.Query(ctx, `SELECT id, job_id, payload FROM outbox
		WHERE published_at IS NULL ORDER BY id LIMIT $1 FOR UPDATE SKIP LOCKED`, limit)
	if err != nil {
		return 0, fmt.Errorf("select outbox: %w", err)
	}
	var batch []OutboxRow
	for rows.Next() {
		var row OutboxRow
		if err := rows.Scan(&row.ID, &row.JobID, &row.Payload); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan outbox: %w", err)
		}
		batch = append(batch, row)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("select outbox: %w", err)
	}

	var fnErr error
	for _, row := range batch {
		if ctx.Err() != nil {
			fnErr = ctx.Err()
			break
		}
		if e := fn(ctx, row); e != nil {
			if !errors.Is(e, ErrBadOutboxPayload) {
				fnErr = e
				break
			}
			if err := appendEvent(dctx, tx, row.JobID, "outbox_bad_payload", "", "", "",
				map[string]any{"outbox_id": row.ID}); err != nil {
				return n, fmt.Errorf("outbox event: %w", err)
			}
		}
		if _, err := tx.Exec(dctx, `UPDATE outbox SET published_at=now(), payload=NULL WHERE id=$1`, row.ID); err != nil {
			return n, fmt.Errorf("mark outbox %d: %w", row.ID, err)
		}
		n++
	}
	if err := tx.Commit(dctx); err != nil {
		return 0, fmt.Errorf("commit outbox batch: %w", err)
	}
	return n, fnErr
}
