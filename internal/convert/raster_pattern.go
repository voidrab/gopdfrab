package convert

import (
	"image"
	"math"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// A Pattern colour space names its colour with scn rather than numbers, so
// the ordinary numeric path never fires and the fill colour stays whatever
// the preceding cs left behind -- black. Shading patterns (PatternType 2)
// are now painted through their shading; tiling patterns (PatternType 1) are
// reported as a drop, since a flat black fill would be a silent loss.

// isPatternColorSpace reports whether cs is the Pattern colour space, in
// either the bare-name or [/Pattern base] form.
func isPatternColorSpace(cs pdf.PDFValue) bool {
	switch v := cs.(type) {
	case pdf.PDFName:
		return v.Value == "Pattern"
	case pdf.PDFArray:
		if len(v) == 0 {
			return false
		}
		name, ok := v[0].(pdf.PDFName)
		return ok && name.Value == "Pattern"
	}
	return false
}

// patternShading resolves a shading pattern's /Shading, caching the parsed
// form per pattern dict so a repeated fill re-parses nothing.
func (r *renderer) patternShading(pattern pdf.PDFDict) (*shading, bool) {
	key := pdf.ValuePointer(pattern.Entries)
	if sh, ok := r.shadingCache[key]; ok {
		return sh, sh != nil
	}
	resources, _ := pattern.Entries["Resources"].(pdf.PDFDict)
	sh, ok := parseShading(pattern.Entries["Shading"], resources)
	if !ok {
		sh = nil
	}
	if r.shadingCache == nil {
		r.shadingCache = map[uintptr]*shading{}
	}
	r.shadingCache[key] = sh
	return sh, sh != nil
}

// patternToDevice maps a pattern's own space to device space. A pattern
// matrix is relative to the default coordinate system of the page (or form)
// it is used in, not to the CTM in force when it is selected.
func (r *renderer) patternToDevice(pattern pdf.PDFDict) Matrix {
	m := IdentityMatrix
	if a, err := pdf.FloatArray(pattern.Entries["Matrix"]); err == nil && len(a) == 6 {
		m = Matrix{A: a[0], B: a[1], C: a[2], D: a[3], E: a[4], F: a[5]}
	}
	return m.Mul(r.patternBase)
}

// selectPattern resolves a scn/SCN pattern-name operand. A tiling pattern is
// reported as a drop and yields no pattern, so the caller paints nothing
// rather than a misleading flat colour.
func (r *renderer) selectPattern(name string, resources pdf.PDFDict) (pdf.PDFDict, bool) {
	patterns, _ := resources.Entries["Pattern"].(pdf.PDFDict)
	pattern, ok := patterns.Entries[name].(pdf.PDFDict)
	if !ok {
		r.drop(dropShading)
		return pdf.PDFDict{}, false
	}
	switch pdf.DictInt(pattern, "PatternType", 0) {
	case 2:
		if _, ok := r.patternShading(pattern); !ok {
			r.drop(dropShading)
			return pdf.PDFDict{}, false
		}
		return pattern, true
	case 1:
		r.drop(dropTilingPattern)
	default:
		r.drop(dropShading)
	}
	return pdf.PDFDict{}, false
}

// patternSampler returns a per-pixel colour source for a shading pattern, or
// false when the shading is a mesh (which is geometry, not a field, and so is
// painted separately).
func (r *renderer) patternSampler(pattern pdf.PDFDict, alpha float64) (func(x, y int) ([3]float64, float64), bool) {
	sh, ok := r.patternShading(pattern)
	if !ok || sh.isMesh() {
		return nil, false
	}
	inv, ok := r.patternToDevice(pattern).Invert()
	if !ok {
		return nil, false
	}
	return func(x, y int) ([3]float64, float64) {
		p := inv.Apply(Point{float64(x) + 0.5, float64(y) + 0.5})
		rgb, painted := sh.colorAt(p.X, p.Y)
		if !painted {
			return [3]float64{}, 0
		}
		return rgb, alpha
	}, true
}

// paintPatternPath paints contours with gs's shading pattern, reporting
// whether it handled the paint. Mesh patterns fall back to painting the mesh
// over the path's bounding box, the same approximation the renderer already
// makes for clipping.
func (r *renderer) paintPatternPath(target *image.RGBA, contours [][]Point, pattern pdf.PDFDict, gs *renderState, evenOdd bool, stroke bool) bool {
	if pattern.Entries == nil {
		return false
	}
	alpha := gs.fillAlpha
	if stroke {
		alpha = gs.strokeAlpha
	}
	if sample, ok := r.patternSampler(pattern, alpha); ok {
		if stroke {
			StrokePathShaded(target, contours, gs.lineWidth*ctmScale(gs.ctm), sample)
		} else {
			FillPathShaded(target, contours, evenOdd, sample)
		}
		return true
	}

	sh, ok := r.patternShading(pattern)
	if !ok || !sh.isMesh() {
		return false
	}
	minX, minY, maxX, maxY := boundsOfContours(contours)
	area := clipToBounds(target.Bounds(), intersectRect(gs.clip, [4]float64{
		math.Floor(minX), math.Floor(minY), math.Ceil(maxX), math.Ceil(maxY),
	}))
	r.paintShading(sh, r.patternToDevice(pattern), alpha, area)
	return true
}
