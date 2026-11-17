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

// paintAnnotations paints the normal appearance of each annotation over the
// page, in the order the page lists them (ISO 32000-1 12.5.5). base maps the
// page's default user space to device space and clip is the device clip a
// drawing starts in. An annotation that is hidden, has no normal appearance,
// or whose optional content is off is not drawn; nor is a popup, which a
// viewer shows only on demand. Nor is one outside the canvas, which would
// paint nothing.
func (r *renderer) paintAnnotations(annots pdf.PDFArray, resources pdf.PDFDict, base content.Matrix, clip [4]float64) {
	for _, a := range annots {
		if r.ctx.Err() != nil {
			return
		}
		annot, ok := a.(pdf.PDFDict)
		if !ok {
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
		ctm := place.Mul(base)
		if clipToBounds(r.canvas.Bounds(), formClip(ap, formCTM(ap, ctm), clip)).Empty() {
			continue
		}
		gs := newRenderState(ctm, clip)
		r.doForm(ap, resources, &gs)
	}
}

// normalAppearance returns the form XObject that draws annot in its normal
// state: the normal appearance stream (/AP /N) or, where /N holds one stream
// per appearance state, the stream that the state name (/AS) selects.
func normalAppearance(annot pdf.PDFDict) (pdf.PDFDict, bool) {
	ap, _ := annot.Entries.Get("AP").(pdf.PDFDict)
	n, _ := ap.Entries.Get("N").(pdf.PDFDict)
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
	// An appearance stream is a form, so it may omit its subtype (/Subtype).
	if st, ok := n.Entries.Get("Subtype").(pdf.PDFName); ok && st.Value != "Form" {
		return pdf.PDFDict{}, false
	}
	return n, true
}

// appearancePlacement returns the matrix A of ISO 32000-1 12.5.5 that fits the
// appearance's bounding box (/BBox), transformed by its matrix (/Matrix), onto
// the annotation's rectangle (/Rect). The appearance's content then runs under
// its matrix, then A, then the page's CTM. It reports false for a missing or
// degenerate rectangle or box.
func appearancePlacement(annot, ap pdf.PDFDict) (content.Matrix, bool) {
	rect, err := pdf.FloatArray(annot.Entries.Get("Rect"))
	if err != nil || len(rect) != 4 {
		return content.Matrix{}, false
	}
	bbox, err := pdf.FloatArray(ap.Entries.Get("BBox"))
	if err != nil || len(bbox) != 4 {
		return content.Matrix{}, false
	}
	b := deviceBounds(bbox[0], bbox[1], bbox[2]-bbox[0], bbox[3]-bbox[1], formCTM(ap, content.IdentityMatrix), 0)
	rx0, ry0 := math.Min(rect[0], rect[2]), math.Min(rect[1], rect[3])
	rx1, ry1 := math.Max(rect[0], rect[2]), math.Max(rect[1], rect[3])
	sx, sy := (rx1-rx0)/(b[2]-b[0]), (ry1-ry0)/(b[3]-b[1])
	// An empty rectangle or box, or an infinite one, gives a scale that is
	// zero, infinite or NaN, which !(s > 0) rejects along with zero.
	if !(sx > 0 && sy > 0) || math.IsInf(sx, 0) || math.IsInf(sy, 0) {
		return content.Matrix{}, false
	}
	return content.Matrix{A: sx, D: sy, E: rx0 - b[0]*sx, F: ry0 - b[1]*sy}, true
}
