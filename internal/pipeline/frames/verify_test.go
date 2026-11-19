package frames

import (
	"ainote/internal/llm"
	"bytes"
	"context"
	"github.com/stretchr/testify/require"
	"image"
	"image/jpeg"
	"testing"
)

type visionFake struct {
	llm.Provider
	request llm.JSONRequest
	reply   string
}

func (p *visionFake) GenerateJSON(_ context.Context, r llm.JSONRequest) (llm.JSONResponse, error) {
	p.request = r
	return llm.JSONResponse{Text: p.reply}, nil
}
func TestVisionThumbnailAndIndices(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, jpeg.Encode(&buf, image.NewGray(image.Rect(0, 0, 1536, 400)), nil))
	p := &visionFake{reply: `{"frames":[{"index":4,"shows_visual":true,"matches_caption":false,"revised_caption":"line chart"}]}`}
	v := NewLLMVerifier(p)
	in := []VerifyInput{{Index: 4, JPEG: buf.Bytes(), Caption: "old", Reason: "growth"}}
	out, err := v.Verify(context.Background(), in)
	require.NoError(t, err)
	require.Equal(t, "line chart", out[0].RevisedCaption)
	require.Equal(t, "#4", p.request.Images[0].Label)
	size, err := jpeg.DecodeConfig(bytes.NewReader(p.request.Images[0].JPEG))
	require.NoError(t, err)
	require.Equal(t, 768, size.Width)
	require.Equal(t, 200, size.Height)
	for _, bad := range []string{`{"frames":[]}`, `{"frames":[{"index":4}]}`, `{"frames":[{"index":1,"shows_visual":true,"matches_caption":true,"revised_caption":""}]}`} {
		p.reply = bad
		_, err := v.Verify(context.Background(), in)
		require.Error(t, err)
	}
}
