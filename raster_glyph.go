package pdfrab

import (
	"encoding/binary"
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
	rec := glyfRecord(tables, gid)
	if len(rec) == 0 {
		return nil, true // empty outline, e.g. space -- not an error
	}
	if glyfIsComposite(rec) {
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

// glyphOutlineFromCFF extracts gid's outline from a bare CFF table's
// CharStrings INDEX, interpreting the Type2 charstring moveto/lineto/curveto
// subset (hstem/vstem/hintmask hints are parsed and discarded).
func glyphOutlineFromCFF(cff []byte, gid int) (GlyphPath, bool) {
	td, ok := parseCFFTopDict(cff)
	if !ok || td.csOffset < 0 {
		return GlyphPath{}, false
	}
	entries, _ := parseCFFIndex(cff, td.csOffset)
	if gid < 0 || gid >= len(entries) {
		return GlyphPath{}, false
	}
	contours := interpretType2Charstring(entries[gid])
	return GlyphPath{Contours: contours}, true
}

// type2Interp holds the running state of a Type2 charstring interpreter.
type type2Interp struct {
	stack       []float64
	x, y        float64
	contours    [][]Point
	cur         []Point
	nStems      int
	widthParsed bool
}

// interpretType2Charstring runs a CFF Type2 charstring and returns its
// outline as flattened polygon contours in glyph-space units.
func interpretType2Charstring(cs []byte) [][]Point {
	in := &type2Interp{}
	in.run(cs)
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

func (in *type2Interp) run(cs []byte) {
	i := 0
	for i < len(cs) {
		b := cs[i]
		switch {
		case b >= 32 || b == 28:
			v, n := readType2Number(cs[i:])
			in.stack = append(in.stack, v)
			i += n
		case b == 12: // two-byte escape operators -- none affect outline geometry
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
			i++
		default:
			in.stack = in.stack[:0]
			i++
		}
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

// glyphOutlineFromType1 extracts a named glyph's outline from a decrypted
// Type1 font program, interpreting the Type1 charstring moveto/lineto/curveto
// subset (hint operators are parsed and discarded).
func glyphOutlineFromType1(fontData []byte, glyphName string) (GlyphPath, bool) {
	binStart := type1EexecBinStart(fontData)
	cs := type1CharStringsSection(fontData, binStart)
	if cs == nil {
		return GlyphPath{}, false
	}
	for _, m := range type1CharStringRe.FindAllSubmatchIndex(cs, -1) {
		name := string(cs[m[2]:m[3]])
		if name != glyphName {
			continue
		}
		n := atoiSafe(string(cs[m[4]:m[5]]))
		if n <= 0 || m[1]+n > len(cs) {
			return GlyphPath{}, false
		}
		dec := decryptType1Block(cs[m[1]:m[1]+n], 4330)
		contours := interpretType1Charstring(dec)
		return GlyphPath{Contours: contours}, true
	}
	return GlyphPath{}, false
}

func atoiSafe(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return -1
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// type1Interp holds the running state of a Type1 charstring interpreter.
type type1Interp struct {
	stack    []float64
	x, y     float64
	sbx      float64
	contours [][]Point
	cur      []Point
}

// interpretType1Charstring runs a (decrypted) Type1 charstring and returns
// its outline as flattened polygon contours in glyph-space units.
func interpretType1Charstring(cs []byte) [][]Point {
	in := &type1Interp{}
	in.run(cs)
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

func (in *type1Interp) run(cs []byte) {
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
		case b == 10: // callsubr -- subroutines (incl. flex/hints) aren't resolvable
			// without the Subrs array; drop the subr number and continue.
			if len(in.stack) > 0 {
				in.stack = in.stack[:len(in.stack)-1]
			}
			i++
		case b == 11: // return
			i++
		case b == 13: // hsbw: sbx wx
			if len(in.stack) >= 2 {
				in.sbx = in.stack[0]
				in.x, in.y = in.sbx, 0
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
				return
			}
			esc := cs[i]
			i++
			switch esc {
			case 12: // div
				if len(in.stack) >= 2 {
					a := in.stack[:len(in.stack)-2]
					num, den := in.stack[len(in.stack)-2], in.stack[len(in.stack)-1]
					if den != 0 {
						in.stack = append(a, num/den)
					} else {
						in.stack = a
					}
				}
			case 6: // seac (accented char) -- composition unsupported, skip
				in.stack = in.stack[:0]
			case 7: // sbw
				if len(in.stack) >= 4 {
					in.sbx = in.stack[0]
					in.x, in.y = in.sbx, in.stack[1]
				}
				in.stack = in.stack[:0]
			default:
				in.stack = in.stack[:0]
			}
		case b == 14: // endchar
			in.stack = in.stack[:0]
			return
		default:
			in.stack = in.stack[:0]
			i++
		}
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
