package frames

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"

	"ainote/internal/llm"
	"github.com/santhosh-tekuri/jsonschema/v5"
)

// VerifyInput pairs a captured frame with the proposed description.
type VerifyInput struct {
	Index           int
	JPEG            []byte
	Caption, Reason string
}

// VerifyOutput records the vision model's decision for a single frame.
type VerifyOutput struct {
	Index          int    `json:"index"`
	ShowsVisual    bool   `json:"shows_visual"`
	MatchesCaption bool   `json:"matches_caption"`
	RevisedCaption string `json:"revised_caption"`
}

// Verifier checks all supplied frames in one batch.
type Verifier interface {
	Verify(context.Context, []VerifyInput) ([]VerifyOutput, error)
}
type llmVerifier struct{ provider llm.Provider }

// NewLLMVerifier creates a batched vision verifier.
func NewLLMVerifier(p llm.Provider) Verifier { return llmVerifier{p} }

//go:embed prompts/verify.tmpl
var verificationPrompt string

const verificationSchema = `{"type":"object","required":["frames"],"properties":{"frames":{"type":"array","items":{"type":"object","required":["index","shows_visual","matches_caption","revised_caption"],"properties":{"index":{"type":"integer"},"shows_visual":{"type":"boolean"},"matches_caption":{"type":"boolean"},"revised_caption":{"type":"string"}}}}}}`

var verificationContract = jsonschema.MustCompileString("verify.json", verificationSchema)

func thumbnail(data []byte) ([]byte, error) {
	src, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	bounds := src.Bounds()
	const width = 768
	if bounds.Dx() <= width {
		return data, nil
	}
	height := max(1, bounds.Dy()*width/bounds.Dx())
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			dst.Set(x, y, src.At(bounds.Min.X+x*bounds.Dx()/width, bounds.Min.Y+y*bounds.Dy()/height))
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, nil); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (v llmVerifier) Verify(ctx context.Context, in []VerifyInput) ([]VerifyOutput, error) {
	if len(in) == 0 {
		return nil, nil
	}
	req := llm.JSONRequest{System: verificationPrompt, MaxOutputTokens: 256 * len(in)}
	if err := json.Unmarshal([]byte(verificationSchema), &req.Schema); err != nil {
		return nil, err
	}
	var captions []map[string]any
	wanted := make(map[int]bool, len(in))
	for _, f := range in {
		data, err := thumbnail(f.JPEG)
		if err != nil {
			return nil, err
		}
		req.Images = append(req.Images, llm.Image{Label: fmt.Sprintf("#%d", f.Index), JPEG: data})
		captions = append(captions, map[string]any{"index": f.Index, "caption": f.Caption, "reason": f.Reason})
		wanted[f.Index] = true
	}
	metadata, err := json.Marshal(captions)
	if err != nil {
		return nil, err
	}
	req.Prompt = string(metadata)
	resp, err := v.provider.GenerateJSON(ctx, req)
	if err != nil {
		return nil, err
	}
	var raw any
	if err := json.Unmarshal([]byte(resp.Text), &raw); err != nil {
		return nil, err
	}
	if err := verificationContract.Validate(raw); err != nil {
		return nil, err
	}
	var result struct{ Frames []VerifyOutput }
	if err := json.Unmarshal([]byte(resp.Text), &result); err != nil {
		return nil, err
	}
	for _, out := range result.Frames {
		if !wanted[out.Index] {
			return nil, fmt.Errorf("unexpected verification index %d", out.Index)
		}
		delete(wanted, out.Index)
	}
	if len(wanted) != 0 {
		return nil, fmt.Errorf("verification omitted %d frames", len(wanted))
	}
	return result.Frames, nil
}
