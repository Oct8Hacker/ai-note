package llm

import (
	"context"
	"encoding/base64"
	"errors"
	"net/url"
	"strings"
	"time"
)

type compat struct{ endpoint }

// NewOpenAICompat connects to an OpenAI-compatible chat completions API.
func NewOpenAICompat(name, baseURL, apiKey, model string, timeout time.Duration) Provider {
	return &compat{endpoint{name, baseURL, apiKey, model, httpClient(timeout)}}
}
func (p *compat) SupportsImages() bool { return true }
func (p *compat) Ping(ctx context.Context) error {
	if p.name == "openrouter" {
		author, slug, ok := strings.Cut(p.model, "/")
		if !ok || author == "" || slug == "" {
			return errors.New("OpenRouter requires an author/model identifier")
		}
		return p.call(ctx, "GET", "/model/"+url.PathEscape(author)+"/"+url.PathEscape(slug), nil, nil)
	}
	return p.call(ctx, "GET", "/models/"+url.PathEscape(p.model), nil, nil)
}
func (p *compat) GenerateJSON(ctx context.Context, r JSONRequest) (JSONResponse, error) {
	var content any = r.Prompt
	if len(r.Images) > 0 {
		parts := []any{map[string]string{"type": "text", "text": r.Prompt}}
		for _, image := range r.Images {
			parts = append(parts, map[string]string{"type": "text", "text": image.Label}, map[string]any{"type": "image_url", "image_url": map[string]string{"url": "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(image.JPEG)}})
		}
		content = parts
	}
	body := map[string]any{"model": p.model, "messages": []any{map[string]string{"role": "system", "content": r.System}, map[string]any{"role": "user", "content": content}}, "response_format": map[string]string{"type": "json_object"}}
	if r.MaxOutputTokens > 0 {
		body["max_tokens"] = r.MaxOutputTokens
	}
	var out struct {
		Choices []struct{ Message struct{ Content string } }
		Usage   struct {
			Input  int `json:"prompt_tokens"`
			Output int `json:"completion_tokens"`
		}
	}
	if err := p.call(ctx, "POST", "/chat/completions", body, &out); err != nil {
		return JSONResponse{}, err
	}
	if len(out.Choices) == 0 {
		return JSONResponse{}, errors.New("provider returned no choices")
	}
	return JSONResponse{out.Choices[0].Message.Content, out.Usage.Input, out.Usage.Output}, nil
}
