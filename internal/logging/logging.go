// Package logging builds the structured JSON logger with secret redaction.
package logging

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

var redactedKeys = map[string]bool{
	"notion_token": true, "token": true, "authorization": true, "api_key": true, "client_secret": true,
}

// New returns a JSON logger tagged with the component name (FR-ADM-5).
func New(level, component string) *slog.Logger {
	return newWithWriter(os.Stdout, level, component)
}

func newWithWriter(w io.Writer, level, component string) *slog.Logger {
	var lv slog.Level
	if err := lv.UnmarshalText([]byte(level)); err != nil {
		lv = slog.LevelInfo
	}
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level: lv,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if redactedKeys[strings.ToLower(a.Key)] {
				return slog.String(a.Key, "[REDACTED]")
			}
			return a
		},
	})
	return slog.New(h).With("component", component)
}
