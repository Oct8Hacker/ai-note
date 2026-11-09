package pipelinetest

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"ainote/internal/pipeline"
)

func TestMemStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := NewMemStore()
	id := uuid.New()

	var got pipeline.Transcript
	found, err := s.LoadArtifact(ctx, id, "transcript", &got)
	require.NoError(t, err)
	require.False(t, found)

	in := pipeline.Transcript{Language: "en", Segments: []pipeline.Segment{{StartSec: 1, EndSec: 2, Text: "hi"}}}
	require.NoError(t, s.SaveArtifact(ctx, id, "transcript", in))
	in.Segments[0].Text = "mutated"
	found, err = s.LoadArtifact(ctx, id, "transcript", &got)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "hi", got.Segments[0].Text)
	require.Len(t, s.Artifacts(), 1)

	require.NoError(t, s.UpsertFrame(ctx, pipeline.Frame{JobID: id, Idx: 0, Caption: "a"}))
	require.NoError(t, s.UpsertFrame(ctx, pipeline.Frame{JobID: id, Idx: 0, Caption: "b"}))
	fs, err := s.ListFrames(ctx, id)
	require.NoError(t, err)
	require.Len(t, fs, 1)
	require.Equal(t, "b", fs[0].Caption)
}

func TestFakeStageAndDeps(t *testing.T) {
	boom := context.DeadlineExceeded
	f := &FakeStage{Status: pipeline.StatusSummarizing, Results: []error{boom}}
	require.Equal(t, pipeline.StatusSummarizing, f.Name())
	_, err := f.Run(context.Background(), &pipeline.Job{}, TestDeps(NewMemStore()))
	require.Equal(t, boom, err)
	_, err = f.Run(context.Background(), &pipeline.Job{}, TestDeps(NewMemStore()))
	require.NoError(t, err)
	require.Equal(t, 2, f.CallCount())
}
