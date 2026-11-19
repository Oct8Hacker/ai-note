// Package transcript converts YouTube captions into timestamped pipeline transcripts.
package transcript

import (
	"encoding/json"
	"sort"
	"strings"

	"ainote/internal/pipeline"
)

// ParseJSON3 decodes caption events and orders timed text by its start time.
func ParseJSON3(data []byte) ([]pipeline.Segment, error) {
	var doc struct {
		Events []struct {
			StartMS    float64 `json:"tStartMs"`
			DurationMS float64 `json:"dDurationMs"`
			Segs       []struct {
				Text string `json:"utf8"`
			}
		}
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	var segments []pipeline.Segment
	for _, event := range doc.Events {
		if len(event.Segs) == 0 {
			continue
		}
		var text strings.Builder
		for _, part := range event.Segs {
			text.WriteString(part.Text)
		}
		segments = append(segments, pipeline.Segment{StartSec: event.StartMS / 1000, EndSec: (event.StartMS + event.DurationMS) / 1000, Text: text.String()})
	}
	sort.SliceStable(segments, func(i, j int) bool { return segments[i].StartSec < segments[j].StartSec })
	return segments, nil
}
