package raster

import (
	"strings"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// checker8 is an 8x8 8-bit gray checkerboard of black and white samples.
func checker8() string {
	var b strings.Builder
	for y := range 8 {
		for x := range 8 {
			if (x+y)%2 == 0 {
				b.WriteByte(255)
			} else {
				b.WriteByte(0)
			}
		}
	}
	return b.String()
}

func TestShrunkImagesAverageTheirPixels(t *testing.T) {
	// 8x8 pixels shown in a 2x2 area at the lower left of the page: every
	// device pixel covers a 4x4 block holding as much black as white.
	content := "q 2 0 0 2 0 0 cm BI /W 8 /H 8 /CS /G /BPC 8 ID " + checker8() + " EI Q"
	img := render20(t, content, pdf.PDFDict{})
	for _, p := range [][2]int{{0, 18}, {1, 18}, {0, 19}, {1, 19}} {
		if c := nrgbaAt(t, img, p[0], p[1]); c.R < 110 || c.R > 145 {
			t.Errorf("shrunk checkerboard at %v = %v, want mid grey", p, c)
		}
	}
}

func TestEnlargedImagesStayCrispUnlessInterpolated(t *testing.T) {
	// Two pixels, black then white, enlarged to 20 pixels across.
	px := "\x00\xff"
	crisp := render20(t, "q 20 0 0 20 0 0 cm BI /W 2 /H 1 /CS /G /BPC 8 ID "+px+" EI Q", pdf.PDFDict{})
	smooth := render20(t, "q 20 0 0 20 0 0 cm BI /W 2 /H 1 /CS /G /BPC 8 /I true ID "+px+" EI Q", pdf.PDFDict{})
	// Around the boundary at x=10 the crisp image steps, the smooth one ramps.
	if !isBlack(t, crisp, 9, 10) || !isWhite(t, crisp, 10, 10) {
		t.Errorf("enlarged image is not crisp: %v %v", nrgbaAt(t, crisp, 9, 10), nrgbaAt(t, crisp, 10, 10))
	}
	if c := nrgbaAt(t, smooth, 9, 10); c.R < 30 || c.R > 225 {
		t.Errorf("interpolated pixel next to the edge = %v, want between black and white", c)
	}
	if c := nrgbaAt(t, smooth, 10, 10); c.R < 30 || c.R > 225 {
		t.Errorf("interpolated pixel past the edge = %v, want between black and white", c)
	}
}

func TestShrunkStencilMasksGiveCoverage(t *testing.T) {
	// An 8x8 stencil painted in a 2x2 area, with alternating painted and
	// unpainted samples: black ink at about half coverage.
	var rows strings.Builder
	for range 8 {
		rows.WriteByte(0xAA) // 1010 1010: sample 0 paints
	}
	content := "0 0 0 rg q 2 0 0 2 0 0 cm BI /IM true /W 8 /H 8 /BPC 1 ID " + rows.String() + " EI Q"
	img := render20(t, content, pdf.PDFDict{})
	for _, p := range [][2]int{{0, 19}, {1, 18}} {
		if c := nrgbaAt(t, img, p[0], p[1]); c.R < 100 || c.R > 155 {
			t.Errorf("shrunk stencil at %v = %v, want half-covered grey", p, c)
		}
	}
}

func TestShrunkSoftMasksAreFiltered(t *testing.T) {
	// A 1x1 white image under an 8x8 alternating soft mask, shown at 2x2: the
	// mask averages to half opacity instead of picking opaque or clear.
	mask := imageDict(map[string]pdf.PDFValue{
		"Subtype": pdf.PDFName{Value: "Image"}, "Width": pdf.PDFInteger(8), "Height": pdf.PDFInteger(8),
		"ColorSpace": pdf.PDFName{Value: "DeviceGray"}, "BitsPerComponent": pdf.PDFInteger(8),
	}, []byte(checker8()))
	base := imageDict(map[string]pdf.PDFValue{
		"Subtype": pdf.PDFName{Value: "Image"}, "Width": pdf.PDFInteger(1), "Height": pdf.PDFInteger(1),
		"ColorSpace": pdf.PDFName{Value: "DeviceGray"}, "BitsPerComponent": pdf.PDFInteger(8),
		"SMask": mask,
	}, []byte{0})
	res := resourcesWith("XObject", map[string]pdf.PDFValue{"Im": base})
	img := render20(t, "q 2 0 0 2 0 0 cm /Im Do Q", res)
	if c := nrgbaAt(t, img, 0, 19); c.R < 110 || c.R > 145 {
		t.Errorf("image under a shrunk soft mask = %v, want half-blended black over white", c)
	}
}

func TestInterpolatesReadsTheFlag(t *testing.T) {
	on := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Interpolate": pdf.PDFBoolean(true)})}
	off := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Interpolate": pdf.PDFInteger(1)})}
	if !interpolates(on) || interpolates(off) || interpolates(pdf.PDFDict{}) {
		t.Error("interpolates")
	}
}
