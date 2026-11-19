package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"ainote/internal/pipeline"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func saveArtifact(ctx context.Context, q execer, id uuid.UUID, kind string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal artifact %s: %w", kind, err)
	}
	return writeCheckpoint(ctx, q, `INSERT INTO job_artifacts (job_id, kind, data) SELECT id, $2, $3 FROM jobs WHERE id=$1 AND status <> 'CANCELLED' FOR NO KEY UPDATE
		ON CONFLICT (job_id, kind) DO UPDATE SET data = EXCLUDED.data, created_at = now()`, id, kind, b)
}

type checkpointStore struct{ r *Repo }

// Checkpoints returns the pipeline.CheckpointStore backed by this repo.
func (r *Repo) Checkpoints() pipeline.CheckpointStore { return checkpointStore{r} }

func (c checkpointStore) SaveArtifact(ctx context.Context, id uuid.UUID, kind string, v any) error {
	return saveArtifact(ctx, c.r.pool, id, kind, v)
}

func (c checkpointStore) LoadArtifact(ctx context.Context, id uuid.UUID, kind string, v any) (bool, error) {
	var b []byte
	err := c.r.pool.QueryRow(ctx, `SELECT data FROM job_artifacts WHERE job_id=$1 AND kind=$2`, id, kind).Scan(&b)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return false, fmt.Errorf("decode artifact %s: %w", kind, err)
	}
	return true, nil
}

func (c checkpointStore) UpsertFrame(ctx context.Context, f pipeline.Frame) error {
	return writeCheckpoint(ctx, c.r.pool, `INSERT INTO job_frames (job_id, idx, requested_sec, captured_sec, section_index,
			caption, reason, status, verified, image_jpeg, notion_file_upload_id, notion_uploaded_at, warning_code)
		SELECT id,$2,$3,$4,$5,$6,$7,$8,$9,$10,NULLIF($11,''),$12,NULLIF($13,'')
		FROM jobs WHERE id=$1 AND status <> 'CANCELLED' FOR NO KEY UPDATE
		ON CONFLICT (job_id, idx) DO UPDATE SET requested_sec=EXCLUDED.requested_sec,
			captured_sec=EXCLUDED.captured_sec, section_index=EXCLUDED.section_index,
			caption=EXCLUDED.caption, reason=EXCLUDED.reason, status=EXCLUDED.status,
			verified=EXCLUDED.verified, image_jpeg=EXCLUDED.image_jpeg,
			notion_file_upload_id=EXCLUDED.notion_file_upload_id,
			notion_uploaded_at=EXCLUDED.notion_uploaded_at, warning_code=EXCLUDED.warning_code`,
		f.JobID, f.Idx, f.RequestedSec, f.CapturedSec, f.SectionIndex, f.Caption, f.Reason,
		string(f.Status), f.Verified, f.ImageJPEG, f.NotionFileUploadID, f.NotionUploadedAt, f.WarningCode)
}

func (c checkpointStore) ListFrames(ctx context.Context, id uuid.UUID) ([]pipeline.Frame, error) {
	rows, err := c.r.pool.Query(ctx, `SELECT job_id, idx, requested_sec::float8, captured_sec::float8, section_index,
			caption, reason, status, verified, image_jpeg, notion_file_upload_id, notion_uploaded_at, warning_code
		FROM job_frames WHERE job_id=$1 ORDER BY idx`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []pipeline.Frame
	for rows.Next() {
		var (
			f          pipeline.Frame
			status     string
			upID, warn *string
		)
		if err := rows.Scan(&f.JobID, &f.Idx, &f.RequestedSec, &f.CapturedSec, &f.SectionIndex,
			&f.Caption, &f.Reason, &status, &f.Verified, &f.ImageJPEG, &upID, &f.NotionUploadedAt, &warn); err != nil {
			return nil, err
		}
		f.Status = pipeline.FrameStatus(status)
		f.NotionFileUploadID, f.WarningCode = deref(upID), deref(warn)
		out = append(out, f)
	}
	return out, rows.Err()
}

// Checkpoint writes and cancellation lock the same job row. A stale stage
// gets an error even when it only writes an artifact or frame in a child table.
func writeCheckpoint(ctx context.Context, q execer, sql string, args ...any) error {
	tag, err := q.Exec(ctx, sql, args...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrIllegalTransition
	}
	return nil
}

func (c checkpointStore) SetVideoMeta(ctx context.Context, id uuid.UUID, title string, durationSec int) error {
	return writeCheckpoint(ctx, c.r.pool, `UPDATE jobs SET video_title=$2, duration_sec=$3, updated_at=now() WHERE id=$1 AND status <> 'CANCELLED'`, id, title, durationSec)
}

func (c checkpointStore) SetNotionParent(ctx context.Context, id uuid.UUID, resolved, kind, titleProp string) error {
	return writeCheckpoint(ctx, c.r.pool, `UPDATE jobs SET notion_resolved_parent=$2, notion_parent_kind=$3, notion_title_prop=NULLIF($4,''),
		updated_at=now() WHERE id=$1 AND status <> 'CANCELLED'`, id, resolved, kind, titleProp)
}

func (c checkpointStore) MarkNotionCreateStarted(ctx context.Context, id uuid.UUID) error {
	return writeCheckpoint(ctx, c.r.pool, `UPDATE jobs SET notion_create_started_at=COALESCE(notion_create_started_at, now()), updated_at=now()
		WHERE id=$1 AND status='PUBLISHING'`, id)
}

func (c checkpointStore) SetNotionPage(ctx context.Context, id uuid.UUID, pageID, url string) error {
	return writeCheckpoint(ctx, c.r.pool, `UPDATE jobs SET notion_page_id=$2, notion_page_url=$3, updated_at=now() WHERE id=$1 AND status <> 'CANCELLED'`, id, pageID, url)
}

func (c checkpointStore) Warnings(ctx context.Context, id uuid.UUID) ([]pipeline.Warning, error) {
	var b []byte
	err := c.r.pool.QueryRow(ctx, `SELECT warnings FROM jobs WHERE id=$1`, id).Scan(&b)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var w []pipeline.Warning
	if err := json.Unmarshal(b, &w); err != nil {
		return nil, fmt.Errorf("decode warnings: %w", err)
	}
	return w, nil
}
