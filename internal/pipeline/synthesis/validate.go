package synthesis

import (
	"ainote/internal/pipeline"
	"errors"
	"math"
	"strings"
)

func badOutput(err error) error { return &pipeline.RetryableError{Code: "LLM_BAD_OUTPUT", Err: err} }

// Validate cleans bullets, identifies unusable visuals, and repairs invalid section indices.
func Validate(s pipeline.Synthesis, durationSec int) (pipeline.Synthesis, []pipeline.Warning, error) {
	if strings.TrimSpace(s.Title) == "" || len(s.Sections) == 0 {
		return s, nil, badOutput(errors.New("notes require a title and at least one section"))
	}
	for i, sec := range s.Sections {
		bullets := make([]string, 0, len(sec.Bullets))
		for _, b := range sec.Bullets {
			if b = strings.TrimSpace(b); b != "" {
				bullets = append(bullets, b)
			}
		}
		s.Sections[i].Bullets = bullets
	}
	kept := make([]pipeline.Visual, 0, len(s.Visuals))
	var warnings []pipeline.Warning
	for _, v := range s.Visuals {
		if v.TimestampSec < 0 || v.TimestampSec > float64(durationSec) || strings.TrimSpace(v.Caption) == "" || strings.TrimSpace(v.Reason) == "" {
			ts := v.TimestampSec
			warnings = append(warnings, pipeline.Warning{Code: pipeline.WarnVisualDroppedInvalid, TimestampSec: &ts})
			continue
		}
		if v.SectionIndex < 0 || v.SectionIndex >= len(s.Sections) {
			nearest := math.Inf(1)
			for i, sec := range s.Sections {
				distance := max(0, sec.StartSec-v.TimestampSec, v.TimestampSec-sec.EndSec)
				if distance < nearest {
					nearest = distance
					v.SectionIndex = i
				}
			}
		}
		kept = append(kept, v)
	}
	s.Visuals = kept
	return s, warnings, nil
}
