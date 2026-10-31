package raster

import (
	"slices"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

func hasDrop(drops []string, want string) bool {
	return slices.Contains(drops, want)
}

// TestRasterReportsShading verifies that an sh operator naming a shading that
// isn't in the resources reports a drop rather than silently painting nothing.
// (A shading the rasterizer can draw is covered in shading_test.go.)
func TestRasterReportsShading(t *testing.T) {
	page := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Contents": pdf.NewStreamDict(nil, []byte("q /Sh1 sh Q")),
	})}
	_, drops, err := RenderPage(page, pdf.PDFDict{}, [4]float64{0, 0, 20, 20}, 72, nil)
	if err != nil {
		t.Fatalf("RenderPage: %v", err)
	}
	if !hasDrop(drops, DropShading) {
		t.Errorf("drops = %v, want %q", drops, DropShading)
	}
}

// TestRasterDrawsInlineImage verifies that a BI/ID/EI inline image is drawn,
// not dropped.
func TestRasterDrawsInlineImage(t *testing.T) {
	content := "q 20 0 0 20 0 0 cm BI /W 2 /H 1 /BPC 8 /CS /G ID \x00\xff EI Q"
	page := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Contents": pdf.NewStreamDict(nil, []byte(content)),
	})}
	img, drops, err := RenderPage(page, pdf.PDFDict{}, [4]float64{0, 0, 20, 20}, 72, nil)
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

// TestRasterCleanPageReportsNothing verifies that an ordinary vector page
// drops nothing.
func TestRasterCleanPageReportsNothing(t *testing.T) {
	page := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Contents": pdf.NewStreamDict(nil, []byte("1 0 0 rg 5 5 10 10 re f")),
	})}
	_, drops, err := RenderPage(page, pdf.PDFDict{}, [4]float64{0, 0, 20, 20}, 72, nil)
	if err != nil {
		t.Fatalf("RenderPage: %v", err)
	}
	if len(drops) != 0 {
		t.Errorf("clean page reported drops %v", drops)
	}
}

// placeholderImagePage paints a 20x20 Image XObject /Im0 over the whole page.
func placeholderImagePage(im pdf.PDFDict) (pdf.PDFDict, pdf.PDFDict) {
	page := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Contents": pdf.NewStreamDict(nil, []byte("q 20 0 0 20 0 0 cm /Im0 Do Q")),
	})}
	resources := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"XObject": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Im0": im})},
	})}
	return page, resources
}

// TestRasterReportsPlaceholderImage verifies that an image whose codec has no
// decoder is still painted as the gray placeholder, and that the page reports
// it dropped under the codec's name instead of passing the gray off as the
// picture.
func TestRasterReportsPlaceholderImage(t *testing.T) {
	for filter, want := range map[string]string{"JPXDecode": "JPEG 2000 image", "JBIG2Decode": "JBIG2 image"} {
		t.Run(filter, func(t *testing.T) {
			page, resources := placeholderImagePage(imageDict(map[string]pdf.PDFValue{
				"Subtype": pdf.PDFName{Value: "Image"},
				"Width":   pdf.PDFInteger(2), "Height": pdf.PDFInteger(2),
				"BitsPerComponent": pdf.PDFInteger(8), "ColorSpace": pdf.PDFName{Value: "DeviceGray"},
				"Filter": pdf.PDFName{Value: filter},
			}, []byte{0}))
			img, drops, err := RenderPage(page, resources, [4]float64{0, 0, 20, 20}, 72, nil)
			if err != nil {
				t.Fatalf("RenderPage: %v", err)
			}
			if !hasDrop(drops, want) {
				t.Errorf("drops = %v, want %q", drops, want)
			}
			if got := nrgbaAt(t, img, 10, 10); got.R != 127 {
				t.Errorf("pixel = %v, want the mid-gray placeholder", got)
			}
		})
	}
}

// TestRasterPlaceholderMaskIsLeftOff verifies that a soft or stencil mask that
// only decodes to the placeholder is reported and not applied, so the image it
// belongs to shows at full strength rather than faded by a flat gray.
func TestRasterPlaceholderMaskIsLeftOff(t *testing.T) {
	jbig2 := imageDict(map[string]pdf.PDFValue{
		"Width": pdf.PDFInteger(2), "Height": pdf.PDFInteger(2),
		"BitsPerComponent": pdf.PDFInteger(1), "ColorSpace": pdf.PDFName{Value: "DeviceGray"},
		"Filter": pdf.PDFName{Value: "JBIG2Decode"},
	}, []byte{0})
	for _, key := range []string{"SMask", "Mask"} {
		t.Run(key, func(t *testing.T) {
			page, resources := placeholderImagePage(imageDict(map[string]pdf.PDFValue{
				"Subtype": pdf.PDFName{Value: "Image"},
				"Width":   pdf.PDFInteger(1), "Height": pdf.PDFInteger(1),
				"BitsPerComponent": pdf.PDFInteger(8), "ColorSpace": pdf.PDFName{Value: "DeviceGray"},
				key: jbig2,
			}, []byte{0}))
			img, drops, err := RenderPage(page, resources, [4]float64{0, 0, 20, 20}, 72, nil)
			if err != nil {
				t.Fatalf("RenderPage: %v", err)
			}
			if !hasDrop(drops, "JBIG2 image") {
				t.Errorf("drops = %v, want %q", drops, "JBIG2 image")
			}
			if got := nrgbaAt(t, img, 10, 10); got.R != 0 {
				t.Errorf("pixel = %v, want the black image unmasked", got)
			}
		})
	}
}

// TestRasterReportsPlaceholderInlineImage verifies that an inline CCITT image
// that fails to decode is reported under its codec, the same as an XObject
// would be.
func TestRasterReportsPlaceholderInlineImage(t *testing.T) {
	content := "q 20 0 0 20 0 0 cm BI /W 2 /H 2 /BPC 1 /CS /G /F /CCF ID \x00 EI Q"
	page := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Contents": pdf.NewStreamDict(nil, []byte(content)),
	})}
	_, drops, err := RenderPage(page, pdf.PDFDict{}, [4]float64{0, 0, 20, 20}, 72, nil)
	if err != nil {
		t.Fatalf("RenderPage: %v", err)
	}
	if !hasDrop(drops, "CCITT image") || hasDrop(drops, dropInlineImage) {
		t.Errorf("drops = %v, want %q only", drops, "CCITT image")
	}
}
