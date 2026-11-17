package raster

import (
	"context"
	"image"
	"testing"

	"github.com/voidrab/gopdfrab/internal/content"
	"github.com/voidrab/gopdfrab/internal/pdf"
)

// redAppearance is a form whose 10 by 10 box is filled red.
func redAppearance() pdf.PDFDict {
	return pdf.NewStreamDict(pdf.DictOf(map[string]pdf.PDFValue{
		"BBox": numArray(0, 0, 10, 10),
	}), []byte("1 0 0 rg 0 0 10 10 re f"))
}

// annotPage is a blank 100 by 100 page with the annotations given.
func annotPage(annots ...pdf.PDFValue) pdf.PDFDict {
	return pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Contents": pdf.NewStreamDict(nil, nil),
		"Annots":   pdf.PDFArray(annots),
	})}
}

func annotWith(rect pdf.PDFArray, ap pdf.PDFValue, extra map[string]pdf.PDFValue) pdf.PDFDict {
	e := map[string]pdf.PDFValue{
		"Subtype": pdf.PDFName{Value: "Widget"},
		"Rect":    rect,
		"AP":      pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"N": ap})},
	}
	for k, v := range extra {
		e[k] = v
	}
	return pdf.PDFDict{Entries: pdf.DictOf(e)}
}

var annotBox = [4]float64{0, 0, 100, 100}

func renderAnnotPage(t *testing.T, page pdf.PDFDict) *image.RGBA {
	t.Helper()
	img, _, err := RenderPageContext(context.Background(), page, pdf.PDFDict{}, annotBox, 72, nil, nil)
	if err != nil {
		t.Fatalf("RenderPageContext: %v", err)
	}
	return img
}

func isRed(t *testing.T, img *image.RGBA, x, y int) bool {
	t.Helper()
	px := nrgbaAt(t, img, x, y)
	return px.R == 255 && px.G == 0 && px.B == 0
}

// TestAnnotationAppearanceFitsRect covers an appearance whose box is scaled
// onto a larger /Rect: it fills the rectangle, and nothing outside it.
func TestAnnotationAppearanceFitsRect(t *testing.T) {
	// Rect x 20..60, y 50..90 in PDF space: device rows 10..50.
	img := renderAnnotPage(t, annotPage(annotWith(numArray(20, 50, 60, 90), redAppearance(), nil)))
	for _, p := range []image.Point{{22, 12}, {40, 30}, {58, 48}} {
		if !isRed(t, img, p.X, p.Y) {
			t.Errorf("pixel %v inside the annotation is not red", p)
		}
	}
	for _, p := range []image.Point{{10, 30}, {70, 30}, {40, 5}, {40, 60}} {
		if !isWhite(t, img, p.X, p.Y) {
			t.Errorf("pixel %v outside the annotation is not white", p)
		}
	}
}

// TestAnnotationAppearanceMatrix covers an appearance whose /Matrix moves its
// box: the transformed box, not the raw one, is what is fitted to /Rect.
func TestAnnotationAppearanceMatrix(t *testing.T) {
	ap := redAppearance()
	ap.Entries.Set("Matrix", numArray(1, 0, 0, 1, 500, 500))
	img := renderAnnotPage(t, annotPage(annotWith(numArray(0, 0, 10, 10), ap, nil)))
	if !isRed(t, img, 5, 95) {
		t.Error("appearance with a translating /Matrix was not placed on its /Rect")
	}
}

// TestAnnotationAppearanceState covers /N holding a stream per state: the one
// /AS names is drawn.
func TestAnnotationAppearanceState(t *testing.T) {
	off := pdf.NewStreamDict(pdf.DictOf(map[string]pdf.PDFValue{"BBox": numArray(0, 0, 10, 10)}), []byte("0 0 1 rg 0 0 10 10 re f"))
	states := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"On": redAppearance(), "Off": off})}
	img := renderAnnotPage(t, annotPage(annotWith(numArray(0, 0, 10, 10), states, map[string]pdf.PDFValue{"AS": pdf.PDFName{Value: "On"}})))
	if !isRed(t, img, 5, 95) {
		t.Error("the appearance /AS selects was not drawn")
	}
	img = renderAnnotPage(t, annotPage(annotWith(numArray(0, 0, 10, 10), states, nil)))
	if !isWhite(t, img, 5, 95) {
		t.Error("an annotation with states but no /AS was drawn")
	}
}

// TestAnnotationNotDrawn covers annotations a viewer does not show.
func TestAnnotationNotDrawn(t *testing.T) {
	rect := numArray(0, 0, 10, 10)
	cases := map[string]pdf.PDFDict{
		"hidden":     annotWith(rect, redAppearance(), map[string]pdf.PDFValue{"F": pdf.PDFInteger(annotHidden)}),
		"no view":    annotWith(rect, redAppearance(), map[string]pdf.PDFValue{"F": pdf.PDFInteger(annotNoView | 4)}),
		"popup":      annotWith(rect, redAppearance(), map[string]pdf.PDFValue{"Subtype": pdf.PDFName{Value: "Popup"}}),
		"no AP":      {Entries: pdf.DictOf(map[string]pdf.PDFValue{"Rect": rect})},
		"no Rect":    annotWith(pdf.PDFArray{}, redAppearance(), nil),
		"empty Rect": annotWith(numArray(5, 5, 5, 10), redAppearance(), nil),
		"image appearance": annotWith(rect, pdf.NewStreamDict(pdf.DictOf(map[string]pdf.PDFValue{
			"Subtype": pdf.PDFName{Value: "Image"}, "BBox": numArray(0, 0, 10, 10),
		}), nil), nil),
	}
	for name, annot := range cases {
		t.Run(name, func(t *testing.T) {
			img := renderAnnotPage(t, annotPage(annot))
			if !isWhite(t, img, 5, 95) {
				t.Error("annotation was drawn")
			}
		})
	}
}

// TestAnnotationPrintFlagDrawn covers the common /F 4 (Print): it is shown.
func TestAnnotationPrintFlagDrawn(t *testing.T) {
	img := renderAnnotPage(t, annotPage(annotWith(numArray(0, 0, 10, 10), redAppearance(), map[string]pdf.PDFValue{"F": pdf.PDFInteger(4)})))
	if !isRed(t, img, 5, 95) {
		t.Error("annotation with the Print flag was not drawn")
	}
}

// TestAnnotationOptionalContent covers an annotation in a layer the document
// turns off.
func TestAnnotationOptionalContent(t *testing.T) {
	ocg := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Type": pdf.PDFName{Value: "OCG"}})}
	props := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"OCGs": pdf.PDFArray{ocg},
		"D":    pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"OFF": pdf.PDFArray{ocg}})},
	})}
	oc := content.OptionalContentOf(props)
	page := annotPage(annotWith(numArray(0, 0, 10, 10), redAppearance(), map[string]pdf.PDFValue{"OC": ocg}))
	img, _, err := RenderPageContext(context.Background(), page, pdf.PDFDict{}, annotBox, 72, oc, nil)
	if err != nil {
		t.Fatalf("RenderPageContext: %v", err)
	}
	if !isWhite(t, img, 5, 95) {
		t.Error("annotation in a hidden layer was drawn")
	}
}

// TestRenderPageRegionDrawsAnnotations covers a region render, which shows
// annotations like a whole-page one.
func TestRenderPageRegionDrawsAnnotations(t *testing.T) {
	page := annotPage(annotWith(numArray(0, 0, 10, 10), redAppearance(), nil))
	img, _, err := RenderPageRegion(context.Background(), page, pdf.PDFDict{}, annotBox, 72, image.Rect(0, 80, 20, 100), nil, nil)
	if err != nil {
		t.Fatalf("RenderPageRegion: %v", err)
	}
	if !isRed(t, img, 5, 15) {
		t.Error("region render did not draw the annotation")
	}
}

// TestRenderPageLeavesOutAnnotations covers RenderPage, which renders only the
// page's content for conversion, where the annotations stay as they are.
func TestRenderPageLeavesOutAnnotations(t *testing.T) {
	page := annotPage(annotWith(numArray(0, 0, 10, 10), redAppearance(), nil))
	img, _, err := RenderPage(page, pdf.PDFDict{}, annotBox, 72, nil)
	if err != nil {
		t.Fatalf("RenderPage: %v", err)
	}
	if !isWhite(t, img, 5, 95) {
		t.Error("RenderPage drew an annotation")
	}
}

// TestExtractTextLeavesOutAnnotations covers text extraction, which reads only
// the page's content.
func TestExtractTextLeavesOutAnnotations(t *testing.T) {
	font := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Font"}, "Subtype": pdf.PDFName{Value: "Type1"}, "BaseFont": pdf.PDFName{Value: "Helvetica"},
	})}
	ap := pdf.NewStreamDict(pdf.DictOf(map[string]pdf.PDFValue{
		"BBox":      numArray(0, 0, 10, 10),
		"Resources": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Font": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"F1": font})}})},
	}), []byte("BT /F1 8 Tf 0 0 Td (x) Tj ET"))
	page := annotPage(annotWith(numArray(0, 0, 10, 10), ap, nil))
	glyphs, err := ExtractText(context.Background(), page, pdf.PDFDict{}, annotBox, nil, nil)
	if err != nil {
		t.Fatalf("ExtractText: %v", err)
	}
	if len(glyphs) != 0 {
		t.Errorf("ExtractText returned %d glyphs from an annotation", len(glyphs))
	}
}
