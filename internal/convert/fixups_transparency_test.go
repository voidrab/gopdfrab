package convert

import (
	"image"
	"testing"
)

func TestPackRGBSamples(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Pix[0], img.Pix[1], img.Pix[2] = 10, 20, 30
	out := packRGBSamples(img)
	if len(out) != 2*2*3 {
		t.Fatalf("packRGBSamples len = %d, want 12", len(out))
	}
	if out[0] != 10 || out[1] != 20 || out[2] != 30 {
		t.Errorf("first pixel = %v, want [10 20 30]", out[:3])
	}
}
