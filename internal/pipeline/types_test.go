package pipeline

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStatusOrder(t *testing.T) {
	next, ok := StatusSummarizing.NextStage()
	require.True(t, ok)
	require.Equal(t, StatusExtractingFrames, next)
	_, ok = StatusPublishing.NextStage()
	require.False(t, ok)
	require.True(t, StatusDeadLettered.IsTerminal())
	require.False(t, StatusPublishing.IsTerminal())
}

func TestNotionBucketStable(t *testing.T) {
	require.Equal(t, NotionBucket("secret_x"), NotionBucket("secret_x"))
	require.NotContains(t, NotionBucket("secret_x"), "secret")
	require.NotEqual(t, NotionBucket("a"), NotionBucket("b"))
	require.Len(t, NotionBucket("a"), len("notion:")+16)
}

func TestSynthesisJSONTags(t *testing.T) {
	b, err := json.Marshal(Synthesis{Title: "t", Sections: []Section{{Heading: "h"}}, Visuals: []Visual{{TimestampSec: 1}}})
	require.NoError(t, err)
	for _, k := range []string{"title", "overview", "sections", "heading", "start_sec", "end_sec", "bullets", "visuals", "timestamp_sec", "section_index", "caption", "reason", "priority"} {
		require.Contains(t, string(b), `"`+k+`"`)
	}
}
