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
)

// ErrNotDeadLettered is returned by ResetForReplay when the job exists but is
// not in DEAD_LETTERED.
var ErrNotDeadLettered = errors.New("job is not dead-lettered")

// Event is one row of a job's audit trail (job_events).
type Event struct {
	At         time.Time       `json:"at"`
	Type       string          `json:"type"`
	FromStatus *string         `json:"from_status"`
	ToStatus   *string         `json:"to_status"`
	WorkerID   string          `json:"worker_id,omitempty"`
	Detail     json.RawMessage `json:"detail,omitempty"`
}

// DLQEntry is a dead-lettered job with its attempt history. The embedded
// JobView carries the last error.
type DLQEntry struct {
	JobView
	Events []Event `json:"events"`
}

// ListDeadLettered returns up to limit dead-lettered jobs, most recently
// finished first, each with its events oldest first.
func (r *Repo) ListDeadLettered(ctx context.Context, limit int) ([]DLQEntry, error) {
	rows, err := r.pool.Query(ctx, `SELECT id FROM jobs WHERE status = 'DEAD_LETTERED'
		ORDER BY finished_at DESC NULLS LAST, id LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list dead-lettered: %w", err)
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan dead-lettered: %w", err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list dead-lettered: %w", err)
	}

	out := make([]DLQEntry, 0, len(ids))
	for _, id := range ids {
		v, err := r.GetJobView(ctx, id)
		if errors.Is(err, ErrNotFound) {
			continue // deleted between the two queries
		}
		if err != nil {
			return nil, err
		}
		evs, err := r.listEvents(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, DLQEntry{JobView: v, Events: evs})
	}
	return out, nil
}

func (r *Repo) listEvents(ctx context.Context, id uuid.UUID) ([]Event, error) {
	rows, err := r.pool.Query(ctx, `SELECT at, type, from_status, to_status, COALESCE(worker_id, ''), detail
		FROM job_events WHERE job_id = $1 ORDER BY at, id`, id)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	defer rows.Close()
	evs := []Event{}
	for rows.Next() {
		var e Event
		var detail []byte
		if err := rows.Scan(&e.At, &e.Type, &e.FromStatus, &e.ToStatus, &e.WorkerID, &detail); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		if len(detail) > 0 {
			e.Detail = json.RawMessage(detail)
		}
		evs = append(evs, e)
	}
	return evs, rows.Err()
}

// ResetForReplay re-opens a DEAD_LETTERED job in one transaction: the status
// returns to the stage it was in when it was dead-lettered (QUEUED if that is
// unknown; EXTRACTING_FRAMES instead of PUBLISHING when purged frames must be
// recaptured), all retry counters and the error are cleared, and a new outbox row
// carrying outboxPayload is written. It returns ErrNotFound for an unknown job
// and ErrNotDeadLettered for any other status.
func (r *Repo) ResetForReplay(ctx context.Context, id uuid.UUID, outboxPayload []byte) (pipeline.Job, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return pipeline.Job{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var status string
	err = tx.QueryRow(ctx, `SELECT status FROM jobs WHERE id = $1 FOR UPDATE`, id).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return pipeline.Job{}, ErrNotFound
	}
	if err != nil {
		return pipeline.Job{}, err
	}
	if pipeline.Status(status) != pipeline.StatusDeadLettered {
		return pipeline.Job{}, fmt.Errorf("%w: job %s is %s", ErrNotDeadLettered, id, status)
	}

	stage := pipeline.StatusQueued
	var from *string
	err = tx.QueryRow(ctx, `SELECT from_status FROM job_events
		WHERE job_id = $1 AND type = 'transition' AND to_status = 'DEAD_LETTERED'
		ORDER BY id DESC LIMIT 1`, id).Scan(&from)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return pipeline.Job{}, err
	}
	if from != nil {
		for _, s := range nonTerminal {
			if string(s) == *from {
				stage = s
			}
		}
	}

	// Frame bytes are purged 24 h after a terminal status (SRS 7.2), but their
	// rows still read 'captured'. Reset those to 'pending' so the frame stage
	// recaptures them from the video with ffmpeg (Constraint B); frames already
	// uploaded to Notion no longer need their bytes and are left alone.
	if stage == pipeline.StatusExtractingFrames || stage == pipeline.StatusPublishing {
		tag, err := tx.Exec(ctx, `UPDATE job_frames SET status = 'pending', captured_sec = NULL,
				verified = false, warning_code = NULL
			WHERE job_id = $1 AND status = 'captured' AND image_jpeg IS NULL AND notion_file_upload_id IS NULL`, id)
		if err != nil {
			return pipeline.Job{}, fmt.Errorf("reset purged frames: %w", err)
		}
		if tag.RowsAffected() > 0 {
			stage = pipeline.StatusExtractingFrames
		}
	}

	if _, err := tx.Exec(ctx, `UPDATE jobs SET status = $2, progress = $3,
			attempts = 0, rate_limit_retries = 0, deferrals = 0,
			error_code = NULL, error_message = NULL, finished_at = NULL,
			waiting_reason = NULL, waiting_provider = NULL, next_attempt_at = NULL,
			lease_owner = NULL, lease_until = NULL, updated_at = now()
		WHERE id = $1`, id, string(stage), GlobalProgress(stage, 0)); err != nil {
		return pipeline.Job{}, fmt.Errorf("reset job: %w", err)
	}
	if err := appendEvent(ctx, tx, id, "replay", status, string(stage), "", nil); err != nil {
		return pipeline.Job{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO outbox (job_id, payload) VALUES ($1, $2)`, id, outboxPayload); err != nil {
		return pipeline.Job{}, fmt.Errorf("insert outbox: %w", err)
	}
	job, err := scanJob(tx.QueryRow(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id = $1`, id))
	if err != nil {
		return pipeline.Job{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return pipeline.Job{}, err
	}
	return job, nil
}

// LastReplayAt returns when the job was last replayed; ok is false if never.
func (r *Repo) LastReplayAt(ctx context.Context, id uuid.UUID) (at time.Time, ok bool, err error) {
	var t *time.Time
	if err := r.pool.QueryRow(ctx, `SELECT max(at) FROM job_events WHERE job_id = $1 AND type = 'replay'`, id).Scan(&t); err != nil {
		return time.Time{}, false, fmt.Errorf("last replay: %w", err)
	}
	if t == nil {
		return time.Time{}, false, nil
	}
	return *t, true, nil
}

// PurgeFrameImages clears the stored JPEG bytes of frames whose job reached a
// terminal status more than olderThan ago (SRS §7.2). Frame metadata is kept.
// It returns the number of frames cleared.
func (r *Repo) PurgeFrameImages(ctx context.Context, olderThan time.Duration) (int64, error) {
	tag, err := r.pool.Exec(ctx, `UPDATE job_frames SET image_jpeg = NULL
		WHERE image_jpeg IS NOT NULL AND job_id IN (
			SELECT id FROM jobs WHERE status = ANY($1) AND finished_at < now() - make_interval(secs => $2))`,
		statusStrings(terminalStatuses), olderThan.Seconds())
	if err != nil {
		return 0, fmt.Errorf("purge frame images: %w", err)
	}
	return tag.RowsAffected(), nil
}

var terminalStatuses = []pipeline.Status{
	pipeline.StatusCompleted, pipeline.StatusCompletedWithWarnings,
	pipeline.StatusFailed, pipeline.StatusDeadLettered, pipeline.StatusCancelled,
}
