package jobs

import (
	"time"

	"ainote/internal/pipeline"

	"github.com/google/uuid"
)

// Waiting describes a scheduled retry.
type Waiting struct {
	Reason        string    `json:"reason"`
	Provider      string    `json:"provider,omitempty"`
	NextAttemptAt time.Time `json:"next_attempt_at"`
}

// ErrorBody is the terminal failure detail.
type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Result is the outcome of a completed job.
type Result struct {
	NotionPageURL   string `json:"notion_page_url"`
	FramesRequested int    `json:"frames_requested"`
	FramesPublished int    `json:"frames_published"`
}

// JobView is the public JSON shape of GET /jobs/{id}.
type JobView struct {
	JobID       uuid.UUID          `json:"job_id"`
	Status      pipeline.Status    `json:"status"`
	Progress    int                `json:"progress"`
	VideoID     string             `json:"video_id"`
	VideoTitle  *string            `json:"video_title"`
	DurationSec *int               `json:"duration_sec"`
	Warnings    []pipeline.Warning `json:"warnings"`
	Error       *ErrorBody         `json:"error"`
	Result      *Result            `json:"result"`
	Waiting     *Waiting           `json:"waiting"`
	Attempt     int                `json:"attempt"`
	CreatedAt   time.Time          `json:"created_at"`
	UpdatedAt   time.Time          `json:"updated_at"`
}
