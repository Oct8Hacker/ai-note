package logging

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRedacts(t *testing.T) {
	var buf bytes.Buffer
	l := newWithWriter(&buf, "info", "gateway")
	l.Info("hello", "token", "secret_x", "notion_token", "secret_y", "api_key", "secret_z", "keep", "visible")
	out := buf.String()
	require.NotContains(t, out, "secret_")
	require.Contains(t, out, "[REDACTED]")
	require.Contains(t, out, `"component":"gateway"`)
	require.Contains(t, out, "visible")
}

func TestLevel(t *testing.T) {
	var buf bytes.Buffer
	l := newWithWriter(&buf, "warn", "x")
	l.Info("nope")
	require.Empty(t, buf.String())
}
