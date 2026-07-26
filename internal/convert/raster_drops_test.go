package convert

import (
	"image"
	"slices"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

func hasDrop(drops []string, want string) bool {
	return slices.Contains(drops, want)
}

// TestRasterReportsShading: an sh operator naming a shading that isn't in the
// resources reports a drop rather than silently painting nothing. (A shading
// the rasterizer can draw is covered in raster_shading_test.go.)
func TestRasterReportsShading(t *testing.T) {
	page := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Contents": pdf.PDFDict{HasStream: true, RawStream: []byte("q /Sh1 sh Q")},
	}}
	_, drops, err := RenderPage(page, pdf.PDFDict{}, [4]float64{0, 0, 20, 20}, 72)
	if err != nil {
		t.Fatalf("RenderPage: %v", err)
	}
	if !hasDrop(drops, dropShading) {
		t.Errorf("drops = %v, want %q", drops, dropShading)
	}
}

// TestRasterDrawsInlineImage: a BI/ID/EI inline image is drawn, not dropped.
func TestRasterDrawsInlineImage(t *testing.T) {
	content := "q 20 0 0 20 0 0 cm BI /W 2 /H 1 /BPC 8 /CS /G ID \x00\xff EI Q"
	page := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Contents": pdf.PDFDict{HasStream: true, RawStream: []byte(content)},
	}}
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
	page := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Contents": pdf.PDFDict{HasStream: true, RawStream: []byte("1 0 0 rg 5 5 10 10 re f")},
	}}
	_, drops, err := RenderPage(page, pdf.PDFDict{}, [4]float64{0, 0, 20, 20}, 72)
	if err != nil {
		t.Fatalf("RenderPage: %v", err)
	}
	if len(drops) != 0 {
		t.Errorf("clean page reported drops %v", drops)
	}
}

// TestRasterInvisibleTextRenderMode: text drawn under render mode 3 (invisible,
// the OCR-layer case) must paint nothing, while the same text at mode 0 does.
func TestRasterInvisibleTextRenderMode(t *testing.T) {
	ff := loadEmbeddableTTF(t)
	desc := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "FontDescriptor"}, "FontName": pdf.PDFName{Value: "LiberationSans"},
		"Flags": pdf.PDFInteger(32), "FontFile2": ff,
	}}
	widths := make(pdf.PDFArray, 95)
	for i := range widths {
		widths[i] = pdf.PDFInteger(500)
	}
	font := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Font"}, "Subtype": pdf.PDFName{Value: "TrueType"},
		"BaseFont": pdf.PDFName{Value: "LiberationSans"}, "Encoding": pdf.PDFName{Value: "WinAnsiEncoding"},
		"FirstChar": pdf.PDFInteger(32), "LastChar": pdf.PDFInteger(126),
		"Widths": widths, "FontDescriptor": desc,
	}}
	resources := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Font": pdf.PDFDict{Entries: map[string]pdf.PDFValue{"F1": font}},
	}}
	render := func(tr string) *image.RGBA {
		page := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
			"Contents": pdf.PDFDict{HasStream: true,
				RawStream: []byte("BT /F1 20 Tf 0 0 0 rg " + tr + " 5 15 Td (ABC) Tj ET")},
		}}
		img, _, err := RenderPage(page, resources, [4]float64{0, 0, 120, 40}, 72)
		if err != nil {
			t.Fatalf("RenderPage: %v", err)
		}
		return img
	}

	if inkFraction(render("0 Tr")) < inkThreshold {
		t.Fatal("mode 0 text painted no ink; the test cannot distinguish invisibility")
	}
	if got := inkFraction(render("3 Tr")); got >= inkThreshold {
		t.Errorf("mode 3 (invisible) text painted ink fraction %.4f, want ~0", got)
	}
}

// TestFlattenPageReportsDrops: flattening a page whose shading cannot be drawn
// records the dropped feature, which rasterBackstop surfaces as
// ConvertResult.RasterDrops.
func TestFlattenPageReportsDrops(t *testing.T) {
	page := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Contents": pdf.PDFDict{HasStream: true, RawStream: []byte("q /Sh1 sh Q\n0 0 0 rg 2 2 5 5 re f")},
	}}
	drops, changed := flattenPageToImage(page, pdf.PDFDict{}, [4]float64{0, 0, 20, 20}, 72)
	if !changed {
		t.Fatal("flattenPageToImage did not flatten the page")
	}
	if !hasDrop(drops, dropShading) {
		t.Errorf("flatten drops = %v, want %q", drops, dropShading)
	}
}
