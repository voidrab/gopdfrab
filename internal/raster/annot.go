package raster

import (
	"math"

	"github.com/voidrab/gopdfrab/internal/content"
	"github.com/voidrab/gopdfrab/internal/pdf"
)

// Annotation flags (ISO 32000-1 12.5.3) that keep an annotation off the screen.
const (
	annotHidden = 1 << 1
	annotNoView = 1 << 5
)

// pageAnnots returns the page's /Annots array, or nil when it has none.
func pageAnnots(page pdf.PDFDict) pdf.PDFArray {
	annots, _ := page.Entries.Get("Annots").(pdf.PDFArray)
	return annots
}

// paintAnnotations paints the normal appearance of each annotation over the
// page, in the order the page lists them (ISO 32000-1 12.5.5). base maps the
// page's default user space to device space and clip is the device clip a
// drawing starts in. An annotation that is hidden, has no normal appearance,
// or whose optional content is off is not drawn; nor is a popup, which a
// viewer shows only on demand.
func (r *renderer) paintAnnotations(annots pdf.PDFArray, resources pdf.PDFDict, base content.Matrix, clip [4]float64) {
	for _, a := range annots {
		if r.ctx != nil && r.ctx.Err() != nil {
			return
		}
		annot, ok := a.(pdf.PDFDict)
		if !ok || annot.Entries == nil {
			continue
		}
		if flags, _ := pdf.PDFNumberToInt(annot.Entries.Get("F")); flags&(annotHidden|annotNoView) != 0 {
			continue
		}
		if st, _ := annot.Entries.Get("Subtype").(pdf.PDFName); st.Value == "Popup" {
			continue
		}
		if r.oc.Hides(annot.Entries.Get("OC")) {
			continue
		}
		ap, ok := normalAppearance(annot)
		if !ok || r.oc.Hides(ap.Entries.Get("OC")) {
			continue
		}
		place, ok := appearancePlacement(annot, ap)
		if !ok {
			continue
		}
		gs := newRenderState(place.Mul(base), clip)
		r.doForm(ap, resources, &gs)
	}
}

// normalAppearance returns the form XObject that draws annot normally: its
// /AP /N stream, or, where /N holds one stream for each appearance state, the
// one /AS selects.
func normalAppearance(annot pdf.PDFDict) (pdf.PDFDict, bool) {
	ap, _ := annot.Entries.Get("AP").(pdf.PDFDict)
	if ap.Entries == nil {
		return pdf.PDFDict{}, false
	}
	n, _ := ap.Entries.Get("N").(pdf.PDFDict)
	if n.Entries == nil {
		return pdf.PDFDict{}, false
	}
	if !n.IsStream() {
		state, ok := annot.Entries.Get("AS").(pdf.PDFName)
		if !ok {
			return pdf.PDFDict{}, false
		}
		n, _ = n.Entries.Get(state.Value).(pdf.PDFDict)
	}
	if !n.IsStream() {
		return pdf.PDFDict{}, false
	}
	// An appearance stream is a form, so it may leave out its /Subtype.
	if st, ok := n.Entries.Get("Subtype").(pdf.PDFName); ok && st.Value != "Form" {
		return pdf.PDFDict{}, false
	}
	return n, true
}

// appearancePlacement returns the matrix A of ISO 32000-1 12.5.5 that fits an
// appearance's /BBox, transformed by its /Matrix, onto the annotation's /Rect.
// The appearance's content then runs under its /Matrix, then A, then the
// page's CTM. It reports false for a missing or degenerate rectangle or box.
func appearancePlacement(annot, ap pdf.PDFDict) (content.Matrix, bool) {
	rect, err := pdf.FloatArray(annot.Entries.Get("Rect"))
	if err != nil || len(rect) != 4 {
		return content.Matrix{}, false
	}
	bbox, err := pdf.FloatArray(ap.Entries.Get("BBox"))
	if err != nil || len(bbox) != 4 {
		return content.Matrix{}, false
	}
	m := formCTM(ap, content.IdentityMatrix)
	x0, y0, x1, y1 := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, c := range []content.Point{{X: bbox[0], Y: bbox[1]}, {X: bbox[2], Y: bbox[1]}, {X: bbox[2], Y: bbox[3]}, {X: bbox[0], Y: bbox[3]}} {
		p := m.Apply(c)
		x0, y0 = math.Min(x0, p.X), math.Min(y0, p.Y)
		x1, y1 = math.Max(x1, p.X), math.Max(y1, p.Y)
	}
	rx0, ry0 := math.Min(rect[0], rect[2]), math.Min(rect[1], rect[3])
	rx1, ry1 := math.Max(rect[0], rect[2]), math.Max(rect[1], rect[3])
	if x1-x0 <= 0 || y1-y0 <= 0 || rx1-rx0 <= 0 || ry1-ry0 <= 0 {
		return content.Matrix{}, false
	}
	sx, sy := (rx1-rx0)/(x1-x0), (ry1-ry0)/(y1-y0)
	if math.IsInf(sx, 0) || math.IsInf(sy, 0) || math.IsNaN(sx) || math.IsNaN(sy) {
		return content.Matrix{}, false
	}
	return content.Matrix{A: sx, D: sy, E: rx0 - x0*sx, F: ry0 - y0*sy}, true
}
