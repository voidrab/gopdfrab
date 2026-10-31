package raster

import (
	"image"
	"testing"

	"github.com/voidrab/gopdfrab/internal/content"
	"github.com/voidrab/gopdfrab/internal/pdf"
)

// hiddenGroup returns an optional content group and the configuration that
// turns it off.
func hiddenGroup() (pdf.PDFDict, *content.OptionalContent) {
	g := pdf.NewPDFDict()
	g.Entries.Set("Type", pdf.PDFName{Value: "OCG"})
	config := pdf.NewPDFDict()
	config.Entries.Set("OFF", pdf.PDFArray{g})
	props := pdf.NewPDFDict()
	props.Entries.Set("D", config)
	catalog := pdf.NewPDFDict()
	catalog.Entries.Set("OCProperties", props)
	return g, content.NewOptionalContent(catalog)
}

// renderHidden renders content on a 20x20 page under oc, with the hidden
// group g available as the property list /Off and extra in the resources.
func renderHidden(t *testing.T, src string, g pdf.PDFDict, oc *content.OptionalContent, extra map[string]pdf.PDFValue) *image.RGBA {
	t.Helper()
	entries := map[string]pdf.PDFValue{
		"Properties": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Off": g})},
	}
	for k, v := range extra {
		entries[k] = v
	}
	page := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Contents": pdf.NewStreamDict(nil, []byte(src)),
	})}
	img, _, err := RenderPage(page, pdf.PDFDict{Entries: pdf.DictOf(entries)}, [4]float64{0, 0, 20, 20}, 72, oc)
	if err != nil {
		t.Fatalf("RenderPage: %v", err)
	}
	return img
}

// TestRenderPageHidesOptionalContent checks that content in a hidden layer
// marks nothing, for each kind of drawing, while the graphics state it sets
// still applies to the content after it.
func TestRenderPageHidesOptionalContent(t *testing.T) {
	g, oc := hiddenGroup()
	square := formXObject("0 0 20 20 re f", nil)
	hiddenForm := formXObject("0 0 20 20 re f", map[string]pdf.PDFValue{"OC": g})
	xobjects := map[string]pdf.PDFValue{
		"XObject": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Sq": square, "Hid": hiddenForm})},
	}
	for _, tc := range []struct {
		name string
		src  string
	}{
		{"fill", "/OC /Off BDC 0 0 20 20 re f EMC"},
		{"stroke", "/OC /Off BDC 4 w 0 10 m 20 10 l S EMC"},
		{"form", "/OC /Off BDC /Sq Do EMC"},
		{"form in a hidden layer of its own", "/Hid Do"},
		{"shading", "/OC /Off BDC /Sh1 sh EMC"},
		{"inline image", "/OC /Off BDC q 0 g 20 0 0 20 0 0 cm BI /IM true /W 1 /H 1 /BPC 1 ID \x00 EI Q EMC"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			extra := map[string]pdf.PDFValue{"XObject": xobjects["XObject"], "Shading": redShadingResources().Entries.Get("Shading")}
			if n, _, _, _, _ := ink(renderHidden(t, tc.src, g, oc, extra)); n != 0 {
				t.Errorf("%d pixels inked, want none", n)
			}
			// Without the configuration the same content is drawn.
			if n, _, _, _, _ := ink(renderHidden(t, tc.src, g, nil, extra)); n == 0 {
				t.Error("nothing inked with every layer visible")
			}
		})
	}

	t.Run("state and clip still apply", func(t *testing.T) {
		img := renderHidden(t, "/OC /Off BDC 1 0 0 rg 0 0 10 20 re W n EMC 0 0 20 20 re f", g, oc, nil)
		if got := nrgbaAt(t, img, 5, 10); got.R != 255 || got.G != 0 {
			t.Errorf("inside the clip = %v, want the red set in the hidden layer", got)
		}
		assertUnpainted(t, img, 15, 10, "outside the clip set in the hidden layer")
	})
}

// TestRenderPageHiddenTextKeepsItsAdvance checks that hidden text marks
// nothing but still moves the text position, so the visible text after it
// stays where the document put it.
func TestRenderPageHiddenTextKeepsItsAdvance(t *testing.T) {
	g, oc := hiddenGroup()
	font := type3Font("0 0 400 750 re f", 0.001, 500, "square")
	extra := map[string]pdf.PDFValue{
		"Font": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"F1": font})},
	}
	for _, show := range []string{"(A) Tj", "[(A)] TJ", "(A) '", "0 0 (A) \""} {
		img := renderHidden(t, "BT /F1 20 Tf 0 2 Td /OC /Off BDC "+show+" EMC 0 Tr (A) Tj ET", g, oc, extra)
		assertUnpainted(t, img, 4, 10, show+": the hidden glyph")
		if show == "(A) Tj" || show == "[(A)] TJ" {
			if got := nrgbaAt(t, img, 14, 10); got.R != 0 {
				t.Errorf("%s: the visible glyph at x=14 = %v, want painted", show, got)
			}
		}
	}
}
