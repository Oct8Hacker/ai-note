package config

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validKey() string { return base64.StdEncoding.EncodeToString(make([]byte, 32)) }

func workerEnv(t *testing.T) {
	clearAmbient(t)
	t.Helper()
	t.Setenv("TOKEN_ENC_KEY", validKey())
	t.Setenv("GEMINI_API_KEY", "k")
}

func TestLoadWorkerDefaults(t *testing.T) {
	workerEnv(t)
	w, err := LoadWorker()
	require.NoError(t, err)
	assert.Equal(t, 5, w.MaxAttempts)
	assert.Equal(t, []time.Duration{10 * time.Second, 30 * time.Second, 90 * time.Second, 270 * time.Second}, w.RetryDelays)
	assert.Equal(t, 1200, w.MaxVideoSeconds)
	assert.Equal(t, 8, w.LLMRPM)
	assert.Equal(t, 200, w.LLMRPD)
	assert.Equal(t, 3*time.Second, w.DeferDelay)
	assert.Equal(t, 3.0, w.MaxFrameDriftSec)
	assert.Equal(t, "gemini", w.LLMProvider)
	assert.Equal(t, "gemini-2.5-flash", w.LLMModel)
	assert.Equal(t, "gemini", w.LLMVisionProvider)
	assert.Equal(t, "gemini-2.5-flash", w.LLMVisionModel)
	assert.Equal(t, 2*time.Second, w.HeartbeatInterval)
	assert.Equal(t, 6*time.Second, w.HeartbeatTTL)
	assert.Equal(t, 60*time.Second, w.DoctorInterval)
	assert.Equal(t, 0.0, w.Faults.LLM429Rate)
	assert.False(t, w.Faults.FrameFailAll)
}

func TestMissingTokenKeyFails(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "k")
	t.Setenv("TOKEN_ENC_KEY", "")
	_, err := LoadWorker()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "TOKEN_ENC_KEY")
}

func TestTokenKeyMustBe32Bytes(t *testing.T) {
	workerEnv(t)
	t.Setenv("TOKEN_ENC_KEY", base64.StdEncoding.EncodeToString(make([]byte, 16)))
	_, err := LoadWorker()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "TOKEN_ENC_KEY")

	t.Setenv("TOKEN_ENC_KEY", "!!!not-base64")
	_, err = LoadWorker()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "TOKEN_ENC_KEY")
}

func TestGeminiKeyRequiredOnlyForGemini(t *testing.T) {
	clearAmbient(t)
	t.Setenv("TOKEN_ENC_KEY", validKey())
	t.Setenv("GEMINI_API_KEY", "")
	_, err := LoadWorker()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GEMINI_API_KEY")

	t.Setenv("LLM_PROVIDER", "ollama")
	w, err := LoadWorker()
	require.NoError(t, err)
	assert.Equal(t, "llama3.2", w.LLMModel)
}

func TestBadRetryDelays(t *testing.T) {
	workerEnv(t)
	t.Setenv("RETRY_DELAYS", "10s,banana")
	_, err := LoadWorker()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "RETRY_DELAYS")

	t.Setenv("RETRY_DELAYS", "10s,-5s")
	_, err = LoadWorker()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "RETRY_DELAYS")
}

func TestParseDurations(t *testing.T) {
	d, err := ParseDurations(" 1s, 2m ")
	require.NoError(t, err)
	assert.Equal(t, []time.Duration{time.Second, 2 * time.Minute}, d)
	_, err = ParseDurations("")
	assert.Error(t, err)
}

func TestProviderRules(t *testing.T) {
	workerEnv(t)
	t.Setenv("LLM_PROVIDER", "bogus")
	_, err := LoadWorker()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "LLM_PROVIDER")

	t.Setenv("LLM_PROVIDER", "openrouter")
	t.Setenv("OPENROUTER_API_KEY", "k")
	_, err = LoadWorker()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "LLM_MODEL")

	t.Setenv("LLM_MODEL", "m")
	w, err := LoadWorker()
	require.NoError(t, err)
	assert.Equal(t, "openrouter", w.LLMVisionProvider)
	assert.Equal(t, "m", w.LLMVisionModel)

	t.Setenv("LLM_FALLBACK_PROVIDER", "groq")
	_, err = LoadWorker()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GROQ_API_KEY")
	t.Setenv("GROQ_API_KEY", "g")
	w, err = LoadWorker()
	require.NoError(t, err)
	assert.Equal(t, "llama-3.3-70b-versatile", w.LLMFallbackModel)
}

func TestWorkerRanges(t *testing.T) {
	cases := map[string]string{
		"MAX_ATTEMPTS":       "0",
		"MAX_VIDEO_SECONDS":  "0",
		"MAX_FRAMES":         "-1",
		"FAULT_LLM_429_RATE": "1.5",
		"LLM_RPD_RESET_TZ":   "Mars/Base",
		"FRAME_CONCURRENCY":  "0",
		"WORKER_CONCURRENCY": "0",
		"LLM_RPM":            "-1",
	}
	for k, v := range cases {
		t.Run(k, func(t *testing.T) {
			workerEnv(t)
			t.Setenv(k, v)
			_, err := LoadWorker()
			require.Error(t, err)
			assert.Contains(t, err.Error(), k)
		})
	}
}

func TestFaultEnv(t *testing.T) {
	workerEnv(t)
	t.Setenv("FAULT_FRAME_FAIL_ALL", "true")
	t.Setenv("FAULT_STAGE_DELAY_MS", "1500")
	t.Setenv("FAULT_NOTION_429_RATE", "0.5")
	w, err := LoadWorker()
	require.NoError(t, err)
	assert.True(t, w.Faults.FrameFailAll)
	assert.Equal(t, 1500, w.Faults.StageDelayMs)
	assert.Equal(t, 0.5, w.Faults.Notion429Rate)
}

func gatewayEnv(t *testing.T) {
	clearAmbient(t)
	t.Helper()
	t.Setenv("TOKEN_ENC_KEY", validKey())
	t.Setenv("ADMIN_TOKEN", "admin")
}

func TestLoadGatewayDefaults(t *testing.T) {
	gatewayEnv(t)
	g, err := LoadGateway()
	require.NoError(t, err)
	assert.Equal(t, 8080, g.Port)
	assert.Equal(t, "http://localhost:8080", g.PublicBaseURL)
	assert.Equal(t, 10, g.DeliveryLimit)
	assert.Equal(t, 2*time.Second, g.OutboxSweepInterval)
	assert.Equal(t, "http://rabbitmq:15672", g.RabbitMgmtURL)
	assert.Equal(t, "guest", g.RabbitUser)
	assert.Equal(t, 10*time.Second, g.AMQPHeartbeat)
	assert.Equal(t, 2.5, g.NotionRPS)
	assert.Equal(t, "2025-09-03", g.NotionVersion)
	assert.Empty(t, g.CORSAllowedOrigins)
}

func TestGatewayRequiresAdminToken(t *testing.T) {
	t.Setenv("TOKEN_ENC_KEY", validKey())
	_, err := LoadGateway()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ADMIN_TOKEN")
}

func TestGatewayNotionPairAndCORS(t *testing.T) {
	gatewayEnv(t)
	t.Setenv("NOTION_CLIENT_ID", "id")
	_, err := LoadGateway()
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "NOTION_CLIENT_SECRET"))

	t.Setenv("NOTION_CLIENT_SECRET", "s")
	t.Setenv("CORS_ALLOWED_ORIGINS", "chrome-extension://abc,http://localhost:3000")
	g, err := LoadGateway()
	require.NoError(t, err)
	assert.Equal(t, []string{"chrome-extension://abc", "http://localhost:3000"}, g.CORSAllowedOrigins)
}

// clearAmbient blanks variables that may leak in from the host environment.
func clearAmbient(t *testing.T) {
	t.Helper()
	for _, k := range []string{"LLM_PROVIDER", "LLM_MODEL", "LLM_FALLBACK_PROVIDER", "LLM_VISION_PROVIDER",
		"GROQ_API_KEY", "OPENROUTER_API_KEY", "ANTHROPIC_API_KEY", "NOTION_CLIENT_ID", "NOTION_CLIENT_SECRET", "CORS_ALLOWED_ORIGINS"} {
		t.Setenv(k, "")
	}
}
