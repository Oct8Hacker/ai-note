package synthesis

import (
	"ainote/internal/pipeline"
	"math"
	"slices"
	"sort"
)

// SelectVisuals removes near-duplicates by priority, distributes slots across sections,
// and returns the selected moments in playback order.
func SelectVisuals(visuals []pipeline.Visual, window float64, maxFrames int) []pipeline.Visual {
	ranked := slices.Clone(visuals)
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].Priority != ranked[j].Priority {
			return ranked[i].Priority < ranked[j].Priority
		}
		return ranked[i].TimestampSec < ranked[j].TimestampSec
	})
	var times []float64
	bySection := map[int][]pipeline.Visual{}
	var order []int
	for _, v := range ranked {
		if slices.ContainsFunc(times, func(t float64) bool { return math.Abs(t-v.TimestampSec) <= window }) {
			continue
		}
		times = append(times, v.TimestampSec)
		if _, ok := bySection[v.SectionIndex]; !ok {
			order = append(order, v.SectionIndex)
		}
		bySection[v.SectionIndex] = append(bySection[v.SectionIndex], v)
	}
	slices.Sort(order)
	selected := []pipeline.Visual{}
	for round := 0; len(selected) < min(maxFrames, len(times)); round++ {
		for _, index := range order {
			vs := bySection[index]
			if round < len(vs) {
				selected = append(selected, vs[round])
				if len(selected) == maxFrames {
					break
				}
			}
		}
	}
	sort.SliceStable(selected, func(i, j int) bool { return selected[i].TimestampSec < selected[j].TimestampSec })
	return selected
}
