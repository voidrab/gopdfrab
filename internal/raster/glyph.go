package raster

import (
	"encoding/binary"

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

// contourSink flattens a charstring's outline into polygon contours.
type contourSink struct {
	contours [][]Point
	cur      []Point
	at       Point
}

// glyphPath closes the open contour and returns the outline drawn.
func (s *contourSink) glyphPath() GlyphPath {
	s.closeContour()
	return GlyphPath{Contours: s.contours}
}

func (s *contourSink) closeContour() {
	if len(s.cur) > 1 {
		s.contours = append(s.contours, s.cur)
	}
	s.cur = nil
}

func (s *contourSink) MoveTo(x, y float64) {
	s.closeContour()
	s.at = Point{x, y}
	s.cur = []Point{s.at}
}

func (s *contourSink) LineTo(x, y float64) {
	s.at = Point{x, y}
	s.cur = append(s.cur, s.at)
}

func (s *contourSink) CurveTo(x1, y1, x2, y2, x3, y3 float64) {
	p3 := Point{x3, y3}
	s.cur = append(s.cur, flattenCubic(s.at, Point{x1, y1}, Point{x2, y2}, p3, 1.0)...)
	s.at = p3
}

// cffOutline returns glyph gid's outline in prog.
func cffOutline(prog *fontprog.CFFCharstrings, gid int) (GlyphPath, bool) {
	var s contourSink
	if !prog.Outline(gid, &s) {
		return GlyphPath{}, false
	}
	return s.glyphPath(), true
}

// type1Outline returns the named glyph's outline in prog.
func type1Outline(prog *fontprog.Type1Charstrings, name string) (GlyphPath, bool) {
	var s contourSink
	if !prog.Outline(name, &s) {
		return GlyphPath{}, false
	}
	return s.glyphPath(), true
}
