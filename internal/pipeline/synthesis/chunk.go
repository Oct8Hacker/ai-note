package synthesis

import (
	"ainote/internal/pipeline"
	"unicode/utf8"
)

// Chunk splits on caption boundaries. An oversized single caption remains intact.
func Chunk(segs []pipeline.Segment, maxTokens int, overlapSec float64) [][]pipeline.Segment {
	lengths := make([]int, len(segs))
	for i, s := range segs {
		lengths[i] = utf8.RuneCountInString(transcriptLine(s))
	}
	var chunks [][]pipeline.Segment
	for start := 0; start < len(segs); {
		end := start + 1
		size := lengths[start]
		for end < len(segs) && (size+lengths[end]+3)/4 <= maxTokens {
			size += lengths[end]
			end++
		}
		chunks = append(chunks, segs[start:end])
		if end == len(segs) {
			break
		}
		// Retain only overlap that leaves room for at least one new caption.
		next := end
		size = lengths[end]
		cutoff := segs[end-1].EndSec - overlapSec
		for next > start+1 && segs[next-1].StartSec >= cutoff && (size+lengths[next-1]+3)/4 <= maxTokens {
			next--
			size += lengths[next]
		}
		start = next
	}
	return chunks
}
