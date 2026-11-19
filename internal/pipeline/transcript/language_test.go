package transcript

import (
	"ainote/internal/youtube"
	"github.com/stretchr/testify/require"
	"testing"
)

func langs(names ...string) map[string]bool {
	tracks := map[string]bool{}
	for _, name := range names {
		tracks[name] = true
	}
	return tracks
}
func TestPickLanguage(t *testing.T) {
	for _, tc := range []struct {
		manual, auto map[string]bool
		want         string
		generated    bool
	}{
		{langs("en-GB", "en"), langs("en"), "en", false},
		{langs("en-GB"), langs("en"), "en-GB", false},
		{langs("de"), langs("en-US", "en"), "en", true},
		{langs("de"), langs("en-US"), "en-US", true},
		{langs("fr", "de"), langs("ar"), "de", false},
		{nil, langs("hi"), "hi", true},
		{map[string]bool{"en": false}, nil, "", false},
	} {
		got, auto, ok := PickLanguage(youtube.Metadata{Subtitles: tc.manual, AutoCaptions: tc.auto})
		require.Equal(t, tc.want, got)
		require.Equal(t, tc.generated, auto)
		require.Equal(t, tc.want != "", ok)
	}
}
