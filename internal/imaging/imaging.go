package imaging

import (
	"bytes"
	"fmt"
	"image"

	"github.com/disintegration/imaging"
)

const (
	MaxDimension = 2000

	JPEGQuality = 82
)

// Downsample decodes a JPEG, scales it down to MaxDimension along the long edge (if it exceeds that limit),
// and re-encodes it as a JPEG with the specified quality. Images smaller than MaxDimension are not upscaled.
//
// IMPORTANT regarding EXIF-orientation: many phones do not physically rotate the pixels when shooting
// in a "sideways" or rotated orientation; instead, they record the actual rotation in the EXIF
// Orientation tag while saving the pixels exactly as they were captured on the sensor. Standard image/jpeg
// The Go decoder completely ignores this tag. Without explicit EXIF-handling, a shot that
// looks perfectly upright in the phone's gallery might end up being sent to the vision LLM
// literally rotated by 90° or 180°—causing the model to either fail to read the letter or,
// worse, latch onto something else in the frame and start hallucinating unrelated content.
// imaging.AutoOrientation(true) reads the EXIF-data and physically rotates the pixels
// before further processing.
func Downsample(data []byte) ([]byte, error) {
	img, err := imaging.Decode(bytes.NewReader(data), imaging.AutoOrientation(true))
	if err != nil {
		return nil, fmt.Errorf("decode image: %w", err)
	}

	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()

	var resized image.Image = img
	longestSide := width
	if height > longestSide {
		longestSide = height
	}

	if longestSide > MaxDimension {
		if width >= height {
			resized = imaging.Resize(img, MaxDimension, 0, imaging.Lanczos)
		} else {
			resized = imaging.Resize(img, 0, MaxDimension, imaging.Lanczos)
		}
	}

	var out bytes.Buffer
	if err := imaging.Encode(&out, resized, imaging.JPEG, imaging.JPEGQuality(JPEGQuality)); err != nil {
		return nil, fmt.Errorf("encode jpeg: %w", err)
	}
	return out.Bytes(), nil
}
