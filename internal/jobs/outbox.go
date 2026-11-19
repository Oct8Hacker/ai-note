package jobs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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
var finishTimeout = 10 * time.Second

// markFault is a test hook: when non-nil and it returns an error for a row id,
// that row's mark fails.
var markFault func(id int64) error

// finish returns a fresh bounded context detached from ctx, so a mark or
// commit still completes after ctx is cancelled and is not starved by a slow
// publish earlier in the batch.
func finish(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), finishTimeout)
}

// RelayBatch processes up to limit unpublished outbox rows, oldest first, in
// one transaction. Rows are selected FOR UPDATE SKIP LOCKED, so concurrent
// relays never take the same row; the locks are held until the batch commits.
// For each row, fn is called (it should publish with confirms); on success the
// row is marked published (published_at=now(), payload=NULL) inside a
// savepoint, so a failing mark undoes only that row. If fn or a mark fails,
// the batch stops, rows already marked are committed, and the error is
// returned. n is the number of rows marked published.
//
// Delivery is at-least-once: if a mark or the final commit fails (or the
// process crashes) after a confirmed publish, that row stays unpublished and
// is published again. Consumers must be idempotent (the worker's lease and
// status checks provide this).
func (r *Repo) RelayBatch(ctx context.Context, limit int, fn func(ctx context.Context, row OutboxRow) error) (n int, err error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() {
		rctx, cancel := finish(ctx)
		defer cancel()
		_ = tx.Rollback(rctx)
	}()

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

	var stopErr error
	for _, row := range batch {
		if ctx.Err() != nil {
			stopErr = ctx.Err()
			break
		}
		bad := false
		if e := fn(ctx, row); e != nil {
			if !errors.Is(e, ErrBadOutboxPayload) {
				stopErr = e
				break
			}
			bad = true
		}
		if err := markPublished(ctx, tx, row, bad); err != nil {
			stopErr = err
			break
		}
		n++
	}
	cctx, cancel := finish(ctx)
	defer cancel()
	if err := tx.Commit(cctx); err != nil {
		return 0, fmt.Errorf("commit outbox batch: %w", err)
	}
	return n, stopErr
}

// markPublished marks row published inside a savepoint; on failure only this
// row's changes are rolled back.
func markPublished(ctx context.Context, tx pgx.Tx, row OutboxRow, bad bool) (err error) {
	mctx, cancel := finish(ctx)
	defer cancel()
	sp, err := tx.Begin(mctx)
	if err != nil {
		return fmt.Errorf("savepoint: %w", err)
	}
	defer func() {
		if err != nil {
			_ = sp.Rollback(mctx)
		}
	}()
	if markFault != nil {
		if err := markFault(row.ID); err != nil {
			return err
		}
	}
	if bad {
		if err := appendEvent(mctx, sp, row.JobID, "outbox_bad_payload", "", "", "",
			map[string]any{"outbox_id": row.ID}); err != nil {
			return fmt.Errorf("outbox event: %w", err)
		}
	}
	if _, err := sp.Exec(mctx, `UPDATE outbox SET published_at=now(), payload=NULL WHERE id=$1`, row.ID); err != nil {
		return fmt.Errorf("mark outbox %d: %w", row.ID, err)
	}
	return sp.Commit(mctx)
}
