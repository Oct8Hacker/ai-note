package youtube

import (
	"ainote/internal/pipeline"
	"ainote/internal/pipeline/pipelinetest"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type fakeRunner struct {
	name string
	args []string
	out  []byte
}

func (r *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, []byte, error) {
	r.name = name
	r.args = args
	return r.out, nil, nil
}

type recordingLimiter struct {
	bucket string
	n      int
}

type runnerFunc func(context.Context, string, ...string) ([]byte, []byte, error)

func (f runnerFunc) Run(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	return f(ctx, name, args...)
}

func TestCaptionsReadsSelectedTrackAndCleansUp(t *testing.T) {
	for _, mode := range []string{"--write-subs", "--write-auto-subs"} {
		t.Run(mode, func(t *testing.T) {
			var path string
			r := runnerFunc(func(_ context.Context, _ string, args ...string) ([]byte, []byte, error) {
				i := slices.Index(args, "-o")
				if i < 0 || i+1 >= len(args) || !slices.Contains(args, mode) {
					t.Fatalf("unexpected args: %v", args)
				}
				path = strings.Replace(args[i+1], "%(ext)s", "en.json3", 1)
				return nil, nil, os.WriteFile(filepath.Clean(path), []byte("caption fixture"), 0600)
			})
			client := NewClient(r, pipelinetest.NoopLimiter{}, pipelinetest.NoFaults{}, "")
			data, err := client.Captions(context.Background(), "dQw4w9WgXcQ", "en", mode == "--write-auto-subs")
			if err != nil || string(data) != "caption fixture" {
				t.Fatalf("got %q, %v", data, err)
			}
			if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
				t.Fatalf("temporary directory still present: %v", err)
			}
		})
	}
}

func TestStreamURL(t *testing.T) {
	r := &fakeRunner{out: []byte("https://example.org/stream\n")}
	client := NewClient(r, pipelinetest.NoopLimiter{}, pipelinetest.NoFaults{}, "")
	url, err := client.StreamURL(context.Background(), "dQw4w9WgXcQ")
	if err != nil || url != "https://example.org/stream" || !slices.Contains(r.args, "bv*[height<=720][ext=mp4]/b[height<=720]") {
		t.Fatal(url, err, r.args)
	}
}

func (l *recordingLimiter) Acquire(_ context.Context, b string, n int) error {
	l.bucket = b
	l.n = n
	return nil
}
func TestMetadataParses(t *testing.T) {
	data, err := os.ReadFile("testdata/metadata.json")
	if err != nil {
		t.Fatal(err)
	}
	r := &fakeRunner{out: data}
	l := &recordingLimiter{}
	c := NewClient(r, l, pipelinetest.NoFaults{}, "")
	m, err := c.Metadata(context.Background(), "dQw4w9WgXcQ")
	if err != nil || m.Title != "Lecture" || m.DurationSec != 1147 || !m.Subtitles["en"] || m.Subtitles["fr"] || !m.AutoCaptions["hi"] {
		t.Fatalf("%+v: %v", m, err)
	}
	if r.name != "yt-dlp" || !slices.Contains(r.args, "https://www.youtube.com/watch?v=dQw4w9WgXcQ") {
		t.Fatal(r.name, r.args)
	}
	if l.bucket != pipeline.BucketYouTube || l.n != 1 {
		t.Fatal(l)
	}
}
func TestRejectsBadVideoID(t *testing.T) {
	r := &fakeRunner{}
	c := NewClient(r, pipelinetest.NoopLimiter{}, pipelinetest.NoFaults{}, "")
	_, err := c.Metadata(context.Background(), "abc;rm -rf")
	if err == nil || r.name != "" {
		t.Fatal(err, r.name)
	}
}
