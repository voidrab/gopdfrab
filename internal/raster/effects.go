package raster

import (
	"image"
	"math"

	"github.com/voidrab/gopdfrab/internal/content"
	"github.com/voidrab/gopdfrab/internal/pdf"
)

// dropEffects is the feature recorded when a page exceeds effectPixelBudget and
// its blend modes and soft masks are painted without them.
const dropEffects = "blend mode or soft mask"

// effectPixelBudget bounds the pixels one render composites through a blend mode
// or soft mask, since each painting operation under one costs a layer: about
// 2000 full pages at the default resolution. Past it, content paints plainly.
const effectPixelBudget = 4_000_000_000

// softMaskCacheBytes bounds the rendered masks one render keeps for reuse.
const softMaskCacheBytes = 256 << 20

// softMask is the opacity an ExtGState /SMask gives each device pixel.
type softMask struct {
	bounds image.Rectangle
	alpha  []uint8
	// outside is the opacity beyond bounds.
	outside uint8
}

func (m *softMask) at(x, y int) float64 {
	if !(image.Point{X: x, Y: y}).In(m.bounds) {
		return float64(m.outside) / 255
	}
	return float64(m.alpha[(y-m.bounds.Min.Y)*m.bounds.Dx()+x-m.bounds.Min.X]) / 255
}

// softMaskKey identifies a rendered mask: the same dictionary under another CTM
// is a different mask, since a mask's coordinates are those in force at the gs
// operator.
type softMaskKey struct {
	dict   uintptr
	ctm    content.Matrix
	canvas image.Rectangle
}

// applySoftMask sets gs's soft mask from an ExtGState soft mask (/SMask) value.
// /None clears it, and so does a value that cannot be drawn.
func (r *renderer) applySoftMask(v pdf.PDFValue, resources pdf.PDFDict, gs *renderState) {
	d, ok := v.(pdf.PDFDict)
	if !ok {
		gs.softMask = nil
		return
	}
	key := softMaskKey{pdf.ValuePointer(d.Entries), gs.CTM, r.canvas.Bounds()}
	mask, cached := r.softMasks[key]
	if !cached {
		mask = r.renderSoftMask(d, resources, gs)
		size := 0
		if mask != nil {
			size = len(mask.alpha)
		}
		if r.softMasks == nil || r.softMaskBytes+size > softMaskCacheBytes {
			r.softMasks, r.softMaskBytes = map[softMaskKey]*softMask{}, 0
		}
		r.softMaskBytes += size
		r.softMasks[key] = mask
	}
	gs.softMask = mask
}

// renderSoftMask renders the group of a soft-mask dictionary into a mask over
// the current canvas (ISO 32000-1 11.5). It returns nil when there is no group
// to render, which masks nothing.
func (r *renderer) renderSoftMask(d, resources pdf.PDFDict, gs *renderState) *softMask {
	form, ok := d.Entries.Get("G").(pdf.PDFDict)
	if !ok || !form.IsStream() || r.depth > maxFormDepth {
		return nil
	}
	data, err := pdf.DecodeStream(form)
	if err != nil {
		return nil
	}
	r.depth++
	defer func() { r.depth-- }()

	canvasBounds := r.canvas.Bounds()
	canvasClip := [4]float64{float64(canvasBounds.Min.X), float64(canvasBounds.Min.Y), float64(canvasBounds.Max.X), float64(canvasBounds.Max.Y)}
	ctm := formCTM(form, gs.CTM)
	clip := formClip(form, ctm, canvasClip)
	// Nothing paints outside the group's box, where the mask is a single value.
	bounds := clipToBounds(canvasBounds, clip)
	if !r.chargeEffects(bounds) {
		return nil
	}

	luminosity := d.Entries.Get("S") != pdf.PDFValue(pdf.PDFName{Value: "Alpha"})
	formRes := resourcesOr(form, resources)
	layer := image.NewRGBA(bounds)
	var backdrop [3]float64
	if luminosity {
		backdrop = softMaskBackdrop(d, form, formRes)
		if len(layer.Pix) > 0 {
			storeRGBA64(layer.Pix, 0, backdrop[0], backdrop[1], backdrop[2], 1)
			for filled := 4; filled < len(layer.Pix); filled *= 2 {
				copy(layer.Pix[filled:], layer.Pix[:filled])
			}
		}
	}

	r.onLayer(layer, func() { r.execContent(data, formRes, newRenderState(ctm, clip)) })

	transfer := softMaskTransfer(d)
	mask := &softMask{bounds: bounds, alpha: make([]uint8, bounds.Dx()*bounds.Dy())}
	for i := range mask.alpha {
		px := layer.Pix[i*4 : i*4+4]
		v := px[3]
		if luminosity {
			v = luminosityOf(px)
		}
		mask.alpha[i] = transfer[v]
	}
	// An alpha mask's backdrop is the zero value, so nothing paints there either.
	mask.outside = transfer[uint8(lum(backdrop)*255+0.5)]
	return mask
}

// luminosityOf is the luminosity of an opaque RGBA pixel.
func luminosityOf(px []uint8) uint8 {
	return uint8(lum([3]float64{float64(px[0]), float64(px[1]), float64(px[2])}) + 0.5)
}

// softMaskBackdrop returns the colour a luminosity mask's group starts on: the
// backdrop colour (/BC) in the group's colour space, or black.
func softMaskBackdrop(d, form, resources pdf.PDFDict) [3]float64 {
	comps, err := pdf.FloatArray(d.Entries.Get("BC"))
	if err != nil {
		return [3]float64{}
	}
	group, _ := form.Entries.Get("Group").(pdf.PDFDict)
	space := group.Entries.Get("CS")
	if space == nil {
		switch len(comps) {
		case 1:
			space = pdf.PDFName{Value: "DeviceGray"}
		case 3:
			space = pdf.PDFName{Value: "DeviceRGB"}
		case 4:
			space = pdf.PDFName{Value: "DeviceCMYK"}
		default:
			return [3]float64{}
		}
	}
	red, green, blue := pdf.ResolveColor(space, comps, resources)
	return [3]float64{red, green, blue}
}

// softMaskTransfer tabulates the transfer function (/TR) of a soft-mask
// dictionary, which maps mask values to opacities. An identity, absent or
// unusable function changes nothing.
func softMaskTransfer(d pdf.PDFDict) *[256]uint8 {
	table := new([256]uint8)
	var fn pdf.Function
	if v := d.Entries.Get("TR"); v != nil && v != pdf.PDFValue(pdf.PDFName{Value: "Identity"}) {
		fn, _ = pdf.ParseFunction(v)
	}
	for i := range table {
		table[i] = uint8(i)
		if fn == nil {
			continue
		}
		if out := fn.Eval([]float64{float64(i) / 255}); len(out) > 0 {
			table[i] = uint8(math.Round(math.Max(0, math.Min(1, out[0])) * 255))
		}
	}
	return table
}

// blendModeOf returns the blend mode selected by a blend mode (/BM) value: the
// first name the renderer knows, taken from a name or an array of names, or ""
// for Normal.
func blendModeOf(v pdf.PDFValue) string {
	names := []pdf.PDFValue{v}
	if arr, ok := v.(pdf.PDFArray); ok {
		names = arr
	}
	for _, item := range names {
		name, ok := item.(pdf.PDFName)
		if !ok {
			continue
		}
		if name.Value == "Normal" || name.Value == "Compatible" {
			return ""
		}
		if blendModes[name.Value] != nil {
			return name.Value
		}
	}
	return ""
}

// withEffects runs paint, which marks the canvas. Under a soft mask or blend
// mode, it marks a separate layer over the area reach returns, which is then
// composited with them; paint runs with neither set in gs.
func (r *renderer) withEffects(gs *renderState, reach func() image.Rectangle, paint func()) {
	if gs.softMask == nil && gs.blendMode == "" {
		paint()
		return
	}
	mask, mode := gs.softMask, gs.blendMode
	gs.softMask, gs.blendMode = nil, ""
	defer func() { gs.softMask, gs.blendMode = mask, mode }()

	area := clipToBounds(r.canvas.Bounds(), gs.clip)
	if reach != nil {
		area = area.Intersect(reach())
	}
	if mask != nil && mask.outside == 0 {
		area = area.Intersect(mask.bounds)
	}
	if !r.chargeEffects(area) {
		paint()
		return
	}
	layer := image.NewRGBA(area)
	r.onLayer(layer, paint)
	compositeLayer(r.canvas, layer, mask, mode, 1)
}

// chargeEffects counts area against effectPixelBudget and reports whether it
// fitted. When not, it records the drop.
func (r *renderer) chargeEffects(area image.Rectangle) bool {
	r.effectPixels += int64(area.Dx()) * int64(area.Dy())
	if r.effectPixels > effectPixelBudget {
		r.drop(dropEffects)
		return false
	}
	return true
}

// onLayer runs paint with layer as the canvas.
func (r *renderer) onLayer(layer *image.RGBA, paint func()) {
	canvas := r.canvas
	r.canvas = layer
	paint()
	r.canvas = canvas
}

// compositeLayer composites layer onto dst at the opacity alpha, through mask
// and the blend mode, by the basic compositing formula of ISO 32000-1 11.3.6.
// Both hold premultiplied samples.
func compositeLayer(dst, layer *image.RGBA, mask *softMask, mode string, alpha float64) {
	area := layer.Bounds()
	mix := blendModes[mode]
	for y := area.Min.Y; y < area.Max.Y; y++ {
		src := layer.Pix[layer.PixOffset(area.Min.X, y):layer.PixOffset(area.Max.X, y)]
		row := dst.Pix[dst.PixOffset(area.Min.X, y):dst.PixOffset(area.Max.X, y)]
		for i := 0; i < len(src); i += 4 {
			if src[i+3] == 0 {
				continue
			}
			opacity := alpha
			if mask != nil {
				opacity *= mask.at(area.Min.X+i/4, y)
			}
			sa := float64(src[i+3]) * opacity / 255
			if sa == 0 {
				continue
			}
			if mix == nil {
				for c := range 4 {
					row[i+c] = uint8(float64(src[i+c])*opacity + float64(row[i+c])*(1-sa) + 0.5)
				}
				continue
			}
			var cs, cb [3]float64
			for c := range cs {
				cs[c] = float64(src[i+c]) / float64(src[i+3])
				if row[i+3] != 0 {
					cb[c] = float64(row[i+c]) / float64(row[i+3])
				}
			}
			ab := float64(row[i+3]) / 255
			mixed := mix(cb, cs)
			ar := ab + sa - ab*sa
			var out [3]float64
			for c := range out {
				// The premultiplied result, divided by ar for storeRGBA64.
				out[c] = ((1-sa)*ab*cb[c] + sa*((1-ab)*cs[c]+ab*mixed[c])) / ar
			}
			storeRGBA64(row, i, out[0], out[1], out[2], ar)
		}
	}
}

// separable makes a blend mode of a function of one component.
func separable(fn func(cb, cs float64) float64) func(cb, cs [3]float64) [3]float64 {
	return func(cb, cs [3]float64) [3]float64 {
		return [3]float64{fn(cb[0], cs[0]), fn(cb[1], cs[1]), fn(cb[2], cs[2])}
	}
}

// blendModes are the blend modes of ISO 32000-1 Tables 136 and 137 other than
// Normal: the separable ones work on one component at a time, the others on the
// colour as a whole.
var blendModes = map[string]func(cb, cs [3]float64) [3]float64{
	"Multiply": separable(func(cb, cs float64) float64 { return cb * cs }),
	"Screen":   separable(screen),
	"Overlay":  separable(func(cb, cs float64) float64 { return hardLight(cs, cb) }),
	"Darken":   separable(math.Min),
	"Lighten":  separable(math.Max),
	"ColorDodge": separable(func(cb, cs float64) float64 {
		switch {
		case cb == 0:
			return 0
		case cb >= 1-cs:
			return 1
		}
		return cb / (1 - cs)
	}),
	"ColorBurn": separable(func(cb, cs float64) float64 {
		switch {
		case cb >= 1:
			return 1
		case 1-cb >= cs:
			return 0
		}
		return 1 - (1-cb)/cs
	}),
	"HardLight": separable(hardLight),
	"SoftLight": separable(func(cb, cs float64) float64 {
		if cs <= 0.5 {
			return cb - (1-2*cs)*cb*(1-cb)
		}
		d := math.Sqrt(cb)
		if cb <= 0.25 {
			d = ((16*cb-12)*cb + 4) * cb
		}
		return cb + (2*cs-1)*(d-cb)
	}),
	"Difference": separable(func(cb, cs float64) float64 { return math.Abs(cb - cs) }),
	"Exclusion":  separable(func(cb, cs float64) float64 { return cb + cs - 2*cb*cs }),
	"Hue":        func(cb, cs [3]float64) [3]float64 { return setLum(setSat(cs, sat(cb)), lum(cb)) },
	"Saturation": func(cb, cs [3]float64) [3]float64 { return setLum(setSat(cb, sat(cs)), lum(cb)) },
	"Color":      func(cb, cs [3]float64) [3]float64 { return setLum(cs, lum(cb)) },
	"Luminosity": func(cb, cs [3]float64) [3]float64 { return setLum(cb, lum(cs)) },
}

func screen(cb, cs float64) float64 { return cb + cs - cb*cs }

func hardLight(cb, cs float64) float64 {
	if cs <= 0.5 {
		return cb * 2 * cs
	}
	return screen(cb, 2*cs-1)
}

func lum(c [3]float64) float64 { return 0.3*c[0] + 0.59*c[1] + 0.11*c[2] }

func sat(c [3]float64) float64 {
	return max(c[0], c[1], c[2]) - min(c[0], c[1], c[2])
}

func setLum(c [3]float64, l float64) [3]float64 {
	d := l - lum(c)
	c = [3]float64{c[0] + d, c[1] + d, c[2] + d}
	l = lum(c)
	n, x := min(c[0], c[1], c[2]), max(c[0], c[1], c[2])
	for i := range c {
		if n < 0 {
			c[i] = l + (c[i]-l)*l/(l-n)
		}
	}
	for i := range c {
		if x > 1 {
			c[i] = l + (c[i]-l)*(1-l)/(x-l)
		}
	}
	return c
}

func setSat(c [3]float64, s float64) [3]float64 {
	n, x := min(c[0], c[1], c[2]), max(c[0], c[1], c[2])
	if x == n {
		return [3]float64{}
	}
	for i := range c {
		c[i] = (c[i] - n) * s / (x - n)
	}
	return c
}

// textReach bounds where showing a string of n bytes can mark from the current
// text position: a run along the text direction, allowing each glyph two ems,
// and two ems across it. adjust is the sum of the absolute TJ adjustments. A
// Type 3 glyph can draw anything, so its reach is unbounded.
func textReach(n int, adjust float64, gs *renderState) image.Rectangle {
	if gs.font.Entries.Get("Subtype") == pdf.PDFValue(pdf.PDFName{Value: "Type3"}) {
		return noHint
	}
	em := math.Abs(gs.fontSize)
	hScale := math.Max(1, math.Abs(gs.hScale))
	spacing := float64(n) * (math.Abs(gs.charSpace) + math.Abs(gs.wordSpace))
	back := em * (2 + adjust/1000) * hScale
	ahead := (em*(2*float64(n+1)+adjust/1000) + spacing) * hScale
	across := 2*em + math.Abs(gs.rise) + gs.lineWidth
	return clipToBounds(noHint, deviceBounds(-back, -across, back+ahead, 2*across, gs.tm.Mul(gs.CTM), 2))
}

// imageReach is the device area an image, which fills the unit square of user
// space, can mark.
func imageReach(gs *renderState) image.Rectangle {
	return clipToBounds(noHint, deviceBounds(0, 0, 1, 1, gs.CTM, 1))
}

// xobjectReach is the device area a Do of the named XObject can mark.
func xobjectReach(xobj pdf.PDFDict, gs *renderState) image.Rectangle {
	if xobj.Entries.Get("Subtype") == pdf.PDFValue(pdf.PDFName{Value: "Image"}) {
		return imageReach(gs)
	}
	return clipToBounds(noHint, formClip(xobj, formCTM(xobj, gs.CTM), noClip))
}

// noClip is a clip that restricts nothing.
var noClip = [4]float64{math.MinInt32, math.MinInt32, math.MaxInt32, math.MaxInt32}

// deviceBounds is the device-space bounding box, grown by margin on each side, of
// a user-space rectangle.
func deviceBounds(x, y, w, h float64, ctm content.Matrix, margin float64) [4]float64 {
	var box pathBuilder
	box.rect(x, y, w, h)
	minX, minY, maxX, maxY := boundsOfContours(box.deviceContours(ctm))
	return [4]float64{minX - margin, minY - margin, maxX + margin, maxY + margin}
}

// tjReach is textReach for the operand array of a TJ.
func tjReach(operands []pdf.PDFValue, gs *renderState) image.Rectangle {
	if len(operands) == 0 {
		return image.Rectangle{}
	}
	arr, _ := operands[len(operands)-1].(pdf.PDFArray)
	n, adjust := 0, 0.0
	for _, item := range arr {
		if v, ok := pdf.PDFNumberToFloat(item); ok {
			adjust += math.Abs(v)
		} else if raw, ok := pdf.StringBytes(item); ok {
			n += len(raw)
		}
	}
	return textReach(n, adjust, gs)
}
