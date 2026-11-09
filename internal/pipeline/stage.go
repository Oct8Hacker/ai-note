package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

// Stage is one step of the job pipeline; Name doubles as the job status while it runs.
type Stage interface {
	Name() Status
	Run(ctx context.Context, job *Job, deps Deps) (StageResult, error)
}

// StageResult carries a stage's output artifact and metadata back to the runner.
type StageResult struct {
	ArtifactKind    string // "transcript" | "synthesis" | ""
	Artifact        any
	VideoTitle      *string
	DurationSec     *int
	FramesRequested *int
	FramesPublished *int
	PageURL         string
}

// Deps bundles the collaborators handed to every stage run.
type Deps struct {
	Store    CheckpointStore
	Limiter  Limiter
	Faults   Faults
	Progress func(stagePct int) // 0..100 within the stage
	Warn     func(Warning)      // deduplicated by (code, timestamp) downstream
	// NotionToken is decrypted; non-empty only for PUBLISHING.
	NotionToken string
	Log         *slog.Logger
}

// CheckpointStore persists stage artifacts, frames and Notion state so a retried job can resume.
type CheckpointStore interface {
	SaveArtifact(ctx context.Context, jobID uuid.UUID, kind string, v any) error
	LoadArtifact(ctx context.Context, jobID uuid.UUID, kind string, v any) (bool, error)
	UpsertFrame(ctx context.Context, f Frame) error
	ListFrames(ctx context.Context, jobID uuid.UUID) ([]Frame, error)
	SetVideoMeta(ctx context.Context, jobID uuid.UUID, title string, durationSec int) error
	SetNotionParent(ctx context.Context, jobID uuid.UUID, resolved, kind, titleProp string) error
	MarkNotionCreateStarted(ctx context.Context, jobID uuid.UUID) error
	SetNotionPage(ctx context.Context, jobID uuid.UUID, pageID, url string) error
	Warnings(ctx context.Context, jobID uuid.UUID) ([]Warning, error)
}

// Limiter returns *RateLimitedError if the wait would exceed the maximum.
type Limiter interface {
	Acquire(ctx context.Context, bucket string, n int) error
}

// FaultPoint names a place where a Faults implementation may inject a failure.
type FaultPoint string

// Fault points, one per external dependency plus per-frame capture.
const (
	FaultLLM     FaultPoint = "llm"
	FaultNotion  FaultPoint = "notion"
	FaultYouTube FaultPoint = "youtube"
	FaultFrame   FaultPoint = "frame"
)

// Faults lets tests and chaos runs inject failures at defined FaultPoints.
type Faults interface {
	// Check returns *RateLimitedError for llm/notion/youtube, ErrInjectedFrameFailure for frame.
	Check(ctx context.Context, p FaultPoint) error
	FrameDelay(ctx context.Context) time.Duration
}

// ErrInjectedFrameFailure is returned by Faults.Check at FaultFrame to simulate a failed frame capture.
var ErrInjectedFrameFailure = errors.New("injected frame failure")

// Rate-limit bucket names passed to Limiter.Acquire.
const (
	BucketLLMRequests         = "llm:requests"
	BucketLLMTokens           = "llm:tokens"
	BucketLLMDaily            = "llm:daily"
	BucketLLMFallbackRequests = "llm-fallback:requests"
	BucketYouTube             = "youtube"
)

// NotionBucket returns "notion:" + first 16 hex chars of sha256(token).
func NotionBucket(token string) string {
	sum := sha256.Sum256([]byte(token))
	return "notion:" + hex.EncodeToString(sum[:])[:16]
}
