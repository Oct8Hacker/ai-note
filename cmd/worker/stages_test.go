package main

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"ainote/internal/config"
	"ainote/internal/llm"
	"ainote/internal/notion"
	"ainote/internal/pipeline"
	"ainote/internal/pipeline/frames"
	"ainote/internal/pipeline/pipelinetest"
	"ainote/internal/pipeline/publish"
	"ainote/internal/pipeline/synthesis"
	"ainote/internal/pipeline/transcript"
	"ainote/internal/youtube"
)

// stubLLM stands in for a model client; realStages only stores it.
type stubLLM struct{ name string }

func (s stubLLM) Name() string { return s.name }
func (stubLLM) GenerateJSON(context.Context, llm.JSONRequest) (llm.JSONResponse, error) {
	return llm.JSONResponse{}, nil
}
func (stubLLM) SupportsImages() bool       { return true }
func (stubLLM) Ping(context.Context) error { return nil }

func testWorkerConfig() config.Worker {
	return config.Worker{
		MaxVideoSeconds:         1200,
		LLMMaxInputTokens:       6000,
		FrameDedupWindowSec:     5,
		MaxFrames:               8,
		NotesLanguage:           "English",
		FrameVerify:             true,
		FrameConcurrency:        4,
		FrameTimeout:            30 * time.Second,
		SceneThreshold:          0.3,
		MaxFrameDriftSec:        3,
		NotionDefaultParentID:   "parent-123",
		NotionMaxImageBytes:     4500000,
		NotionUploadReuseWindow: 50 * time.Minute,
	}
}

func testClients() (*youtube.Client, *notion.Client) {
	yt := youtube.NewClient(youtube.ExecRunner{}, pipelinetest.NoopLimiter{}, pipelinetest.NoFaults{}, "")
	nc := notion.New("", "2025-09-03", pipelinetest.NoopLimiter{}, pipelinetest.NoFaults{}, nil)
	return yt, nc
}

func TestRealStagesCoversEveryStage(t *testing.T) {
	yt, nc := testClients()
	stages := realStages(testWorkerConfig(), yt, youtube.ExecRunner{}, stubLLM{"text"}, stubLLM{"vision"}, nc)

	want := []pipeline.Status{
		pipeline.StatusTranscribing, pipeline.StatusSummarizing,
		pipeline.StatusExtractingFrames, pipeline.StatusPublishing,
	}
	require.Len(t, stages, len(want))
	for _, st := range want {
		require.Contains(t, stages, st)
		assert.Equal(t, st, stages[st].Name(), "stage registered under the wrong status")
	}
}

func TestRealStagesUsesConfig(t *testing.T) {
	cfg := testWorkerConfig()
	yt, nc := testClients()
	text, vision := stubLLM{"text"}, stubLLM{"vision"}
	stages := realStages(cfg, yt, youtube.ExecRunner{}, text, vision, nc)

	tr, ok := stages[pipeline.StatusTranscribing].(*transcript.Stage)
	require.True(t, ok)
	assert.Same(t, yt, tr.YT)
	assert.Equal(t, 1200, tr.MaxVideoSeconds)

	sy, ok := stages[pipeline.StatusSummarizing].(*synthesis.Stage)
	require.True(t, ok)
	assert.Equal(t, text, sy.LLM)
	assert.Equal(t, 6000, sy.MaxInputTokens)
	assert.Equal(t, 5.0, sy.DedupWindowSec)
	assert.Equal(t, 8, sy.MaxFrames)
	assert.Equal(t, "English", sy.NotesLanguage)

	fr, ok := stages[pipeline.StatusExtractingFrames].(*frames.Stage)
	require.True(t, ok)
	assert.Same(t, yt, fr.YT)
	assert.NotNil(t, fr.FF)
	assert.NotNil(t, fr.Verifier)
	assert.True(t, fr.Verify)
	assert.Equal(t, 4, fr.Concurrency)
	assert.Equal(t, 30*time.Second, fr.FrameTimeout)
	assert.Equal(t, 0.3, fr.SceneThreshold)
	assert.Equal(t, 3.0, fr.MaxDriftSec)
	assert.True(t, fr.Snap)
	assert.True(t, fr.PickSharpest)

	pu, ok := stages[pipeline.StatusPublishing].(*publish.Stage)
	require.True(t, ok)
	assert.Same(t, nc, pu.Notion)
	assert.Equal(t, "parent-123", pu.DefaultParentID)
	assert.Equal(t, 50*time.Minute, pu.UploadReuse)
	assert.Equal(t, 4500000, pu.MaxImageBytes)
}

func TestRealStagesVerificationOff(t *testing.T) {
	yt, nc := testClients()

	cfg := testWorkerConfig()
	cfg.FrameVerify = false
	fr := realStages(cfg, yt, youtube.ExecRunner{}, stubLLM{"text"}, stubLLM{"vision"}, nc)[pipeline.StatusExtractingFrames].(*frames.Stage)
	assert.False(t, fr.Verify, "FRAME_VERIFY=false must turn verification off")

	// Without a vision model there is nothing to verify with.
	fr = realStages(testWorkerConfig(), yt, youtube.ExecRunner{}, stubLLM{"text"}, nil, nc)[pipeline.StatusExtractingFrames].(*frames.Stage)
	assert.False(t, fr.Verify)
	assert.Nil(t, fr.Verifier)
}
