package synthesis

import (
	"ainote/internal/pipeline"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestChunkSplitsWithOverlap(t *testing.T) {
	var segs []pipeline.Segment
	for i := 0; i < 7; i++ {
		segs = append(segs, pipeline.Segment{StartSec: float64(i * 30), EndSec: float64((i + 1) * 30), Text: "abc"})
	}
	require.Len(t, Chunk(segs, 1000, 30), 1)
	chunks := Chunk(segs, 9, 30)
	require.Len(t, chunks, 3)
	require.Equal(t, segs[:3], chunks[0])
	require.Equal(t, segs[2:5], chunks[1])
	require.Equal(t, segs[4:], chunks[2])
	require.Len(t, Chunk(segs, 1, 30), 7)
}
