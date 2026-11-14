package raster

import (
	"image"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// noWidths is a non-embedded Helvetica without /Widths, as the standard 14
// fonts may legally be: the reader must know their metrics itself.
func noWidths() pdf.PDFDict {
	return pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Font"}, "Subtype": pdf.PDFName{Value: "Type1"},
		"BaseFont": pdf.PDFName{Value: "Helvetica"}, "Encoding": pdf.PDFName{Value: "WinAnsiEncoding"},
	})}
}

func renderWith(t *testing.T, font pdf.PDFDict, body string) *image.RGBA {
	t.Helper()
	img, _ := renderSized(t, 80, 30, "BT "+body+" ET", fontResources(font))
	return img
}

func TestStandardFontWithoutWidthsAdvancesByItsMetrics(t *testing.T) {
	// In Helvetica, H is 722 units wide, i is 222 and the space is 278.
	explicit := helvetica(map[string]pdf.PDFValue{
		"FirstChar": pdf.PDFInteger(32), "LastChar": pdf.PDFInteger(105),
		"Widths": func() pdf.PDFArray {
			w := make(pdf.PDFArray, 105-32+1)
			for i := range w {
				w[i] = pdf.PDFInteger(0)
			}
			w[0] = pdf.PDFInteger(278)      // space
			w[72-32] = pdf.PDFInteger(722)  // H
			w[105-32] = pdf.PDFInteger(222) // i
			return w
		}(),
	})
	show := "/F1 20 Tf 3 5 Td (H iH) Tj"
	want := renderWith(t, explicit, show)
	got := renderWith(t, noWidths(), show)
	if n, _, _, _, _ := ink(got); n == 0 {
		t.Fatal("nothing was drawn")
	}
	for i := range want.Pix {
		if d := int(want.Pix[i]) - int(got.Pix[i]); d > 2 || d < -2 {
			t.Fatalf("a font without /Widths is laid out differently from one with Helvetica's widths (byte %d)", i)
		}
	}
}

func TestSubstitutedSerifAndMonoUseTheirOwnMetrics(t *testing.T) {
	// Courier is monospaced: two different letters advance by the same amount,
	// so "iH" and "Hi" cover the same horizontal extent.
	courier := func() pdf.PDFDict {
		f := noWidths()
		f.Entries.Set("BaseFont", pdf.PDFName{Value: "Courier"})
		return f
	}
	a := renderWith(t, courier(), "/F1 16 Tf 3 5 Td (iH) Tj")
	b := renderWith(t, courier(), "/F1 16 Tf 3 5 Td (Hi) Tj")
	_, aMinX, _, aMaxX, _ := ink(a)
	_, bMinX, _, bMaxX, _ := ink(b)
	if aMaxX-aMinX < 8 || bMaxX-bMinX < 8 || aMaxX-bMaxX > 2 || bMaxX-aMaxX > 2 {
		t.Errorf("Courier extents differ: %d..%d vs %d..%d", aMinX, aMaxX, bMinX, bMaxX)
	}
	// Times' narrow i against Helvetica's wider letters: serif text is
	// laid out with the serif face's own advances.
	times := func() pdf.PDFDict {
		f := noWidths()
		f.Entries.Set("BaseFont", pdf.PDFName{Value: "Times-Roman"})
		return f
	}
	st := renderWith(t, times(), "/F1 16 Tf 3 5 Td (aaaa) Tj")
	sh := renderWith(t, noWidths(), "/F1 16 Tf 3 5 Td (aaaa) Tj")
	_, _, _, tMax, _ := ink(st)
	_, _, _, hMax, _ := ink(sh)
	if tMax == hMax {
		t.Errorf("Times and Helvetica rows of a end at the same pixel %d", tMax)
	}
}

func TestMissingWidthWinsOverTheFace(t *testing.T) {
	// With a descriptor MissingWidth, codes outside /Widths use it, as before.
	f := helvetica(map[string]pdf.PDFValue{
		"FirstChar": pdf.PDFInteger(65), "LastChar": pdf.PDFInteger(65),
		"Widths": pdf.PDFArray{pdf.PDFInteger(600)},
		"FontDescriptor": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"MissingWidth": pdf.PDFInteger(1000),
		})},
	})
	wide := renderWith(t, f, "/F1 10 Tf 2 5 Td (AB) Tj")
	_, _, _, maxX, _ := ink(wide)
	if maxX < 14 {
		t.Errorf("B with a 1000-unit MissingWidth ends at %d, expected further right", maxX)
	}
}
