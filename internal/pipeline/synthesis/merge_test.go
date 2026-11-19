package synthesis

import (
	"ainote/internal/pipeline"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestMergeKeepsOrder(t *testing.T) {
	a := pipeline.Section{Heading: "A", EndSec: 30, Bullets: []string{"one"}}
	b := pipeline.Section{Heading: "overlap", StartSec: 20, EndSec: 35, Bullets: []string{"one", "two"}}
	c := pipeline.Section{Heading: "C", StartSec: 40}
	d := pipeline.Section{Heading: "D", StartSec: 70}
	parts := []pipeline.Synthesis{{Title: "first", Sections: []pipeline.Section{a}}, {Sections: []pipeline.Section{b, c}, Visuals: []pipeline.Visual{{SectionIndex: 0}, {SectionIndex: 1}}}, {Sections: []pipeline.Section{d}}}
	got := Merge(parts, [][2]float64{{0, 30}, {20, 65}, {60, 80}})
	require.Len(t, got.Sections, 3)
	require.Equal(t, []string{"one", "two"}, got.Sections[0].Bullets)
	require.Equal(t, "C", got.Sections[1].Heading)
	require.Equal(t, "D", got.Sections[2].Heading)
	require.Equal(t, 1, got.Visuals[1].SectionIndex)
}
