package frames

import (
	"bytes"
	"image"
	"image/draw"
	"image/jpeg"
)

// Sharpness returns the variance of the grayscale four-neighbour Laplacian.
// Higher values indicate stronger edges. Images without an interior score zero.
func Sharpness(data []byte) (float64, error) {
	src, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		return 0, err
	}
	bounds := src.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width < 3 || height < 3 {
		return 0, nil
	}
	gray := image.NewGray(bounds)
	draw.Draw(gray, bounds, src, bounds.Min, draw.Src)
	var sum, squares float64
	for y := 1; y < height-1; y++ {
		for x := 1; x < width-1; x++ {
			i := y*gray.Stride + x
			pixels := gray.Pix
			lap := float64(int(pixels[i-gray.Stride]) + int(pixels[i+gray.Stride]) + int(pixels[i-1]) + int(pixels[i+1]) - 4*int(pixels[i]))
			sum += lap
			squares += lap * lap
		}
	}
	count := float64(width-2) * float64(height-2)
	mean := sum / count
	return max(0, squares/count-mean*mean), nil
}
