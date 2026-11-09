package api

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseNotionID(t *testing.T) {
	const want = "01234567-89ab-cdef-0123-456789abcdef"
	for _, in := range []string{
		want,
		"0123456789abcdef0123456789abcdef",
		"0123456789ABCDEF0123456789ABCDEF",
		"https://www.notion.so/My-Page-0123456789abcdef0123456789abcdef",
		"https://www.notion.so/ws/0123456789abcdef0123456789abcdef?v=1",
	} {
		got, err := ParseNotionID(in)
		require.NoError(t, err, in)
		require.Equal(t, want, got, in)
	}
	for _, in := range []string{"", "xyz", "0123456789abcdef", "https://evil.com/0123456789abcdef0123456789abcdef", "0123456789abcdef0123456789abcdeg"} {
		_, err := ParseNotionID(in)
		require.Error(t, err, in)
	}
}
