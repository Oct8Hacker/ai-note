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

var throttleStatus = regexp.MustCompile(`\b429\b|\b403 Forbidden\b`)

func classifyFFmpeg(stderr string, err error) error {
	if err == nil {
		return nil
	}
	if throttleStatus.MatchString(stderr) {
		return &pipeline.RateLimitedError{Provider: "youtube", RetryAfter: 30 * time.Second}
	}
	return &pipeline.RetryableError{Code: "FRAME_CAPTURE_FAILED", Err: err}
}
func (f *ffmpeg) Capture(ctx context.Context, src string, atSec float64) ([]byte, error) {
	data, diagnostic, err := f.runner.Run(ctx, "ffmpeg", "-hide_banner", "-nostdin", "-loglevel", "error", "-ss", seconds(max(0, atSec)), "-i", src, "-frames:v", "1", "-q:v", "2", "-f", "image2", "-c:v", "mjpeg", "pipe:1")
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
	_, diagnostic, err := f.runner.Run(ctx, "ffmpeg", "-hide_banner", "-nostdin", "-nostats", "-loglevel", "info", "-ss", seconds(fromSec), "-t", seconds(toSec-fromSec), "-i", src, "-an", "-vf", filter, "-f", "null", "-")
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
