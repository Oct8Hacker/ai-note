// Package youtube retrieves video metadata, captions and stream URLs using yt-dlp.
package youtube

import (
	"ainote/internal/pipeline"
	"strings"
	"time"
)

// ClassifyYtDlpError translates yt-dlp diagnostics into worker retry decisions.
func ClassifyYtDlpError(stderr string, runErr error) error {
	if runErr == nil {
		return nil
	}
	code := "YTDLP_FAILED"
	switch {
	case strings.Contains(stderr, "HTTP Error 429"):
		return &pipeline.RateLimitedError{Provider: "youtube", RetryAfter: 30 * time.Second}
	case strings.Contains(stderr, "Sign in to confirm you"):
		code = "YOUTUBE_BOT_CHECK"
	case strings.Contains(stderr, "Private video"), strings.Contains(stderr, "Video unavailable"), strings.Contains(stderr, "This video is not available"):
		code = "VIDEO_UNAVAILABLE"
	}
	return &pipeline.RetryableError{Code: code, Err: runErr}
}
