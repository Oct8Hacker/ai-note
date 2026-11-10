package synthesis

import (
	"ainote/internal/pipeline"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestValidateDropsBadVisuals(t *testing.T) {
	s := pipeline.Synthesis{Title: "T", Sections: []pipeline.Section{{EndSec: 20}, {StartSec: 20, EndSec: 50, Bullets: []string{" ", "ok"}}}, Visuals: []pipeline.Visual{{TimestampSec: 9999}, {TimestampSec: 30, SectionIndex: 99, Caption: "chart", Reason: "comparison"}}}
	got, w, err := Validate(s, 50)
	require.NoError(t, err)
	require.Len(t, w, 1)
	require.Equal(t, pipeline.WarnVisualDroppedInvalid, w[0].Code)
	require.Equal(t, 9999., *w[0].TimestampSec)
	require.Len(t, got.Visuals, 1)
	require.Equal(t, 1, got.Visuals[0].SectionIndex)
	require.Equal(t, []string{"ok"}, got.Sections[1].Bullets)
}
