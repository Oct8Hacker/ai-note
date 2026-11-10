package transcript

import (
	"ainote/internal/pipeline"
	"github.com/stretchr/testify/require"
	"os"
	"testing"
)

func TestCleanAutoCaptions(t *testing.T) {
	data, err := os.ReadFile("testdata/auto_en.json3")
	require.NoError(t, err)
	raw, err := ParseJSON3(data)
	require.NoError(t, err)
	require.Equal(t, []pipeline.Segment{{StartSec: 1.5, EndSec: 3, Text: "so today"}, {StartSec: 4.5, EndSec: 6, Text: "we will learn"}}, Clean(raw[:4]))
	require.Equal(t, pipeline.Segment{StartSec: 4.5, EndSec: 6.5, Text: "we will learn more"}, Clean(raw)[1])
}
