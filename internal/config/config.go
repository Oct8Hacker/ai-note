// Package config loads typed, validated environment configuration for the
// gateway and worker binaries. Loading fails fast on any invalid value and
// validation errors name the offending environment variable.
package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"
	_ "time/tzdata" // embed tz database so LoadLocation works in minimal containers

	"github.com/caarlos0/env/v11"
)

// Common holds settings shared by gateway and worker.
type Common struct {
	DatabaseURL   string          `env:"DATABASE_URL" envDefault:"postgres://notes:notes@postgres:5432/notes?sslmode=disable"`
	AMQPURL       string          `env:"AMQP_URL" envDefault:"amqp://guest:guest@rabbitmq:5672/"`
	RedisURL      string          `env:"REDIS_URL" envDefault:"redis://redis:6379/0"`
	AMQPHeartbeat time.Duration   `env:"AMQP_HEARTBEAT" envDefault:"10s"`
	TokenEncKey   string          `env:"TOKEN_ENC_KEY"`
	LogLevel      string          `env:"LOG_LEVEL" envDefault:"info"`
	RetryDelays   []time.Duration `env:"RETRY_DELAYS" envDefault:"10s,30s,90s,270s"`
	DeferDelay    time.Duration   `env:"DEFER_DELAY" envDefault:"3s"`
	NotionRPS     float64         `env:"NOTION_RPS" envDefault:"2.5"`
	NotionVersion string          `env:"NOTION_VERSION" envDefault:"2025-09-03"`
}

// Gateway holds gateway-only settings.
type Gateway struct {
	Common
	Port                int           `env:"GATEWAY_PORT" envDefault:"8080"`
	PublicBaseURL       string        `env:"PUBLIC_BASE_URL" envDefault:"http://localhost:8080"`
	AdminToken          string        `env:"ADMIN_TOKEN"`
	CORSAllowedOrigins  []string      `env:"CORS_ALLOWED_ORIGINS"`
	NotionClientID      string        `env:"NOTION_CLIENT_ID"`
	NotionClientSecret  string        `env:"NOTION_CLIENT_SECRET"`
	NotionRedirectURI   string        `env:"NOTION_REDIRECT_URI"`
	DeliveryLimit       int           `env:"DELIVERY_LIMIT" envDefault:"10"`
	OutboxSweepInterval time.Duration `env:"OUTBOX_SWEEP_INTERVAL" envDefault:"2s"`
	RabbitMgmtURL       string        `env:"RABBITMQ_MGMT_URL" envDefault:"http://rabbitmq:15672"`
	RabbitUser          string        `env:"RABBITMQ_USER" envDefault:"guest"`
	RabbitPass          string        `env:"RABBITMQ_PASS" envDefault:"guest"`
}

// FaultDefaults are process-wide fault-injection defaults (SRS 9.6).
type FaultDefaults struct {
	LLM429Rate     float64 `env:"FAULT_LLM_429_RATE"`
	Notion429Rate  float64 `env:"FAULT_NOTION_429_RATE"`
	YouTube429Rate float64 `env:"FAULT_YOUTUBE_429_RATE"`
	FrameFailRate  float64 `env:"FAULT_FRAME_FAIL_RATE"`
	FrameFailAll   bool    `env:"FAULT_FRAME_FAIL_ALL"`
	StageDelayMs   int     `env:"FAULT_STAGE_DELAY_MS"`
}

// Worker holds worker-only settings.
type Worker struct {
	Common
	LLMProvider         string `env:"LLM_PROVIDER" envDefault:"gemini"`
	LLMModel            string `env:"LLM_MODEL"`
	LLMVisionProvider   string `env:"LLM_VISION_PROVIDER"`
	LLMVisionModel      string `env:"LLM_VISION_MODEL"`
	LLMFallbackProvider string `env:"LLM_FALLBACK_PROVIDER"`
	LLMFallbackModel    string `env:"LLM_FALLBACK_MODEL"`

	GeminiAPIKey     string `env:"GEMINI_API_KEY"`
	GroqAPIKey       string `env:"GROQ_API_KEY"`
	OpenRouterAPIKey string `env:"OPENROUTER_API_KEY"`
	AnthropicAPIKey  string `env:"ANTHROPIC_API_KEY"`
	OllamaURL        string `env:"OLLAMA_URL" envDefault:"http://ollama:11434"`

	LLMRPM            int           `env:"LLM_RPM" envDefault:"8"`
	LLMTPM            int           `env:"LLM_TPM" envDefault:"200000"`
	LLMRPD            int           `env:"LLM_RPD" envDefault:"200"`
	LLMMaxInputTokens int           `env:"LLM_MAX_INPUT_TOKENS" envDefault:"6000"`
	LLMTimeout        time.Duration `env:"LLM_TIMEOUT" envDefault:"90s"`
	LimiterMaxWait    time.Duration `env:"LIMITER_MAX_WAIT" envDefault:"60s"`
	LLMRPDResetTZ     string        `env:"LLM_RPD_RESET_TZ" envDefault:"America/Los_Angeles"`

	YouTubeRPS       float64 `env:"YOUTUBE_RPS" envDefault:"1"`
	YtDlpCookiesFile string  `env:"YTDLP_COOKIES_FILE"`

	MaxVideoSeconds     int           `env:"MAX_VIDEO_SECONDS" envDefault:"1200"`
	MaxFrames           int           `env:"MAX_FRAMES" envDefault:"8"`
	FrameConcurrency    int           `env:"FRAME_CONCURRENCY" envDefault:"4"`
	FrameTimeout        time.Duration `env:"FRAME_TIMEOUT" envDefault:"30s"`
	FrameVerify         bool          `env:"FRAME_VERIFY" envDefault:"true"`
	SceneThreshold      float64       `env:"SCENE_THRESHOLD" envDefault:"0.3"`
	MaxFrameDriftSec    float64       `env:"MAX_FRAME_DRIFT_SEC" envDefault:"3"`
	FrameDedupWindowSec float64       `env:"FRAME_DEDUP_WINDOW_SEC" envDefault:"5"`
	NotesLanguage       string        `env:"NOTES_LANGUAGE" envDefault:"English"`

	NotionDefaultParentID string `env:"NOTION_DEFAULT_PARENT_ID"`
	NotionMaxImageBytes   int    `env:"NOTION_MAX_IMAGE_BYTES" envDefault:"4500000"`

	MaxAttempts         int `env:"MAX_ATTEMPTS" envDefault:"5"`
	MaxRateLimitRetries int `env:"MAX_RATE_LIMIT_RETRIES" envDefault:"8"`
	MaxDeferrals        int `env:"MAX_DEFERRALS" envDefault:"30"`
	DeliveryLimit       int `env:"DELIVERY_LIMIT" envDefault:"10"`
	WorkerConcurrency   int `env:"WORKER_CONCURRENCY" envDefault:"1"`

	LeaseTTL          time.Duration `env:"LEASE_TTL" envDefault:"30s"`
	HeartbeatInterval time.Duration `env:"WORKER_HEARTBEAT_INTERVAL" envDefault:"2s"`
	HeartbeatTTL      time.Duration `env:"WORKER_HEARTBEAT_TTL" envDefault:"6s"`
	ShutdownGrace     time.Duration `env:"SHUTDOWN_GRACE" envDefault:"25s"`
	DoctorInterval    time.Duration `env:"DOCTOR_INTERVAL" envDefault:"60s"`

	Faults FaultDefaults
}

// ParseDurations parses a comma-separated duration list such as "10s,30s,90s".
func ParseDurations(s string) ([]time.Duration, error) {
	if strings.TrimSpace(s) == "" {
		return nil, errors.New("empty duration list")
	}
	parts := strings.Split(s, ",")
	out := make([]time.Duration, 0, len(parts))
	for _, p := range parts {
		d, err := time.ParseDuration(strings.TrimSpace(p))
		if err != nil {
			return nil, fmt.Errorf("invalid duration %q: %w", p, err)
		}
		out = append(out, d)
	}
	return out, nil
}

var parseOpts = env.Options{
	FuncMap: map[reflect.Type]env.ParserFunc{
		reflect.TypeOf([]time.Duration{}): func(v string) (any, error) {
			d, err := ParseDurations(v)
			if err != nil {
				return nil, fmt.Errorf("RETRY_DELAYS: %w", err)
			}
			return d, nil
		},
	},
}

// LoadGateway reads and validates gateway configuration from the environment.
func LoadGateway() (Gateway, error) {
	var g Gateway
	if err := env.ParseWithOptions(&g, parseOpts); err != nil {
		return Gateway{}, fmt.Errorf("config: %w", err)
	}
	if err := g.validate(); err != nil {
		return Gateway{}, fmt.Errorf("config: %w", err)
	}
	return g, nil
}

// LoadWorker reads and validates worker configuration from the environment.
func LoadWorker() (Worker, error) {
	var w Worker
	if err := env.ParseWithOptions(&w, parseOpts); err != nil {
		return Worker{}, fmt.Errorf("config: %w", err)
	}
	if err := w.validate(); err != nil {
		return Worker{}, fmt.Errorf("config: %w", err)
	}
	return w, nil
}

func (c Common) validate() error {
	if c.TokenEncKey == "" {
		return errors.New("TOKEN_ENC_KEY is required (32-byte base64)")
	}
	raw, err := base64.StdEncoding.DecodeString(c.TokenEncKey)
	if err != nil {
		return fmt.Errorf("TOKEN_ENC_KEY must be valid base64: %w", err)
	}
	if len(raw) != 32 {
		return fmt.Errorf("TOKEN_ENC_KEY must decode to exactly 32 bytes, got %d", len(raw))
	}
	if len(c.RetryDelays) == 0 {
		return errors.New("RETRY_DELAYS must not be empty")
	}
	for _, d := range c.RetryDelays {
		if d <= 0 {
			return errors.New("RETRY_DELAYS entries must be positive")
		}
		if d%time.Second != 0 {
			return fmt.Errorf("RETRY_DELAYS entry %v must be a whole number of seconds (>= 1s)", d)
		}
	}
	if err := positiveDur("AMQP_HEARTBEAT", c.AMQPHeartbeat); err != nil {
		return err
	}
	if err := positiveDur("DEFER_DELAY", c.DeferDelay); err != nil {
		return err
	}
	if c.NotionRPS <= 0 {
		return errors.New("NOTION_RPS must be > 0")
	}
	return nil
}

func (g Gateway) validate() error {
	if err := g.Common.validate(); err != nil {
		return err
	}
	if g.AdminToken == "" {
		return errors.New("ADMIN_TOKEN is required")
	}
	if g.Port < 1 || g.Port > 65535 {
		return errors.New("GATEWAY_PORT must be 1..65535")
	}
	if g.DeliveryLimit < 1 {
		return errors.New("DELIVERY_LIMIT must be >= 1")
	}
	if err := positiveDur("OUTBOX_SWEEP_INTERVAL", g.OutboxSweepInterval); err != nil {
		return err
	}
	if (g.NotionClientID == "") != (g.NotionClientSecret == "") {
		if g.NotionClientID == "" {
			return errors.New("NOTION_CLIENT_ID is required when NOTION_CLIENT_SECRET is set")
		}
		return errors.New("NOTION_CLIENT_SECRET is required when NOTION_CLIENT_ID is set")
	}
	return nil
}

// providerDefaultModel lists providers with a built-in model default;
// openrouter and claude require LLM_MODEL explicitly.
var providerDefaultModel = map[string]string{
	"gemini": "gemini-2.5-flash",
	"ollama": "llama3.2",
	"groq":   "llama-3.3-70b-versatile",
}

var validProviders = map[string]bool{"gemini": true, "ollama": true, "groq": true, "openrouter": true, "claude": true}

func (w *Worker) providerKey(p string) (envName, val string) {
	switch p {
	case "gemini":
		return "GEMINI_API_KEY", w.GeminiAPIKey
	case "groq":
		return "GROQ_API_KEY", w.GroqAPIKey
	case "openrouter":
		return "OPENROUTER_API_KEY", w.OpenRouterAPIKey
	case "claude":
		return "ANTHROPIC_API_KEY", w.AnthropicAPIKey
	}
	return "", "-" // ollama needs no key
}

// resolveProvider checks provider validity, fills the model default and checks the key.
func (w *Worker) resolveProvider(provVar, modelVar, prov string, model *string) error {
	if !validProviders[prov] {
		return fmt.Errorf("%s %q invalid: must be one of gemini, ollama, groq, openrouter, claude", provVar, prov)
	}
	if *model == "" {
		def, ok := providerDefaultModel[prov]
		if !ok {
			return fmt.Errorf("%s is required when %s is %q", modelVar, provVar, prov)
		}
		*model = def
	}
	if name, val := w.providerKey(prov); val == "" {
		return fmt.Errorf("%s is required when %s is %q", name, provVar, prov)
	}
	return nil
}

func (w *Worker) validate() error {
	if err := w.Common.validate(); err != nil {
		return err
	}
	if err := w.resolveProvider("LLM_PROVIDER", "LLM_MODEL", w.LLMProvider, &w.LLMModel); err != nil {
		return err
	}
	if w.LLMVisionProvider == "" {
		w.LLMVisionProvider = w.LLMProvider
		if w.LLMVisionModel == "" {
			w.LLMVisionModel = w.LLMModel
		}
	}
	if err := w.resolveProvider("LLM_VISION_PROVIDER", "LLM_VISION_MODEL", w.LLMVisionProvider, &w.LLMVisionModel); err != nil {
		return err
	}
	if w.LLMFallbackProvider != "" {
		if err := w.resolveProvider("LLM_FALLBACK_PROVIDER", "LLM_FALLBACK_MODEL", w.LLMFallbackProvider, &w.LLMFallbackModel); err != nil {
			return err
		}
	}
	if _, err := time.LoadLocation(w.LLMRPDResetTZ); err != nil {
		return fmt.Errorf("LLM_RPD_RESET_TZ %q invalid: %w", w.LLMRPDResetTZ, err)
	}

	for _, c := range []struct {
		name   string
		v, min int
	}{
		{"LLM_RPM", w.LLMRPM, 0},
		{"LLM_TPM", w.LLMTPM, 0},
		{"LLM_RPD", w.LLMRPD, 0},
		{"LLM_MAX_INPUT_TOKENS", w.LLMMaxInputTokens, 1},
		{"MAX_VIDEO_SECONDS", w.MaxVideoSeconds, 1},
		{"MAX_FRAMES", w.MaxFrames, 0},
		{"FRAME_CONCURRENCY", w.FrameConcurrency, 1},
		{"NOTION_MAX_IMAGE_BYTES", w.NotionMaxImageBytes, 1},
		{"MAX_ATTEMPTS", w.MaxAttempts, 1},
		{"MAX_RATE_LIMIT_RETRIES", w.MaxRateLimitRetries, 0},
		{"MAX_DEFERRALS", w.MaxDeferrals, 0},
		{"DELIVERY_LIMIT", w.DeliveryLimit, 1},
		{"WORKER_CONCURRENCY", w.WorkerConcurrency, 1},
		{"FAULT_STAGE_DELAY_MS", w.Faults.StageDelayMs, 0},
	} {
		if c.v < c.min {
			return fmt.Errorf("%s must be >= %d, got %d", c.name, c.min, c.v)
		}
	}
	for _, c := range []struct {
		name string
		v    time.Duration
	}{
		{"LLM_TIMEOUT", w.LLMTimeout},
		{"LIMITER_MAX_WAIT", w.LimiterMaxWait},
		{"FRAME_TIMEOUT", w.FrameTimeout},
		{"LEASE_TTL", w.LeaseTTL},
		{"WORKER_HEARTBEAT_INTERVAL", w.HeartbeatInterval},
		{"WORKER_HEARTBEAT_TTL", w.HeartbeatTTL},
		{"SHUTDOWN_GRACE", w.ShutdownGrace},
		{"DOCTOR_INTERVAL", w.DoctorInterval},
	} {
		if err := positiveDur(c.name, c.v); err != nil {
			return err
		}
	}
	if w.YouTubeRPS <= 0 {
		return errors.New("YOUTUBE_RPS must be > 0")
	}
	for _, c := range []struct {
		name string
		v    float64
	}{
		{"SCENE_THRESHOLD", w.SceneThreshold},
		{"FAULT_LLM_429_RATE", w.Faults.LLM429Rate},
		{"FAULT_NOTION_429_RATE", w.Faults.Notion429Rate},
		{"FAULT_YOUTUBE_429_RATE", w.Faults.YouTube429Rate},
		{"FAULT_FRAME_FAIL_RATE", w.Faults.FrameFailRate},
	} {
		if c.v < 0 || c.v > 1 {
			return fmt.Errorf("%s must be within 0..1, got %v", c.name, c.v)
		}
	}
	if w.MaxFrameDriftSec < 0 {
		return errors.New("MAX_FRAME_DRIFT_SEC must be >= 0")
	}
	if w.FrameDedupWindowSec < 0 {
		return errors.New("FRAME_DEDUP_WINDOW_SEC must be >= 0")
	}
	if w.HeartbeatTTL <= w.HeartbeatInterval {
		return errors.New("WORKER_HEARTBEAT_TTL must be greater than WORKER_HEARTBEAT_INTERVAL")
	}
	return nil
}

func positiveDur(name string, d time.Duration) error {
	if d <= 0 {
		return fmt.Errorf("%s must be > 0", name)
	}
	return nil
}
