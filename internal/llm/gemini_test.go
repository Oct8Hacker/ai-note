package llm

import (
	"ainote/internal/pipeline"
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGeminiJSONMode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-goog-api-key") != "test-key" {
			t.Error("missing Gemini API key")
		}
		var b struct {
			GenerationConfig struct {
				ResponseMimeType   string
				ResponseJSONSchema map[string]any
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil || b.GenerationConfig.ResponseMimeType != "application/json" || b.GenerationConfig.ResponseJSONSchema["type"] != "object" {
			t.Errorf("generation config: %+v, %v", b, err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"candidates":[{"content":{"parts":[{"text":"{}"}]}}]}`)
	}))
	defer srv.Close()
	p, err := newGemini("test-key", "test-model", time.Second, srv.URL)
	require.NoError(t, err)
	out, err := p.GenerateJSON(context.Background(), JSONRequest{Prompt: "test", Schema: map[string]any{"type": "object"}})
	require.NoError(t, err)
	require.Equal(t, "{}", out.Text)
}

// Gemini Flash models think before answering, thinking cannot be turned off
// on gemini-3.8-flash, and thought tokens count against maxOutputTokens. The
// caller's budget is for the visible JSON, so the provider adds headroom.
func TestGeminiReservesThinkingHeadroom(t *testing.T) {
	var got []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			GenerationConfig struct {
				MaxOutputTokens int
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
			t.Errorf("decode request: %v", err)
		}
		got = append(got, b.GenerationConfig.MaxOutputTokens)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"candidates":[{"content":{"parts":[{"text":"{}"}]}}]}`)
	}))
	defer srv.Close()
	p, err := newGemini("test-key", "test-model", time.Second, srv.URL)
	require.NoError(t, err)

	_, err = p.GenerateJSON(context.Background(), JSONRequest{Prompt: "notes", MaxOutputTokens: 2048})
	require.NoError(t, err)
	_, err = p.GenerateJSON(context.Background(), JSONRequest{Prompt: "no cap"})
	require.NoError(t, err)

	require.Equal(t, []int{2048 + geminiThinkingHeadroom, 0}, got, "visible budget + headroom; no cap stays uncapped")
}

func TestGeminiRetryInfo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"status":"RESOURCE_EXHAUSTED","details":[{"retryDelay":"7s"}]}}`)
	}))
	defer srv.Close()
	p, err := newGemini("test-key", "test-model", time.Second, srv.URL)
	require.NoError(t, err)
	_, err = p.GenerateJSON(context.Background(), JSONRequest{Prompt: "hello"})
	var rate *pipeline.RateLimitedError
	require.ErrorAs(t, err, &rate)
	require.Equal(t, 7*time.Second, rate.RetryAfter)
}
