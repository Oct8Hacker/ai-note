package publish

import (
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestPageTitle(t *testing.T) {
	id := ShortID(uuid.MustParse("00abcd12-abcd-4321-baaa-123456789abc"))
	require.Equal(t, "00abcd", id)
	require.Equal(t, strings.Repeat("📘", 150)+" — Notes · 00abcd", PageTitle(strings.Repeat("📘", 151), id))
	require.Equal(t, "https://www.youtube.com/watch?v=vid&t=75s", YouTubeLink("vid", 75.9))
}
func TestSplitRichText(t *testing.T) {
	s := strings.Repeat("界", 4500)
	parts := SplitRichText(s, 2000)
	require.Len(t, parts, 3)
	require.Equal(t, s, strings.Join(parts, ""))
	for _, p := range parts {
		require.LessOrEqual(t, utf8.RuneCountInString(p), 2000)
		require.True(t, utf8.ValidString(p))
	}
	require.Equal(t, []string{"one ", "two"}, SplitRichText("one two", 5))
}
