package llm

import (
	"context"
	"fmt"
	"math"
	"time"

	"google.golang.org/genai"
)

// geminiThinkingHeadroom is added to a request's MaxOutputTokens. Gemini Flash
// models think before answering (on gemini-3.8-flash it cannot be turned off),
// and thought tokens count against maxOutputTokens; without headroom the JSON
// is cut off and synthesis fails with LLM_BAD_OUTPUT. SDK v1.20.0 has no
// thinking-level setting, so a larger cap is the available lever.
const geminiThinkingHeadroom = 8192

type gemini struct {
	client *genai.Client
	model  string
}

// NewGemini constructs the Gemini API client using API-key authentication.
func NewGemini(apiKey, model string, timeout time.Duration) (Provider, error) {
	return newGemini(apiKey, model, timeout, "")
}
func newGemini(key, model string, timeout time.Duration, base string) (Provider, error) {
	client, err := genai.NewClient(context.Background(), &genai.ClientConfig{APIKey: key, Backend: genai.BackendGeminiAPI, HTTPClient: httpClient(timeout), HTTPOptions: genai.HTTPOptions{BaseURL: base}})
	if err != nil {
		return nil, err
	}
	return &gemini{client, model}, nil
}
func (*gemini) Name() string         { return "gemini" }
func (*gemini) SupportsImages() bool { return true }
func (p *gemini) Ping(ctx context.Context) error {
	_, err := p.client.Models.Get(ctx, p.model, nil)
	return providerError(ctx, err)
}
func (p *gemini) GenerateJSON(ctx context.Context, r JSONRequest) (JSONResponse, error) {
	if r.MaxOutputTokens < 0 || r.MaxOutputTokens > math.MaxInt32-geminiThinkingHeadroom {
		return JSONResponse{}, fmt.Errorf("invalid max output tokens: %d", r.MaxOutputTokens)
	}
	maxOut := int32(r.MaxOutputTokens) // bounded by the check above
	if maxOut > 0 {                    // 0 means no cap; leave it uncapped
		maxOut += geminiThinkingHeadroom
	}
	cfg := &genai.GenerateContentConfig{ResponseMIMEType: "application/json", ResponseJsonSchema: r.Schema, MaxOutputTokens: maxOut}
	if r.System != "" {
		cfg.SystemInstruction = &genai.Content{Parts: []*genai.Part{{Text: r.System}}}
	}
	parts := []*genai.Part{{Text: r.Prompt}}
	for _, image := range r.Images {
		parts = append(parts, &genai.Part{Text: image.Label}, &genai.Part{InlineData: &genai.Blob{MIMEType: "image/jpeg", Data: image.JPEG}})
	}
	out, err := p.client.Models.GenerateContent(ctx, p.model, []*genai.Content{{Role: "user", Parts: parts}}, cfg)
	if err != nil {
		return JSONResponse{}, providerError(ctx, err)
	}
	result := JSONResponse{Text: out.Text()}
	if out.UsageMetadata != nil {
		result.InputTokens = int(out.UsageMetadata.PromptTokenCount)
		result.OutputTokens = int(out.UsageMetadata.CandidatesTokenCount)
	}
	return result, nil
}
