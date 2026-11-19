package youtube

import (
	"ainote/internal/pipeline"
	"errors"
	"os"
	"testing"
	"time"
)

func TestClassifyYtDlpError(t *testing.T) {
	for file, code := range map[string]string{"429": "RATE_LIMITED", "bot": "YOUTUBE_BOT_CHECK", "private": "VIDEO_UNAVAILABLE", "unavailable": "VIDEO_UNAVAILABLE", "generic": "YTDLP_FAILED"} {
		stderr := []byte("network failure")
		if file != "generic" {
			var err error
			stderr, err = os.ReadFile("testdata/stderr_" + file + ".txt") // #nosec G304 -- fixture names come from the fixed test table.
			if err != nil {
				t.Fatal(err)
			}
		}
		err := ClassifyYtDlpError(string(stderr), errors.New("exit status 1"))
		if pipeline.ErrorCode(err) != code {
			t.Fatalf("%s: %v", file, err)
		}
		if file == "429" {
			var rl *pipeline.RateLimitedError
			if !errors.As(err, &rl) || rl.Provider != "youtube" || rl.RetryAfter != 30*time.Second {
				t.Fatal(err)
			}
		}
	}
}
