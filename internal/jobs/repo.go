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
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned for unknown job IDs.
var ErrNotFound = errors.New("job not found")

// Repo is the job repository.
type Repo struct{ pool *pgxpool.Pool }

// NewRepo returns a Repo backed by pool.
func NewRepo(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

// NewJob is the input to CreateJob.
type NewJob struct {
	ID                                                  uuid.UUID
	VideoID, YouTubeURL, NotionParentID, IdempotencyKey string
}

const jobColumns = `id, video_id, youtube_url, status, video_title, duration_sec,
	notion_parent_id, notion_resolved_parent, notion_parent_kind, notion_title_prop,
	notion_page_id, notion_page_url, notion_create_started_at, attempts`

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func scanJob(row pgx.Row) (pipeline.Job, error) {
	var (
		j                                                    pipeline.Job
		status                                               string
		title, parent, resolved, kind, prop, pageID, pageURL *string
		dur                                                  *int
	)
	err := row.Scan(&j.ID, &j.VideoID, &j.YouTubeURL, &status, &title, &dur,
		&parent, &resolved, &kind, &prop, &pageID, &pageURL, &j.NotionCreateStartedAt, &j.Attempts)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return j, ErrNotFound
		}
		return j, err
	}
	j.Status = pipeline.Status(status)
	j.VideoTitle = deref(title)
	if dur != nil {
		j.DurationSec = *dur
	}
	j.NotionParentID, j.NotionResolvedParent = deref(parent), deref(resolved)
	j.NotionParentKind, j.NotionTitleProp = deref(kind), deref(prop)
	j.NotionPageID, j.NotionPageURL = deref(pageID), deref(pageURL)
	return j, nil
}

// CreateJob inserts a QUEUED job and its outbox row in one transaction. When
// the idempotency key already exists, the existing job is returned with
// created=false and no outbox row is written.
func (r *Repo) CreateJob(ctx context.Context, in NewJob, outboxPayload []byte) (pipeline.Job, bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return pipeline.Job{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var idem, parent *string
	if in.IdempotencyKey != "" {
		idem = &in.IdempotencyKey
	}
	if in.NotionParentID != "" {
		parent = &in.NotionParentID
	}
	var gotID uuid.UUID
	err = tx.QueryRow(ctx, `INSERT INTO jobs (id, video_id, youtube_url, status, progress, notion_parent_id, idempotency_key)
		VALUES ($1,$2,$3,'QUEUED',0,$4,$5)
		ON CONFLICT (idempotency_key) DO NOTHING RETURNING id`,
		in.ID, in.VideoID, in.YouTubeURL, parent, idem).Scan(&gotID)
	if errors.Is(err, pgx.ErrNoRows) {
		if idem == nil {
			return pipeline.Job{}, false, fmt.Errorf("insert job: unexpected conflict")
		}
		job, err := scanJob(tx.QueryRow(ctx, `SELECT `+jobColumns+` FROM jobs WHERE idempotency_key=$1`, *idem))
		return job, false, err
	}
	if err != nil {
		return pipeline.Job{}, false, fmt.Errorf("insert job: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO outbox (job_id, payload) VALUES ($1,$2)`, gotID, outboxPayload); err != nil {
		return pipeline.Job{}, false, fmt.Errorf("insert outbox: %w", err)
	}
	job, err := scanJob(tx.QueryRow(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id=$1`, gotID))
	if err != nil {
		return pipeline.Job{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return pipeline.Job{}, false, err
	}
	return job, true, nil
}

// GetJob loads a job by ID.
func (r *Repo) GetJob(ctx context.Context, id uuid.UUID) (pipeline.Job, error) {
	return scanJob(r.pool.QueryRow(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id=$1`, id))
}

// GetJobView builds the public view of a job. It never writes.
func (r *Repo) GetJobView(ctx context.Context, id uuid.UUID) (JobView, error) {
	var (
		v                        JobView
		status                   string
		warnings                 []byte
		pageURL, errCode, errMsg *string
		waitReason, waitProvider *string
		framesReq, framesPub     *int
		nextAttempt              *time.Time
		stillWaiting             bool
	)
	err := r.pool.QueryRow(ctx, `SELECT id, status, progress, video_id, video_title, duration_sec, warnings,
		error_code, error_message, notion_page_url, frames_requested, frames_published,
		waiting_reason, waiting_provider, next_attempt_at, COALESCE(next_attempt_at > now(), false),
		attempts, created_at, updated_at, `+canCancelSQL+` FROM jobs WHERE id=$1`, id, statusStrings(nonTerminal)).
		Scan(&v.JobID, &status, &v.Progress, &v.VideoID, &v.VideoTitle, &v.DurationSec, &warnings,
			&errCode, &errMsg, &pageURL, &framesReq, &framesPub,
			&waitReason, &waitProvider, &nextAttempt, &stillWaiting,
			&v.Attempt, &v.CreatedAt, &v.UpdatedAt, &v.CanCancel)
	if errors.Is(err, pgx.ErrNoRows) {
		return JobView{}, ErrNotFound
	}
	if err != nil {
		return JobView{}, err
	}
	v.Status = pipeline.Status(status)
	v.Warnings = []pipeline.Warning{}
	if len(warnings) > 0 {
		if err := json.Unmarshal(warnings, &v.Warnings); err != nil {
			return JobView{}, fmt.Errorf("decode warnings: %w", err)
		}
		if v.Warnings == nil {
			v.Warnings = []pipeline.Warning{}
		}
	}
	switch v.Status {
	case pipeline.StatusCompleted, pipeline.StatusCompletedWithWarnings:
		res := &Result{NotionPageURL: deref(pageURL)}
		if framesReq != nil {
			res.FramesRequested = *framesReq
		}
		if framesPub != nil {
			res.FramesPublished = *framesPub
		}
		v.Result = res
	case pipeline.StatusFailed, pipeline.StatusDeadLettered:
		v.Error = &ErrorBody{Code: deref(errCode), Message: deref(errMsg)}
	}
	if stillWaiting && nextAttempt != nil {
		v.Waiting = &Waiting{Reason: deref(waitReason), Provider: deref(waitProvider), NextAttemptAt: *nextAttempt}
	}
	return v, nil
}
