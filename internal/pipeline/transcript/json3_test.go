package transcript

import (
	"ainote/internal/pipeline"
	"github.com/stretchr/testify/require"
	"os"
	"testing"
)

func TestParseJSON3Manual(t *testing.T) {
	data, err := os.ReadFile("testdata/manual_en.json3")
	require.NoError(t, err)
	got, err := ParseJSON3(data)
	require.NoError(t, err)
	require.Equal(t, []pipeline.Segment{{StartSec: 0, EndSec: 2.5, Text: "hello and welcome"}}, got)
	_, err = ParseJSON3([]byte("{"))
	require.Error(t, err)
}
