package jobs

import (
	"errors"

	"ainote/internal/pipeline"
)

// ErrIllegalTransition is returned when a conditional status update matches no row.
var ErrIllegalTransition = errors.New("illegal transition")

var nonTerminal = []pipeline.Status{
	pipeline.StatusQueued, pipeline.StatusTranscribing, pipeline.StatusSummarizing,
	pipeline.StatusExtractingFrames, pipeline.StatusPublishing,
}

// AllowedFrom returns the statuses from which a job may move to `to` (SRS §4.2).
func AllowedFrom(to pipeline.Status) []pipeline.Status {
	switch to {
	case pipeline.StatusTranscribing:
		return []pipeline.Status{pipeline.StatusQueued}
	case pipeline.StatusSummarizing:
		return []pipeline.Status{pipeline.StatusTranscribing}
	case pipeline.StatusExtractingFrames:
		return []pipeline.Status{pipeline.StatusSummarizing}
	case pipeline.StatusPublishing:
		return []pipeline.Status{pipeline.StatusExtractingFrames}
	case pipeline.StatusCompleted, pipeline.StatusCompletedWithWarnings:
		return []pipeline.Status{pipeline.StatusPublishing}
	case pipeline.StatusFailed, pipeline.StatusDeadLettered:
		return append([]pipeline.Status(nil), nonTerminal...)
	}
	return nil
}

func statusStrings(ss []pipeline.Status) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = string(s)
	}
	return out
}
