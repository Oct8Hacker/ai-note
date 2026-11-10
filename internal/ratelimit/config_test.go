package ratelimit

import (
	"testing"

	"github.com/stretchr/testify/require"

	"ainote/internal/config"
)

func TestFromWorkerConfig(t *testing.T) {
	var cfg config.Worker
	cfg.NotionRPS = 2.5
	cfg.LLMRPM, cfg.LLMTPM, cfg.LLMRPD = 12, 600, 200
	cfg.LLMRPDResetTZ = "America/Los_Angeles"
	cfg.YouTubeRPS = 1

	b, notion, daily := FromWorkerConfig(cfg)
	require.Equal(t, BucketSpec{Capacity: 2, RefillPerSec: 0.2}, b["llm:requests"])
	require.Equal(t, b["llm:requests"], b["llm-fallback:requests"])
	require.Equal(t, BucketSpec{Capacity: 100, RefillPerSec: 10}, b["llm:tokens"])
	require.Equal(t, BucketSpec{Capacity: 1, RefillPerSec: 1}, b["youtube"])
	require.Equal(t, BucketSpec{Capacity: 3, RefillPerSec: 2.5}, notion)
	require.Equal(t, 200, daily["llm:daily"].Limit)
	require.Equal(t, "America/Los_Angeles", daily["llm:daily"].Loc.String())

	cfg.LLMRPM, cfg.LLMRPD = 3, 0 // RPM/6 < 1 -> capacity floor of 1; daily off
	b, _, daily = FromWorkerConfig(cfg)
	require.Equal(t, 1.0, b["llm:requests"].Capacity)
	require.InDelta(t, 0.05, b["llm:requests"].RefillPerSec, 1e-9)
	require.NotContains(t, daily, "llm:daily")
}
