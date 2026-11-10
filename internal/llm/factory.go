package llm

import (
	"ainote/internal/config"
	"ainote/internal/pipeline"
	"context"
	"fmt"
)

// FromConfig constructs guarded text and vision clients from worker configuration.
func FromConfig(cfg config.Worker, lim pipeline.Limiter, f pipeline.Faults, on429 func(context.Context)) (text Provider, vision Provider, err error) {
	makeProvider := func(name, model string) (Provider, error) {
		switch name {
		case "gemini":
			return NewGemini(cfg.GeminiAPIKey, model, cfg.LLMTimeout)
		case "ollama":
			return NewOllama(cfg.OllamaURL, model, cfg.LLMTimeout), nil
		case "groq":
			return NewOpenAICompat(name, "https://api.groq.com/openai/v1", cfg.GroqAPIKey, model, cfg.LLMTimeout), nil
		case "openrouter":
			return NewOpenAICompat(name, "https://openrouter.ai/api/v1", cfg.OpenRouterAPIKey, model, cfg.LLMTimeout), nil
		default:
			return nil, fmt.Errorf("unknown LLM_PROVIDER %q", name)
		}
	}
	var fallback Provider
	if cfg.LLMFallbackProvider != "" {
		fallback, err = makeProvider(cfg.LLMFallbackProvider, cfg.LLMFallbackModel)
		if err != nil {
			return nil, nil, err
		}
	}
	guarded := func(name, model string) (Provider, error) {
		p, e := makeProvider(name, model)
		if e != nil {
			return nil, e
		}
		return &Guarded{Primary: p, Fallback: fallback, Limiter: lim, Faults: f, On429: on429, TokenChunkSize: cfg.LLMTPM / 6}, nil
	}
	text, err = guarded(cfg.LLMProvider, cfg.LLMModel)
	if err != nil {
		return nil, nil, err
	}
	name, model := cfg.LLMVisionProvider, cfg.LLMVisionModel
	if name == "" {
		name = cfg.LLMProvider
	}
	if model == "" {
		model = cfg.LLMModel
	}
	if name == cfg.LLMProvider && model == cfg.LLMModel {
		return text, text, nil
	}
	vision, err = guarded(name, model)
	if err != nil {
		return nil, nil, err
	}
	return text, vision, nil
}
