package synthesis

import (
	"ainote/internal/pipeline"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestFormatTimestamp(t *testing.T) {
	require.Equal(t, "01:15", FormatTimestamp(75.9))
	require.Equal(t, "1:02:05", FormatTimestamp(3725))
	require.Equal(t, "[01:15] hello\n", FormatTranscript([]pipeline.Segment{{StartSec: 75, Text: "hello"}}))
}
