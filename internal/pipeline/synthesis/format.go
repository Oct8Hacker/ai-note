// Package synthesis turns timestamped transcripts into structured notes.
package synthesis

import (
	"ainote/internal/pipeline"
	"fmt"
	"strings"
)

// FormatTimestamp formats whole seconds as mm:ss or h:mm:ss.
func FormatTimestamp(sec float64) string {
	n := int(sec)
	if n >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", n/3600, n/60%60, n%60)
	}
	return fmt.Sprintf("%02d:%02d", n/60, n%60)
}
func transcriptLine(s pipeline.Segment) string {
	return "[" + FormatTimestamp(s.StartSec) + "] " + s.Text + "\n"
}

// FormatTranscript renders each caption with its original video timestamp.
func FormatTranscript(segs []pipeline.Segment) string {
	var b strings.Builder
	for _, s := range segs {
		b.WriteString(transcriptLine(s))
	}
	return b.String()
}
