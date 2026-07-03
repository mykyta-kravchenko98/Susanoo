package imaging

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"

	"golang.org/x/image/draw"
)

const (
	MaxDimension = 2000

	JPEGQuality = 82
)

func Downsample(data []byte) ([]byte, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode image: %w", err)
	}

	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()

	longestSide := width
	if height > longestSide {
		longestSide = height
	}

	resized := img
	if longestSide > MaxDimension {
		scale := float64(MaxDimension) / float64(longestSide)
		newWidth := int(float64(width) * scale)
		newHeight := int(float64(height) * scale)

		dst := image.NewRGBA(image.Rect(0, 0, newWidth, newHeight))
		draw.CatmullRom.Scale(dst, dst.Bounds(), img, bounds, draw.Over, nil)
		resized = dst
	}

	var out bytes.Buffer
	if err := jpeg.Encode(&out, resized, &jpeg.Options{Quality: JPEGQuality}); err != nil {
		return nil, fmt.Errorf("encode jpeg: %w", err)
	}
	return out.Bytes(), nil
}