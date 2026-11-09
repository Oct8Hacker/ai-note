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

func TestDepsRecordWarningsAndProgress(t *testing.T) {
	s := NewMemStore()
	d := TestDeps(s)
	ts := 12.5
	d.Warn(pipeline.Warning{Code: pipeline.WarnFrameNotRelevant, TimestampSec: &ts})
	ts2 := 12.5
	d.Warn(pipeline.Warning{Code: pipeline.WarnFrameNotRelevant, TimestampSec: &ts2})
	d.Progress(10)
	d.Progress(50)
	ws, err := s.Warnings(context.Background(), uuid.New())
	require.NoError(t, err)
	require.Len(t, ws, 1)
	require.Equal(t, []int{10, 50}, s.Progress())
}

func TestFakeStageCountsBeforeHook(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	f := &FakeStage{Status: pipeline.StatusTranscribing, Hook: func(context.Context) error {
		close(started)
		<-release
		return nil
	}}
	done := make(chan struct{})
	go func() { _, _ = f.Run(context.Background(), &pipeline.Job{}, pipeline.Deps{}); close(done) }()
	<-started
	require.Equal(t, 1, f.CallCount())
	close(release)
	<-done
}

func TestFakeStageReturnsResult(t *testing.T) {
	f := &FakeStage{Result: pipeline.StageResult{PageURL: "u"}}
	r, err := f.Run(context.Background(), &pipeline.Job{}, pipeline.Deps{})
	require.NoError(t, err)
	require.Equal(t, "u", r.PageURL)
}
