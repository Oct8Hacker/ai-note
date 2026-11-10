package synthesis

import (
	"ainote/internal/llm"
	"ainote/internal/pipeline"
	"ainote/internal/pipeline/pipelinetest"
	"context"
	"github.com/stretchr/testify/require"
	"testing"
)

const valid = `{"title":"Intro","overview":"Overview","sections":[{"heading":"Start","start_sec":0,"end_sec":20,"bullets":["Hello"]}],"visuals":[]}`

type fakeLLM struct {
	llm.Provider
	responses []string
	err       error
	requests  []llm.JSONRequest
}

func (f *fakeLLM) GenerateJSON(_ context.Context, r llm.JSONRequest) (llm.JSONResponse, error) {
	f.requests = append(f.requests, r)
	if len(f.responses) == 0 {
		return llm.JSONResponse{}, f.err
	}
	text := f.responses[0]
	f.responses = f.responses[1:]
	return llm.JSONResponse{Text: text}, nil
}
func setup(t *testing.T, p *fakeLLM) (*Stage, *pipeline.Job, *pipelinetest.MemStore) {
	t.Helper()
	job := &pipeline.Job{DurationSec: 80}
	store := pipelinetest.NewMemStore()
	require.NoError(t, store.SaveArtifact(context.Background(), job.ID, "transcript", pipeline.Transcript{Segments: []pipeline.Segment{{StartSec: 0, EndSec: 20, Text: "first"}, {StartSec: 60, EndSec: 80, Text: "second"}}}))
	return &Stage{LLM: p, MaxInputTokens: 100, NotesLanguage: "Hindi", MaxFrames: 8, DedupWindowSec: 5}, job, store
}
func TestStageShortVideoAndRepair(t *testing.T) {
	for _, responses := range [][]string{{valid}, {"{", valid}, {`{"title":"bad"}`, valid}} {
		f := &fakeLLM{responses: responses}
		s, j, store := setup(t, f)
		result, err := s.Run(context.Background(), j, pipelinetest.TestDeps(store))
		require.NoError(t, err)
		require.Equal(t, "synthesis", result.ArtifactKind)
		require.Equal(t, 0, *result.FramesRequested)
		require.Len(t, f.requests, len(responses))
		require.Contains(t, f.requests[0].Prompt, "Hindi")
	}
}
func TestStageResumesFromChunkCheckpoint(t *testing.T) {
	f := &fakeLLM{responses: []string{valid}}
	s, j, store := setup(t, f)
	s.MaxInputTokens = 4
	require.NoError(t, store.SaveArtifact(context.Background(), j.ID, "synthesis_chunk:0", pipeline.Synthesis{Title: "cached", Sections: []pipeline.Section{{Heading: "one"}}}))
	_, err := s.Run(context.Background(), j, pipelinetest.TestDeps(store))
	require.NoError(t, err)
	require.Len(t, f.requests, 1)
	require.Contains(t, f.requests[0].Prompt, "second")
	require.NotContains(t, f.requests[0].Prompt, "[00:00] first")
}
func TestStagePropagatesRateLimit(t *testing.T) {
	f := &fakeLLM{err: &pipeline.RateLimitedError{Provider: "llm"}}
	s, j, store := setup(t, f)
	_, err := s.Run(context.Background(), j, pipelinetest.TestDeps(store))
	require.ErrorIs(t, err, f.err)
	require.Len(t, f.requests, 1)
}
func TestStageRepairFails(t *testing.T) {
	f := &fakeLLM{responses: []string{"{", "{}"}}
	s, j, store := setup(t, f)
	_, err := s.Run(context.Background(), j, pipelinetest.TestDeps(store))
	require.Equal(t, "LLM_BAD_OUTPUT", pipeline.ErrorCode(err))
	require.Len(t, f.requests, 2)
	require.Len(t, store.Artifacts(), 1)
}
