// Package doctor checks worker dependencies and shares recent reports through Redis.
package doctor

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"ainote/internal/llm"
	"ainote/internal/pipeline"
	"ainote/internal/youtube"
	"github.com/redis/go-redis/v9"
)

// Result describes one dependency check and how to resolve a failure.
type Result struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
	Fix    string `json:"fix,omitempty"`
}

// Checks holds the worker clients and optional video ID used by Run.
type Checks struct {
	Text, Vision llm.Provider
	Runner       youtube.Runner
	YT           *youtube.Client
	KnownVideoID string
}

const keyFix = "LLM key rejected — check GEMINI_API_KEY (or your provider's key) and LLM_MODEL"

func model(ctx context.Context, name string, p llm.Provider, vision bool) Result {
	if p == nil {
		return Result{Name: name, Detail: "Model configuration unavailable", Fix: keyFix}
	}
	if vision && !p.SupportsImages() {
		return Result{Name: name, Detail: "Model does not support images", Fix: "LLM_VISION_MODEL must support images, or set FRAME_VERIFY=false"}
	}
	if err := p.Ping(ctx); err != nil {
		fix := "Check the model endpoint and worker network connection"
		if pipeline.ErrorCode(err) == "LLM_CONFIG_ERROR" {
			fix = keyFix
		}
		return Result{Name: name, Detail: pipeline.ErrorCode(err), Fix: fix}
	}
	return Result{Name: name, OK: true}
}

func binary(ctx context.Context, r youtube.Runner, name, command, arg string) Result {
	if _, _, err := r.Run(ctx, command, arg); err != nil {
		return Result{Name: name, Detail: "Version check failed", Fix: "Install " + command + " in the worker image and check PATH"}
	}
	return Result{Name: name, OK: true}
}

// Run probes both models, the worker binaries and a public video's metadata.
func Run(ctx context.Context, c Checks) []Result {
	results := []Result{model(ctx, "llm_text", c.Text, false), model(ctx, "llm_vision", c.Vision, true),
		binary(ctx, c.Runner, "yt_dlp", "yt-dlp", "--version"), binary(ctx, c.Runner, "ffmpeg", "ffmpeg", "-version")}
	id := c.KnownVideoID
	if id == "" {
		id = "jNQXAC9IVRw"
	}
	result := Result{Name: "youtube_reachable", OK: true}
	if _, err := c.YT.Metadata(ctx, id); err != nil {
		result.OK = false
		result.Detail = pipeline.ErrorCode(err)
		result.Fix = "Check the worker network connection and yt-dlp installation"
		if result.Detail == "YOUTUBE_BOT_CHECK" {
			result.Fix = "YouTube is asking to confirm you're not a bot — see README › Troubleshooting › YouTube cookies"
		}
	}
	return append(results, result)
}

// Publish replaces a worker's last report and expiry atomically.
func Publish(ctx context.Context, rdb *redis.Client, workerID string, res []Result, ttl time.Duration) {
	data, err := json.Marshal(res)
	if err == nil {
		err = rdb.Set(ctx, "doctor:"+workerID, data, ttl).Err()
	}
	if err != nil && ctx.Err() == nil {
		slog.Warn("could not publish worker doctor report", "worker", workerID, "error", err)
	}
}

// ReadAll returns the latest unexpired report for each worker.
func ReadAll(ctx context.Context, rdb *redis.Client) (map[string][]Result, error) {
	reports := make(map[string][]Result)
	keys := rdb.Scan(ctx, 0, "doctor:*", 100).Iterator()
	for keys.Next(ctx) {
		key := keys.Val()
		data, err := rdb.Get(ctx, key).Bytes()
		if errors.Is(err, redis.Nil) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var res []Result
		if err = json.Unmarshal(data, &res); err != nil {
			return nil, err
		}
		reports[strings.TrimPrefix(key, "doctor:")] = res
	}
	return reports, keys.Err()
}
