package publish

import (
	"ainote/internal/notion"
	"ainote/internal/pipeline"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
	"time"
)

type textSpan struct {
	Text struct {
		Content string
		Link    *struct{ URL string }
	}
}
type content struct {
	Rich    []textSpan `json:"rich_text"`
	Caption []textSpan
	Upload  struct{ ID string } `json:"file_upload"`
	Type    string
	Icon    struct{ Emoji string }
}

func unpack(t *testing.T, b notion.Block) content {
	t.Helper()
	require.Equal(t, "block", b["object"])
	data, err := json.Marshal(b[b["type"].(string)])
	require.NoError(t, err)
	var c content
	require.NoError(t, json.Unmarshal(data, &c))
	return c
}
func joined(spans []textSpan) string {
	var b strings.Builder
	for _, s := range spans {
		b.WriteString(s.Text.Content)
	}
	return b.String()
}
func TestBlocksOrderAndCaptions(t *testing.T) {
	now := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
	job := pipeline.Job{VideoID: "vid", DurationSec: 120}
	syn := pipeline.Synthesis{Overview: "Summary", Sections: []pipeline.Section{{Heading: "First", EndSec: 60, Bullets: []string{"a"}}, {Heading: "Second", StartSec: 60, EndSec: 120, Bullets: []string{"b"}}}}
	first, second := 12.4, 60.5
	frames := []pipeline.Frame{{Idx: 0, SectionIndex: 0, RequestedSec: 10, CapturedSec: &first, Caption: "Diagram", Reason: "Flow", Status: "captured"}, {Idx: 1, SectionIndex: 0, RequestedSec: 59, CapturedSec: &second, Caption: "Chart", Reason: "Trend", Status: "captured"}}
	got := BuildBlocks(job, syn, frames, map[int]string{0: "u0", 1: "u1"}, nil, now)
	var kinds []string
	for _, b := range got {
		kinds = append(kinds, b["type"].(string))
	}
	require.Equal(t, []string{"paragraph", "heading_2", "paragraph", "heading_2", "bulleted_list_item", "image", "heading_2", "bulleted_list_item", "image", "callout"}, kinds)
	meta := unpack(t, got[0])
	require.Contains(t, joined(meta.Rich), "Duration 02:00")
	require.Contains(t, joined(meta.Rich), "2026-11-11")
	require.Contains(t, meta.Rich[0].Text.Link.URL, "watch?v=vid")
	require.Equal(t, "Overview", joined(unpack(t, got[1]).Rich))
	require.Equal(t, "Summary", joined(unpack(t, got[2]).Rich))
	heading := unpack(t, got[6])
	require.Contains(t, joined(heading.Rich), "2. Second (01:00–02:00)")
	require.Equal(t, YouTubeLink("vid", 60), heading.Rich[0].Text.Link.URL)
	img := unpack(t, got[5])
	require.Equal(t, "file_upload", img.Type)
	require.Equal(t, "u0", img.Upload.ID)
	require.Contains(t, joined(img.Caption), "Diagram — Why it matters: Flow")
	require.Contains(t, joined(img.Caption), "00:12")
	var links []string
	for _, s := range img.Caption {
		if s.Text.Link != nil {
			links = append(links, s.Text.Link.URL)
		}
	}
	require.Equal(t, []string{YouTubeLink("vid", first)}, links)
	require.Equal(t, "u1", unpack(t, got[8]).Upload.ID)
	require.Equal(t, "ℹ️", unpack(t, got[9]).Icon.Emoji)
}
func TestTextOnlyAndFrameGuards(t *testing.T) {
	ts := 4.
	syn := pipeline.Synthesis{Sections: []pipeline.Section{{EndSec: 10}}}
	frames := []pipeline.Frame{{Idx: 0, Status: "captured", Caption: " ", CapturedSec: &ts}, {Idx: 1, Status: "captured", Caption: "Diagram", CapturedSec: &ts}, {Idx: 2, Status: "omitted", Caption: "caption", CapturedSec: &ts}, {Idx: 3, Status: "captured", Caption: "missing timestamp"}}
	for _, rows := range [][]pipeline.Frame{nil, frames} {
		got := BuildBlocks(pipeline.Job{}, syn, rows, map[int]string{0: "u", 2: "v", 3: "w"}, nil, time.Time{})
		calls := 0
		for _, b := range got {
			require.NotEqual(t, "image", b["type"])
			if b["type"] == "callout" {
				calls++
				require.Equal(t, "ℹ️", unpack(t, b).Icon.Emoji)
			}
		}
		require.Equal(t, 1, calls)
	}
}
func TestWarningCalloutPlainWords(t *testing.T) {
	ts := 461.
	got := BuildBlocks(pipeline.Job{}, pipeline.Synthesis{}, nil, nil, []pipeline.Warning{{Code: pipeline.WarnFrameExtractionFailed, TimestampSec: &ts}, {Code: "UNKNOWN_CODE"}}, time.Time{})
	warn := unpack(t, got[len(got)-2])
	require.Equal(t, "⚠️", warn.Icon.Emoji)
	msg := joined(warn.Rich)
	require.Contains(t, msg, "couldn't be captured")
	require.Contains(t, msg, "07:41")
	require.NotContains(t, msg, "FRAME_")
	require.NotContains(t, msg, "UNKNOWN_CODE")
}
