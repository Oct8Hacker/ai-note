// Package queue holds the RabbitMQ message contract shared by gateway and workers.
package queue

import (
	"time"

	"github.com/google/uuid"
)

// Message is the body of a jobs.work message. The Notion token travels only
// in sealed form.
type Message struct {
	V              int       `json:"v"`
	JobID          uuid.UUID `json:"job_id"`
	NotionTokenEnc string    `json:"notion_token_enc"`
	EnqueuedAt     time.Time `json:"enqueued_at"`
	Reason         string    `json:"reason"`
}
