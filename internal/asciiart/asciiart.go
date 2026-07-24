// Package asciiart converts a decoded image into a monospace ASCII-art
// string: registration profile pictures, chat photo previews, and
// unlockable ASCII avatars/borders are all rendered through the same
// luminance-to-character mapping.
package asciiart

import (
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"strings"
)

// ramp maps luminance (dark to light) onto characters of increasing visual
// density, the standard technique for terminal ASCII-art conversion.
const ramp = " .:-=+*#%@"

// rowAspect corrects for terminal character cells being roughly twice as
// tall as they are wide: sampling half as many rows as a square pixel grid
// would suggest keeps the rendered art from looking vertically stretched.
const rowAspect = 0.5

// FromFile reads and decodes the image at path, then renders it as cols
// columns of ASCII art via FromImage.
func FromFile(path string, cols int) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("asciiart: open %s: %w", path, err)
	}
	defer f.Close()

	img, _, err := image.Decode(f)
	if err != nil {
		return "", fmt.Errorf("asciiart: decode %s: %w", path, err)
	}
	return FromImage(img, cols), nil
}

// FromImage renders img as cols columns of ASCII art, one line per sampled
// row. Each output cell is the average luminance of the source pixels it
// covers (box downsampling), not a single nearest-neighbor pixel, so small
// details don't disappear or alias when shrinking a normal photo down to a
// few dozen columns.
func FromImage(img image.Image, cols int) string {
	if cols < 1 {
		cols = 1
	}
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= 0 || height <= 0 {
		return ""
	}

	rows := int(float64(height) / float64(width) * float64(cols) * rowAspect)
	if rows < 1 {
		rows = 1
	}

	var b strings.Builder
	for row := 0; row < rows; row++ {
		y0 := bounds.Min.Y + row*height/rows
		y1 := bounds.Min.Y + (row+1)*height/rows
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for col := 0; col < cols; col++ {
			x0 := bounds.Min.X + col*width/cols
			x1 := bounds.Min.X + (col+1)*width/cols
			if x1 <= x0 {
				x1 = x0 + 1
			}
			b.WriteByte(ramp[rampIndex(averageLuminance(img, x0, y0, x1, y1))])
		}
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

// averageLuminance returns the mean perceptual luminance (0-1) of every
// pixel in [x0,x1)x[y0,y1).
func averageLuminance(img image.Image, x0, y0, x1, y1 int) float64 {
	var sum float64
	var n int
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			// RGBA() returns 16-bit-scaled components; normalize to 0-1
			// before applying the standard luminance weights.
			sum += 0.299*float64(r)/65535 + 0.587*float64(g)/65535 + 0.114*float64(bl)/65535
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}

func rampIndex(luminance float64) int {
	i := int(luminance * float64(len(ramp)))
	if i >= len(ramp) {
		i = len(ramp) - 1
	}
	if i < 0 {
		i = 0
	}
	return i
}
