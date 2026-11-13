package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"ainote/internal/config"
	"ainote/internal/pipeline/pipelinetest"

	"github.com/stretchr/testify/require"
)

func TestFactoryProviders(t *testing.T) {
	for _, tc := range []struct{ name, base, key string }{
		{"gemini", "", ""},
		{"ollama", "", ""},
		{"groq", "https://api.groq.com/openai/v1", "groq-test-key"},
		{"openrouter", "https://openrouter.ai/api/v1", "router-test-key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Worker{LLMProvider: tc.name, LLMModel: "test-model", GeminiAPIKey: "gemini-test-key", GroqAPIKey: "groq-test-key", OpenRouterAPIKey: "router-test-key", LLMTimeout: time.Second}
			text, _, err := FromConfig(cfg, nil, nil, nil)
			require.NoError(t, err)
			require.Equal(t, tc.name, text.Name())
			if tc.base != "" {
				adapter := text.(*Guarded).Primary.(*compat)
				require.Equal(t, tc.base, adapter.base)
				require.Equal(t, tc.key, adapter.key)
				require.Equal(t, cfg.LLMModel, adapter.model)
				require.Equal(t, cfg.LLMTimeout, adapter.client.Timeout)
			}
		})
	}
	for setting, cfg := range map[string]config.Worker{
		"LLM_PROVIDER":          {LLMProvider: "unknown"},
		"LLM_VISION_PROVIDER":   {LLMProvider: "ollama", LLMVisionProvider: "unknown"},
		"LLM_FALLBACK_PROVIDER": {LLMProvider: "ollama", LLMFallbackProvider: "unknown"},
	} {
		t.Run(setting, func(t *testing.T) {
			_, _, err := FromConfig(cfg, nil, nil, nil)
			require.ErrorContains(t, err, setting)
		})
	}
}

func TestVisionFallsBackToText(t *testing.T) {
	cfg := config.Worker{LLMProvider: "groq", LLMModel: "text"}
	text, vision, err := FromConfig(cfg, nil, nil, nil)
	require.NoError(t, err)
	require.Same(t, text, vision)
	cfg.LLMVisionModel = "vision"
	text, vision, err = FromConfig(cfg, nil, nil, nil)
	require.NoError(t, err)
	require.NotSame(t, text, vision)
	require.Equal(t, "groq", vision.Name())
	require.Equal(t, "vision", vision.(*Guarded).Primary.(*compat).model)
}

func TestFallbackConfigured(t *testing.T) {
	cfg := config.Worker{LLMProvider: "openrouter", LLMModel: "primary", LLMFallbackProvider: "groq", LLMFallbackModel: "backup", GroqAPIKey: "groq-test-key", LLMTimeout: time.Second}
	text, _, err := FromConfig(cfg, pipelinetest.NoopLimiter{}, pipelinetest.NoFaults{}, nil)
	require.NoError(t, err)
	guard := text.(*Guarded)
	require.NotNil(t, guard.Fallback)
	require.Equal(t, "groq", guard.Fallback.Name())
	primaryCalls, backupCalls := 0, 0
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		primaryCalls++
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer primary.Close()
	backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backupCalls++
		if r.URL.Path != "/chat/completions" || r.Header.Get("Authorization") != "Bearer groq-test-key" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = fmt.Fprint(w, `{"choices":[{"message":{"content":"{}"}}]}`)
	}))
	defer backup.Close()
	guard.Primary.(*compat).base = primary.URL
	guard.Fallback.(*compat).base = backup.URL
	out, err := text.GenerateJSON(context.Background(), JSONRequest{Prompt: "Return JSON"})
	require.NoError(t, err)
	require.Equal(t, "{}", out.Text)
	require.Equal(t, 2, primaryCalls)
	require.Equal(t, 1, backupCalls)
}
