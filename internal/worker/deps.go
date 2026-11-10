package worker

import (
	"context"
	"log/slog"

	"ainote/internal/pipeline"
)

// buildDeps assembles the collaborators handed to one stage run. The token is
// the decrypted Notion token and is non-empty only for PUBLISHING. Progress
// and warning writes are best effort: a failure is logged, never fatal.
func (rt *Runtime) buildDeps(ctx context.Context, job pipeline.Job, token string, log *slog.Logger) pipeline.Deps {
	stage, id := job.Status, job.ID
	return pipeline.Deps{
		Store:   rt.repo.Checkpoints(),
		Limiter: rt.limiter,
		Faults:  rt.faults,
		Progress: func(p int) {
			if err := rt.repo.SetProgress(ctx, id, stage, p); err != nil {
				log.Warn("set progress failed", "err", err)
			}
		},
		Warn: func(w pipeline.Warning) {
			if err := rt.repo.AddWarning(ctx, id, w); err != nil {
				log.Warn("add warning failed", "err", err, "code", w.Code)
			}
		},
		NotionToken: token,
		Log:         log,
	}
}
