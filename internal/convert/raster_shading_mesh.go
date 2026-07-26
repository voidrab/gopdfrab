package convert

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

func (sh *shading) initMesh(dict pdf.PDFDict) bool {
	sh.bitsPerCoord = pdf.DictInt(dict, "BitsPerCoordinate", 0)
	sh.bitsPerComp = pdf.DictInt(dict, "BitsPerComponent", 0)
	sh.bitsPerFlag = pdf.DictInt(dict, "BitsPerFlag", 0)
	sh.vertsPerRow = pdf.DictInt(dict, "VerticesPerRow", 0)

	// A /Function reduces every vertex to a single parametric value.
	sh.ncomp = pdf.ColorSpaceComponents(sh.cs)
	if sh.fn.valid() {
		sh.ncomp = 1
	}
	if sh.ncomp <= 0 || sh.bitsPerCoord <= 0 || sh.bitsPerCoord > 32 ||
		sh.bitsPerComp <= 0 || sh.bitsPerComp > 32 {
		return false
	}
	if sh.kind != 5 && (sh.bitsPerFlag <= 0 || sh.bitsPerFlag > 32) {
		return false
	}
	if sh.kind == 5 && sh.vertsPerRow < 2 {
		return false
	}

	decode, err := pdf.FloatArray(dict.Entries["Decode"])
	if err != nil || len(decode) < 4+2*sh.ncomp {
		return false
	}
	sh.meshDecode = decode
	return dict.HasStream
}

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

// readCoords reads one vertex's x,y through the /Decode ranges.
func (sh *shading) readCoords(m *meshReader) (Point, bool) {
	rx, ok := m.read(sh.bitsPerCoord)
	if !ok {
		return Point{}, false
	}
	ry, ok := m.read(sh.bitsPerCoord)
	if !ok {
		return Point{}, false
	}
	return Point{
		X: decodeValue(rx, sh.bitsPerCoord, sh.meshDecode[0], sh.meshDecode[1]),
		Y: decodeValue(ry, sh.bitsPerCoord, sh.meshDecode[2], sh.meshDecode[3]),
	}, true
}

// readColor reads one vertex's colour components and resolves them to RGB,
// through /Function first when the shading declares one.
func (sh *shading) readColor(m *meshReader) ([3]float64, bool) {
	comps := make([]float64, sh.ncomp)
	for i := range comps {
		raw, ok := m.read(sh.bitsPerComp)
		if !ok {
			return [3]float64{}, false
		}
		lo, hi := sh.meshDecode[4+2*i], sh.meshDecode[5+2*i]
		comps[i] = decodeValue(raw, sh.bitsPerComp, lo, hi)
	}
	if sh.fn.valid() {
		comps = sh.fn.eval(comps)
	}
	return sh.colorFromComps(comps), true
}

func (sh *shading) readVertex(m *meshReader) (meshVertex, bool) {
	p, ok := sh.readCoords(m)
	if !ok {
		return meshVertex{}, false
	}
	rgb, ok := sh.readColor(m)
	if !ok {
		return meshVertex{}, false
	}
	return meshVertex{p: p, rgb: rgb}, true
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

// paintMeshShading decodes the mesh stream and paints it, clipped to area.
func (r *renderer) paintMeshShading(sh *shading, shadingToDevice Matrix, alpha float64, area image.Rectangle) {
	if area.Empty() || alpha <= 0 {
		return
	}
	data, err := pdf.DecodeStream(sh.dict)
	if err != nil {
		r.drop(dropShading)
		return
	}
	target := r.canvas
	if area != r.canvas.Bounds() {
		sub, ok := r.canvas.SubImage(area).(*image.RGBA)
		if !ok {
			return
		}
		target = sub
	}

	mp := &meshPainter{canvas: target, toDevice: shadingToDevice, alpha: alpha}
	m := &meshReader{data: data}
	switch sh.kind {
	case 4:
		sh.paintFreeFormMesh(m, mp)
	case 5:
		sh.paintLatticeMesh(m, mp)
	case 6, 7:
		sh.paintPatchMesh(m, mp)
	}
	if mp.overflow || m.short {
		r.drop(dropShading)
	}
}

// paintFreeFormMesh walks a type 4 stream, where each vertex's edge flag says
// how it joins the two before it.
func (sh *shading) paintFreeFormMesh(m *meshReader, mp *meshPainter) {
	var va, vb, vc meshVertex
	have := 0
	for !m.exhausted() && !mp.overflow {
		flag, ok := m.read(sh.bitsPerFlag)
		if !ok {
			return
		}
		v, ok := sh.readVertex(m)
		if !ok {
			return
		}
		m.align()

		if flag == 0 || have < 3 {
			// A flag-0 vertex starts a fresh triangle: the next two vertices
			// carry their own (also 0) flags and complete it.
			va = v
			second, ok := sh.readTaggedVertex(m)
			if !ok {
				return
			}
			third, ok := sh.readTaggedVertex(m)
			if !ok {
				return
			}
			vb, vc = second, third
			have = 3
		} else if flag == 1 {
			// Share the previous triangle's second edge.
			va, vb, vc = vb, vc, v
		} else {
			// Flag 2 shares the first edge, so va is carried over.
			vb, vc = vc, v
		}
		mp.tri(va, vb, vc)
	}
}

// readTaggedVertex reads a flag-prefixed vertex whose flag is not needed.
func (sh *shading) readTaggedVertex(m *meshReader) (meshVertex, bool) {
	if _, ok := m.read(sh.bitsPerFlag); !ok {
		return meshVertex{}, false
	}
	v, ok := sh.readVertex(m)
	if !ok {
		return meshVertex{}, false
	}
	m.align()
	return v, true
}

// paintLatticeMesh walks a type 5 stream, a plain row-major grid of vertices
// with no flags, triangulating each cell of adjacent rows.
func (sh *shading) paintLatticeMesh(m *meshReader, mp *meshPainter) {
	perRow := sh.vertsPerRow
	var prev []meshVertex
	for !m.exhausted() && !mp.overflow {
		row := make([]meshVertex, 0, perRow)
		for range perRow {
			v, ok := sh.readVertex(m)
			if !ok {
				return
			}
			row = append(row, v)
		}
		if prev != nil {
			for i := 0; i+1 < perRow; i++ {
				mp.quad(prev[i], prev[i+1], row[i+1], row[i])
			}
		}
		prev = row
	}
}

// paintPatchMesh walks a type 6 (Coons) or 7 (tensor) stream. A tensor
// patch's four interior control points are read but not used: the surface is
// evaluated by the Coons formula over the shared boundary curves, which for a
// raster fallback differs from the true tensor surface only in the interior.
func (sh *shading) paintPatchMesh(m *meshReader, mp *meshPainter) {
	pointsPerPatch := 12
	if sh.kind == 7 {
		pointsPerPatch = 16
	}
	var prevPts []Point
	var prevCols [4][3]float64

	for !m.exhausted() && !mp.overflow {
		flag, ok := m.read(sh.bitsPerFlag)
		if !ok {
			return
		}
		newPatch := flag == 0 || prevPts == nil

		pts := make([]Point, 12)
		var cols [4][3]float64
		first := 0
		if !newPatch {
			// Flags 1-3 reuse one edge (4 points) and 2 colours of the
			// previous patch, per ISO 32000-1 Table 85.
			edge, c0, c1 := sharedEdge(int(flag), prevPts, prevCols)
			copy(pts, edge)
			cols[0], cols[1] = c0, c1
			first = 4
		}
		for i := first; i < 12; i++ {
			p, ok := sh.readCoords(m)
			if !ok {
				return
			}
			pts[i] = p
		}
		// A tensor patch's interior points follow the boundary ones.
		for i := 12; i < pointsPerPatch; i++ {
			if _, ok := sh.readCoords(m); !ok {
				return
			}
		}
		firstCol := 0
		if !newPatch {
			firstCol = 2
		}
		for i := firstCol; i < 4; i++ {
			rgb, ok := sh.readColor(m)
			if !ok {
				return
			}
			cols[i] = rgb
		}
		m.align()

		mp.coonsPatch(pts, cols)
		prevPts, prevCols = pts, cols
	}
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
