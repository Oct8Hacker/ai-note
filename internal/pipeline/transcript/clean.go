package transcript

import (
	"regexp"
	"strings"

	"ainote/internal/pipeline"
)

var noiseMarker = regexp.MustCompile(`\[\p{L}+\]`)

// Clean removes noise and adjacent repetitions and joins subsecond fragments.
func Clean(segments []pipeline.Segment) []pipeline.Segment {
	var cleaned []pipeline.Segment
	var previousText string
	for _, seg := range segments {
		seg.Text = strings.Join(strings.Fields(noiseMarker.ReplaceAllString(seg.Text, " ")), " ")
		if seg.Text == "" || seg.Text == previousText {
			continue
		}
		previousText = seg.Text
		if len(cleaned) > 0 && seg.EndSec-seg.StartSec < 1 {
			previous := &cleaned[len(cleaned)-1]
			previous.Text += " " + seg.Text
			previous.EndSec = max(previous.EndSec, seg.EndSec)
			continue
		}
		cleaned = append(cleaned, seg)
	}
	return cleaned
}
