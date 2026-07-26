package convert

import (
	"image"
	"math"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// Shadings (ISO 32000-1 8.7.4.5) are painted by sampling: every device pixel
// in the target area is mapped back into shading space and asked for its
// colour. That mirrors compositeImage's inverse-CTM loop and keeps the whole
// family -- function-based, axial and radial -- behind one colorAt method.
// Mesh types (4-7) are geometry rather than a field and are handled
// separately in raster_shading_mesh.go.

// shadingLUTSize is how finely a type 2/3 shading's parametric domain is
// pre-evaluated. Sampling per pixel would call the PDF function millions of
// times per page; the domain is one-dimensional, so a table indexed by the
// parametric value is exact enough for a raster fallback.
const shadingLUTSize = 256

// shading is a parsed /Shading dictionary reduced to what the rasterizer needs.
type shading struct {
	kind      int
	cs        pdf.PDFValue
	resources pdf.PDFDict
	fn        shadingFunc

	coords []float64
	domain [2]float64 // types 2/3: parametric range
	extend [2]bool
	lut    [][3]float64 // types 2/3: colour sampled across domain

	dom4    [4]float64 // type 1: x and y domain
	inverse Matrix     // type 1: inverse of /Matrix

	bbox    [4]float64 // shading-space clip
	hasBBox bool

	dict pdf.PDFDict
}

// shadingFunc wraps a /Function entry, which is either one function with n
// outputs or an array of n single-output functions (ISO 32000-1 8.7.4.5.2).
type shadingFunc struct{ fns []pdf.Function }

func (sf shadingFunc) valid() bool { return len(sf.fns) > 0 }

func (sf shadingFunc) eval(in []float64) []float64 {
	if len(sf.fns) == 1 {
		return sf.fns[0].Eval(in)
	}
	out := make([]float64, 0, len(sf.fns))
	for _, f := range sf.fns {
		out = append(out, f.Eval(in)...)
	}
	return out
}

func parseShadingFunc(v pdf.PDFValue) shadingFunc {
	if arr, ok := v.(pdf.PDFArray); ok {
		fns := make([]pdf.Function, 0, len(arr))
		for _, item := range arr {
			f, err := pdf.ParseFunction(item)
			if err != nil {
				return shadingFunc{}
			}
			fns = append(fns, f)
		}
		return shadingFunc{fns: fns}
	}
	f, err := pdf.ParseFunction(v)
	if err != nil {
		return shadingFunc{}
	}
	return shadingFunc{fns: []pdf.Function{f}}
}

// parseShading reads a /Shading entry into the renderer's form. ok is false
// for a shading whose type or required entries the rasterizer cannot use, so
// the caller reports a drop rather than painting something wrong.
func parseShading(v pdf.PDFValue, resources pdf.PDFDict) (*shading, bool) {
	dict, ok := v.(pdf.PDFDict)
	if !ok {
		return nil, false
	}
	sh := &shading{
		kind:      pdf.DictInt(dict, "ShadingType", 0),
		cs:        resolveShadingColorSpace(dict, resources),
		resources: resources,
		fn:        parseShadingFunc(dict.Entries["Function"]),
		dict:      dict,
	}
	if sh.cs == nil {
		return nil, false
	}
	if bbox, err := pdf.FloatArray(dict.Entries["BBox"]); err == nil && len(bbox) == 4 {
		sh.bbox, sh.hasBBox = [4]float64{bbox[0], bbox[1], bbox[2], bbox[3]}, true
	}

	switch sh.kind {
	case 1:
		return sh, sh.initFunctionBased(dict)
	case 2, 3:
		return sh, sh.initAxialRadial(dict)
	}
	return nil, false
}

// resolveShadingColorSpace reads /ColorSpace, resolving a bare name through
// the resource dictionary the way image colour spaces are resolved.
func resolveShadingColorSpace(dict pdf.PDFDict, resources pdf.PDFDict) pdf.PDFValue {
	cs := dict.Entries["ColorSpace"]
	if name, ok := cs.(pdf.PDFName); ok {
		if named, ok := pdf.LookupNamedColorSpace(name.Value, resources); ok {
			return named
		}
	}
	return cs
}

func (sh *shading) initFunctionBased(dict pdf.PDFDict) bool {
	if !sh.fn.valid() {
		return false
	}
	sh.dom4 = [4]float64{0, 1, 0, 1}
	if d, err := pdf.FloatArray(dict.Entries["Domain"]); err == nil && len(d) == 4 {
		sh.dom4 = [4]float64{d[0], d[1], d[2], d[3]}
	}
	m := IdentityMatrix
	if a, err := pdf.FloatArray(dict.Entries["Matrix"]); err == nil && len(a) == 6 {
		m = Matrix{A: a[0], B: a[1], C: a[2], D: a[3], E: a[4], F: a[5]}
	}
	inv, ok := m.Invert()
	if !ok {
		return false
	}
	sh.inverse = inv
	return true
}

func (sh *shading) initAxialRadial(dict pdf.PDFDict) bool {
	want := 4
	if sh.kind == 3 {
		want = 6
	}
	coords, err := pdf.FloatArray(dict.Entries["Coords"])
	if err != nil || len(coords) != want {
		return false
	}
	if !sh.fn.valid() {
		return false
	}
	sh.coords = coords

	sh.domain = [2]float64{0, 1}
	if d, err := pdf.FloatArray(dict.Entries["Domain"]); err == nil && len(d) == 2 {
		sh.domain = [2]float64{d[0], d[1]}
	}
	if e, ok := dict.Entries["Extend"].(pdf.PDFArray); ok && len(e) == 2 {
		sh.extend[0] = e[0] == pdf.PDFBoolean(true)
		sh.extend[1] = e[1] == pdf.PDFBoolean(true)
	}

	sh.lut = make([][3]float64, shadingLUTSize)
	for i := range sh.lut {
		s := float64(i) / float64(shadingLUTSize-1)
		t := sh.domain[0] + s*(sh.domain[1]-sh.domain[0])
		sh.lut[i] = sh.colorFromComps(sh.fn.eval([]float64{t}))
	}
	return true
}

// colorFromComps converts a function's output components to RGB through the
// shading's colour space.
func (sh *shading) colorFromComps(comps []float64) [3]float64 {
	r, g, b := pdf.ResolveColor(sh.cs, comps, sh.resources)
	return [3]float64{r, g, b}
}

// lutAt looks up the pre-evaluated colour for a parametric position in [0,1].
func (sh *shading) lutAt(s float64) [3]float64 {
	i := int(math.Round(s * float64(shadingLUTSize-1)))
	return sh.lut[pdf.ClampInt(i, 0, shadingLUTSize-1)]
}

// colorAt returns the shading's colour at a point in shading space, or false
// where the shading paints nothing (outside its extent, domain or /BBox).
func (sh *shading) colorAt(x, y float64) ([3]float64, bool) {
	if sh.hasBBox && (x < sh.bbox[0] || x > sh.bbox[2] || y < sh.bbox[1] || y > sh.bbox[3]) {
		return [3]float64{}, false
	}
	switch sh.kind {
	case 1:
		return sh.functionBasedAt(x, y)
	case 2:
		return sh.axialAt(x, y)
	case 3:
		return sh.radialAt(x, y)
	}
	return [3]float64{}, false
}

func (sh *shading) functionBasedAt(x, y float64) ([3]float64, bool) {
	p := sh.inverse.Apply(Point{x, y})
	if p.X < sh.dom4[0] || p.X > sh.dom4[1] || p.Y < sh.dom4[2] || p.Y > sh.dom4[3] {
		return [3]float64{}, false
	}
	return sh.colorFromComps(sh.fn.eval([]float64{p.X, p.Y})), true
}

func (sh *shading) axialAt(x, y float64) ([3]float64, bool) {
	x0, y0, x1, y1 := sh.coords[0], sh.coords[1], sh.coords[2], sh.coords[3]
	dx, dy := x1-x0, y1-y0
	den := dx*dx + dy*dy
	if den == 0 {
		// A degenerate axis has no gradient direction; the whole plane takes
		// the domain's start colour, but only where an Extend allows painting.
		if sh.extend[0] || sh.extend[1] {
			return sh.lutAt(0), true
		}
		return [3]float64{}, false
	}
	s := ((x-x0)*dx + (y-y0)*dy) / den
	s, ok := sh.clampExtend(s)
	if !ok {
		return [3]float64{}, false
	}
	return sh.lutAt(s), true
}

func (sh *shading) radialAt(x, y float64) ([3]float64, bool) {
	x0, y0, r0 := sh.coords[0], sh.coords[1], sh.coords[2]
	x1, y1, r1 := sh.coords[3], sh.coords[4], sh.coords[5]
	dx, dy, dr := x1-x0, y1-y0, r1-r0
	px, py := x-x0, y-y0

	// The point lies on the circle for parameter s when
	// (px - s*dx)^2 + (py - s*dy)^2 = (r0 + s*dr)^2, i.e. a*s^2 + b*s + c = 0.
	a := dx*dx + dy*dy - dr*dr
	b := -2 * (px*dx + py*dy + r0*dr)
	c := px*px + py*py - r0*r0

	// Larger s wins: circles are painted in order of increasing s, so the
	// last one covering the point is the one visible (ISO 32000-1 8.7.4.5.4).
	if math.Abs(a) < 1e-9 {
		if b == 0 {
			return [3]float64{}, false
		}
		return sh.radialCandidate(-c/b, r0, dr)
	}
	disc := b*b - 4*a*c
	if disc < 0 {
		return [3]float64{}, false
	}
	root := math.Sqrt(disc)
	s1, s2 := (-b+root)/(2*a), (-b-root)/(2*a)
	if s1 < s2 {
		s1, s2 = s2, s1
	}
	if rgb, ok := sh.radialCandidate(s1, r0, dr); ok {
		return rgb, true
	}
	return sh.radialCandidate(s2, r0, dr)
}

// radialCandidate accepts one root of the radial quadratic if its circle has
// a non-negative radius and the root lies in the extended domain.
func (sh *shading) radialCandidate(s, r0, dr float64) ([3]float64, bool) {
	if r0+s*dr < 0 {
		return [3]float64{}, false
	}
	clamped, ok := sh.clampExtend(s)
	if !ok {
		return [3]float64{}, false
	}
	return sh.lutAt(clamped), true
}

// clampExtend maps a raw parametric value into [0,1], honouring /Extend at
// each end; ok is false where the shading does not extend and so paints nothing.
func (sh *shading) clampExtend(s float64) (float64, bool) {
	switch {
	case s < 0:
		if !sh.extend[0] {
			return 0, false
		}
		return 0, true
	case s > 1:
		if !sh.extend[1] {
			return 0, false
		}
		return 1, true
	}
	return s, true
}

// paintShading fills area by mapping each device pixel back through
// shadingToDevice and sampling the shading there.
func (r *renderer) paintShading(sh *shading, shadingToDevice Matrix, alpha float64, area image.Rectangle) {
	inv, ok := shadingToDevice.Invert()
	if !ok || area.Empty() || alpha <= 0 {
		return
	}
	for y := area.Min.Y; y < area.Max.Y; y++ {
		for x := area.Min.X; x < area.Max.X; x++ {
			p := inv.Apply(Point{float64(x) + 0.5, float64(y) + 0.5})
			rgb, ok := sh.colorAt(p.X, p.Y)
			if !ok {
				continue
			}
			blendPixel(r.canvas, x, y, rgb, alpha)
		}
	}
}

// paintShadingOp implements the sh operator: paint the named shading over the
// current clip, in the current user space.
func (r *renderer) paintShadingOp(operands []pdf.PDFValue, resources pdf.PDFDict, gs *renderState) {
	sh, ok := r.namedShading(operands, resources)
	if !ok {
		return
	}
	r.paintShading(sh, gs.ctm, gs.fillAlpha, clipToBounds(r.canvas.Bounds(), gs.clip))
}

// namedShading resolves an operator's shading-name operand against the
// /Shading resource dictionary, reporting a drop when it cannot be used.
func (r *renderer) namedShading(operands []pdf.PDFValue, resources pdf.PDFDict) (*shading, bool) {
	if len(operands) == 0 {
		r.drop(dropShading)
		return nil, false
	}
	name, ok := operands[len(operands)-1].(pdf.PDFName)
	if !ok {
		r.drop(dropShading)
		return nil, false
	}
	shadings, _ := resources.Entries["Shading"].(pdf.PDFDict)
	sh, ok := parseShading(shadings.Entries[name.Value], resources)
	if !ok {
		r.drop(dropShading)
		return nil, false
	}
	return sh, true
}
