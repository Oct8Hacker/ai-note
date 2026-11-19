package youtube

import (
	"ainote/internal/pipeline"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var videoIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)

// Metadata describes the video and the languages offering JSON3 captions.
type Metadata struct {
	Title                   string
	DurationSec             int
	Subtitles, AutoCaptions map[string]bool
}

// Client fetches YouTube resources under the shared rate and fault controls.
type Client struct {
	runner      Runner
	limiter     pipeline.Limiter
	faults      pipeline.Faults
	cookiesFile string
}

// NewClient configures a YouTube client with an optional cookies file.
func NewClient(r Runner, lim pipeline.Limiter, f pipeline.Faults, cookiesFile string) *Client {
	return &Client{runner: r, limiter: lim, faults: f, cookiesFile: cookiesFile}
}
func (c *Client) run(ctx context.Context, id string, args ...string) ([]byte, error) {
	if !videoIDPattern.MatchString(id) {
		return nil, errors.New("invalid YouTube video ID")
	}
	if err := c.faults.Check(ctx, pipeline.FaultYouTube); err != nil {
		return nil, err
	}
	if err := c.limiter.Acquire(ctx, pipeline.BucketYouTube, 1); err != nil {
		return nil, err
	}
	args = append(args, "--no-warnings", "--ignore-config")
	if c.cookiesFile != "" {
		args = append(args, "--cookies", c.cookiesFile)
	}
	args = append(args, "--", "https://www.youtube.com/watch?v="+id)
	stdout, stderr, err := c.runner.Run(ctx, "yt-dlp", args...)
	if err != nil {
		return nil, ClassifyYtDlpError(string(stderr), err)
	}
	return stdout, nil
}

type tracks map[string][]struct{ Ext string }

func (ts tracks) json3Languages() map[string]bool {
	langs := make(map[string]bool)
	for lang, formats := range ts {
		for _, format := range formats {
			if format.Ext == "json3" {
				langs[lang] = true
				break
			}
		}
	}
	return langs
}

// Metadata reads video details without downloading the video.
func (c *Client) Metadata(ctx context.Context, id string) (Metadata, error) {
	data, err := c.run(ctx, id, "-J", "--skip-download", "--no-playlist")
	if err != nil {
		return Metadata{}, err
	}
	var raw struct {
		Title     string
		Duration  float64
		Subtitles tracks
		Auto      tracks `json:"automatic_captions"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return Metadata{}, fmt.Errorf("decode YouTube metadata: %w", err)
	}
	return Metadata{Title: raw.Title, DurationSec: int(raw.Duration), Subtitles: raw.Subtitles.json3Languages(), AutoCaptions: raw.Auto.json3Languages()}, nil
}

// Captions retrieves one manual or automatic JSON3 caption track.
func (c *Client) Captions(ctx context.Context, id, lang string, auto bool) ([]byte, error) {
	dir, err := os.MkdirTemp("", "ainote-captions-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	mode := "--write-subs"
	if auto {
		mode = "--write-auto-subs"
	}
	_, err = c.run(ctx, id, "--skip-download", "--no-playlist", mode, "--sub-langs", regexp.QuoteMeta(lang), "--sub-format", "json3", "-o", filepath.Join(dir, "caption.%(ext)s"))
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.Name() == "caption."+lang+".json3" {
			return os.ReadFile(filepath.Join(dir, entry.Name())) // #nosec G304 -- entry comes from the private temporary directory listing.
		}
	}
	return nil, errors.New("yt-dlp did not produce the requested JSON3 caption file")
}

// StreamURL resolves a fresh direct URL for frame capture.
func (c *Client) StreamURL(ctx context.Context, id string) (string, error) {
	data, err := c.run(ctx, id, "--no-playlist", "-g", "-f", "bv*[height<=720][ext=mp4]/b[height<=720]")
	if err != nil {
		return "", err
	}
	url := strings.TrimSpace(string(data))
	if url == "" {
		return "", errors.New("yt-dlp returned an empty stream URL")
	}
	return url, nil
}
