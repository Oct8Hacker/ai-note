package notion

import (
	"ainote/internal/pipeline"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ErrObjectNotFound indicates that a Notion object was missing or inaccessible.
var ErrObjectNotFound = errors.New("notion object not found")

func unavailable() error { return &pipeline.RetryableError{Code: "NOTION_UNAVAILABLE"} }
func responseError(resp *http.Response, a auth, authorization string) error {
	switch resp.StatusCode {
	case 401, 403:
		return &pipeline.FatalError{Code: "NOTION_UNAUTHORIZED"}
	case 404:
		return ErrObjectNotFound
	case 429:
		delay := 30 * time.Second
		header := resp.Header.Get("Retry-After")
		if n, err := strconv.ParseInt(header, 10, 32); err == nil && n >= 0 {
			delay = time.Duration(n) * time.Second
		} else if t, err := http.ParseTime(header); err == nil {
			delay = max(0, time.Until(t))
		}
		return &pipeline.RateLimitedError{Provider: "notion", RetryAfter: delay}
	case 400:
		var problem struct{ Message string }
		_ = json.NewDecoder(io.LimitReader(resp.Body, 65536)).Decode(&problem)
		for _, s := range []string{authorization, strings.TrimPrefix(authorization, "Basic "), a.token, a.secret, a.code} {
			if s != "" {
				problem.Message = strings.ReplaceAll(problem.Message, s, "[redacted]")
			}
		}
		return &pipeline.RetryableError{Code: "NOTION_BAD_REQUEST", Err: errors.New(problem.Message)}
	default:
		return unavailable()
	}
}
