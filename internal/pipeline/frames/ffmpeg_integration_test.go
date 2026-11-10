//go:build integration

package frames

import (
	"bytes"
	"context"
	"fmt"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ainote/internal/youtube"
	"github.com/stretchr/testify/require"
)

var slides string

func TestMain(m *testing.M) { os.Exit(runIntegration(m)) }
func runIntegration(m *testing.M) int {
	dir, err := os.MkdirTemp("", "ainote-slides-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() { _ = os.RemoveAll(dir) }()
	slides = filepath.Join(dir, "slides.mp4")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, stderr, err := (youtube.ExecRunner{}).Run(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=c=red:s=320x240:d=3", "-f", "lavfi", "-i", "color=c=blue:s=320x240:d=3", "-filter_complex", "concat=n=2:v=1:a=0", "-c:v", "mpeg4", slides)
	if err != nil {
		fmt.Fprintf(os.Stderr, "generate test slides: %v: %s\n", err, stderr)
		return 1
	}
	return m.Run()
}
func TestCaptureRealFrame(t *testing.T) {
	ff := NewFFmpeg(youtube.ExecRunner{})
	for _, sec := range []float64{1, 4} {
		data, err := ff.Capture(context.Background(), slides, sec)
		require.NoError(t, err)
		require.True(t, bytes.HasPrefix(data, []byte{0xff, 0xd8}))
		img, err := jpeg.Decode(bytes.NewReader(data))
		require.NoError(t, err)
		r, _, b, _ := img.At(160, 120).RGBA()
		if sec < 3 {
			require.Greater(t, r, b)
		} else {
			require.Greater(t, b, r)
		}
	}
	_, err := ff.Capture(context.Background(), slides, 20)
	require.Error(t, err)
}
func TestSceneChangeDetected(t *testing.T) {
	ff := NewFFmpeg(youtube.ExecRunner{})
	changes, err := ff.SceneChanges(context.Background(), slides, 1, 5, 0.1)
	require.NoError(t, err)
	require.Len(t, changes, 1)
	require.InDelta(t, 3., changes[0], 0.05)
}
