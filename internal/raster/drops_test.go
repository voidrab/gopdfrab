package raster

import (
	"slices"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

func hasDrop(drops []string, want string) bool {
	return slices.Contains(drops, want)
}

// TestRasterReportsShading: an sh operator naming a shading that isn't in the
// resources reports a drop rather than silently painting nothing. (A shading
// the rasterizer can draw is covered in shading_test.go.)
func TestRasterReportsShading(t *testing.T) {
	page := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Contents": pdf.PDFDict{HasStream: true, RawStream: []byte("q /Sh1 sh Q")},
	})}
	_, drops, err := RenderPage(page, pdf.PDFDict{}, [4]float64{0, 0, 20, 20}, 72)
	if err != nil {
		t.Fatalf("RenderPage: %v", err)
	}
	if !hasDrop(drops, DropShading) {
		t.Errorf("drops = %v, want %q", drops, DropShading)
	}
}

// TestRasterDrawsInlineImage: a BI/ID/EI inline image is drawn, not dropped.
func TestRasterDrawsInlineImage(t *testing.T) {
	content := "q 20 0 0 20 0 0 cm BI /W 2 /H 1 /BPC 8 /CS /G ID \x00\xff EI Q"
	page := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Contents": pdf.PDFDict{HasStream: true, RawStream: []byte(content)},
	})}
	img, drops, err := RenderPage(page, pdf.PDFDict{}, [4]float64{0, 0, 20, 20}, 72)
	if err != nil {
		t.Fatalf("RenderPage: %v", err)
	}
	if len(drops) != 0 {
		t.Errorf("drops = %v, want none", drops)
	}
	if got := nrgbaAt(t, img, 5, 10); got.R != 0 {
		t.Errorf("left half = %v, want the black sample", got)
	}
	if got := nrgbaAt(t, img, 15, 10); got.R != 255 {
		t.Errorf("right half = %v, want the white sample", got)
	}
}

// TestRasterCleanPageReportsNothing: an ordinary vector page drops nothing.
func TestRasterCleanPageReportsNothing(t *testing.T) {
	page := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Contents": pdf.PDFDict{HasStream: true, RawStream: []byte("1 0 0 rg 5 5 10 10 re f")},
	})}
	_, drops, err := RenderPage(page, pdf.PDFDict{}, [4]float64{0, 0, 20, 20}, 72)
	if err != nil {
		t.Fatalf("RenderPage: %v", err)
	}
	if len(drops) != 0 {
		t.Errorf("clean page reported drops %v", drops)
	}
}
