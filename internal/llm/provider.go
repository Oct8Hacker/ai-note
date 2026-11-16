// Package llm provides structured generation and shared quota controls.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"ainote/internal/pipeline"
)

// Image supplies a labeled JPEG to a multimodal request.
type Image struct {
	Label string
	JPEG  []byte
}

// JSONRequest describes a structured generation request.
type JSONRequest struct {
	System, Prompt  string
	Images          []Image
	Schema          map[string]any
	MaxOutputTokens int
}

// JSONResponse contains generated text and provider-reported usage.
type JSONResponse struct {
	Text                      string
	InputTokens, OutputTokens int
}

// Provider supports generation and a generation-free model check.
type Provider interface {
	Name() string
	GenerateJSON(context.Context, JSONRequest) (JSONResponse, error)
	SupportsImages() bool
	Ping(context.Context) error
}

// providerTransport classifies errors before SDKs discard HTTP headers.
type providerTransport struct{}

func (providerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 400 {
		return resp, nil
	}
	defer func() { _ = resp.Body.Close() }()
	var detail struct {
		Error struct {
			Status  string
			Details []struct{ RetryDelay string }
		}
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 65536)).Decode(&detail)
	if resp.StatusCode == 429 || detail.Error.Status == "RESOURCE_EXHAUSTED" {
		delay := 30 * time.Second
		for _, d := range detail.Error.Details {
			if parsed, err := time.ParseDuration(d.RetryDelay); err == nil && parsed >= 0 {
				delay = parsed
				break
			}
		}
		header := resp.Header.Get("Retry-After")
		if seconds, err := strconv.ParseInt(header, 10, 32); err == nil && seconds >= 0 {
			delay = time.Duration(seconds) * time.Second
		} else if date, err := http.ParseTime(header); err == nil {
			delay = max(time.Until(date), 0)
		}
		return nil, &pipeline.RateLimitedError{Provider: "llm", RetryAfter: delay}
	}
	// 404 is retried too: Gemini returned it under load for a model that
	// exists. A misspelt LLM_MODEL is caught at startup by the doctor's Ping.
	if resp.StatusCode >= 500 || resp.StatusCode == 408 || resp.StatusCode == 404 {
		return nil, &pipeline.RetryableError{Code: "LLM_UNAVAILABLE", Err: fmt.Errorf("provider HTTP %d", resp.StatusCode)}
	}
	return nil, &pipeline.FatalError{Code: "LLM_CONFIG_ERROR", Msg: fmt.Sprintf("provider HTTP %d", resp.StatusCode)}
}
func providerError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var rate *pipeline.RateLimitedError
	var fatal *pipeline.FatalError
	var retry *pipeline.RetryableError
	switch {
	case errors.As(err, &rate):
		return rate
	case errors.As(err, &fatal):
		return fatal
	case errors.As(err, &retry):
		return retry
	}
	return &pipeline.RetryableError{Code: "LLM_UNAVAILABLE", Err: errors.New("provider request failed")}
}
func httpClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: providerTransport{}}
}

type endpoint struct {
	name, base, key, model string
	client                 *http.Client
}

func (e endpoint) Name() string { return e.name }
func (e endpoint) call(ctx context.Context, method, path string, body, out any) error {
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(e.base, "/")+path, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if e.key != "" {
		req.Header.Set("Authorization", "Bearer "+e.key)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return providerError(ctx, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
