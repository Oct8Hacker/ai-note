package api

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseVideoID(t *testing.T) {
	ok := map[string]string{
		"https://www.youtube.com/watch?v=dQw4w9WgXcQ":              "dQw4w9WgXcQ",
		"https://youtube.com/watch?v=dQw4w9WgXcQ&t=1m30s&list=PLx": "dQw4w9WgXcQ",
		"https://m.youtube.com/watch?feature=share&v=dQw4w9WgXcQ":  "dQw4w9WgXcQ",
		"https://WWW.YouTube.com/watch?v=dQw4w9WgXcQ":              "dQw4w9WgXcQ",
		"https://youtu.be/dQw4w9WgXcQ?si=abc":                      "dQw4w9WgXcQ",
		"https://www.youtube.com/embed/dQw4w9WgXcQ":                "dQw4w9WgXcQ",
		"https://www.youtube.com/shorts/dQw4w9WgXcQ":               "dQw4w9WgXcQ",
		"https://www.youtube.com/live/dQw4w9WgXcQ":                 "dQw4w9WgXcQ",
	}
	for in, want := range ok {
		got, err := ParseVideoID(in)
		require.NoError(t, err, in)
		require.Equal(t, want, got, in)
	}
	bad := []string{"", "not a url", "https://vimeo.com/123", "https://www.youtube.com/playlist?list=PLx",
		"https://www.youtube.com/watch?v=short", "https://evil.com/watch?v=dQw4w9WgXcQ", "javascript:alert(1)",
		"https://youtu.be/", "ftp://youtube.com/watch?v=dQw4w9WgXcQ"}
	for _, in := range bad {
		_, err := ParseVideoID(in)
		require.Error(t, err, in)
	}
}
