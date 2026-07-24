package asciiart

import (
	"image"
	"image/color"
	"strings"
	"testing"
)

func TestFromImageBlackAndWhite(t *testing.T) {
	// A 10x10 image split vertically: left half black, right half white.
	// Rendered at 10 columns, every cell in the left half should land on
	// the darkest ramp character and every cell in the right half on the
	// lightest.
	img := image.NewGray(image.Rect(0, 0, 10, 10))
	for y := 0; y < 10; y++ {
		for x := 0; x < 10; x++ {
			v := uint8(0)
			if x >= 5 {
				v = 255
			}
			img.SetGray(x, y, color.Gray{Y: v})
		}
	}

	art := FromImage(img, 10)
	lines := strings.Split(art, "\n")
	if len(lines) == 0 {
		t.Fatalf("expected at least one row, got none")
	}
	for _, line := range lines {
		if len(line) != 10 {
			t.Fatalf("expected 10 columns, got %d (%q)", len(line), line)
		}
		for i, ch := range line {
			want := byte(ramp[0])
			if i >= 5 {
				want = byte(ramp[len(ramp)-1])
			}
			if byte(ch) != want {
				t.Errorf("col %d: got %q, want %q", i, ch, want)
			}
		}
	}
}

func TestFromImageAspectCorrection(t *testing.T) {
	// A square image should render fewer rows than columns, since terminal
	// cells are taller than they are wide.
	img := image.NewGray(image.Rect(0, 0, 100, 100))
	art := FromImage(img, 40)
	rows := strings.Split(art, "\n")
	if len(rows) >= 40 {
		t.Errorf("expected row count well under column count 40 for a square image, got %d rows", len(rows))
	}
	if len(rows) == 0 {
		t.Errorf("expected at least one row")
	}
}

func TestFromImageEmptyBounds(t *testing.T) {
	img := image.NewGray(image.Rect(0, 0, 0, 0))
	if got := FromImage(img, 10); got != "" {
		t.Errorf("expected empty output for zero-size image, got %q", got)
	}
}

func TestFromImageMinCols(t *testing.T) {
	img := image.NewGray(image.Rect(0, 0, 4, 4))
	art := FromImage(img, 0)
	if art == "" {
		t.Errorf("expected non-empty output even with cols<1 (clamped to 1)")
	}
}
