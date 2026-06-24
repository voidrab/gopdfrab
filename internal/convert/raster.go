package convert

import (
	"encoding/binary"
	"fmt"
	"image"
	"math"

	"github.com/voidrab/gopdfrab/internal/pdf"

	"github.com/voidrab/gopdfrab/internal/verify"
)

// RenderPage rasterizes a single page's content streams into an opaque RGBA
// buffer at the given resolution, walking the content-stream graphics-state
// machine: CTM (q/Q/cm), path construction and painting (m/l/c/v/y/h/re,
// f/F/f*/S/s/B/B*/b/b*/n), colour (g/G/rg/RG/k/K/cs/CS/sc/SC/scn/SCN), alpha
// (gs ExtGState ca/CA), Form/Image XObjects (Do, recursing into Forms and
// compositing Images including their own /SMask), and text (BT/ET/Tf/Td/TD/
// Tm/T*/Tj/TJ/'/"). Clipping (W/W*) is approximated as a bounding box.
func RenderPage(page pdf.PDFDict, resources pdf.PDFDict, mediaBox [4]float64, dpi int) (*image.RGBA, error) {
	content, err := pageContentBytes(page)
	if err != nil {
		return nil, err
	}
	return renderContent(content, resources, mediaBox, dpi)
}

// renderFormContent rasterizes a Form XObject's own /BBox + content in
// isolation, ignoring its /Matrix and whatever CTM is active at each /Do
// that invokes it: those keep applying, unchanged, to whatever the Form
// paints once flattened, since only its content is being replaced, not its
// identity or placement. Returns the rendered buffer and the BBox it was
// rendered against (needed to place the replacement image back into it).
func renderFormContent(form pdf.PDFDict, resources pdf.PDFDict, dpi int) (*image.RGBA, [4]float64, error) {
	bbox, err := pdf.FloatArray(form.Entries["BBox"])
	if err != nil || len(bbox) != 4 {
		return nil, [4]float64{}, fmt.Errorf("raster: missing or invalid Form /BBox")
	}
	box := [4]float64{bbox[0], bbox[1], bbox[2], bbox[3]}
	content, err := pdf.DecodeStream(form)
	if err != nil {
		return nil, [4]float64{}, err
	}
	canvas, err := renderContent(content, resources, box, dpi)
	return canvas, box, err
}

// renderContent is the shared core behind RenderPage and renderFormContent:
// it rasterizes content into a fresh opaque-white canvas sized from bounds
// (a user-space rect) at dpi, then runs the graphics-state machine over it.
func renderContent(content []byte, resources pdf.PDFDict, bounds [4]float64, dpi int) (*image.RGBA, error) {
	width := int(math.Ceil((bounds[2] - bounds[0]) * float64(dpi) / 72))
	height := int(math.Ceil((bounds[3] - bounds[1]) * float64(dpi) / 72))
	if width <= 0 || height <= 0 || width > 20000 || height > 20000 {
		return nil, fmt.Errorf("raster: degenerate or oversized bounds")
	}
	canvas := image.NewRGBA(image.Rect(0, 0, width, height))
	for i := range canvas.Pix {
		canvas.Pix[i] = 0xFF // opaque white backdrop
	}

	scale := float64(dpi) / 72
	// Device CTM: PDF user space origin bottom-left Y-up -> image space origin top-left Y-down.
	base := Matrix{A: scale, D: -scale, E: -bounds[0] * scale, F: bounds[3] * scale}
	gs := renderState{
		ctm: base, fillAlpha: 1, strokeAlpha: 1, lineWidth: 1, hScale: 1,
		clip: [4]float64{0, 0, float64(width), float64(height)},
	}
	r := &renderer{canvas: canvas, fontCache: map[uintptr]*fontInfo{}}
	r.execContent(content, resources, gs)
	return canvas, nil
}

// pageContentBytes concatenates a page's /Contents stream(s) (a single
// stream or an array of streams, per the spec, joined by whitespace).
func pageContentBytes(page pdf.PDFDict) ([]byte, error) {
	var out []byte
	switch c := page.Entries["Contents"].(type) {
	case pdf.PDFDict:
		data, err := pdf.DecodeStream(c)
		if err != nil {
			return nil, err
		}
		out = data
	case pdf.PDFArray:
		for _, item := range c {
			d, ok := item.(pdf.PDFDict)
			if !ok {
				continue
			}
			data, err := pdf.DecodeStream(d)
			if err != nil {
				return nil, err
			}
			out = append(out, data...)
			out = append(out, '\n')
		}
	}
	return out, nil
}

// renderState is the graphics state saved/restored by q/Q (the current path
// under construction is tracked separately, since q/Q does not affect it).
type renderState struct {
	ctm                     Matrix
	fillRGB, strokeRGB      [3]float64
	fillCS, strokeCS        pdf.PDFValue
	fillAlpha, strokeAlpha  float64
	lineWidth               float64
	clip                    [4]float64 // device-space bbox: xmin,ymin,xmax,ymax

	font      pdf.PDFDict
	fontSize  float64
	charSpace float64
	wordSpace float64
	hScale    float64
	leading   float64
	tm, tlm   Matrix
}

// renderer carries the mutable bits shared across a RenderPage call: the
// output canvas, a font-info cache keyed by font dict identity, and a
// recursion-depth guard against pathological/cyclic Form XObject graphs.
type renderer struct {
	canvas    *image.RGBA
	fontCache map[uintptr]*fontInfo
	depth     int
}

// pathBuilder accumulates the current path's subpaths in user space, kept
// outside renderState since q/Q does not save/restore it.
type pathBuilder struct {
	subpaths [][]Point
	cur      []Point
	start    Point
	curPt    Point
}

func (p *pathBuilder) moveTo(pt Point) {
	if len(p.cur) > 0 {
		p.subpaths = append(p.subpaths, p.cur)
	}
	p.cur = []Point{pt}
	p.start = pt
	p.curPt = pt
}

func (p *pathBuilder) lineTo(pt Point) {
	if len(p.cur) == 0 {
		p.cur = []Point{p.curPt}
	}
	p.cur = append(p.cur, pt)
	p.curPt = pt
}

func (p *pathBuilder) curveTo(c1, c2, end Point) {
	if len(p.cur) == 0 {
		p.cur = []Point{p.curPt}
	}
	p.cur = append(p.cur, flattenCubic(p.curPt, c1, c2, end, 0.1)...)
	p.curPt = end
}

func (p *pathBuilder) closePath() {
	if len(p.cur) > 0 {
		p.cur = append(p.cur, p.start)
		p.curPt = p.start
	}
}

func (p *pathBuilder) rect(x, y, w, h float64) {
	p.moveTo(Point{x, y})
	p.lineTo(Point{x + w, y})
	p.lineTo(Point{x + w, y + h})
	p.lineTo(Point{x, y + h})
	p.closePath()
}

// contours returns all subpaths transformed to device space by ctm.
func (p *pathBuilder) deviceContours(ctm Matrix) [][]Point {
	all := p.subpaths
	if len(p.cur) > 0 {
		all = append(all, p.cur)
	}
	out := make([][]Point, 0, len(all))
	for _, sp := range all {
		dp := make([]Point, len(sp))
		for i, pt := range sp {
			dp[i] = ctm.Apply(pt)
		}
		out = append(out, dp)
	}
	return out
}

func (p *pathBuilder) reset() {
	*p = pathBuilder{}
}

// execContent interprets one content stream's operators against gs (passed
// by value, so callee-local q/Q changes never leak to the caller).
func (r *renderer) execContent(data []byte, resources pdf.PDFDict, gs renderState) {
	var gsStack []renderState
	var path pathBuilder
	pendingClip := false
	pendingClipEvenOdd := false

	applyPendingClip := func() {
		if !pendingClip {
			return
		}
		pendingClip = false
		contours := path.deviceContours(gs.ctm)
		if len(contours) == 0 {
			return
		}
		minX, minY, maxX, maxY := boundsOfContours(contours)
		gs.clip = intersectRect(gs.clip, [4]float64{minX, minY, maxX, maxY})
	}

	pdf.NewContentScanner(data).Scan(func(op string, operands []pdf.PDFValue) {
		nums := func(n int) []float64 {
			out := make([]float64, n)
			if len(operands) < n {
				return out
			}
			tail := operands[len(operands)-n:]
			for i, v := range tail {
				out[i], _ = pdf.PDFNumberToFloat(v)
			}
			return out
		}

		switch op {
		case "q":
			gsStack = append(gsStack, gs)
		case "Q":
			if n := len(gsStack); n > 0 {
				gs = gsStack[n-1]
				gsStack = gsStack[:n-1]
			}
		case "cm":
			a := nums(6)
			m := Matrix{A: a[0], B: a[1], C: a[2], D: a[3], E: a[4], F: a[5]}
			gs.ctm = m.Mul(gs.ctm)
		case "w":
			a := nums(1)
			gs.lineWidth = a[0]
		case "m":
			a := nums(2)
			path.moveTo(Point{a[0], a[1]})
		case "l":
			a := nums(2)
			path.lineTo(Point{a[0], a[1]})
		case "c":
			a := nums(6)
			path.curveTo(Point{a[0], a[1]}, Point{a[2], a[3]}, Point{a[4], a[5]})
		case "v":
			a := nums(4)
			path.curveTo(path.curPt, Point{a[0], a[1]}, Point{a[2], a[3]})
		case "y":
			a := nums(4)
			end := Point{a[2], a[3]}
			path.curveTo(Point{a[0], a[1]}, end, end)
		case "h":
			path.closePath()
		case "re":
			a := nums(4)
			path.rect(a[0], a[1], a[2], a[3])
		case "W":
			pendingClip, pendingClipEvenOdd = true, false
		case "W*":
			pendingClip, pendingClipEvenOdd = true, true
		case "f", "F", "f*", "S", "s", "B", "B*", "b", "b*", "n":
			r.paintPath(&path, &gs, op)
			applyPendingClip()
			_ = pendingClipEvenOdd
			path.reset()
		case "g":
			a := nums(1)
			gs.fillRGB = [3]float64{a[0], a[0], a[0]}
			gs.fillCS = pdf.PDFName{Value: "DeviceGray"}
		case "G":
			a := nums(1)
			gs.strokeRGB = [3]float64{a[0], a[0], a[0]}
			gs.strokeCS = pdf.PDFName{Value: "DeviceGray"}
		case "rg":
			a := nums(3)
			gs.fillRGB = [3]float64{a[0], a[1], a[2]}
			gs.fillCS = pdf.PDFName{Value: "DeviceRGB"}
		case "RG":
			a := nums(3)
			gs.strokeRGB = [3]float64{a[0], a[1], a[2]}
			gs.strokeCS = pdf.PDFName{Value: "DeviceRGB"}
		case "k":
			a := nums(4)
			gs.fillRGB[0], gs.fillRGB[1], gs.fillRGB[2] = pdf.CMYKToRGB(a)
			gs.fillCS = pdf.PDFName{Value: "DeviceCMYK"}
		case "K":
			a := nums(4)
			gs.strokeRGB[0], gs.strokeRGB[1], gs.strokeRGB[2] = pdf.CMYKToRGB(a)
			gs.strokeCS = pdf.PDFName{Value: "DeviceCMYK"}
		case "cs":
			gs.fillCS = resolveOperandColorSpace(operands, resources)
			gs.fillRGB = [3]float64{0, 0, 0}
		case "CS":
			gs.strokeCS = resolveOperandColorSpace(operands, resources)
			gs.strokeRGB = [3]float64{0, 0, 0}
		case "sc", "scn":
			if comps := numericOperands(operands); len(comps) > 0 && gs.fillCS != nil {
				r, g, b := pdf.ResolveColor(gs.fillCS, comps, resources)
				gs.fillRGB = [3]float64{r, g, b}
			}
		case "SC", "SCN":
			if comps := numericOperands(operands); len(comps) > 0 && gs.strokeCS != nil {
				r, g, b := pdf.ResolveColor(gs.strokeCS, comps, resources)
				gs.strokeRGB = [3]float64{r, g, b}
			}
		case "gs":
			r.applyExtGState(operands, resources, &gs)
		case "Do":
			r.doXObject(operands, resources, &gs)
		case "BT":
			gs.tm, gs.tlm = IdentityMatrix, IdentityMatrix
		case "Tf":
			r.applyTf(operands, resources, &gs)
		case "Tc":
			a := nums(1)
			gs.charSpace = a[0]
		case "Tw":
			a := nums(1)
			gs.wordSpace = a[0]
		case "Tz":
			a := nums(1)
			gs.hScale = a[0] / 100
		case "TL":
			a := nums(1)
			gs.leading = a[0]
		case "Td":
			a := nums(2)
			gs.tlm = Matrix{A: 1, D: 1, E: a[0], F: a[1]}.Mul(gs.tlm)
			gs.tm = gs.tlm
		case "TD":
			a := nums(2)
			gs.leading = -a[1]
			gs.tlm = Matrix{A: 1, D: 1, E: a[0], F: a[1]}.Mul(gs.tlm)
			gs.tm = gs.tlm
		case "Tm":
			a := nums(6)
			gs.tlm = Matrix{A: a[0], B: a[1], C: a[2], D: a[3], E: a[4], F: a[5]}
			gs.tm = gs.tlm
		case "T*":
			gs.tlm = Matrix{A: 1, D: 1, F: -gs.leading}.Mul(gs.tlm)
			gs.tm = gs.tlm
		case "Tj":
			r.showText(verify.ShownStringBytes(op, operands), resources, &gs)
		case "'":
			gs.tlm = Matrix{A: 1, D: 1, F: -gs.leading}.Mul(gs.tlm)
			gs.tm = gs.tlm
			r.showText(verify.ShownStringBytes(op, operands), resources, &gs)
		case "\"":
			a3 := nums(2) // aw ac on top, string is the operand itself, handled by shownStringBytes
			gs.wordSpace, gs.charSpace = a3[0], a3[1]
			gs.tlm = Matrix{A: 1, D: 1, F: -gs.leading}.Mul(gs.tlm)
			gs.tm = gs.tlm
			r.showText(verify.ShownStringBytes(op, operands), resources, &gs)
		case "TJ":
			r.showTextArray(operands, resources, &gs)
		}
	})
}

func boundsOfContours(contours [][]Point) (minX, minY, maxX, maxY float64) {
	first := true
	for _, c := range contours {
		for _, p := range c {
			if first {
				minX, maxX, minY, maxY = p.X, p.X, p.Y, p.Y
				first = false
				continue
			}
			minX, maxX = math.Min(minX, p.X), math.Max(maxX, p.X)
			minY, maxY = math.Min(minY, p.Y), math.Max(maxY, p.Y)
		}
	}
	return
}

func intersectRect(a, b [4]float64) [4]float64 {
	return [4]float64{
		math.Max(a[0], b[0]), math.Max(a[1], b[1]),
		math.Min(a[2], b[2]), math.Min(a[3], b[3]),
	}
}

// clipContours intersects each contour's device-space points against gs's
// bounding-box clip by intersecting the path's own bbox; since FillPath/
// StrokePath only paint within canvas.Bounds(), the clip is applied by
// further restricting the canvas rect passed to a scratch sub-image.
func (r *renderer) paintPath(path *pathBuilder, gs *renderState, op string) {
	contours := path.deviceContours(gs.ctm)
	if len(contours) == 0 {
		return
	}
	clipped := clipToBounds(r.canvas.Bounds(), gs.clip)
	target := r.canvas
	if clipped != r.canvas.Bounds() {
		sub, ok := r.canvas.SubImage(clipped).(*image.RGBA)
		if ok {
			target = sub
		}
	}

	evenOdd := op == "f*" || op == "B*" || op == "b*"
	switch op {
	case "f", "F", "f*":
		FillPath(target, contours, gs.fillRGB, gs.fillAlpha, evenOdd)
	case "S", "s":
		StrokePath(target, contours, gs.lineWidth*ctmScale(gs.ctm), gs.strokeRGB, gs.strokeAlpha)
	case "B", "B*", "b", "b*":
		FillPath(target, contours, gs.fillRGB, gs.fillAlpha, evenOdd)
		StrokePath(target, contours, gs.lineWidth*ctmScale(gs.ctm), gs.strokeRGB, gs.strokeAlpha)
	case "n":
		// Path constructed only to set a clip region; no paint.
	}
}

// clipToBounds intersects bounds with a device-space rect, returning bounds
// unchanged if rect doesn't actually restrict it.
func clipToBounds(bounds image.Rectangle, rect [4]float64) image.Rectangle {
	r := image.Rect(int(math.Floor(rect[0])), int(math.Floor(rect[1])), int(math.Ceil(rect[2])), int(math.Ceil(rect[3])))
	return bounds.Intersect(r)
}

// ctmScale approximates a uniform scale factor from ctm, used to convert a
// PDF line width (in user-space units) to device-space pixels.
func ctmScale(ctm Matrix) float64 {
	sx := math.Hypot(ctm.A, ctm.B)
	sy := math.Hypot(ctm.C, ctm.D)
	return (sx + sy) / 2
}

func numericOperands(operands []pdf.PDFValue) []float64 {
	var out []float64
	for _, v := range operands {
		if f, ok := pdf.PDFNumberToFloat(v); ok {
			out = append(out, f)
		}
	}
	return out
}

// resolveOperandColorSpace resolves a cs/CS operator's color-space name
// operand against resources (DeviceGray/RGB/CMYK pass through as-is).
func resolveOperandColorSpace(operands []pdf.PDFValue, resources pdf.PDFDict) pdf.PDFValue {
	if len(operands) == 0 {
		return nil
	}
	name, ok := operands[len(operands)-1].(pdf.PDFName)
	if !ok {
		return operands[len(operands)-1]
	}
	switch name.Value {
	case "DeviceGray", "DeviceRGB", "DeviceCMYK", "Pattern":
		return name
	}
	if cs, ok := pdf.LookupNamedColorSpace(name.Value, resources); ok {
		return cs
	}
	return name
}

// applyExtGState applies a gs operator's ca/CA alpha from the named
// ExtGState resource (soft-mask groups are out of scope -- see raster.go's
// doc comment; the dedicated ImageWithSoftMask check is handled per-image
// in doXObject, and ExtGState /SMask is already neutralized by extGStateFixer
// before this renderer ever runs).
func (r *renderer) applyExtGState(operands []pdf.PDFValue, resources pdf.PDFDict, gs *renderState) {
	if len(operands) == 0 {
		return
	}
	name, ok := operands[len(operands)-1].(pdf.PDFName)
	if !ok {
		return
	}
	extGStates, _ := resources.Entries["ExtGState"].(pdf.PDFDict)
	egs, ok := extGStates.Entries[name.Value].(pdf.PDFDict)
	if !ok {
		return
	}
	if ca, ok := pdf.PDFNumberToFloat(egs.Entries["ca"]); ok {
		gs.fillAlpha = ca
	}
	if CA, ok := pdf.PDFNumberToFloat(egs.Entries["CA"]); ok {
		gs.strokeAlpha = CA
	}
}

// doXObject paints a Form (recursing with composed CTM/Resources) or Image
// (decoded via DecodeImageRGBA, compositing its /SMask if present) XObject.
func (r *renderer) doXObject(operands []pdf.PDFValue, resources pdf.PDFDict, gs *renderState) {
	if len(operands) == 0 {
		return
	}
	name, ok := operands[len(operands)-1].(pdf.PDFName)
	if !ok {
		return
	}
	xobjects, _ := resources.Entries["XObject"].(pdf.PDFDict)
	xobj, ok := xobjects.Entries[name.Value].(pdf.PDFDict)
	if !ok || !xobj.HasStream {
		return
	}
	subtype, _ := xobj.Entries["Subtype"].(pdf.PDFName)
	switch subtype.Value {
	case "Form":
		if r.depth > 12 {
			return
		}
		r.depth++
		defer func() { r.depth-- }()
		formRes, _ := xobj.Entries["Resources"].(pdf.PDFDict)
		if formRes.Entries == nil {
			formRes = resources
		}
		childGS := *gs
		if m, err := pdf.FloatArray(xobj.Entries["Matrix"]); err == nil && len(m) == 6 {
			fm := Matrix{A: m[0], B: m[1], C: m[2], D: m[3], E: m[4], F: m[5]}
			childGS.ctm = fm.Mul(gs.ctm)
		}
		data, err := pdf.DecodeStream(xobj)
		if err != nil {
			return
		}
		r.execContent(data, formRes, childGS)
	case "Image":
		r.paintImage(xobj, resources, gs)
	}
}

// paintImage maps an Image XObject's unit square through the CTM, sampling
// the decoded RGBA (and, if present, its /SMask's luminosity as a per-pixel
// alpha multiplier) into the canvas with nearest-neighbour resampling.
func (r *renderer) paintImage(xobj pdf.PDFDict, resources pdf.PDFDict, gs *renderState) {
	img, err := DecodeImageRGBA(xobj, resources)
	if err != nil {
		return
	}
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	if w == 0 || h == 0 {
		return
	}

	var smask *image.RGBA
	var smW, smH int
	if sm, ok := xobj.Entries["SMask"].(pdf.PDFDict); ok {
		if decoded, err := DecodeImageRGBA(sm, resources); err == nil {
			smask = decoded
			smW, smH = decoded.Bounds().Dx(), decoded.Bounds().Dy()
		}
	}

	ctm := gs.ctm
	inv, ok := ctm.Invert()
	if !ok {
		return
	}
	corners := []Point{{0, 0}, {1, 0}, {1, 1}, {0, 1}}
	minX, minY, maxX, maxY := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, c := range corners {
		p := ctm.Apply(c)
		minX, maxX = math.Min(minX, p.X), math.Max(maxX, p.X)
		minY, maxY = math.Min(minY, p.Y), math.Max(maxY, p.Y)
	}
	clip := intersectRect(gs.clip, [4]float64{minX, minY, maxX, maxY})
	bounds := r.canvas.Bounds()
	x0, y0 := int(math.Max(math.Floor(clip[0]), float64(bounds.Min.X))), int(math.Max(math.Floor(clip[1]), float64(bounds.Min.Y)))
	x1, y1 := int(math.Min(math.Ceil(clip[2]), float64(bounds.Max.X))), int(math.Min(math.Ceil(clip[3]), float64(bounds.Max.Y)))

	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			p := inv.Apply(Point{float64(x) + 0.5, float64(y) + 0.5})
			if p.X < 0 || p.X >= 1 || p.Y < 0 || p.Y >= 1 {
				continue
			}
			col := pdf.ClampInt(int(p.X*float64(w)), 0, w-1)
			row := pdf.ClampInt(int((1-p.Y)*float64(h)), 0, h-1)
			px := img.RGBAAt(col, row)
			alpha := float64(px.A) / 255 * gs.fillAlpha
			if smask != nil {
				scol := pdf.ClampInt(col*smW/w, 0, smW-1)
				srow := pdf.ClampInt(row*smH/h, 0, smH-1)
				lum := smask.RGBAAt(scol, srow)
				alpha *= float64(lum.R) / 255
			}
			if alpha <= 0 {
				continue
			}
			rgb := [3]float64{float64(px.R) / 255, float64(px.G) / 255, float64(px.B) / 255}
			blendPixel(r.canvas, x, y, rgb, alpha)
		}
	}
}


// applyTf resolves a Tf operator's named font resource into gs, building (and
// caching) its fontInfo.
func (r *renderer) applyTf(operands []pdf.PDFValue, resources pdf.PDFDict, gs *renderState) {
	if len(operands) < 2 {
		return
	}
	name, ok := operands[len(operands)-2].(pdf.PDFName)
	if !ok {
		return
	}
	size, _ := pdf.PDFNumberToFloat(operands[len(operands)-1])
	fonts, _ := resources.Entries["Font"].(pdf.PDFDict)
	fd, ok := fonts.Entries[name.Value].(pdf.PDFDict)
	if !ok {
		return
	}
	gs.font = fd
	gs.fontSize = size
}

// showTextArray implements TJ: strings are shown via showText, numeric
// adjustments shift the text position by -adj/1000*fontSize*hScale (no
// adjustment for vertical writing mode, out of scope).
func (r *renderer) showTextArray(operands []pdf.PDFValue, resources pdf.PDFDict, gs *renderState) {
	if len(operands) == 0 {
		return
	}
	arr, ok := operands[len(operands)-1].(pdf.PDFArray)
	if !ok {
		return
	}
	for _, item := range arr {
		switch v := item.(type) {
		case pdf.PDFString:
			r.showText(pdf.DecodePDFLiteralStringBytes(v.Value), resources, gs)
		case pdf.PDFHexString:
			r.showText(pdf.DecodePDFHexStringBytes(v.Value), resources, gs)
		default:
			if adj, ok := pdf.PDFNumberToFloat(v); ok {
				shift := -adj / 1000 * gs.fontSize * gs.hScale
				gs.tm = Matrix{A: 1, D: 1, E: shift}.Mul(gs.tm)
			}
		}
	}
}

// showText paints each glyph in raw (decoded from the content stream) bytes
// and advances gs.tm, mirroring the PDF text-showing algorithm (9.4.3): the
// glyph displacement is (w0*fontSize + charSpace + wordSpace)*hScale.
func (r *renderer) showText(raw []byte, resources pdf.PDFDict, gs *renderState) {
	if gs.font.Entries == nil || len(raw) == 0 {
		return
	}
	fi := r.fontInfoFor(gs.font, resources)
	if fi == nil {
		return
	}
	i := 0
	for i < len(raw) {
		var code int
		if fi.bytesPerCode == 2 {
			if i+1 >= len(raw) {
				break
			}
			code = int(raw[i])<<8 | int(raw[i+1])
			i += 2
		} else {
			code = int(raw[i])
			i++
		}

		width := fi.defaultWidth
		if w, ok := fi.widths[code]; ok {
			width = w
		}

		if gp, ok := fi.glyphFor(code); ok && len(gp.Contours) > 0 {
			// Glyph space (1000-unit em) -> text space (font-size units) -> user space (tm) -> device (ctm).
			glyphToText := Matrix{A: gs.fontSize * gs.hScale / 1000, D: gs.fontSize / 1000}
			textToDevice := glyphToText.Mul(gs.tm).Mul(gs.ctm)
			contours := make([][]Point, len(gp.Contours))
			for ci, c := range gp.Contours {
				dc := make([]Point, len(c))
				for pi, p := range c {
					dc[pi] = textToDevice.Apply(p)
				}
				contours[ci] = dc
			}
			FillPath(r.canvas, contours, gs.fillRGB, gs.fillAlpha, false)
		}

		ws := 0.0
		if fi.bytesPerCode == 1 && code == 32 {
			ws = gs.wordSpace
		}
		advance := (width/1000*gs.fontSize + gs.charSpace + ws) * gs.hScale
		gs.tm = Matrix{A: 1, D: 1, E: advance}.Mul(gs.tm)
	}
}

// fontInfo is a font resource's resolved rendering data: per-code advance
// widths and a glyph-outline lookup, built once per font dict and cached.
type fontInfo struct {
	bytesPerCode int
	defaultWidth float64
	widths       map[int]float64
	glyphFor     func(code int) (GlyphPath, bool)
}

func (r *renderer) fontInfoFor(font pdf.PDFDict, resources pdf.PDFDict) *fontInfo {
	key := pdf.ValuePointer(font.Entries)
	if fi, ok := r.fontCache[key]; ok {
		return fi
	}
	fi := buildFontInfo(font, resources)
	r.fontCache[key] = fi
	return fi
}

func buildFontInfo(font pdf.PDFDict, resources pdf.PDFDict) *fontInfo {
	if df, ok := font.Entries["DescendantFonts"].(pdf.PDFArray); ok && len(df) > 0 {
		if desc, ok := df[0].(pdf.PDFDict); ok {
			return buildCompositeFontInfo(desc)
		}
	}
	return buildSimpleFontInfo(font)
}

// buildCompositeFontInfo handles Type0/Identity-H fonts: 2-byte codes are
// CIDs, mapped to GIDs via /CIDToGIDMap (Identity or a stream), and glyph
// outlines come directly from the descendant's embedded program by GID --
// no name/cmap resolution needed, since both TrueType loca/glyf and CFF
// CharStrings INDEX are already GID-ordered.
func buildCompositeFontInfo(desc pdf.PDFDict) *fontInfo {
	fi := &fontInfo{bytesPerCode: 2, defaultWidth: 1000, widths: map[int]float64{}}
	if dw, ok := pdf.PDFNumberToFloat(desc.Entries["DW"]); ok {
		fi.defaultWidth = dw
	}
	if w, ok := desc.Entries["W"].(pdf.PDFArray); ok {
		for _, pair := range verify.ParseCIDWidths(w) {
			fi.widths[pair[0]] = float64(pair[1])
		}
	}

	cidToGID := func(cid int) int { return cid }
	if c2g, ok := desc.Entries["CIDToGIDMap"].(pdf.PDFDict); ok && c2g.HasStream {
		if data, err := pdf.DecodeStream(c2g); err == nil {
			cidToGID = func(cid int) int {
				if cid*2+2 > len(data) {
					return 0
				}
				return int(binary.BigEndian.Uint16(data[cid*2:]))
			}
		}
	}

	desc2, _ := desc.Entries["FontDescriptor"].(pdf.PDFDict)
	if ff2, ok := desc2.Entries["FontFile2"].(pdf.PDFDict); ok {
		data, err := pdf.DecodeStream(ff2)
		if err == nil {
			if tables, ok := verify.ParseSfnt(data); ok {
				fi.glyphFor = func(code int) (GlyphPath, bool) {
					return glyphOutlineFromTrueType(tables, cidToGID(code))
				}
				return fi
			}
		}
	}
	if ff3, ok := desc2.Entries["FontFile3"].(pdf.PDFDict); ok {
		data, err := pdf.DecodeStream(ff3)
		if err == nil {
			cff := extractCFFBytes(data)
			if cff != nil {
				fi.glyphFor = func(code int) (GlyphPath, bool) {
					return glyphOutlineFromCFF(cff, cidToGID(code))
				}
				return fi
			}
		}
	}
	fi.glyphFor = func(int) (GlyphPath, bool) { return GlyphPath{}, false }
	return fi
}

// buildSimpleFontInfo handles Type1/TrueType/MMType1 simple fonts: single
// byte codes map to glyph names via Encoding (BaseEncoding + Differences),
// then to outlines via the embedded font program.
func buildSimpleFontInfo(font pdf.PDFDict) *fontInfo {
	fi := &fontInfo{bytesPerCode: 1, widths: map[int]float64{}}
	firstChar := pdf.DictInt(font, "FirstChar", 0)
	if widths, ok := font.Entries["Widths"].(pdf.PDFArray); ok {
		for i, w := range widths {
			if v, ok := pdf.PDFNumberToFloat(w); ok {
				fi.widths[firstChar+i] = v
			}
		}
	}

	desc, _ := font.Entries["FontDescriptor"].(pdf.PDFDict)
	if mw, ok := pdf.PDFNumberToFloat(desc.Entries["MissingWidth"]); ok {
		fi.defaultWidth = mw
	}

	names := resolveSimpleEncoding(font.Entries["Encoding"])

	if ff, ok := desc.Entries["FontFile"].(pdf.PDFDict); ok {
		data, err := pdf.DecodeStream(ff)
		if err == nil {
			fi.glyphFor = func(code int) (GlyphPath, bool) {
				if code < 0 || code > 255 || names[code] == "" {
					return GlyphPath{}, false
				}
				return glyphOutlineFromType1(data, names[code])
			}
			return fi
		}
	}
	if ff2, ok := desc.Entries["FontFile2"].(pdf.PDFDict); ok {
		data, err := pdf.DecodeStream(ff2)
		if err == nil {
			if tables, ok := verify.ParseSfnt(data); ok {
				fi.glyphFor = func(code int) (GlyphPath, bool) {
					return glyphOutlineFromTrueType(tables, simpleCodeToGID(tables, code, names))
				}
				return fi
			}
		}
	}
	if ff3, ok := desc.Entries["FontFile3"].(pdf.PDFDict); ok {
		data, err := pdf.DecodeStream(ff3)
		if err == nil {
			if cff := extractCFFBytes(data); cff != nil {
				fi.glyphFor = func(code int) (GlyphPath, bool) {
					// No charset-based name->GID resolution is implemented;
					// falling back to code-as-GID is a documented approximation.
					return glyphOutlineFromCFF(cff, code)
				}
				return fi
			}
		}
	}
	fi.glyphFor = func(int) (GlyphPath, bool) { return GlyphPath{}, false }
	return fi
}

// extractCFFBytes returns a FontFile3 stream's raw CFF table bytes, whether
// it's a bare CFF program or an OpenType-wrapped one (Subtype /OpenType).
func extractCFFBytes(data []byte) []byte {
	if len(data) >= 4 && data[0] == 1 {
		return data
	}
	if tables, ok := verify.ParseSfnt(data); ok {
		if cff, ok := tables["CFF "]; ok {
			return cff
		}
	}
	return nil
}

// simpleCodeToGID resolves a simple TrueType font's character code to a
// GID: first via the code's Unicode value (from its WinAnsi-resolved glyph
// name) through a (3,1) cmap, then via raw/symbolic (3,0)/(1,0) cmap
// lookups, falling back to the code itself as a last resort.
func simpleCodeToGID(tables map[string][]byte, code int, names [256]string) int {
	gidMap := verify.ParseCmapFormat4(verify.TTWindowsBMPCmap(tables))
	if gidMap != nil && code >= 0 && code < 256 {
		if name := names[code]; name != "" {
			if uni, ok := glyphNameToWinAnsiCode(name); ok {
				if gid, ok := gidMap[verify.WinAnsiToUnicode[uni]]; ok {
					return int(gid)
				}
			}
		}
	}
	if gidMap != nil {
		if gid, ok := gidMap[uint16(code)]; ok {
			return int(gid)
		}
		if gid, ok := gidMap[0xF000+uint16(code)]; ok {
			return int(gid)
		}
	}
	return code
}

// glyphNameToWinAnsiCode reverse-looks-up verify.WinAnsiGlyphName for name,
// returning the WinAnsi code that produces it (used to recover a Unicode
// value for a Differences-renamed glyph).
func glyphNameToWinAnsiCode(name string) (int, bool) {
	for i, n := range verify.WinAnsiGlyphName {
		if n == name {
			return i, true
		}
	}
	return 0, false
}

// resolveSimpleEncoding builds a code->glyph-name table from a simple font's
// /Encoding entry, mirroring validateType1SubsetCoverage's pattern
// (checks_font_program.go).
func resolveSimpleEncoding(enc pdf.PDFValue) [256]string {
	var names [256]string
	switch e := enc.(type) {
	case pdf.PDFName:
		switch e.Value {
		case "WinAnsiEncoding":
			names = verify.WinAnsiGlyphName
		default:
			names = verify.StandardEncoding
		}
	case pdf.PDFDict:
		base, _ := e.Entries["BaseEncoding"].(pdf.PDFName)
		switch base.Value {
		case "WinAnsiEncoding":
			names = verify.WinAnsiGlyphName
		default:
			names = verify.StandardEncoding
		}
		if diffs, ok := e.Entries["Differences"].(pdf.PDFArray); ok {
			code := 0
			for _, item := range diffs {
				switch d := item.(type) {
				case pdf.PDFInteger:
					code = int(d)
				case pdf.PDFName:
					if code >= 0 && code < 256 {
						names[code] = d.Value
					}
					code++
				}
			}
		}
	default:
		names = verify.StandardEncoding
	}
	return names
}
