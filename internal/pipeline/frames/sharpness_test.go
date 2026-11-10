package frames

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSharpness(t *testing.T) {
	score := func(contrast uint8) float64 {
		img := image.NewGray(image.Rect(0, 0, 64, 64))
		for y := 0; y < 64; y++ {
			for x := 0; x < 64; x++ {
				v := uint8(128)
				if (x/4+y/4)%2 == 0 {
					v += contrast
				} else {
					v -= contrast
				}
				img.SetGray(x, y, color.Gray{Y: v})
			}
		}
		var buf bytes.Buffer
		require.NoError(t, jpeg.Encode(&buf, img, nil))
		v, err := Sharpness(buf.Bytes())
		require.NoError(t, err)
		return v
	}
	require.Greater(t, score(120), score(0))
	_, err := Sharpness([]byte("not JPEG"))
	require.Error(t, err)
}
