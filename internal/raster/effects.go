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
	dict uintptr
	ctm  content.Matrix
}

// applySoftMask sets gs's soft mask from an ExtGState soft mask (/SMask) value.
// /None clears it, and so does a value that cannot be drawn.
func (r *renderer) applySoftMask(v pdf.PDFValue, resources pdf.PDFDict, gs *renderState) {
	d, ok := v.(pdf.PDFDict)
	if !ok {
		gs.softMask = nil
		return
	}
	key := softMaskKey{pdf.ValuePointer(d.Entries), gs.CTM}
	mask, cached := r.softMasks[key]
	if !cached {
		mask = r.renderSoftMask(d, resources, gs)
		if r.softMasks == nil {
			r.softMasks = map[softMaskKey]*softMask{}
		}
		r.softMasks[key] = mask
	}
	gs.softMask = mask
}

// renderSoftMask renders the group of a soft-mask dictionary into a mask over
// the current canvas (ISO 32000-1 11.5). It returns nil when there is no group
// to render, which masks nothing.
func (r *renderer) renderSoftMask(d, resources pdf.PDFDict, gs *renderState) *softMask {
	form, ok := d.Entries.Get("G").(pdf.PDFDict)
	if !ok || !form.IsStream() || r.depth > 12 {
		return nil
	}
	data, err := pdf.DecodeStream(form)
	if err != nil {
		return nil
	}
	r.depth++
	defer func() { r.depth-- }()

	bounds := r.canvas.Bounds()
	luminosity := d.Entries.Get("S") != pdf.PDFValue(pdf.PDFName{Value: "Alpha"})
	formRes := resourcesOr(form, resources)
	layer := image.NewRGBA(bounds)
	var backdrop [3]float64
	if luminosity {
		backdrop = softMaskBackdrop(d, form, formRes)
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			for x := bounds.Min.X; x < bounds.Max.X; x++ {
				storeRGBA64(layer.Pix, layer.PixOffset(x, y), backdrop[0], backdrop[1], backdrop[2], 1)
			}
		}
	}

	child := renderState{
		GState: content.NewGState(), fillAlpha: 1, strokeAlpha: 1, lineWidth: 1, hScale: 1,
		clip: [4]float64{float64(bounds.Min.X), float64(bounds.Min.Y), float64(bounds.Max.X), float64(bounds.Max.Y)},
	}
	child.CTM = formCTM(form, gs.CTM)
	child.clip = formClip(form, child.CTM, child.clip)

	canvas := r.canvas
	r.canvas = layer
	r.execContent(data, formRes, child)
	r.canvas = canvas

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
	if luminosity {
		mask.outside = transfer[uint8(lum(backdrop)*255+0.5)]
	} else {
		mask.outside = transfer[0]
	}
	return mask
}

// luminosityOf is the luminosity of an opaque RGBA pixel.
func luminosityOf(px []uint8) uint8 {
	return uint8(0.3*float64(px[0]) + 0.59*float64(px[1]) + 0.11*float64(px[2]) + 0.5)
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
		if blendFunctions[name.Value] != nil || nonSeparableBlends[name.Value] != nil {
			return name.Value
		}
	}
	return ""
}

// withEffects runs paint, which marks the canvas. If gs has a soft mask or blend
// mode, paint marks a separate layer over hint, the area paint can reach, and
// that layer is composited with them. paint runs with neither set in gs.
func (r *renderer) withEffects(gs *renderState, hint image.Rectangle, paint func()) {
	if gs.softMask == nil && gs.blendMode == "" {
		paint()
		return
	}
	mask, mode := gs.softMask, gs.blendMode
	gs.softMask, gs.blendMode = nil, ""
	defer func() { gs.softMask, gs.blendMode = mask, mode }()

	area := clipToBounds(r.canvas.Bounds(), gs.clip).Intersect(hint)
	r.effectPixels += int64(area.Dx()) * int64(area.Dy())
	if r.effectPixels > effectPixelBudget {
		r.drop(dropEffects)
		paint()
		return
	}

	backdrop := r.canvas
	r.canvas = image.NewRGBA(area)
	paint()
	layer := r.canvas
	r.canvas = backdrop
	compositeLayer(backdrop, layer, mask, mode)
}

// compositeLayer composites layer onto dst through mask and the blend mode, by
// the basic compositing formula of ISO 32000-1 11.3.6.
func compositeLayer(dst, layer *image.RGBA, mask *softMask, mode string) {
	area := layer.Bounds()
	for y := area.Min.Y; y < area.Max.Y; y++ {
		for x := area.Min.X; x < area.Max.X; x++ {
			src := layer.Pix[layer.PixOffset(x, y):]
			if src[3] == 0 {
				continue
			}
			as := float64(src[3]) / 255
			cs := [3]float64{float64(src[0]) / float64(src[3]), float64(src[1]) / float64(src[3]), float64(src[2]) / float64(src[3])}
			if mask != nil {
				as *= mask.at(x, y)
				if as == 0 {
					continue
				}
			}
			off := dst.PixOffset(x, y)
			ab := float64(dst.Pix[off+3]) / 255
			var cb [3]float64
			if dst.Pix[off+3] != 0 {
				for c := range cb {
					cb[c] = float64(dst.Pix[off+c]) / float64(dst.Pix[off+3])
				}
			}
			mixed := blend(mode, cb, cs)
			ar := ab + as - ab*as
			var out [3]float64
			for c := range out {
				// The premultiplied result, divided by ar for storeRGBA64.
				out[c] = ((1-as)*ab*cb[c] + as*((1-ab)*cs[c]+ab*mixed[c])) / ar
			}
			storeRGBA64(dst.Pix, off, out[0], out[1], out[2], ar)
		}
	}
}

// blend applies a blend mode to a backdrop and source colour. An empty mode is
// Normal.
func blend(mode string, cb, cs [3]float64) [3]float64 {
	if fn := blendFunctions[mode]; fn != nil {
		return [3]float64{fn(cb[0], cs[0]), fn(cb[1], cs[1]), fn(cb[2], cs[2])}
	}
	if fn := nonSeparableBlends[mode]; fn != nil {
		return fn(cb, cs)
	}
	return cs
}

// blendFunctions are the separable blend modes of ISO 32000-1 Table 136, which
// work on one component at a time.
var blendFunctions = map[string]func(cb, cs float64) float64{
	"Multiply": func(cb, cs float64) float64 { return cb * cs },
	"Screen":   screen,
	"Overlay":  func(cb, cs float64) float64 { return hardLight(cs, cb) },
	"Darken":   math.Min,
	"Lighten":  math.Max,
	"ColorDodge": func(cb, cs float64) float64 {
		switch {
		case cb == 0:
			return 0
		case cb >= 1-cs:
			return 1
		}
		return cb / (1 - cs)
	},
	"ColorBurn": func(cb, cs float64) float64 {
		switch {
		case cb >= 1:
			return 1
		case 1-cb >= cs:
			return 0
		}
		return 1 - (1-cb)/cs
	},
	"HardLight": hardLight,
	"SoftLight": func(cb, cs float64) float64 {
		if cs <= 0.5 {
			return cb - (1-2*cs)*cb*(1-cb)
		}
		d := math.Sqrt(cb)
		if cb <= 0.25 {
			d = ((16*cb-12)*cb + 4) * cb
		}
		return cb + (2*cs-1)*(d-cb)
	},
	"Difference": func(cb, cs float64) float64 { return math.Abs(cb - cs) },
	"Exclusion":  func(cb, cs float64) float64 { return cb + cs - 2*cb*cs },
}

func screen(cb, cs float64) float64 { return cb + cs - cb*cs }

func hardLight(cb, cs float64) float64 {
	if cs <= 0.5 {
		return cb * 2 * cs
	}
	return screen(cb, 2*cs-1)
}

// nonSeparableBlends are the blend modes of ISO 32000-1 Table 137, which work on
// the colour as a whole.
var nonSeparableBlends = map[string]func(cb, cs [3]float64) [3]float64{
	"Hue":        func(cb, cs [3]float64) [3]float64 { return setLum(setSat(cs, sat(cb)), lum(cb)) },
	"Saturation": func(cb, cs [3]float64) [3]float64 { return setLum(setSat(cb, sat(cs)), lum(cb)) },
	"Color":      func(cb, cs [3]float64) [3]float64 { return setLum(cs, lum(cb)) },
	"Luminosity": func(cb, cs [3]float64) [3]float64 { return setLum(cb, lum(cs)) },
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
