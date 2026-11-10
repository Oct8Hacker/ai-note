package synthesis

import (
	"ainote/internal/llm"
	"ainote/internal/pipeline"
	"context"
	"embed"
	"fmt"
	"strings"
	"text/template"
)

//go:embed prompts/*.tmpl
var promptFiles embed.FS
var prompts = template.Must(template.ParseFS(promptFiles, "prompts/*.tmpl"))

// Stage synthesizes and checkpoints transcript chunks before selecting visual moments.
type Stage struct {
	LLM            llm.Provider
	MaxInputTokens int
	DedupWindowSec float64
	MaxFrames      int
	NotesLanguage  string
}

// Name identifies the worker's synthesis stage.
func (*Stage) Name() pipeline.Status { return pipeline.StatusSummarizing }
func render(name string, data any) (string, error) {
	var b strings.Builder
	err := prompts.ExecuteTemplate(&b, name, data)
	return b.String(), err
}
func (s *Stage) generate(ctx context.Context, chunk []pipeline.Segment) (pipeline.Synthesis, error) {
	var empty pipeline.Synthesis
	lang := s.NotesLanguage
	if lang == "" {
		lang = "English"
	}
	original, err := render("synthesize.tmpl", map[string]any{"Language": lang, "RangeStart": FormatTimestamp(chunk[0].StartSec), "RangeEnd": FormatTimestamp(chunk[len(chunk)-1].EndSec), "Transcript": FormatTranscript(chunk), "MaxFrames": s.MaxFrames, "Schema": outputSchema})
	if err != nil {
		return empty, err
	}
	req := llm.JSONRequest{Prompt: original, Schema: Schema(), MaxOutputTokens: 2048}
	for repairing := false; ; repairing = true {
		out, err := s.LLM.GenerateJSON(ctx, req)
		if err != nil {
			return empty, err
		}
		part, err := decode(out.Text)
		if err == nil {
			return part, nil
		}
		if repairing {
			return empty, badOutput(err)
		}
		req.Prompt, err = render("repair.tmpl", map[string]string{"Error": err.Error(), "Original": original, "Response": out.Text})
		if err != nil {
			return empty, err
		}
	}
}

// Run resumes saved chunks and returns the finished notes to the worker for checkpointing.
func (s *Stage) Run(ctx context.Context, job *pipeline.Job, deps pipeline.Deps) (pipeline.StageResult, error) {
	var empty pipeline.StageResult
	var transcript pipeline.Transcript
	found, err := deps.Store.LoadArtifact(ctx, job.ID, "transcript", &transcript)
	if err != nil {
		return empty, err
	}
	if !found || len(transcript.Segments) == 0 {
		return empty, fmt.Errorf("missing or empty transcript for job %s", job.ID)
	}
	chunks := Chunk(transcript.Segments, s.MaxInputTokens, 30)
	parts := make([]pipeline.Synthesis, len(chunks))
	ranges := make([][2]float64, len(chunks))
	for i, chunk := range chunks {
		key := fmt.Sprintf("synthesis_chunk:%d", i)
		ranges[i] = [2]float64{chunk[0].StartSec, chunk[len(chunk)-1].EndSec}
		found, err := deps.Store.LoadArtifact(ctx, job.ID, key, &parts[i])
		if err != nil {
			return empty, err
		}
		if !found {
			parts[i], err = s.generate(ctx, chunk)
			if err != nil {
				return empty, err
			}
			if err := deps.Store.SaveArtifact(ctx, job.ID, key, parts[i]); err != nil {
				return empty, err
			}
		}
		deps.Progress(100 * (i + 1) / len(chunks))
	}
	notes, warnings, err := Validate(Merge(parts, ranges), job.DurationSec)
	if err != nil {
		return empty, err
	}
	for _, w := range warnings {
		deps.Warn(w)
	}
	notes.Visuals = SelectVisuals(notes.Visuals, s.DedupWindowSec, s.MaxFrames)
	count := len(notes.Visuals)
	return pipeline.StageResult{ArtifactKind: "synthesis", Artifact: notes, FramesRequested: &count}, nil
}
