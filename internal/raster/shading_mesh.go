package raster

import (
	"image"
	"math"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// Mesh shadings (types 4-7, ISO 32000-1 8.7.4.5.5-8.7.4.5.7) are geometry
// rather than a colour field: the stream holds triangle vertices or Bezier
// patches, each carrying its own colour. They are drawn by subdividing every
// primitive into small flat-filled pieces through the existing scanline
// filler, rather than by adding a Gouraud rasterizer -- a documented
// approximation appropriate to a conversion fallback, where the alternative
// is dropping the content entirely.

const (
	// meshMaxPrimitives bounds how many flat pieces one mesh may paint, so a
	// hostile or degenerate mesh cannot blow up render time. Hitting it is
	// reported as a drop rather than silently truncating the shading.
	meshMaxPrimitives = 200000
	// meshPatchGrid is how finely a Coons/tensor patch is evaluated.
	meshPatchGrid = 10
	// meshMaxSubdiv caps a triangle's per-edge subdivision.
	meshMaxSubdiv = 12
	// meshTargetEdgePx is the device-space edge length each subdivided piece
	// aims for; smaller means smoother gradients and more primitives.
	meshTargetEdgePx = 3.0
)

// meshReader reads the packed bit fields of a mesh stream, refusing any read
// that would run past the end so a truncated stream ends the walk cleanly.
// A refused read sets short: the walkers only read while bits remain, so it
// means a partial trailing record, i.e. content the mesh lost.
type meshReader struct {
	data  []byte
	bit   int
	short bool
}

func (m *meshReader) read(bits int) (uint64, bool) {
	if bits <= 0 || m.bit+bits > len(m.data)*8 {
		m.short = true
		return 0, false
	}
	v := pdf.ReadBits(m.data, m.bit, bits)
	m.bit += bits
	return v, true
}

// align advances to the next byte boundary. Types 4, 6 and 7 pad each vertex
// or patch record out to whole bytes; type 5 packs continuously.
func (m *meshReader) align() { m.bit = (m.bit + 7) &^ 7 }

func (m *meshReader) exhausted() bool { return m.bit >= len(m.data)*8 }

// decodeValue maps a raw field to its declared range, the same linear mapping
// an image sample's /Decode array applies.
func decodeValue(raw uint64, bits int, lo, hi float64) float64 {
	maxVal := float64(uint64(1)<<uint(bits) - 1)
	if maxVal == 0 {
		return lo
	}
	return lo + (float64(raw)/maxVal)*(hi-lo)
}

// meshVertex is one mesh point with its colour already resolved to RGB.
type meshVertex struct {
	p   Point
	rgb [3]float64
}

// meshPainter accumulates the flat pieces a mesh decomposes into, applying
// the primitive cap and painting each through FillPath.
type meshPainter struct {
	canvas    *image.RGBA
	toDevice  Matrix
	alpha     float64
	primitive int
	overflow  bool
}

// tri paints one triangle, subdividing it so a colour gradient across its
// vertices is approximated by several flat pieces.
func (mp *meshPainter) tri(a, b, c meshVertex) {
	if mp.overflow {
		return
	}
	da, db, dc := mp.toDevice.Apply(a.p), mp.toDevice.Apply(b.p), mp.toDevice.Apply(c.p)
	n := subdivisionFor(da, db, dc)
	// Uniform barycentric subdivision into n^2 sub-triangles: row i holds
	// 2*(n-i)-1 alternating up- and down-pointing pieces.
	for i := 0; i < n; i++ {
		for j := 0; j < n-i; j++ {
			mp.flat(a, b, c, float64(i)/float64(n), float64(j)/float64(n), 1/float64(n), false)
			if j < n-i-1 {
				mp.flat(a, b, c, float64(i)/float64(n), float64(j)/float64(n), 1/float64(n), true)
			}
		}
	}
}

// flat paints one sub-triangle of the barycentric grid at (u,v) with side d,
// flipped when it is the downward-pointing piece of the cell.
func (mp *meshPainter) flat(a, b, c meshVertex, u, v, d float64, flipped bool) {
	if mp.overflow {
		return
	}
	if mp.primitive++; mp.primitive > meshMaxPrimitives {
		mp.overflow = true
		return
	}
	corners := [3][2]float64{{u, v}, {u + d, v}, {u, v + d}}
	if flipped {
		corners = [3][2]float64{{u + d, v}, {u + d, v + d}, {u, v + d}}
	}

	pts := make([]Point, 3)
	var rgb [3]float64
	for i, uv := range corners {
		wa, wb, wc := 1-uv[0]-uv[1], uv[0], uv[1]
		p := Point{
			X: wa*a.p.X + wb*b.p.X + wc*c.p.X,
			Y: wa*a.p.Y + wb*b.p.Y + wc*c.p.Y,
		}
		pts[i] = mp.toDevice.Apply(p)
		for k := range rgb {
			rgb[k] += (wa*a.rgb[k] + wb*b.rgb[k] + wc*c.rgb[k]) / 3
		}
	}
	FillPath(mp.canvas, [][]Point{pts}, rgb, mp.alpha, false)
}

// quad paints one grid cell of a patch as two triangles.
func (mp *meshPainter) quad(p00, p10, p11, p01 meshVertex) {
	mp.tri(p00, p10, p11)
	mp.tri(p00, p11, p01)
}

// subdivisionFor picks how many pieces per edge a device-space triangle needs
// for its colour gradient to read as smooth, bounded by meshMaxSubdiv.
func subdivisionFor(a, b, c Point) int {
	longest := math.Max(math.Hypot(b.X-a.X, b.Y-a.Y),
		math.Max(math.Hypot(c.X-b.X, c.Y-b.Y), math.Hypot(a.X-c.X, a.Y-c.Y)))
	return pdf.ClampInt(int(math.Ceil(longest/meshTargetEdgePx)), 1, meshMaxSubdiv)
}

// sharedEdge returns the boundary points and two colours a flag-1/2/3 patch
// inherits from its predecessor.
func sharedEdge(flag int, pts []Point, cols [4][3]float64) ([]Point, [3]float64, [3]float64) {
	switch flag {
	case 1:
		return []Point{pts[3], pts[4], pts[5], pts[6]}, cols[1], cols[2]
	case 2:
		return []Point{pts[6], pts[7], pts[8], pts[9]}, cols[2], cols[3]
	default: // 3
		return []Point{pts[9], pts[10], pts[11], pts[0]}, cols[3], cols[0]
	}
}

// coonsPatch evaluates the Coons surface over a grid and paints each cell,
// with the corner colours interpolated bilinearly.
func (mp *meshPainter) coonsPatch(pts []Point, cols [4][3]float64) {
	// The 12 boundary points run counterclockwise from corner 1 (pts[0]);
	// corners are pts[0], pts[3], pts[6], pts[9] with colours cols[0..3].
	cu0 := [4]Point{pts[0], pts[11], pts[10], pts[9]} // v=0, u: 0 -> 1
	cu1 := [4]Point{pts[3], pts[4], pts[5], pts[6]}   // v=1, u: 0 -> 1
	c0v := [4]Point{pts[0], pts[1], pts[2], pts[3]}   // u=0, v: 0 -> 1
	c1v := [4]Point{pts[9], pts[8], pts[7], pts[6]}   // u=1, v: 0 -> 1

	at := func(u, v float64) meshVertex {
		e0, e1 := bezierAt(cu0, u), bezierAt(cu1, u)
		f0, f1 := bezierAt(c0v, v), bezierAt(c1v, v)
		// Coons surface: the two ruled surfaces less the bilinear corner patch.
		p := Point{
			X: (1-v)*e0.X + v*e1.X + (1-u)*f0.X + u*f1.X -
				((1-u)*(1-v)*pts[0].X + (1-u)*v*pts[3].X + u*v*pts[6].X + u*(1-v)*pts[9].X),
			Y: (1-v)*e0.Y + v*e1.Y + (1-u)*f0.Y + u*f1.Y -
				((1-u)*(1-v)*pts[0].Y + (1-u)*v*pts[3].Y + u*v*pts[6].Y + u*(1-v)*pts[9].Y),
		}
		var rgb [3]float64
		for k := range rgb {
			rgb[k] = (1-u)*(1-v)*cols[0][k] + (1-u)*v*cols[1][k] +
				u*v*cols[2][k] + u*(1-v)*cols[3][k]
		}
		return meshVertex{p: p, rgb: rgb}
	}

	step := 1.0 / meshPatchGrid
	for i := range meshPatchGrid {
		for j := range meshPatchGrid {
			if mp.overflow {
				return
			}
			u, v := float64(i)*step, float64(j)*step
			mp.quad(at(u, v), at(u+step, v), at(u+step, v+step), at(u, v+step))
		}
	}
}

// bezierAt evaluates a cubic Bezier at t.
func bezierAt(c [4]Point, t float64) Point {
	mt := 1 - t
	a, b := mt*mt*mt, 3*mt*mt*t
	d, e := 3*mt*t*t, t*t*t
	return Point{
		X: a*c[0].X + b*c[1].X + d*c[2].X + e*c[3].X,
		Y: a*c[0].Y + b*c[1].Y + d*c[2].Y + e*c[3].Y,
	}
}
