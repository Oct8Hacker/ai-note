// Package frames captures and evaluates source video frames.
package frames

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"ainote/internal/pipeline"
	"ainote/internal/youtube"
)

// FFmpeg provides JPEG capture and scene detection on a video source.
type FFmpeg interface {
	Capture(ctx context.Context, src string, atSec float64) ([]byte, error)
	SceneChanges(ctx context.Context, src string, fromSec, toSec, threshold float64) ([]float64, error)
}
type ffmpeg struct{ runner youtube.Runner }

// NewFFmpeg runs ffmpeg through the supplied subprocess runner.
func NewFFmpeg(r youtube.Runner) FFmpeg { return &ffmpeg{runner: r} }
func seconds(value float64) string      { return strconv.FormatFloat(value, 'f', -1, 64) }

// streamUserAgent is sent with every stream read: YouTube's video servers
// refuse ffmpeg's default "Lavf" agent for some videos (HTTP 403).
const streamUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36"

// errStreamForbidden means the video host refused this stream URL (HTTP 403).
// It is not throttling: the stage retries with a fresh URL (FR-PIPE-20a).
var errStreamForbidden = errors.New("video host refused the stream URL (HTTP 403)")

// httpInput appends the browser User-Agent to ffmpeg's leading options when
// src is an HTTP(S) URL. -user_agent is an HTTP input option: ffmpeg rejects
// it for a local file.
func httpInput(src string, args ...string) []string {
	if strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") {
		args = append(args, "-user_agent", streamUserAgent)
	}
	return args
}

var (
	throttleStatus  = regexp.MustCompile(`\b429\b`)
	forbiddenStatus = regexp.MustCompile(`\b403 Forbidden\b`)
)

func classifyFFmpeg(stderr string, err error) error {
	if err == nil {
		return nil
	}
	if throttleStatus.MatchString(stderr) {
		return &pipeline.RateLimitedError{Provider: "youtube", RetryAfter: 30 * time.Second}
	}
	if forbiddenStatus.MatchString(stderr) {
		return errStreamForbidden
	}
	return &pipeline.RetryableError{Code: "FRAME_CAPTURE_FAILED", Err: err}
}
func (f *ffmpeg) Capture(ctx context.Context, src string, atSec float64) ([]byte, error) {
	data, diagnostic, err := f.runner.Run(ctx, "ffmpeg", append(httpInput(src, "-hide_banner", "-nostdin", "-loglevel", "error"), "-ss", seconds(max(0, atSec)), "-i", src, "-frames:v", "1", "-q:v", "2", "-f", "image2", "-c:v", "mjpeg", "pipe:1")...)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err == nil && len(data) == 0 {
		err = errors.New("ffmpeg produced no frame")
	}
	if err != nil {
		return nil, classifyFFmpeg(string(diagnostic), err)
	}
	return data, nil
}

var presentationTime = regexp.MustCompile(`\bpts_time:\s*([-+0-9.eE]+)`)

func (f *ffmpeg) SceneChanges(ctx context.Context, src string, fromSec, toSec, threshold float64) ([]float64, error) {
	fromSec = max(0, fromSec)
	if toSec <= fromSec {
		return nil, nil
	}
	filter := "select='gt(scene," + seconds(threshold) + ")',showinfo"
	_, diagnostic, err := f.runner.Run(ctx, "ffmpeg", append(httpInput(src, "-hide_banner", "-nostdin", "-nostats", "-loglevel", "info"), "-ss", seconds(fromSec), "-t", seconds(toSec-fromSec), "-i", src, "-an", "-vf", filter, "-f", "null", "-")...)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, classifyFFmpeg(string(diagnostic), err)
	}
	var times []float64
	for _, line := range strings.Split(string(diagnostic), "\n") {
		if !strings.Contains(line, "showinfo") {
			continue
		}
		fields := presentationTime.FindStringSubmatch(line)
		if len(fields) == 0 {
			continue
		}
		relative, err := strconv.ParseFloat(fields[1], 64)
		if err != nil {
			return nil, classifyFFmpeg("", err)
		}
		times = append(times, fromSec+relative)
	}
	return times, nil
}
