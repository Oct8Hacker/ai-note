package synthesis

import (
	"ainote/internal/pipeline"
	"slices"
	"strings"
)

// Merge joins overlapping sections and remaps chunk-local visual indices.
func Merge(parts []pipeline.Synthesis, ranges [][2]float64) pipeline.Synthesis {
	var merged pipeline.Synthesis
	var overviews []string
	for i, part := range parts {
		if i == 0 {
			merged.Title = part.Title
		}
		if part.Overview != "" {
			overviews = append(overviews, part.Overview)
		}
		indices := make([]int, len(part.Sections))
		for j, section := range part.Sections {
			last := len(merged.Sections) - 1
			if i > 0 && last >= 0 && section.StartSec < ranges[i-1][1] {
				dest := &merged.Sections[last]
				for _, b := range section.Bullets {
					if !slices.Contains(dest.Bullets, b) {
						dest.Bullets = append(dest.Bullets, b)
					}
				}
				dest.EndSec = max(dest.EndSec, section.EndSec)
				indices[j] = last
			} else {
				indices[j] = last + 1
				section.Bullets = slices.Clone(section.Bullets)
				merged.Sections = append(merged.Sections, section)
			}
		}
		for _, v := range part.Visuals {
			if v.SectionIndex >= 0 && v.SectionIndex < len(indices) {
				v.SectionIndex = indices[v.SectionIndex]
			} else {
				v.SectionIndex = -1
			}
			merged.Visuals = append(merged.Visuals, v)
		}
	}
	merged.Overview = strings.Join(overviews, " ")
	return merged
}
