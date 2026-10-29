package raster

import (
	"encoding/binary"
	"math"

	"github.com/voidrab/gopdfrab/internal/fontprog"
)

// GlyphPath is a glyph outline flattened to straight-line contours, scaled
// to a 1000-unit em (matching ttAdvanceWidth's PDF-units convention).
type GlyphPath struct {
	Contours [][]Point
}

// glyphOutlineFromTrueType extracts gid's outline from a parsed TrueType
// sfnt's glyf/loca tables, scaled from font units to a 1000-unit em.
func glyphOutlineFromTrueType(tables map[string][]byte, gid int) (GlyphPath, bool) {
	head := tables["head"]
	if len(head) < 20 {
		return GlyphPath{}, false
	}
	upm := int(binary.BigEndian.Uint16(head[18:20]))
	if upm == 0 {
		return GlyphPath{}, false
	}
	contours, ok := ttGlyfContours(tables, gid, 0)
	if !ok {
		return GlyphPath{}, false
	}
	scale := 1000.0 / float64(upm)
	for _, c := range contours {
		for i := range c {
			c[i].X *= scale
			c[i].Y *= scale
		}
	}
	return GlyphPath{Contours: contours}, true
}

// ttGlyfContours returns gid's contours in raw font units, recursing through
// composite glyph components (depth-guarded against malicious/cyclic data).
func ttGlyfContours(tables map[string][]byte, gid, depth int) ([][]Point, bool) {
	if depth > 8 {
		return nil, false
	}
	rec := fontprog.GlyfRecord(tables, gid)
	if len(rec) == 0 {
		return nil, true // empty outline, e.g. space; not an error
	}
	if fontprog.GlyfIsComposite(rec) {
		return ttCompositeContours(tables, rec, depth)
	}
	return ttSimpleGlyphContours(rec)
}

// ttSimpleGlyphContours decodes a TrueType simple-glyph outline (on/off-curve
// points with delta-encoded coordinates) into flattened polygon contours,
// reconstructing quadratic Bezier segments via implicit on-curve midpoints.
func ttSimpleGlyphContours(rec []byte) ([][]Point, bool) {
	if len(rec) < 10 {
		return nil, false
	}
	numContours := int(int16(binary.BigEndian.Uint16(rec)))
	if numContours <= 0 {
		return nil, true
	}
	off := 10
	endPts := make([]int, numContours)
	for i := 0; i < numContours; i++ {
		if off+2 > len(rec) {
			return nil, false
		}
		endPts[i] = int(binary.BigEndian.Uint16(rec[off:]))
		off += 2
		// The endpoints must climb: the point arrays below are sized from the
		// last one, but indexed by all of them.
		if i > 0 && endPts[i] < endPts[i-1] {
			return nil, false
		}
	}
	if off+2 > len(rec) {
		return nil, false
	}
	insLen := int(binary.BigEndian.Uint16(rec[off:]))
	off += 2 + insLen

	numPts := endPts[numContours-1] + 1
	flags := make([]byte, 0, numPts)
	for len(flags) < numPts {
		if off >= len(rec) {
			return nil, false
		}
		f := rec[off]
		off++
		flags = append(flags, f)
		if f&0x08 != 0 { // REPEAT_FLAG
			if off >= len(rec) {
				return nil, false
			}
			repeat := int(rec[off])
			off++
			for i := 0; i < repeat && len(flags) < numPts; i++ {
				flags = append(flags, f)
			}
		}
	}

	xs := make([]int, numPts)
	x := 0
	for i, f := range flags {
		switch {
		case f&0x02 != 0: // X_SHORT_VECTOR
			if off >= len(rec) {
				return nil, false
			}
			d := int(rec[off])
			off++
			if f&0x10 == 0 { // sign bit clear means negative
				d = -d
			}
			x += d
		case f&0x10 == 0: // long vector, signed delta
			if off+2 > len(rec) {
				return nil, false
			}
			x += int(int16(binary.BigEndian.Uint16(rec[off:])))
			off += 2
		}
		xs[i] = x
	}
	ys := make([]int, numPts)
	y := 0
	for i, f := range flags {
		switch {
		case f&0x04 != 0: // Y_SHORT_VECTOR
			if off >= len(rec) {
				return nil, false
			}
			d := int(rec[off])
			off++
			if f&0x20 == 0 {
				d = -d
			}
			y += d
		case f&0x20 == 0:
			if off+2 > len(rec) {
				return nil, false
			}
			y += int(int16(binary.BigEndian.Uint16(rec[off:])))
			off += 2
		}
		ys[i] = y
	}

	var contours [][]Point
	start := 0
	for _, end := range endPts {
		pts := make([]Point, 0, end-start+1)
		onCurve := make([]bool, 0, end-start+1)
		for i := start; i <= end; i++ {
			pts = append(pts, Point{float64(xs[i]), float64(ys[i])})
			onCurve = append(onCurve, flags[i]&0x01 != 0)
		}
		contours = append(contours, quadContourToLines(pts, onCurve))
		start = end + 1
	}
	return contours, true
}

// quadContourToLines walks a cyclic sequence of on/off-curve points,
// synthesizing implicit on-curve midpoints between consecutive off-curve
// points, and flattens each resulting quadratic segment to line points.
func quadContourToLines(pts []Point, onCurve []bool) []Point {
	n := len(pts)
	if n == 0 {
		return nil
	}
	mid := func(a, b Point) Point { return Point{(a.X + b.X) / 2, (a.Y + b.Y) / 2} }

	startIdx := -1
	for i := 0; i < n; i++ {
		if onCurve[i] {
			startIdx = i
			break
		}
	}
	var startPt Point
	if startIdx < 0 {
		// All-off-curve contour: synthesize a start from the first midpoint.
		startPt = mid(pts[0], pts[n-1])
		startIdx = 0
	} else {
		startPt = pts[startIdx]
	}

	var out []Point
	cur := startPt
	var pendingCtrl *Point
	for k := 1; k <= n; k++ {
		i := (startIdx + k) % n
		p := pts[i]
		if onCurve[i] {
			if pendingCtrl != nil {
				out = append(out, quadToLines(cur, *pendingCtrl, p)...)
				pendingCtrl = nil
			} else {
				out = append(out, p)
			}
			cur = p
		} else {
			if pendingCtrl != nil {
				implicit := mid(*pendingCtrl, p)
				out = append(out, quadToLines(cur, *pendingCtrl, implicit)...)
				cur = implicit
			}
			ctrl := p
			pendingCtrl = &ctrl
		}
	}
	if pendingCtrl != nil {
		out = append(out, quadToLines(cur, *pendingCtrl, startPt)...)
	}
	return out
}

// quadToLines flattens a single quadratic Bezier (p0 implicit as the current
// point) by elevating it to a cubic and reusing flattenCubic.
func quadToLines(p0, ctrl, p1 Point) []Point {
	c1 := Point{p0.X + 2.0/3.0*(ctrl.X-p0.X), p0.Y + 2.0/3.0*(ctrl.Y-p0.Y)}
	c2 := Point{p1.X + 2.0/3.0*(ctrl.X-p1.X), p1.Y + 2.0/3.0*(ctrl.Y-p1.Y)}
	return flattenCubic(p0, c1, c2, p1, 1.0)
}

// ttCompositeContours recurses through a composite glyph's components,
// applying each component's offset/scale transform to its sub-contours.
func ttCompositeContours(tables map[string][]byte, rec []byte, depth int) ([][]Point, bool) {
	var contours [][]Point
	off := 10
	for off+4 <= len(rec) {
		flags := binary.BigEndian.Uint16(rec[off:])
		gid := int(binary.BigEndian.Uint16(rec[off+2:]))
		off += 4

		var dx, dy float64
		if flags&0x0001 != 0 { // ARG_1_AND_2_ARE_WORDS
			if off+4 > len(rec) {
				return nil, false
			}
			if flags&0x0002 != 0 { // ARGS_ARE_XY_VALUES
				dx = float64(int16(binary.BigEndian.Uint16(rec[off:])))
				dy = float64(int16(binary.BigEndian.Uint16(rec[off+2:])))
			}
			off += 4
		} else {
			if off+2 > len(rec) {
				return nil, false
			}
			if flags&0x0002 != 0 {
				dx = float64(int8(rec[off]))
				dy = float64(int8(rec[off+1]))
			}
			off += 2
		}

		m := Matrix{A: 1, D: 1, E: dx, F: dy}
		f2dot14 := func(v uint16) float64 { return float64(int16(v)) / 16384.0 }
		switch {
		case flags&0x0008 != 0: // WE_HAVE_A_SCALE
			if off+2 > len(rec) {
				return nil, false
			}
			s := f2dot14(binary.BigEndian.Uint16(rec[off:]))
			m.A, m.D = s, s
			off += 2
		case flags&0x0040 != 0: // WE_HAVE_AN_X_AND_Y_SCALE
			if off+4 > len(rec) {
				return nil, false
			}
			m.A = f2dot14(binary.BigEndian.Uint16(rec[off:]))
			m.D = f2dot14(binary.BigEndian.Uint16(rec[off+2:]))
			off += 4
		case flags&0x0080 != 0: // WE_HAVE_A_TWO_BY_TWO
			if off+8 > len(rec) {
				return nil, false
			}
			m.A = f2dot14(binary.BigEndian.Uint16(rec[off:]))
			m.B = f2dot14(binary.BigEndian.Uint16(rec[off+2:]))
			m.C = f2dot14(binary.BigEndian.Uint16(rec[off+4:]))
			m.D = f2dot14(binary.BigEndian.Uint16(rec[off+6:]))
			off += 8
		}

		sub, ok := ttGlyfContours(tables, gid, depth+1)
		if !ok {
			return nil, false
		}
		for _, c := range sub {
			tc := make([]Point, len(c))
			for i, p := range c {
				tc[i] = m.Apply(p)
			}
			contours = append(contours, tc)
		}
		if flags&0x0020 == 0 { // no MORE_COMPONENTS
			break
		}
	}
	return contours, true
}

// cffProgram is a CFF program parsed once per font: its charstrings and the
// subroutines they call.
type cffProgram struct {
	charStrings [][]byte
	subrs       fontprog.CFFSubrs
}

func parseCFFProgram(cff []byte) (*cffProgram, bool) {
	td, ok := fontprog.ParseCFFTopDict(cff)
	if !ok || td.CSOffset < 0 {
		return nil, false
	}
	entries, _ := fontprog.ParseCFFIndex(cff, td.CSOffset)
	return &cffProgram{charStrings: entries, subrs: fontprog.ParseCFFSubrs(cff, td, len(entries))}, true
}

// outline returns glyph gid's outline.
func (p *cffProgram) outline(gid int) (GlyphPath, bool) {
	if gid < 0 || gid >= len(p.charStrings) {
		return GlyphPath{}, false
	}
	in := &type2Interp{gsubrs: p.subrs.Global, lsubrs: p.subrs.Local(gid)}
	in.run(p.charStrings[gid], 0)
	in.closeContour()
	return GlyphPath{Contours: in.contours}, true
}

// type2Interp holds the running state of a Type2 charstring interpreter.
type type2Interp struct {
	stack       []float64
	x, y        float64
	contours    [][]Point
	cur         []Point
	nStems      int
	widthParsed bool
	// gsubrs and lsubrs are the global and local subroutines callgsubr and
	// callsubr index into.
	gsubrs, lsubrs [][]byte
}

// maxType2SubrDepth is the subroutine nesting limit the Type2 charstring
// format sets.
const maxType2SubrDepth = 10

// interpretType2Charstring runs a CFF Type2 charstring that calls no
// subroutines and returns its outline as flattened polygon contours in
// glyph-space units.
func interpretType2Charstring(cs []byte) [][]Point {
	in := &type2Interp{}
	in.run(cs, 0)
	in.closeContour()
	return in.contours
}

func (in *type2Interp) closeContour() {
	if len(in.cur) > 1 {
		in.contours = append(in.contours, in.cur)
	}
	in.cur = nil
}

func (in *type2Interp) moveTo(x, y float64) {
	in.closeContour()
	in.x, in.y = x, y
	in.cur = []Point{{x, y}}
}

func (in *type2Interp) lineTo(x, y float64) {
	in.x, in.y = x, y
	in.cur = append(in.cur, Point{x, y})
}

func (in *type2Interp) curveTo(x1, y1, x2, y2, x3, y3 float64) {
	p0 := Point{in.x, in.y}
	pts := flattenCubic(p0, Point{x1, y1}, Point{x2, y2}, Point{x3, y3}, 1.0)
	in.cur = append(in.cur, pts...)
	in.x, in.y = x3, y3
}

// maybeWidth consumes a leading width argument from the stack the first time
// a stem/moveto/endchar operator runs with an odd/extra argument, per the
// Type2 charstring spec (the first stack-clearing op may carry width).
func (in *type2Interp) maybeWidth(expectedArgs int) {
	if in.widthParsed {
		return
	}
	in.widthParsed = true
	if len(in.stack) > expectedArgs {
		in.stack = in.stack[1:]
	}
}

// run interprets cs, depth subroutine calls deep. It reports whether the
// glyph ended (endchar, or a call that could not be followed) as opposed to cs
// returning to its caller.
func (in *type2Interp) run(cs []byte, depth int) (ended bool) {
	i := 0
	for i < len(cs) {
		b := cs[i]
		switch {
		case b >= 32 || b == 28:
			v, n := readType2Number(cs[i:])
			in.stack = append(in.stack, v)
			i += n
		case b == 10, b == 29: // callsubr, callgsubr
			if len(in.stack) == 0 || depth >= maxType2SubrDepth {
				return true
			}
			subrs := in.lsubrs
			if b == 29 {
				subrs = in.gsubrs
			}
			sub, ok := fontprog.CFFSubr(subrs, int(in.stack[len(in.stack)-1]))
			in.stack = in.stack[:len(in.stack)-1]
			if !ok || in.run(sub, depth+1) {
				return true
			}
			i++
		case b == 11: // return
			return false
		case b == 12: // two-byte escape: the flex family draws; the rest
			// (arithmetic, storage) do not touch the outline
			if i+1 < len(cs) {
				in.flex(cs[i+1])
			}
			in.stack = in.stack[:0]
			i += 2
		case b == 1, b == 3, b == 18, b == 23: // hstem/vstem/hstemhm/vstemhm
			in.maybeWidth(len(in.stack) &^ 1)
			in.nStems += len(in.stack) / 2
			in.stack = in.stack[:0]
			i++
		case b == 19, b == 20: // hintmask/cntrmask
			in.maybeWidth(len(in.stack) &^ 1)
			in.nStems += len(in.stack) / 2
			in.stack = in.stack[:0]
			i++
			i += (in.nStems + 7) / 8
		case b == 21: // rmoveto
			in.maybeWidth(2)
			if len(in.stack) >= 2 {
				in.moveTo(in.x+in.stack[0], in.y+in.stack[1])
			}
			in.stack = in.stack[:0]
			i++
		case b == 22: // hmoveto
			in.maybeWidth(1)
			if len(in.stack) >= 1 {
				in.moveTo(in.x+in.stack[0], in.y)
			}
			in.stack = in.stack[:0]
			i++
		case b == 4: // vmoveto
			in.maybeWidth(1)
			if len(in.stack) >= 1 {
				in.moveTo(in.x, in.y+in.stack[0])
			}
			in.stack = in.stack[:0]
			i++
		case b == 5: // rlineto
			for j := 0; j+1 < len(in.stack); j += 2 {
				in.lineTo(in.x+in.stack[j], in.y+in.stack[j+1])
			}
			in.stack = in.stack[:0]
			i++
		case b == 6: // hlineto
			in.alternatingLineTo(true)
			i++
		case b == 7: // vlineto
			in.alternatingLineTo(false)
			i++
		case b == 8: // rrcurveto
			for j := 0; j+5 < len(in.stack); j += 6 {
				a := in.stack[j:]
				in.curveTo(in.x+a[0], in.y+a[1], in.x+a[0]+a[2], in.y+a[1]+a[3], in.x+a[0]+a[2]+a[4], in.y+a[1]+a[3]+a[5])
			}
			in.stack = in.stack[:0]
			i++
		case b == 24: // rcurveline
			j := 0
			for ; j+5 < len(in.stack)-2; j += 6 {
				a := in.stack[j:]
				in.curveTo(in.x+a[0], in.y+a[1], in.x+a[0]+a[2], in.y+a[1]+a[3], in.x+a[0]+a[2]+a[4], in.y+a[1]+a[3]+a[5])
			}
			if j+1 < len(in.stack) {
				in.lineTo(in.x+in.stack[j], in.y+in.stack[j+1])
			}
			in.stack = in.stack[:0]
			i++
		case b == 25: // rlinecurve
			j := 0
			for ; j+1 < len(in.stack)-6; j += 2 {
				in.lineTo(in.x+in.stack[j], in.y+in.stack[j+1])
			}
			if j+5 < len(in.stack) {
				a := in.stack[j:]
				in.curveTo(in.x+a[0], in.y+a[1], in.x+a[0]+a[2], in.y+a[1]+a[3], in.x+a[0]+a[2]+a[4], in.y+a[1]+a[3]+a[5])
			}
			in.stack = in.stack[:0]
			i++
		case b == 26: // vvcurveto
			in.vvCurveTo()
			i++
		case b == 27: // hhcurveto
			in.hhCurveTo()
			i++
		case b == 30: // vhcurveto
			in.vhhvCurveTo(false)
			i++
		case b == 31: // hvcurveto
			in.vhhvCurveTo(true)
			i++
		case b == 14: // endchar
			in.maybeWidth(0)
			in.stack = in.stack[:0]
			return true
		default:
			in.stack = in.stack[:0]
			i++
		}
	}
	return false
}

// flex draws the two curves of a flex escape operator (12 34-37), whose
// operands are relative to the current point like rrcurveto's. The flex depth
// argument only matters for hinting, so the curves are always drawn.
func (in *type2Interp) flex(op byte) {
	a := in.stack
	var d [12]float64 // dx1 dy1 ... dx6 dy6
	switch op {
	case 35: // flex: dx1 dy1 ... dx6 dy6 fd
		if len(a) < 12 {
			return
		}
		copy(d[:], a[:12])
	case 34: // hflex: dx1 dx2 dy2 dx3 dx4 dx5 dx6
		if len(a) < 7 {
			return
		}
		d = [12]float64{a[0], 0, a[1], a[2], a[3], 0, a[4], 0, a[5], -a[2], a[6], 0}
	case 36: // hflex1: dx1 dy1 dx2 dy2 dx3 dx4 dx5 dy5 dx6
		if len(a) < 9 {
			return
		}
		d = [12]float64{a[0], a[1], a[2], a[3], a[4], 0, a[5], 0, a[6], a[7], a[8], -(a[1] + a[3] + a[7])}
	case 37: // flex1: dx1 dy1 ... dx5 dy5 d6
		if len(a) < 11 {
			return
		}
		copy(d[:10], a[:10])
		var dx, dy float64
		for j := 0; j < 10; j += 2 {
			dx += a[j]
			dy += a[j+1]
		}
		if math.Abs(dx) > math.Abs(dy) {
			d[10], d[11] = a[10], -dy
		} else {
			d[10], d[11] = -dx, a[10]
		}
	default:
		return
	}
	for j := 0; j < 12; j += 6 {
		in.curveTo(in.x+d[j], in.y+d[j+1], in.x+d[j]+d[j+2], in.y+d[j+1]+d[j+3], in.x+d[j]+d[j+2]+d[j+4], in.y+d[j+1]+d[j+3]+d[j+5])
	}
}

// alternatingLineTo implements hlineto/vlineto, which alternate the axis of
// each successive line segment, starting horizontal (hlineto) or vertical.
func (in *type2Interp) alternatingLineTo(startHorizontal bool) {
	horiz := startHorizontal
	for _, v := range in.stack {
		if horiz {
			in.lineTo(in.x+v, in.y)
		} else {
			in.lineTo(in.x, in.y+v)
		}
		horiz = !horiz
	}
	in.stack = in.stack[:0]
}

// vvCurveTo implements vvcurveto: a series of curves mostly-vertical, with
// an optional leading dx1 on the first curve only.
func (in *type2Interp) vvCurveTo() {
	a := in.stack
	dx1 := 0.0
	j := 0
	if len(a)%4 == 1 {
		dx1 = a[0]
		j = 1
	}
	for ; j+3 < len(a); j += 4 {
		x1, y1 := in.x+dx1, in.y+a[j]
		x2, y2 := x1+a[j+1], y1+a[j+2]
		x3, y3 := x2, y2+a[j+3]
		in.curveTo(x1, y1, x2, y2, x3, y3)
		dx1 = 0
	}
	in.stack = in.stack[:0]
}

// hhCurveTo implements hhcurveto: the horizontal counterpart of vvcurveto.
func (in *type2Interp) hhCurveTo() {
	a := in.stack
	dy1 := 0.0
	j := 0
	if len(a)%4 == 1 {
		dy1 = a[0]
		j = 1
	}
	for ; j+3 < len(a); j += 4 {
		x1, y1 := in.x+a[j], in.y+dy1
		x2, y2 := x1+a[j+1], y1+a[j+2]
		x3, y3 := x2+a[j+3], y2
		in.curveTo(x1, y1, x2, y2, x3, y3)
		dy1 = 0
	}
	in.stack = in.stack[:0]
}

// vhhvCurveTo implements vhcurveto/hvcurveto, which alternate the tangent
// axis between successive curve segments.
func (in *type2Interp) vhhvCurveTo(startHorizontal bool) {
	a := in.stack
	horiz := startHorizontal
	j := 0
	for j+3 < len(a) {
		last := j+4 >= len(a)-1
		if horiz {
			x1, y1 := in.x+a[j], in.y
			x2, y2 := x1+a[j+1], y1+a[j+2]
			y3 := y2 + a[j+3]
			x3 := x2
			if last && j+4 < len(a) {
				x3 = x2 + a[j+4]
			}
			in.curveTo(x1, y1, x2, y2, x3, y3)
		} else {
			x1, y1 := in.x, in.y+a[j]
			x2, y2 := x1+a[j+1], y1+a[j+2]
			x3 := x2 + a[j+3]
			y3 := y2
			if last && j+4 < len(a) {
				y3 = y2 + a[j+4]
			}
			in.curveTo(x1, y1, x2, y2, x3, y3)
		}
		horiz = !horiz
		j += 4
	}
	in.stack = in.stack[:0]
}

// readType2Number decodes a single Type2 charstring numeric operand,
// returning its value and the number of bytes consumed.
func readType2Number(b []byte) (float64, int) {
	v := b[0]
	switch {
	case v >= 32 && v <= 246:
		return float64(int(v) - 139), 1
	case v >= 247 && v <= 250:
		if len(b) < 2 {
			return 0, 1
		}
		return float64((int(v)-247)*256 + int(b[1]) + 108), 2
	case v >= 251 && v <= 254:
		if len(b) < 2 {
			return 0, 1
		}
		return float64(-(int(v)-251)*256 - int(b[1]) - 108), 2
	case v == 28:
		if len(b) < 3 {
			return 0, len(b)
		}
		return float64(int16(binary.BigEndian.Uint16(b[1:3]))), 3
	case v == 255:
		if len(b) < 5 {
			return 0, len(b)
		}
		// 16.16 fixed point.
		fixed := int32(binary.BigEndian.Uint32(b[1:5]))
		return float64(fixed) / 65536.0, 5
	}
	return 0, 1
}

// type1Program is a Type1 font program decrypted once per font: its glyphs'
// charstrings by name and the Subrs they call.
type type1Program struct {
	glyphs map[string][]byte
	subrs  [][]byte
}

func parseType1Program(fontData []byte) (*type1Program, bool) {
	glyphs, subrs, ok := fontprog.Type1Charstrings(fontData)
	if !ok {
		return nil, false
	}
	return &type1Program{glyphs: glyphs, subrs: subrs}, true
}

// outline returns the named glyph's outline.
func (p *type1Program) outline(name string) (GlyphPath, bool) {
	cs, ok := p.glyphs[name]
	if !ok {
		return GlyphPath{}, false
	}
	in := &type1Interp{prog: p}
	in.run(cs, 0)
	in.closeContour()
	return GlyphPath{Contours: in.contours}, true
}

// type1Interp holds the running state of a Type1 charstring interpreter.
type type1Interp struct {
	stack    []float64
	x, y     float64
	sbx      float64
	contours [][]Point
	cur      []Point

	// prog supplies Subrs and, for seac, the base and accent glyphs; nil
	// interprets a lone charstring.
	prog *type1Program
	// ox, oy offset the glyph: seac places its accent this way.
	ox, oy float64
	// inSeac stops a seac component from composing another.
	inSeac bool
	// ps is the PostScript operand stack callothersubr leaves results on
	// for pop.
	ps []float64
	// flexing collects the flex points OtherSubrs 1 starts and 0 draws:
	// inside a flex, rmoveto records a point instead of moving.
	flexing   bool
	flexStart Point
	flexPts   []Point
}

// maxType1SubrDepth bounds callsubr nesting, so subrs calling each other in
// a cycle cannot spin.
const maxType1SubrDepth = 10

// interpretType1Charstring runs a (decrypted) Type1 charstring that calls no
// subroutines and returns its outline as flattened polygon contours in
// glyph-space units.
func interpretType1Charstring(cs []byte) [][]Point {
	in := &type1Interp{}
	in.run(cs, 0)
	in.closeContour()
	return in.contours
}

func (in *type1Interp) closeContour() {
	if len(in.cur) > 1 {
		in.contours = append(in.contours, in.cur)
	}
	in.cur = nil
}

func (in *type1Interp) moveTo(x, y float64) {
	if in.flexing {
		in.x, in.y = x, y
		in.flexPts = append(in.flexPts, Point{x, y})
		return
	}
	in.closeContour()
	in.x, in.y = x, y
	in.cur = []Point{{x, y}}
}

func (in *type1Interp) lineTo(x, y float64) {
	in.x, in.y = x, y
	in.cur = append(in.cur, Point{x, y})
}

func (in *type1Interp) curveTo(x1, y1, x2, y2, x3, y3 float64) {
	p0 := Point{in.x, in.y}
	pts := flattenCubic(p0, Point{x1, y1}, Point{x2, y2}, Point{x3, y3}, 1.0)
	in.cur = append(in.cur, pts...)
	in.x, in.y = x3, y3
}

// pop removes and returns the top n operands, oldest first, or nil when the
// stack holds fewer.
func (in *type1Interp) pop(n int) []float64 {
	if len(in.stack) < n {
		return nil
	}
	args := in.stack[len(in.stack)-n:]
	in.stack = in.stack[:len(in.stack)-n]
	return args
}

// run interprets cs, depth subroutine calls deep. It reports whether the
// glyph ended (endchar, seac, or a call that could not be followed) as opposed
// to cs returning to its caller.
func (in *type1Interp) run(cs []byte, depth int) (ended bool) {
	i := 0
	for i < len(cs) {
		b := cs[i]
		switch {
		case b >= 32:
			v, n := readType1Number(cs[i:])
			in.stack = append(in.stack, v)
			i += n
		case b == 1, b == 3: // hstem/vstem
			in.stack = in.stack[:0]
			i++
		case b == 4: // vmoveto
			if len(in.stack) >= 1 {
				in.moveTo(in.x, in.y+in.stack[0])
			}
			in.stack = in.stack[:0]
			i++
		case b == 5: // rlineto
			if len(in.stack) >= 2 {
				in.lineTo(in.x+in.stack[0], in.y+in.stack[1])
			}
			in.stack = in.stack[:0]
			i++
		case b == 6: // hlineto
			if len(in.stack) >= 1 {
				in.lineTo(in.x+in.stack[0], in.y)
			}
			in.stack = in.stack[:0]
			i++
		case b == 7: // vlineto
			if len(in.stack) >= 1 {
				in.lineTo(in.x, in.y+in.stack[0])
			}
			in.stack = in.stack[:0]
			i++
		case b == 8: // rrcurveto
			if len(in.stack) >= 6 {
				a := in.stack
				in.curveTo(in.x+a[0], in.y+a[1], in.x+a[0]+a[2], in.y+a[1]+a[3], in.x+a[0]+a[2]+a[4], in.y+a[1]+a[3]+a[5])
			}
			in.stack = in.stack[:0]
			i++
		case b == 9: // closepath
			in.stack = in.stack[:0]
			i++
		case b == 10: // callsubr
			a := in.pop(1)
			if a == nil || in.prog == nil || depth >= maxType1SubrDepth {
				return true
			}
			n := int(a[0])
			if n < 0 || n >= len(in.prog.subrs) || in.prog.subrs[n] == nil {
				return true
			}
			if in.run(in.prog.subrs[n], depth+1) {
				return true
			}
			i++
		case b == 11: // return
			return false
		case b == 13: // hsbw: sbx wx
			if len(in.stack) >= 2 {
				in.sbx = in.stack[0]
				in.x, in.y = in.ox+in.sbx, in.oy
			}
			in.stack = in.stack[:0]
			i++
		case b == 21: // rmoveto
			if len(in.stack) >= 2 {
				in.moveTo(in.x+in.stack[0], in.y+in.stack[1])
			}
			in.stack = in.stack[:0]
			i++
		case b == 22: // hmoveto
			if len(in.stack) >= 1 {
				in.moveTo(in.x+in.stack[0], in.y)
			}
			in.stack = in.stack[:0]
			i++
		case b == 30: // vhcurveto
			if len(in.stack) >= 4 {
				a := in.stack
				in.curveTo(in.x, in.y+a[0], in.x+a[1], in.y+a[0]+a[2], in.x+a[1]+a[3], in.y+a[0]+a[2])
			}
			in.stack = in.stack[:0]
			i++
		case b == 31: // hvcurveto
			if len(in.stack) >= 4 {
				a := in.stack
				in.curveTo(in.x+a[0], in.y, in.x+a[0]+a[1], in.y+a[2], in.x+a[0]+a[1], in.y+a[2]+a[3])
			}
			in.stack = in.stack[:0]
			i++
		case b == 12: // two-byte escape
			i++
			if i >= len(cs) {
				return true
			}
			esc := cs[i]
			i++
			if in.escape(esc) {
				return true
			}
		case b == 14: // endchar
			in.stack = in.stack[:0]
			return true
		default:
			in.stack = in.stack[:0]
			i++
		}
	}
	return false
}

// escape runs a two-byte escape operator and reports whether it ended the
// glyph.
func (in *type1Interp) escape(esc byte) (ended bool) {
	switch esc {
	case 12: // div
		if a := in.pop(2); a != nil {
			if a[1] != 0 {
				in.stack = append(in.stack, a[0]/a[1])
			}
		}
		return false
	case 6: // seac: asb adx ady bchar achar
		a := in.pop(5)
		in.stack = in.stack[:0]
		if a != nil {
			in.seac(a[0], a[1], a[2], int(a[3]), int(a[4]))
		}
		return true
	case 7: // sbw: sbx sby wx wy
		if len(in.stack) >= 4 {
			in.sbx = in.stack[0]
			in.x, in.y = in.ox+in.sbx, in.oy+in.stack[1]
		}
	case 16: // callothersubr: arg1 ... argn n othersubr#
		in.callOtherSubr()
		return false
	case 17: // pop: move a callothersubr result onto the stack
		v := 0.0
		if n := len(in.ps); n > 0 {
			v, in.ps = in.ps[n-1], in.ps[:n-1]
		}
		in.stack = append(in.stack, v)
		return false
	case 33: // setcurrentpoint: x y
		if a := in.pop(2); a != nil {
			in.x, in.y = a[0], a[1]
		}
	}
	in.stack = in.stack[:0]
	return false
}

// callOtherSubr runs the OtherSubrs every Type1 program defines the same
// way: 1 starts a flex, 2 marks one of its points, 0 draws it, and 3 (hint
// replacement) hands its subr number back for pop. Any other leaves its
// arguments for pop, as an unknown PostScript procedure would be assumed to.
func (in *type1Interp) callOtherSubr() {
	head := in.pop(2)
	if head == nil {
		in.stack = in.stack[:0]
		return
	}
	n, which := int(head[0]), int(head[1])
	args := in.pop(max(n, 0))
	in.stack = in.stack[:0]
	switch which {
	case 1:
		in.flexing, in.flexStart, in.flexPts = true, Point{in.x, in.y}, nil
		return
	case 2:
		return
	case 0:
		// The first of the seven points is the flex's reference point, which
		// only matters for hinting; the other six are the two curves' control
		// and end points, drawn from where the flex started.
		if in.flexing && len(in.flexPts) == 7 {
			p := in.flexPts
			in.x, in.y = in.flexStart.X, in.flexStart.Y
			in.curveTo(p[1].X, p[1].Y, p[2].X, p[2].Y, p[3].X, p[3].Y)
			in.curveTo(p[4].X, p[4].Y, p[5].X, p[5].Y, p[6].X, p[6].Y)
		}
		in.flexing = false
		// pop pop setcurrentpoint follows, taking the end point x then y.
		if len(args) == 3 {
			in.ps = append(in.ps, args[2], args[1])
		}
		return
	}
	for j := len(args) - 1; j >= 0; j-- {
		in.ps = append(in.ps, args[j])
	}
}

// seac composes a standard accented character: the base glyph bchar at the
// origin and the accent achar offset by (adx-asb, ady), both named through
// StandardEncoding.
func (in *type1Interp) seac(asb, adx, ady float64, bchar, achar int) {
	if in.prog == nil || in.inSeac || bchar < 0 || bchar > 255 || achar < 0 || achar > 255 {
		return
	}
	in.closeContour()
	for _, part := range []struct {
		code   int
		ox, oy float64
	}{{bchar, in.ox, in.oy}, {achar, in.ox + adx - asb, in.oy + ady}} {
		cs, ok := in.prog.glyphs[fontprog.StandardEncoding[part.code]]
		if !ok {
			continue
		}
		sub := &type1Interp{prog: in.prog, ox: part.ox, oy: part.oy, inSeac: true}
		sub.x, sub.y = part.ox, part.oy
		sub.run(cs, 0)
		sub.closeContour()
		in.contours = append(in.contours, sub.contours...)
	}
}

// readType1Number decodes a single Type1 charstring numeric operand.
func readType1Number(b []byte) (float64, int) {
	v := b[0]
	switch {
	case v >= 32 && v <= 246:
		return float64(int(v) - 139), 1
	case v >= 247 && v <= 250:
		if len(b) < 2 {
			return 0, 1
		}
		return float64((int(v)-247)*256 + int(b[1]) + 108), 2
	case v >= 251 && v <= 254:
		if len(b) < 2 {
			return 0, 1
		}
		return float64(-(int(v)-251)*256 - int(b[1]) - 108), 2
	case v == 255:
		if len(b) < 5 {
			return 0, len(b)
		}
		fixed := int32(binary.BigEndian.Uint32(b[1:5]))
		return float64(fixed), 5
	}
	return 0, 1
}
