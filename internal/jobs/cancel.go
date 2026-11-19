package jobs

import (
	"context"
	"errors"

	"ainote/internal/pipeline"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrCancelTooLate means the job is terminal or might already have a Notion page.
var ErrCancelTooLate = errors.New("job can no longer be cancelled")

// The same predicate drives both the public view and the locked cancellation decision.
const canCancelSQL = `status = ANY($2) AND notion_create_started_at IS NULL
	AND COALESCE(notion_page_id, '') = '' AND COALESCE(notion_page_url, '') = ''`

// CancelJob serializes cancellation with the durable checkpoint taken before
// the first request to create a Notion page.
func (r *Repo) CancelJob(ctx context.Context, id uuid.UUID) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var from string
	var eligible bool
	err = tx.QueryRow(ctx, `SELECT status, `+canCancelSQL+` FROM jobs WHERE id=$1 FOR NO KEY UPDATE`,
		id, statusStrings(nonTerminal)).Scan(&from, &eligible)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if pipeline.Status(from) == pipeline.StatusCancelled {
		return nil
	}
	if !eligible {
		return ErrCancelTooLate
	}
	_, err = tx.Exec(ctx, `UPDATE jobs SET status='CANCELLED', progress=100,
		waiting_reason=NULL, waiting_provider=NULL, next_attempt_at=NULL,
		lease_owner=NULL, lease_until=NULL, error_code=NULL, error_message=NULL,
		finished_at=now(), updated_at=now() WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if err := appendEvent(ctx, tx, id, "transition", from, string(pipeline.StatusCancelled), "", nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
