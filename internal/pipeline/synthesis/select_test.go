package synthesis

import (
	"ainote/internal/pipeline"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestSelectVisualsDedupAndCap(t *testing.T) {
	v := []pipeline.Visual{{TimestampSec: 12, Priority: 2}, {TimestampSec: 10, Priority: 1}, {TimestampSec: 50, Priority: 1}, {TimestampSec: 200, SectionIndex: 2, Priority: 3}, {TimestampSec: 100, SectionIndex: 1, Priority: 2}}
	got := SelectVisuals(v, 5, 3)
	require.Equal(t, []pipeline.Visual{v[1], v[4], v[3]}, got)
}
