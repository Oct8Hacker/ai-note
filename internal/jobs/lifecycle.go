package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"ainote/internal/pipeline"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// execer is satisfied by pgx.Tx and *pgxpool.Pool.
type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

func appendEvent(ctx context.Context, q execer, id uuid.UUID, typ, from, to, worker string, detail any) error {
	var d []byte
	if detail != nil {
		var err error
		if d, err = json.Marshal(detail); err != nil {
			return fmt.Errorf("marshal event detail: %w", err)
		}
	}
	_, err := q.Exec(ctx, `INSERT INTO job_events (job_id, type, from_status, to_status, worker_id, detail)
		VALUES ($1, $2, NULLIF($3,''), NULLIF($4,''), NULLIF($5,''), $6)`, id, typ, from, to, worker, d)
	return err
}

// Transition moves a job to `to` if its current status allows it (FR-SM-1).
func (r *Repo) Transition(ctx context.Context, id uuid.UUID, to pipeline.Status, workerID string, detail any) error {
	allowed := AllowedFrom(to)
	if len(allowed) == 0 {
		return fmt.Errorf("%w: no source status for %s", ErrIllegalTransition, to)
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var from string
	err = tx.QueryRow(ctx, `WITH old AS (SELECT id, status FROM jobs WHERE id = $1 FOR UPDATE)
		UPDATE jobs j SET status = $2, progress = GREATEST(j.progress, $4),
			finished_at = CASE WHEN $5 THEN now() ELSE j.finished_at END,
			waiting_reason = NULL, waiting_provider = NULL, next_attempt_at = NULL, updated_at = now()
		FROM old WHERE j.id = old.id AND old.status = ANY($3) RETURNING old.status`,
		id, string(to), statusStrings(allowed), GlobalProgress(to, 0), to.IsTerminal()).Scan(&from)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: job %s -> %s", ErrIllegalTransition, id, to)
	}
	if err != nil {
		return err
	}
	if err := appendEvent(ctx, tx, id, "transition", from, string(to), workerID, detail); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// CompleteStage atomically checkpoints a finished stage and advances the job.
func (r *Repo) CompleteStage(ctx context.Context, id uuid.UUID, cur pipeline.Status, res pipeline.StageResult, workerID string) (pipeline.Status, error) {
	next, ok := cur.NextStage()
	final := cur == pipeline.StatusPublishing
	if !ok && !final {
		return "", fmt.Errorf("%w: cannot complete stage %s", ErrIllegalTransition, cur)
	}
	if final {
		next = pipeline.StatusCompleted // refined in SQL by warnings
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if res.ArtifactKind != "" {
		if err := saveArtifact(ctx, tx, id, res.ArtifactKind, res.Artifact); err != nil {
			return "", err
		}
	}
	var got string
	err = tx.QueryRow(ctx, `UPDATE jobs SET
			status = CASE WHEN $8 THEN (CASE WHEN jsonb_array_length(warnings) > 0
				THEN 'COMPLETED_WITH_WARNINGS' ELSE 'COMPLETED' END) ELSE $3 END,
			progress = GREATEST(progress, $4),
			video_title = COALESCE($5, video_title),
			duration_sec = COALESCE($6, duration_sec),
			frames_requested = COALESCE($9, frames_requested),
			frames_published = COALESCE($10, frames_published),
			notion_page_url = COALESCE(NULLIF($7, ''), notion_page_url),
			finished_at = CASE WHEN $8 THEN now() ELSE finished_at END,
			waiting_reason = NULL, waiting_provider = NULL, next_attempt_at = NULL, updated_at = now()
		WHERE id = $1 AND status = $2 RETURNING status`,
		id, string(cur), string(next), GlobalProgress(next, 0), res.VideoTitle, res.DurationSec,
		res.PageURL, final, res.FramesRequested, res.FramesPublished).Scan(&got)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("%w: job %s not in %s", ErrIllegalTransition, id, cur)
	}
	if err != nil {
		return "", err
	}
	if err := appendEvent(ctx, tx, id, "transition", string(cur), got, workerID, nil); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return pipeline.Status(got), nil
}

// AddWarning appends w unless an identical (code, timestamp_sec) warning exists.
func (r *Repo) AddWarning(ctx context.Context, id uuid.UUID, w pipeline.Warning) error {
	b, err := json.Marshal(w)
	if err != nil {
		return err
	}
	var ts *string
	if w.TimestampSec != nil {
		s := string(mustJSON(*w.TimestampSec))
		ts = &s
	}
	_, err = r.pool.Exec(ctx, `UPDATE jobs SET warnings = warnings || jsonb_build_array($2::jsonb), updated_at = now()
		WHERE id = $1 AND NOT EXISTS (
			SELECT 1 FROM jsonb_array_elements(warnings) e
			WHERE e->>'code' = $3 AND (e->'timestamp_sec') IS NOT DISTINCT FROM $4::jsonb)`,
		id, string(b), w.Code, ts)
	return err
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// RetryKind selects which retry counter a ScheduleRetry call consumes.
type RetryKind string

const (
	RetryFailure   RetryKind = "failure"
	RetryRateLimit RetryKind = "rate_limit"
	RetryDefer     RetryKind = "defer"
)

// RetryDecision is the outcome of ScheduleRetry; N is the counter after increment.
type RetryDecision struct {
	Exhausted bool
	N         int
	Delay     time.Duration
}

// RetryDelay returns the delay for the n-th (1-based) retry; the last value
// repeats, and the result is at least retryAfter.
func RetryDelay(delays []time.Duration, n int, retryAfter time.Duration) time.Duration {
	var d time.Duration
	if len(delays) > 0 {
		d = delays[min(max(n, 1), len(delays))-1]
	}
	return max(d, retryAfter)
}

// ScheduleRetry consumes one unit of the counter for kind. When the budget is
// exhausted the caller should dead-letter; otherwise the job is marked waiting
// until now()+Delay, staying in its current stage (FR-SM-2).
func (r *Repo) ScheduleRetry(ctx context.Context, id uuid.UUID, kind RetryKind, provider string, delays []time.Duration, retryAfter time.Duration, budget int, errCode string) (RetryDecision, error) {
	var col, reason string
	switch kind {
	case RetryFailure:
		col, reason = "attempts", "RETRYING"
	case RetryRateLimit:
		col, reason = "rate_limit_retries", "RATE_LIMITED"
	case RetryDefer:
		col, reason = "deferrals", "BUSY"
	default:
		return RetryDecision{}, fmt.Errorf("unknown retry kind %q", kind)
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return RetryDecision{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var n int
	var status string
	err = tx.QueryRow(ctx, fmt.Sprintf(`UPDATE jobs SET %[1]s = %[1]s + 1,
			error_code = COALESCE(NULLIF($2, ''), error_code), updated_at = now()
		WHERE id = $1 AND status = ANY($3) RETURNING %[1]s, status`, col),
		id, errCode, statusStrings(nonTerminal)).Scan(&n, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return RetryDecision{}, fmt.Errorf("%w: job %s not retryable", ErrIllegalTransition, id)
	}
	if err != nil {
		return RetryDecision{}, err
	}
	dec := RetryDecision{N: n}
	detail := map[string]any{"kind": kind, "n": n, "budget": budget, "error_code": errCode}
	if n >= budget {
		dec.Exhausted = true
		if err := appendEvent(ctx, tx, id, "retry_exhausted", status, status, "", detail); err != nil {
			return RetryDecision{}, err
		}
	} else {
		dec.Delay = RetryDelay(delays, n, retryAfter)
		detail["delay_ms"] = dec.Delay.Milliseconds()
		if _, err := tx.Exec(ctx, `UPDATE jobs SET waiting_reason = $2, waiting_provider = NULLIF($3, ''),
			next_attempt_at = now() + make_interval(secs => $4) WHERE id = $1`,
			id, reason, provider, dec.Delay.Seconds()); err != nil {
			return RetryDecision{}, err
		}
		if err := appendEvent(ctx, tx, id, "retry_scheduled", status, status, "", detail); err != nil {
			return RetryDecision{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return RetryDecision{}, err
	}
	return dec, nil
}

// Fail moves a non-terminal job to FAILED or DEAD_LETTERED.
func (r *Repo) Fail(ctx context.Context, id uuid.UUID, terminal pipeline.Status, code, msg string) error {
	if terminal != pipeline.StatusFailed && terminal != pipeline.StatusDeadLettered {
		return fmt.Errorf("fail: %s is not FAILED or DEAD_LETTERED", terminal)
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var from string
	err = tx.QueryRow(ctx, `WITH old AS (SELECT id, status FROM jobs WHERE id = $1 FOR UPDATE)
		UPDATE jobs j SET status = $2, error_code = $4, error_message = $5, progress = 100, finished_at = now(),
			waiting_reason = NULL, waiting_provider = NULL, next_attempt_at = NULL, updated_at = now()
		FROM old WHERE j.id = old.id AND old.status = ANY($3) RETURNING old.status`,
		id, string(terminal), statusStrings(nonTerminal), code, msg).Scan(&from)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: job %s already terminal or missing", ErrIllegalTransition, id)
	}
	if err != nil {
		return err
	}
	if err := appendEvent(ctx, tx, id, "transition", from, string(terminal), "", map[string]string{"code": code, "message": msg}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
