package llm

// EstimateTokens reserves text, image and output tokens before generation.
func EstimateTokens(req JSONRequest) int {
	return (len(req.System)+len(req.Prompt)+3)/4 + 260*len(req.Images) + req.MaxOutputTokens
}
