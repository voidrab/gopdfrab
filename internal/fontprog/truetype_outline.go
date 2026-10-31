package fontprog

import "encoding/binary"

// maxCompositeDepth bounds how deeply composite glyphs may nest, so a glyph
// that includes itself cannot recurse without end.
const maxCompositeDepth = 8

// ttPoint is a glyf outline point in font units.
type ttPoint struct {
	x, y float64
	on   bool // on the curve, rather than a quadratic control point
}

// UnitsPerEm returns the em size the head table declares. It reports false
// when the table is missing or too short, or when it declares zero.
func UnitsPerEm(tables map[string][]byte) (int, bool) {
	head := tables["head"]
	if len(head) < 20 {
		return 0, false
	}
	upm := int(binary.BigEndian.Uint16(head[18:20]))
	return upm, upm != 0
}

// TrueTypeOutline draws glyph gid of a parsed sfnt into sink, in font units,
// with each quadratic segment drawn as the cubic curve equal to it. It
// reports whether the glyph decoded; when it did not, nothing is drawn. A
// glyph without a record, such as a space, draws nothing and reports true.
func TrueTypeOutline(tables map[string][]byte, gid int, sink PathSink) bool {
	contours, ok := ttGlyphContours(tables, gid, 0)
	if !ok {
		return false
	}
	for _, c := range contours {
		drawTTContour(c, sink)
	}
	return true
}

// ttGlyphContours returns the contours of glyph gid, following composite
// components recursively. It reports false when nesting exceeds
// maxCompositeDepth or a record is malformed.
func ttGlyphContours(tables map[string][]byte, gid, depth int) ([][]ttPoint, bool) {
	if depth > maxCompositeDepth {
		return nil, false
	}
	rec := GlyfRecord(tables, gid)
	if len(rec) == 0 {
		return nil, true
	}
	if GlyfIsComposite(rec) {
		return ttCompositeContours(tables, rec, depth)
	}
	return ttSimpleContours(rec)
}

// ttSimpleContours decodes the contours of a simple glyph record: their end
// points, flags, and delta-encoded coordinates. It reports false when the
// record is truncated or its end points decrease.
func ttSimpleContours(rec []byte) ([][]ttPoint, bool) {
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
		// The endpoints must not decrease. The point arrays are sized from the
		// last one but indexed by all of them.
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

	pts := make([]ttPoint, numPts)
	// readCoords decodes one axis: short is the flag bit of a one-byte delta,
	// and same the bit that gives its sign, or marks a repeated coordinate.
	readCoords := func(short, same byte, set func(p *ttPoint, v float64)) bool {
		v := 0
		for i, f := range flags {
			switch {
			case f&short != 0:
				if off >= len(rec) {
					return false
				}
				d := int(rec[off])
				off++
				if f&same == 0 { // sign bit clear means negative
					d = -d
				}
				v += d
			case f&same == 0: // a signed 16-bit delta
				if off+2 > len(rec) {
					return false
				}
				v += int(int16(binary.BigEndian.Uint16(rec[off:])))
				off += 2
			}
			set(&pts[i], float64(v))
		}
		return true
	}
	if !readCoords(0x02, 0x10, func(p *ttPoint, v float64) { p.x = v }) ||
		!readCoords(0x04, 0x20, func(p *ttPoint, v float64) { p.y = v }) {
		return nil, false
	}
	for i, f := range flags {
		pts[i].on = f&0x01 != 0
	}

	contours := make([][]ttPoint, 0, numContours)
	start := 0
	for _, end := range endPts {
		contours = append(contours, pts[start:end+1])
		start = end + 1
	}
	return contours, true
}

// ttCompositeContours gathers a composite glyph's components, each placed
// by its offset and scale. It reports false when a component record is
// truncated or a component cannot be decoded.
func ttCompositeContours(tables map[string][]byte, rec []byte, depth int) ([][]ttPoint, bool) {
	var contours [][]ttPoint
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

		a, b, c, d := 1.0, 0.0, 0.0, 1.0
		f2dot14 := func(at int) float64 { return float64(int16(binary.BigEndian.Uint16(rec[at:]))) / 16384.0 }
		switch {
		case flags&0x0008 != 0: // WE_HAVE_A_SCALE
			if off+2 > len(rec) {
				return nil, false
			}
			a = f2dot14(off)
			d = a
			off += 2
		case flags&0x0040 != 0: // WE_HAVE_AN_X_AND_Y_SCALE
			if off+4 > len(rec) {
				return nil, false
			}
			a, d = f2dot14(off), f2dot14(off+2)
			off += 4
		case flags&0x0080 != 0: // WE_HAVE_A_TWO_BY_TWO
			if off+8 > len(rec) {
				return nil, false
			}
			a, b, c, d = f2dot14(off), f2dot14(off+2), f2dot14(off+4), f2dot14(off+6)
			off += 8
		}

		sub, ok := ttGlyphContours(tables, gid, depth+1)
		if !ok {
			return nil, false
		}
		for _, sc := range sub {
			tc := make([]ttPoint, len(sc))
			for i, p := range sc {
				tc[i] = ttPoint{a*p.x + c*p.y + dx, b*p.x + d*p.y + dy, p.on}
			}
			contours = append(contours, tc)
		}
		if flags&0x0020 == 0 { // no MORE_COMPONENTS
			break
		}
	}
	return contours, true
}

// drawTTContour draws one closed contour of on- and off-curve points. Two
// off-curve points in a row imply an on-curve point midway between them; a
// contour with no on-curve point starts at the midpoint of its last and first
// points.
func drawTTContour(pts []ttPoint, sink PathSink) {
	n := len(pts)
	if n == 0 {
		return
	}
	mid := func(p, q ttPoint) ttPoint { return ttPoint{(p.x + q.x) / 2, (p.y + q.y) / 2, true} }

	// The walk visits the n points after start, wrapping round, so it ends
	// on start when start is a point of the contour.
	first := 0
	for first < n && !pts[first].on {
		first++
	}
	var start ttPoint
	if first == n {
		start, first = mid(pts[n-1], pts[0]), 0
	} else {
		start, first = pts[first], first+1
	}
	sink.MoveTo(start.x, start.y)

	cur := start
	// quadTo draws the quadratic from cur through control point ctrl to p as
	// the cubic equal to it.
	quadTo := func(ctrl, p ttPoint) {
		sink.CurveTo(cur.x+2.0/3.0*(ctrl.x-cur.x), cur.y+2.0/3.0*(ctrl.y-cur.y),
			p.x+2.0/3.0*(ctrl.x-p.x), p.y+2.0/3.0*(ctrl.y-p.y), p.x, p.y)
		cur = p
	}
	var ctrl *ttPoint
	for k := 0; k < n; k++ {
		p := pts[(first+k)%n]
		switch {
		case p.on && ctrl != nil:
			quadTo(*ctrl, p)
			ctrl = nil
		case p.on:
			sink.LineTo(p.x, p.y)
			cur = p
		default:
			if ctrl != nil {
				quadTo(*ctrl, mid(*ctrl, p))
			}
			ctrl = &p
		}
	}
	if ctrl != nil {
		quadTo(*ctrl, start)
	}
}
