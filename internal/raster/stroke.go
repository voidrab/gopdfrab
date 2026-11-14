package raster

import (
	"math"

	"github.com/voidrab/gopdfrab/internal/content"
)

// LineCap is how an open subpath's ends are finished.
type LineCap int

// The line caps of ISO 32000-1 8.4.3.3.
const (
	CapButt LineCap = iota
	CapRound
	CapSquare
)

// LineJoin is how two segments of a subpath are joined.
type LineJoin int

// The line joins of ISO 32000-1 8.4.3.4.
const (
	JoinMiter LineJoin = iota
	JoinRound
	JoinBevel
)

// StrokeStyle describes a stroke in device space.
type StrokeStyle struct {
	// Width is the line width in pixels; anything under one pixel is drawn
	// one pixel wide, so a hairline never vanishes.
	Width float64
	Cap   LineCap
	Join  LineJoin
	// MiterLimit bounds the ratio of a miter's length to the line width; a
	// sharper join is bevelled. Values under 1 mean the default of 10.
	MiterLimit float64
	// Dash lists alternating on and off lengths in pixels, repeated as
	// needed; empty, all-zero or negative lists stroke solid. DashPhase is
	// how far into the pattern the stroke starts.
	Dash      []float64
	DashPhase float64
}

// maxDashes bounds the dashes of one subpath, past which it is stroked solid:
// at that density the dashes are finer than a pixel anyway.
const maxDashes = 20000

// strokeOutline returns polygons whose nonzero union is the stroke of
// contours. All polygons wind the same way, so overlapping pieces (segments,
// joins, caps) merge instead of cancelling.
func strokeOutline(contours [][]content.Point, st StrokeStyle) [][]content.Point {
	st.Width = math.Max(st.Width, 1)
	if st.MiterLimit < 1 {
		st.MiterLimit = 10
	}
	var pieces [][]content.Point
	for _, c := range contours {
		pts, closed := cleanContour(c)
		for _, pl := range dashed(pts, closed, st) {
			// A zero-length dash is two equal points; it draws as a dot.
			pieces = strokePolyline(pieces, dedupe(pl.pts), pl.closed, st)
		}
	}
	out := pieces[:0]
	for _, p := range pieces {
		if oriented(p) {
			out = append(out, p)
		}
	}
	return out
}

// dedupe drops non-finite points and consecutive duplicates.
func dedupe(c []content.Point) []content.Point {
	pts := make([]content.Point, 0, len(c))
	for _, p := range c {
		if math.IsNaN(p.X) || math.IsNaN(p.Y) || math.IsInf(p.X, 0) || math.IsInf(p.Y, 0) {
			continue
		}
		if n := len(pts); n > 0 && math.Hypot(p.X-pts[n-1].X, p.Y-pts[n-1].Y) < 1e-9 {
			continue
		}
		pts = append(pts, p)
	}
	return pts
}

// cleanContour dedupes a contour and reports whether it closes on its first
// point, which the path builder repeats at the end.
func cleanContour(c []content.Point) ([]content.Point, bool) {
	pts := dedupe(c)
	if n := len(pts); n >= 3 && math.Hypot(pts[0].X-pts[n-1].X, pts[0].Y-pts[n-1].Y) < 1e-9 {
		return pts[:n-1], true
	}
	return pts, false
}

// polyline is one run of a subpath after dashing.
type polyline struct {
	pts    []content.Point
	closed bool
}

// dashed splits a subpath by the style's dash pattern. A solid style returns
// the subpath whole.
func dashed(pts []content.Point, closed bool, st StrokeStyle) []polyline {
	whole := []polyline{{pts, closed}}
	n := len(st.Dash)
	if n == 0 || len(pts) < 2 {
		return whole
	}
	var sum float64
	for _, d := range st.Dash {
		if d < 0 || math.IsNaN(d) || math.IsInf(d, 0) {
			return whole
		}
		sum += d
	}
	if sum <= 0 {
		return whole
	}
	cycle := n // an odd list repeats with on and off swapped, so a cycle is twice as long
	period := sum
	if n%2 == 1 {
		cycle, period = 2*n, 2*sum
	}

	path := pts
	if closed {
		path = append(append([]content.Point(nil), pts...), pts[0])
	}
	var length float64
	for i := 1; i < len(path); i++ {
		length += math.Hypot(path[i].X-path[i-1].X, path[i].Y-path[i-1].Y)
	}
	if length/period*float64(cycle) > maxDashes {
		return whole
	}

	phase := math.Mod(st.DashPhase, period)
	if phase < 0 {
		phase += period
	}
	idx := 0
	for phase > 0 && phase >= st.Dash[idx%n] {
		phase -= st.Dash[idx%n]
		idx++
	}
	left := st.Dash[idx%n] - phase

	var out []polyline
	var cur []content.Point
	for i := 1; i < len(path); i++ {
		a, b := path[i-1], path[i]
		segLen := math.Hypot(b.X-a.X, b.Y-a.Y)
		at := func(d float64) content.Point {
			t := d / segLen
			return content.Point{X: a.X + (b.X-a.X)*t, Y: a.Y + (b.Y-a.Y)*t}
		}
		for pos := 0.0; pos < segLen; {
			step := math.Min(left, segLen-pos)
			on := idx%2 == 0
			if on && cur == nil {
				cur = []content.Point{at(pos)}
			}
			pos += step
			left -= step
			if on {
				cur = append(cur, at(pos))
			}
			if left <= 1e-12 {
				if on {
					out = append(out, polyline{cur, false})
					cur = nil
				}
				idx++
				left = st.Dash[idx%n]
			}
		}
	}
	if cur != nil {
		out = append(out, polyline{cur, false})
	}
	return out
}

// vec is a 2-D vector.
type vec = content.Point

func sub(a, b vec) vec           { return vec{X: a.X - b.X, Y: a.Y - b.Y} }
func add(a, b vec) vec           { return vec{X: a.X + b.X, Y: a.Y + b.Y} }
func scale(a vec, f float64) vec { return vec{X: a.X * f, Y: a.Y * f} }
func dot(a, b vec) float64       { return a.X*b.X + a.Y*b.Y }
func cross(a, b vec) float64     { return a.X*b.Y - a.Y*b.X }

// leftNormal is v rotated a quarter turn.
func leftNormal(v vec) vec { return vec{X: -v.Y, Y: v.X} }

// unit returns the direction from a to b.
func unit(a, b vec) vec {
	d := sub(b, a)
	return scale(d, 1/math.Hypot(d.X, d.Y))
}

// strokePolyline appends the pieces that make up the stroke of one polyline
// to pieces.
func strokePolyline(pieces [][]content.Point, pts []content.Point, closed bool, st StrokeStyle) [][]content.Point {
	h := st.Width / 2
	n := len(pts)
	if n < 2 {
		if n == 1 {
			pieces = appendDot(pieces, pts[0], st)
		}
		return pieces
	}
	segs := n - 1
	if closed {
		segs = n
	}
	dirs := make([]vec, segs)
	for i := range segs {
		dirs[i] = unit(pts[i], pts[(i+1)%n])
		nm := scale(leftNormal(dirs[i]), h)
		p0, p1 := pts[i], pts[(i+1)%n]
		pieces = append(pieces, []content.Point{add(p0, nm), add(p1, nm), sub(p1, nm), sub(p0, nm)})
	}
	for i := 1; i < segs; i++ {
		pieces = appendJoin(pieces, pts[i], dirs[i-1], dirs[i], st)
	}
	if closed {
		pieces = appendJoin(pieces, pts[0], dirs[segs-1], dirs[0], st)
		return pieces
	}
	pieces = appendCap(pieces, pts[0], scale(dirs[0], -1), st)
	return appendCap(pieces, pts[n-1], dirs[segs-1], st)
}

// appendDot draws a zero-length subpath: round caps give a disc and square
// caps a square, butt caps nothing.
func appendDot(pieces [][]content.Point, p content.Point, st StrokeStyle) [][]content.Point {
	h := st.Width / 2
	switch st.Cap {
	case CapRound:
		return append(pieces, arcFan(p, vec{X: 1}, 2*math.Pi, h, false))
	case CapSquare:
		return append(pieces, []content.Point{{X: p.X - h, Y: p.Y - h}, {X: p.X + h, Y: p.Y - h}, {X: p.X + h, Y: p.Y + h}, {X: p.X - h, Y: p.Y + h}})
	}
	return pieces
}

// appendCap finishes an open end at p, where d points away from the line.
func appendCap(pieces [][]content.Point, p, d vec, st StrokeStyle) [][]content.Point {
	h := st.Width / 2
	nm := scale(leftNormal(d), h)
	switch st.Cap {
	case CapSquare:
		fwd := scale(d, h)
		return append(pieces, []content.Point{add(p, nm), add(add(p, nm), fwd), add(sub(p, nm), fwd), sub(p, nm)})
	case CapRound:
		// A half disc bulging along d: from the left normal, a quarter turn
		// clockwise reaches d.
		return append(pieces, arcFan(p, leftNormal(d), -math.Pi, h, true))
	}
	return pieces
}

// appendJoin fills the wedge between the segments arriving at p along d1 and
// leaving along d2, on the outer side of the turn.
func appendJoin(pieces [][]content.Point, p, d1, d2 vec, st StrokeStyle) [][]content.Point {
	h := st.Width / 2
	cr, dt := cross(d1, d2), dot(d1, d2)
	if math.Abs(cr) < 1e-9 && dt > 0 {
		return pieces // the segments continue straight on
	}
	side := 1.0
	if cr > 0 {
		side = -1 // turning left: the outer side is the right
	}
	o1, o2 := scale(leftNormal(d1), side), scale(leftNormal(d2), side)
	a, b := add(p, scale(o1, h)), add(p, scale(o2, h))

	switch st.Join {
	case JoinRound:
		if math.Abs(cr) < 1e-9 { // a full reversal: a half disc ahead of the line
			sweep := math.Pi
			if cross(o1, d1) < 0 {
				sweep = -math.Pi
			}
			return append(pieces, arcFan(p, o1, sweep, h, true))
		}
		return append(pieces, arcFan(p, o1, math.Atan2(cross(o1, o2), dot(o1, o2)), h, true))
	case JoinMiter:
		if 1+dt > 1e-9 && 1/math.Sqrt((1+dt)/2) <= st.MiterLimit {
			tip := add(p, scale(add(o1, o2), h/(1+dt)))
			return append(pieces, []content.Point{p, a, tip, b})
		}
	}
	return append(pieces, []content.Point{p, a, b})
}

// arcFan returns the polygon of a circular sector around centre, from the
// direction start through sweep radians. With fan false the sector is the
// whole disc's outline.
func arcFan(centre, start vec, sweep, radius float64, fan bool) []content.Point {
	// Steps are fine enough that the chord stays within 0.1 pixel of the arc.
	step := math.Pi / 4
	if radius > 0.1 {
		step = math.Min(step, 2*math.Acos(1-0.1/radius))
	}
	steps := max(1, int(math.Ceil(math.Abs(sweep)/step)))
	a0 := math.Atan2(start.Y, start.X)
	var poly []content.Point
	if fan {
		poly = append(poly, centre)
	}
	for i := 0; i <= steps; i++ {
		a := a0 + sweep*float64(i)/float64(steps)
		poly = append(poly, content.Point{X: centre.X + radius*math.Cos(a), Y: centre.Y + radius*math.Sin(a)})
	}
	return poly
}

// oriented reverses poly if needed so it winds positively, and reports
// whether it has any area at all.
func oriented(poly []content.Point) bool {
	var area float64
	for i, p := range poly {
		q := poly[(i+1)%len(poly)]
		area += p.X*q.Y - q.X*p.Y
	}
	if math.Abs(area) < 1e-12 {
		return false
	}
	if area < 0 {
		for i, j := 0, len(poly)-1; i < j; i, j = i+1, j-1 {
			poly[i], poly[j] = poly[j], poly[i]
		}
	}
	return true
}
