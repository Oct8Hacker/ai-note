package main

import (
	"ainote/internal/config"
	"ainote/internal/llm"
	"ainote/internal/notion"
	"ainote/internal/pipeline"
	"ainote/internal/pipeline/frames"
	"ainote/internal/pipeline/publish"
	"ainote/internal/pipeline/synthesis"
	"ainote/internal/pipeline/transcript"
	"ainote/internal/youtube"
)

// realStages builds the four pipeline stages from config and the shared
// clients. The clients already carry the Redis limiter and fault controls, so
// every YouTube, LLM and Notion call made by a stage is rate-limited.
// vision may be nil, in which case frame verification is off.
func realStages(cfg config.Worker, yt *youtube.Client, runner youtube.Runner,
	text, vision llm.Provider, nc *notion.Client) map[pipeline.Status]pipeline.Stage {
	var verifier frames.Verifier
	if vision != nil {
		verifier = frames.NewLLMVerifier(vision)
	}
	return map[pipeline.Status]pipeline.Stage{
		pipeline.StatusTranscribing: &transcript.Stage{
			YT:              yt,
			MaxVideoSeconds: cfg.MaxVideoSeconds,
		},
		pipeline.StatusSummarizing: &synthesis.Stage{
			LLM:            text,
			MaxInputTokens: cfg.LLMMaxInputTokens,
			DedupWindowSec: cfg.FrameDedupWindowSec,
			MaxFrames:      cfg.MaxFrames,
			NotesLanguage:  cfg.NotesLanguage,
		},
		pipeline.StatusExtractingFrames: &frames.Stage{
			YT:             yt,
			FF:             frames.NewFFmpeg(runner),
			Verifier:       verifier,
			Verify:         cfg.FrameVerify && verifier != nil,
			Concurrency:    cfg.FrameConcurrency,
			FrameTimeout:   cfg.FrameTimeout,
			SceneThreshold: cfg.SceneThreshold,
			MaxDriftSec:    cfg.MaxFrameDriftSec,
			// Slide snap and sharpness pick are SHOULD requirements
			// (FR-PIPE-22, FR-PIPE-23); they are always on.
			Snap:         true,
			PickSharpest: true,
		},
		pipeline.StatusPublishing: &publish.Stage{
			Notion:          nc,
			DefaultParentID: cfg.NotionDefaultParentID,
			UploadReuse:     cfg.NotionUploadReuseWindow,
			MaxImageBytes:   cfg.NotionMaxImageBytes,
		},
	}
}
