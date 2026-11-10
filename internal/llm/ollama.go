package llm

import (
	"context"
	"time"
)

type ollama struct{ endpoint }

// NewOllama connects to a local Ollama model.
func NewOllama(baseURL, model string, timeout time.Duration) Provider {
	return &ollama{endpoint{name: "ollama", base: baseURL, model: model, client: httpClient(timeout)}}
}
func (p *ollama) SupportsImages() bool { return true }
func (p *ollama) Ping(ctx context.Context) error {
	return p.call(ctx, "POST", "/api/show", map[string]string{"model": p.model}, nil)
}
func (p *ollama) GenerateJSON(ctx context.Context, r JSONRequest) (JSONResponse, error) {
	prompt := r.Prompt
	var images [][]byte
	for _, image := range r.Images {
		images = append(images, image.JPEG)
		prompt += "\n" + image.Label
	}
	user := map[string]any{"role": "user", "content": prompt}
	if len(images) > 0 {
		user["images"] = images
	}
	var format any = "json"
	if r.Schema != nil {
		format = r.Schema
	}
	body := map[string]any{"model": p.model, "stream": false, "format": format, "messages": []any{map[string]string{"role": "system", "content": r.System}, user}}
	if r.MaxOutputTokens > 0 {
		body["options"] = map[string]int{"num_predict": r.MaxOutputTokens}
	}
	var out struct {
		Message struct{ Content string }
		Input   int `json:"prompt_eval_count"`
		Output  int `json:"eval_count"`
	}
	err := p.call(ctx, "POST", "/api/chat", body, &out)
	return JSONResponse{out.Message.Content, out.Input, out.Output}, err
}
