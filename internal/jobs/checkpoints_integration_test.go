//go:build integration

package jobs

import (
	"context"
	"testing"

	"ainote/internal/pipeline"

	"github.com/stretchr/testify/require"
)

func TestCheckpointStoreFrames(t *testing.T) {
	ctx := context.Background()
	r, id := newJobIn(t, pipeline.StatusExtractingFrames)
	s := r.Checkpoints()
	cap1 := 11.0
	require.NoError(t, s.UpsertFrame(ctx, pipeline.Frame{JobID: id, Idx: 1, RequestedSec: 10, Caption: "b", Reason: "r", Status: "pending"}))
	require.NoError(t, s.UpsertFrame(ctx, pipeline.Frame{JobID: id, Idx: 0, RequestedSec: 5, Caption: "a", Reason: "r", Status: "pending"}))
	require.NoError(t, s.UpsertFrame(ctx, pipeline.Frame{JobID: id, Idx: 1, RequestedSec: 10, CapturedSec: &cap1, SectionIndex: 2,
		Caption: "b2", Reason: "r", Status: "captured", Verified: true, ImageJPEG: []byte{1, 2, 3},
		NotionFileUploadID: "fu", WarningCode: "W"}))
	fs, err := s.ListFrames(ctx, id)
	require.NoError(t, err)
	require.Len(t, fs, 2)
	require.Equal(t, 0, fs[0].Idx)
	require.Equal(t, "b2", fs[1].Caption)
	require.Equal(t, pipeline.FrameStatus("captured"), fs[1].Status)
	require.True(t, fs[1].Verified)
	require.Equal(t, []byte{1, 2, 3}, fs[1].ImageJPEG)
	require.Equal(t, 11.0, *fs[1].CapturedSec)
	require.Equal(t, "fu", fs[1].NotionFileUploadID)
	require.Equal(t, 2, fs[1].SectionIndex)
	require.Nil(t, fs[0].CapturedSec)
}

func TestCheckpointStoreJobFields(t *testing.T) {
	ctx := context.Background()
	r, id := newJobIn(t, pipeline.StatusPublishing)
	s := r.Checkpoints()

	var got pipeline.Transcript
	ok, err := s.LoadArtifact(ctx, id, "transcript", &got)
	require.NoError(t, err)
	require.False(t, ok)
	require.NoError(t, s.SaveArtifact(ctx, id, "transcript", pipeline.Transcript{Language: "en"}))
	require.NoError(t, s.SaveArtifact(ctx, id, "transcript", pipeline.Transcript{Language: "de"}))
	ok, err = s.LoadArtifact(ctx, id, "transcript", &got)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "de", got.Language)

	require.NoError(t, s.SetVideoMeta(ctx, id, "Title", 99))
	require.NoError(t, s.SetNotionParent(ctx, id, "res", "data_source", "Name"))
	require.NoError(t, s.MarkNotionCreateStarted(ctx, id))
	j, _ := r.GetJob(ctx, id)
	first := *j.NotionCreateStartedAt
	require.NoError(t, s.MarkNotionCreateStarted(ctx, id))
	require.NoError(t, s.SetNotionPage(ctx, id, "pg", "https://n/pg"))
	j, err = r.GetJob(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "Title", j.VideoTitle)
	require.Equal(t, 99, j.DurationSec)
	require.Equal(t, "res", j.NotionResolvedParent)
	require.Equal(t, "data_source", j.NotionParentKind)
	require.Equal(t, "Name", j.NotionTitleProp)
	require.True(t, first.Equal(*j.NotionCreateStartedAt))
	require.Equal(t, "pg", j.NotionPageID)
	require.Equal(t, "https://n/pg", j.NotionPageURL)
}
