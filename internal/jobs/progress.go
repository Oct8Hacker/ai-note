package jobs

import (
	"context"

	"ainote/internal/pipeline"

	"github.com/google/uuid"
)

// GlobalProgress maps a stage-local percentage (0..100) to the global job
// progress range of the status (SRS FR-SM-6).
func GlobalProgress(s pipeline.Status, stagePct int) int {
	if s.IsTerminal() {
		return 100
	}
	var lo, hi int
	switch s {
	case pipeline.StatusTranscribing:
		lo, hi = 5, 20
	case pipeline.StatusSummarizing:
		lo, hi = 20, 50
	case pipeline.StatusExtractingFrames:
		lo, hi = 50, 80
	case pipeline.StatusPublishing:
		lo, hi = 80, 99
	default:
		return 0
	}
	stagePct = min(max(stagePct, 0), 100)
	return lo + (hi-lo)*stagePct/100
}

// SetProgress raises progress to the global value for (status, stagePct); it
// never lowers it. It only applies while the job is still in that status.
func (r *Repo) SetProgress(ctx context.Context, id uuid.UUID, status pipeline.Status, stagePct int) error {
	_, err := r.pool.Exec(ctx, `UPDATE jobs SET progress = GREATEST(progress, $3), updated_at = now()
		WHERE id = $1 AND status = $2`, id, string(status), GlobalProgress(status, stagePct))
	return err
}
