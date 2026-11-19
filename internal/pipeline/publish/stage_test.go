package publish

import (
	"ainote/internal/notion"
	"ainote/internal/pipeline"
	"ainote/internal/pipeline/pipelinetest"
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"image"
	"image/jpeg"
	"testing"
	"time"
)

func TestFitJPEGReducesOversizedFrame(t *testing.T) {
	img := image.NewGray(image.Rect(0, 0, 128, 128))
	for i := range img.Pix {
		img.Pix[i] = byte((i*37 + i/128*19) % 256)
	}
	var original bytes.Buffer
	require.NoError(t, jpeg.Encode(&original, img, &jpeg.Options{Quality: 100}))
	limit := original.Len() - 1
	data, err := fitJPEG(original.Bytes(), limit)
	require.NoError(t, err)
	require.LessOrEqual(t, len(data), limit)
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(data))
	require.NoError(t, err)
	require.Equal(t, 128, cfg.Width)
	_, err = fitJPEG(original.Bytes(), 1)
	require.Error(t, err)
}

func (f *fakeNotion) CreatePage(context.Context, string, map[string]any, map[string]any, string) (notion.Page, error) {
	f.creates++
	return notion.Page{ID: "notes", URL: "https://example.org/notes"}, f.createErr
}
func (f *fakeNotion) ListChildren(_ context.Context, _ string, id string) ([]notion.Block, error) {
	if id == "notes" {
		return append([]notion.Block(nil), f.body...), nil
	}
	return f.children, nil
}
func (f *fakeNotion) QueryDataSource(_ context.Context, _, _ string, q map[string]any) ([]notion.Page, error) {
	f.lookup = q
	return f.matches, nil
}
func (f *fakeNotion) ArchiveBlock(_ context.Context, _, id string) error {
	for i, b := range f.body {
		if b["id"] == id {
			f.body = append(f.body[:i], f.body[i+1:]...)
			f.archives++
			return nil
		}
	}
	return notion.ErrObjectNotFound
}
func (f *fakeNotion) AppendChildren(_ context.Context, _, _ string, blocks []notion.Block) error {
	for i, b := range blocks {
		b["id"] = fmt.Sprint(i)
		f.body = append(f.body, b)
		if f.appendErr != nil {
			return f.appendErr
		}
	}
	return nil
}
func (f *fakeNotion) UploadImage(context.Context, string, string, []byte) (string, error) {
	f.uploads++
	return fmt.Sprint("upload", f.uploads), nil
}
func publishFixture(t *testing.T) (*Stage, *pipeline.Job, pipeline.Deps, *fakeNotion) {
	f := &fakeNotion{}
	s := &Stage{Notion: f, DefaultParentID: "parent", Now: func() time.Time { return time.Unix(10000, 0) }}
	j := &pipeline.Job{ID: uuid.New(), VideoID: "video", VideoTitle: "Lecture"}
	d := pipelinetest.TestDeps(pipelinetest.NewMemStore())
	d.NotionToken = "token"
	require.NoError(t, d.Store.SaveArtifact(context.Background(), j.ID, "synthesis", pipeline.Synthesis{Sections: []pipeline.Section{{Heading: "topic", EndSec: 10}}}))
	return s, j, d, f
}

type rejectedCreate struct{ pipeline.CheckpointStore }

func (rejectedCreate) MarkNotionCreateStarted(context.Context, uuid.UUID) error {
	return context.Canceled
}

func TestCancelledCreateCheckpointPreventsNotionPage(t *testing.T) {
	s, j, d, f := publishFixture(t)
	d.Store = rejectedCreate{d.Store}
	_, err := s.Run(context.Background(), j, d)
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, f.creates)
	require.Empty(t, f.body)
}

func TestContentConvergesAfterPartialWrite(t *testing.T) {
	s, j, d, f := publishFixture(t)
	ctx := context.Background()
	f.appendErr = errors.New("interrupted")
	_, err := s.Run(ctx, j, d)
	require.ErrorIs(t, err, f.appendErr)
	require.Len(t, f.body, 1)
	f.appendErr = nil
	reloaded := d.Store.(*pipelinetest.MemStore).Job(j.ID)
	reloaded.VideoID, reloaded.VideoTitle = j.VideoID, j.VideoTitle
	out, err := s.Run(ctx, &reloaded, d)
	require.NoError(t, err)
	require.Equal(t, 0, *out.FramesPublished)
	require.Equal(t, 1, f.creates)
	require.Equal(t, 1, f.archives)
	require.Len(t, f.body, 5)
	require.Equal(t, "https://example.org/notes", out.PageURL)
}
func TestAdoptsAfterCrashWindow(t *testing.T) {
	for _, db := range []bool{false, true} {
		s, j, d, f := publishFixture(t)
		now := s.Now()
		j.NotionCreateStartedAt = &now
		title := PageTitle(j.VideoTitle, ShortID(j.ID))
		if db {
			j.NotionParentID = "db"
			f.db = true
			f.matches = []notion.Page{{ID: "unrelated", Title: PageTitle("Other", ShortID(j.ID))}, {ID: "notes", Title: title, URL: "https://example.org/notes"}}
		} else {
			f.children = []notion.Block{
				{"type": "child_page", "id": "unrelated", "child_page": map[string]any{"title": PageTitle("Other", ShortID(j.ID))}},
				{"type": "child_page", "id": "notes", "child_page": map[string]any{"title": title}},
			}
		}
		out, err := s.Run(context.Background(), j, d)
		require.NoError(t, err)
		require.Zero(t, f.creates)
		require.Equal(t, "https://example.org/notes", out.PageURL)
		if db {
			require.Equal(t, "Lecture", f.lookup["property"])
		}
	}
}
func TestUploadsReusedOrRenewed(t *testing.T) {
	s, j, d, f := publishFixture(t)
	ctx := context.Background()
	s.UploadReuse = 50 * time.Minute
	at := 2.
	for i, age := range []time.Duration{time.Minute, time.Hour} {
		stamp := s.Now().Add(-age)
		require.NoError(t, d.Store.UpsertFrame(ctx, pipeline.Frame{JobID: j.ID, Idx: i, Status: "captured", CapturedSec: &at, Caption: "slide", ImageJPEG: []byte("bytes"), NotionFileUploadID: "previous", NotionUploadedAt: &stamp}))
	}
	out, err := s.Run(ctx, j, d)
	require.NoError(t, err)
	require.Equal(t, 2, *out.FramesPublished)
	require.Equal(t, 1, f.uploads)
	_, err = s.Run(ctx, j, d)
	require.NoError(t, err)
	require.Equal(t, 1, f.uploads)
}
func TestUnauthorizedIsFatal(t *testing.T) {
	s, j, d, f := publishFixture(t)
	f.createErr = &pipeline.FatalError{Code: "NOTION_UNAUTHORIZED"}
	_, err := s.Run(context.Background(), j, d)
	require.ErrorIs(t, err, f.createErr)
	require.Empty(t, f.body)
}
