package convert

import (
	"fmt"
	"image"
	"math"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// meshShading builds a mesh shading stream dict over a 0..20 coordinate range
// with 8-bit fields and DeviceRGB colours, so a test's bytes map linearly:
// a raw 0 is 0 and a raw 255 is 20 (or full intensity for a colour).
func meshShading(kind int, data []byte, extra map[string]pdf.PDFValue) pdf.PDFDict {
	entries := pdf.DictOf(map[string]pdf.PDFValue{
		"ShadingType":       pdf.PDFInteger(kind),
		"ColorSpace":        pdf.PDFName{Value: "DeviceRGB"},
		"BitsPerCoordinate": pdf.PDFInteger(8),
		"BitsPerComponent":  pdf.PDFInteger(8),
		"BitsPerFlag":       pdf.PDFInteger(8),
		"Decode":            numArray(0, 20, 0, 20, 0, 1, 0, 1, 0, 1),
	})
	for k, v := range extra {
		entries.Set(k, v)
	}
	return pdf.PDFDict{Entries: entries, HasStream: true, RawStream: data}
}

// vertex packs one 8-bit-per-field mesh vertex, without a leading flag.
func vertex(x, y, r, g, b byte) []byte { return []byte{x, y, r, g, b} }

// flagVertex packs a vertex preceded by its edge flag.
func flagVertex(flag, x, y, r, g, b byte) []byte {
	return append([]byte{flag}, vertex(x, y, r, g, b)...)
}

// TestRenderFreeFormMesh: a type 4 stream paints its triangles, including one
// joined to the previous by an edge flag.
func TestRenderFreeFormMesh(t *testing.T) {
	var data []byte
	// Lower-left triangle in user space, all red.
	data = append(data, flagVertex(0, 0, 0, 255, 0, 0)...)
	data = append(data, flagVertex(0, 255, 0, 255, 0, 0)...)
	data = append(data, flagVertex(0, 0, 255, 255, 0, 0)...)
	// Flag 1 reuses the last two vertices, completing the square in green.
	data = append(data, flagVertex(1, 255, 255, 0, 255, 0)...)

	img, drops := renderShading(t, meshShading(4, data, nil))
	if len(drops) != 0 {
		t.Errorf("drops = %v, want none", drops)
	}
	// Device y is flipped: user (0,0) is the bottom-left of the canvas.
	if got := nrgbaAt(t, img, 3, 17); got.R < 200 || got.G > 60 {
		t.Errorf("first triangle at (3,17) = %v, want red", got)
	}
	// The second triangle inherits two red vertices from the first, so it
	// ramps red -> green; near its own vertex the green must dominate.
	if got := nrgbaAt(t, img, 18, 2); got.G <= got.R {
		t.Errorf("second triangle at (18,2) = %v, want green-dominant", got)
	}
}

// TestRenderLatticeMesh: a type 5 stream triangulates adjacent rows into a
// filled quad, with no per-vertex flags.
func TestRenderLatticeMesh(t *testing.T) {
	var data []byte
	// Top row (user y = 20), then bottom row (user y = 0), all blue.
	data = append(data, vertex(0, 255, 0, 0, 255)...)
	data = append(data, vertex(255, 255, 0, 0, 255)...)
	data = append(data, vertex(0, 0, 0, 0, 255)...)
	data = append(data, vertex(255, 0, 0, 0, 255)...)

	sh := meshShading(5, data, map[string]pdf.PDFValue{"VerticesPerRow": pdf.PDFInteger(2)})
	img, drops := renderShading(t, sh)
	if len(drops) != 0 {
		t.Errorf("drops = %v, want none", drops)
	}
	for _, p := range [][2]int{{10, 10}, {3, 3}, {16, 16}} {
		if got := nrgbaAt(t, img, p[0], p[1]); got.B < 200 || got.R > 60 {
			t.Errorf("lattice at (%d,%d) = %v, want blue", p[0], p[1], got)
		}
	}
}

// TestRenderCoonsPatch: a type 6 patch whose four boundary curves trace the
// page fills it, with the corner colours interpolated across the surface.
func TestRenderCoonsPatch(t *testing.T) {
	// The 12 boundary points run counterclockwise from corner 1 at (0,0):
	// up the left edge, across the top, down the right, back along the bottom.
	pts := [][2]byte{
		{0, 0}, {0, 85}, {0, 170}, // p1..p3
		{0, 255}, {85, 255}, {170, 255}, // p4..p6
		{255, 255}, {255, 170}, {255, 85}, // p7..p9
		{255, 0}, {170, 0}, {85, 0}, // p10..p12
	}
	data := []byte{0} // flag 0: a standalone patch
	for _, p := range pts {
		data = append(data, p[0], p[1])
	}
	// Corner colours at p1, p4, p7, p10: red, red, blue, blue.
	for _, c := range [][3]byte{{255, 0, 0}, {255, 0, 0}, {0, 0, 255}, {0, 0, 255}} {
		data = append(data, c[0], c[1], c[2])
	}

	img, drops := renderShading(t, meshShading(6, data, nil))
	if len(drops) != 0 {
		t.Errorf("drops = %v, want none", drops)
	}
	// cols[0]/cols[1] sit at u=0 (the left edge), cols[2]/cols[3] at u=1.
	left, right := nrgbaAt(t, img, 2, 10), nrgbaAt(t, img, 17, 10)
	if left.R < 180 {
		t.Errorf("left edge = %v, want red-dominant", left)
	}
	if right.B < 180 {
		t.Errorf("right edge = %v, want blue-dominant", right)
	}
}

// TestRenderMeshTruncatedDrops: a stream ending mid-record has lost content,
// which must be reported rather than half-painted in silence.
func TestRenderMeshTruncatedDrops(t *testing.T) {
	var data []byte
	data = append(data, flagVertex(0, 0, 0, 255, 0, 0)...)
	data = append(data, flagVertex(0, 255, 0, 255, 0, 0)...)
	data = append(data, flagVertex(0, 0, 255, 255, 0, 0)...)
	data = append(data, 1, 255) // a fourth vertex, cut short

	_, drops := renderShading(t, meshShading(4, data, nil))
	if !hasDrop(drops, dropShading) {
		t.Errorf("drops = %v, want %q", drops, dropShading)
	}
}

// TestRenderMeshMalformedDrops: mesh parameters the rasterizer cannot use are
// rejected at parse time.
func TestRenderMeshMalformedDrops(t *testing.T) {
	tests := []struct {
		name  string
		kind  int
		extra map[string]pdf.PDFValue
	}{
		{"no BitsPerCoordinate", 4, map[string]pdf.PDFValue{"BitsPerCoordinate": pdf.PDFInteger(0)}},
		{"oversized BitsPerComponent", 4, map[string]pdf.PDFValue{"BitsPerComponent": pdf.PDFInteger(64)}},
		{"no BitsPerFlag", 4, map[string]pdf.PDFValue{"BitsPerFlag": pdf.PDFInteger(0)}},
		{"short Decode", 4, map[string]pdf.PDFValue{"Decode": numArray(0, 20, 0, 20)}},
		{"lattice without VerticesPerRow", 5, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, drops := renderShading(t, meshShading(tc.kind, []byte{0, 0, 0, 0, 0, 0}, tc.extra))
			if !hasDrop(drops, dropShading) {
				t.Errorf("drops = %v, want %q", drops, dropShading)
			}
		})
	}
}

// TestMeshPrimitiveCap: a mesh that would paint more pieces than the cap stops
// and reports the loss instead of running unbounded.
func TestMeshPrimitiveCap(t *testing.T) {
	mp := &meshPainter{
		canvas:    image.NewRGBA(image.Rect(0, 0, 20, 20)),
		toDevice:  IdentityMatrix,
		alpha:     1,
		primitive: meshMaxPrimitives,
	}
	mp.tri(meshVertex{p: Point{0, 0}}, meshVertex{p: Point{20, 0}}, meshVertex{p: Point{0, 20}})
	if !mp.overflow {
		t.Error("painting past the primitive cap did not set overflow")
	}
}

// TestMeshReaderBounds: the bit reader refuses reads past the end and flags
// them, and align() advances to the next byte boundary.
func TestMeshReaderBounds(t *testing.T) {
	m := &meshReader{data: []byte{0xFF, 0x0F}}
	if v, ok := m.read(4); !ok || v != 0xF {
		t.Errorf("read(4) = %v, %v; want 15, true", v, ok)
	}
	m.align()
	if m.bit != 8 {
		t.Errorf("after align bit = %d, want 8", m.bit)
	}
	if m.exhausted() {
		t.Error("reader reported exhausted with a byte remaining")
	}
	if _, ok := m.read(16); ok {
		t.Error("read past the end succeeded")
	}
	if !m.short {
		t.Error("a refused read did not set short")
	}
	if v, ok := m.read(8); !ok || v != 0x0F {
		t.Errorf("read(8) = %v, %v; want 15, true", v, ok)
	}
	if !m.exhausted() {
		t.Error("reader should be exhausted after consuming both bytes")
	}
}

func TestDecodeValue(t *testing.T) {
	tests := []struct {
		raw    uint64
		bits   int
		lo, hi float64
		want   float64
	}{
		{0, 8, 0, 20, 0},
		{255, 8, 0, 20, 20},
		{128, 8, 0, 255, 128},
		{0, 0, 5, 9, 5}, // a zero-width field has no range to map
	}
	for _, tc := range tests {
		if got := decodeValue(tc.raw, tc.bits, tc.lo, tc.hi); got != tc.want {
			t.Errorf("decodeValue(%d, %d, %v, %v) = %v, want %v",
				tc.raw, tc.bits, tc.lo, tc.hi, got, tc.want)
		}
	}
}

// TestSharedEdgeIndices pins the points and colours a flag-1/2/3 patch
// inherits from its predecessor (ISO 32000-1 Table 85). The mapping is pure
// index arithmetic derived from the spec, so a transcription slip would
// silently distort every multi-patch mesh.
func TestSharedEdgeIndices(t *testing.T) {
	pts := make([]Point, 12)
	for i := range pts {
		pts[i] = Point{X: float64(i), Y: float64(i)}
	}
	var cols [4][3]float64
	for i := range cols {
		cols[i] = [3]float64{float64(i), 0, 0}
	}

	tests := []struct {
		flag     int
		wantPts  []int // indices into pts
		wantCols [2]int
	}{
		{1, []int{3, 4, 5, 6}, [2]int{1, 2}},
		{2, []int{6, 7, 8, 9}, [2]int{2, 3}},
		{3, []int{9, 10, 11, 0}, [2]int{3, 0}},
	}
	for _, tc := range tests {
		t.Run(fmt.Sprintf("flag%d", tc.flag), func(t *testing.T) {
			edge, c0, c1 := sharedEdge(tc.flag, pts, cols)
			if len(edge) != 4 {
				t.Fatalf("edge has %d points, want 4", len(edge))
			}
			for i, want := range tc.wantPts {
				if edge[i] != pts[want] {
					t.Errorf("edge[%d] = %v, want pts[%d] = %v", i, edge[i], want, pts[want])
				}
			}
			if c0 != cols[tc.wantCols[0]] {
				t.Errorf("first colour = %v, want cols[%d]", c0, tc.wantCols[0])
			}
			if c1 != cols[tc.wantCols[1]] {
				t.Errorf("second colour = %v, want cols[%d]", c1, tc.wantCols[1])
			}
		})
	}
}

// coord maps a 0..20 user coordinate to its 8-bit mesh sample.
func coord(v float64) byte { return byte(math.Round(v * 255 / 20)) }

// TestRenderPatchMeshSharedEdge: a second patch joined by an edge flag reuses
// the first's edge and two of its colours, producing one continuous surface
// rather than a detached or misplaced second patch.
func TestRenderPatchMeshSharedEdge(t *testing.T) {
	// Patch 1 covers the bottom half (user y 0..10), all red. Corners run
	// p1=(0,0) p4=(0,10) p7=(20,10) p10=(20,0), counterclockwise from p1.
	patch1 := [][2]float64{
		{0, 0}, {0, 3.3}, {0, 6.7},
		{0, 10}, {6.7, 10}, {13.3, 10},
		{20, 10}, {20, 6.7}, {20, 3.3},
		{20, 0}, {13.3, 0}, {6.7, 0},
	}
	data := []byte{0}
	for _, p := range patch1 {
		data = append(data, coord(p[0]), coord(p[1]))
	}
	for range 4 {
		data = append(data, 255, 0, 0) // red
	}

	// Patch 2, flag 1: inherits patch 1's p4..p7 edge (the y=10 line) as its
	// own p1..p4, then supplies the eight points closing the top half.
	patch2 := [][2]float64{
		{20, 13.3}, {20, 16.7},
		{20, 20}, {13.3, 20}, {6.7, 20},
		{0, 20}, {0, 16.7}, {0, 13.3},
	}
	data = append(data, 1)
	for _, p := range patch2 {
		data = append(data, coord(p[0]), coord(p[1]))
	}
	for range 2 {
		data = append(data, 0, 255, 0) // green
	}

	img, drops := renderShading(t, meshShading(6, data, nil))
	if len(drops) != 0 {
		t.Errorf("drops = %v, want none", drops)
	}
	// Device y is flipped, so user y 0..10 is the lower half of the canvas.
	if got := nrgbaAt(t, img, 10, 15); got.R <= got.G {
		t.Errorf("first patch at (10,15) = %v, want red-dominant", got)
	}
	if got := nrgbaAt(t, img, 10, 1); got.G <= got.R {
		t.Errorf("second patch at (10,1) = %v, want green-dominant", got)
	}
	// The join itself must be covered by one patch or the other, not a gap.
	assertPainted(t, img, 10, 10, "the shared edge")
}

// assertPainted checks a pixel is no longer the canvas's opaque white.
func assertPainted(t *testing.T, img *image.RGBA, x, y int, where string) {
	t.Helper()
	if got := nrgbaAt(t, img, x, y); got.R == 255 && got.G == 255 && got.B == 255 {
		t.Errorf("%s at (%d,%d) is unpainted white", where, x, y)
	}
}

// TestRenderFreeFormMeshFlag2: a flag-2 vertex shares the previous triangle's
// *first* edge, keeping va and replacing only vb and vc. Getting this confused
// with flag 1 mirrors the new triangle across the strip, so it is pinned
// separately.
func TestRenderFreeFormMeshFlag2(t *testing.T) {
	var data []byte
	data = append(data, flagVertex(0, 0, 0, 255, 0, 0)...)
	data = append(data, flagVertex(0, 255, 0, 255, 0, 0)...)
	data = append(data, flagVertex(0, 0, 255, 255, 0, 0)...)
	// Flag 2 keeps va = (0,0) and the third vertex, dropping the second.
	data = append(data, flagVertex(2, 255, 255, 0, 255, 0)...)

	img, drops := renderShading(t, meshShading(4, data, nil))
	if len(drops) != 0 {
		t.Errorf("drops = %v, want none", drops)
	}
	// The flag-2 triangle spans (0,0), (0,20), (20,20), inheriting two red
	// vertices; only its own vertex at user (20,20) is green. Had the walker
	// treated the flag as a 1 the strip would have mirrored and left this
	// corner unpainted.
	if got := nrgbaAt(t, img, 15, 2); got.G <= got.R {
		t.Errorf("flag-2 triangle near its own vertex = %v, want green-dominant", got)
	}
}

// TestRenderMeshWithFunction: a mesh whose /Function is present stores one
// parametric value per vertex instead of full colour components, so /Decode
// carries a single colour range and the function supplies the colour.
func TestRenderMeshWithFunction(t *testing.T) {
	// Each vertex is flag, x, y, t -- four bytes, not six.
	fnVertex := func(flag, x, y, tv byte) []byte { return []byte{flag, x, y, tv} }
	var data []byte
	data = append(data, fnVertex(0, 0, 0, 0)...)
	data = append(data, fnVertex(0, 255, 0, 0)...)
	data = append(data, fnVertex(0, 0, 255, 0)...)

	sh := meshShading(4, data, map[string]pdf.PDFValue{
		"Decode":   numArray(0, 20, 0, 20, 0, 1),
		"Function": expFunction([]float64{1, 0, 0}, []float64{0, 0, 1}),
	})
	img, drops := renderShading(t, sh)
	if len(drops) != 0 {
		t.Errorf("drops = %v, want none", drops)
	}
	// t = 0 everywhere, so the function's C0 (red) covers the triangle.
	if got := nrgbaAt(t, img, 3, 17); got.R < 200 || got.B > 60 {
		t.Errorf("function-coloured mesh at (3,17) = %v, want red", got)
	}
}

// TestRenderTensorPatch: a type 7 patch carries four interior control points
// after its boundary. They are read and skipped, so the record must still be
// consumed at the right width or every following patch would be misaligned.
func TestRenderTensorPatch(t *testing.T) {
	boundary := [][2]byte{
		{0, 0}, {0, 85}, {0, 170},
		{0, 255}, {85, 255}, {170, 255},
		{255, 255}, {255, 170}, {255, 85},
		{255, 0}, {170, 0}, {85, 0},
	}
	interior := [][2]byte{{85, 85}, {85, 170}, {170, 170}, {170, 85}}

	data := []byte{0}
	for _, p := range append(append([][2]byte{}, boundary...), interior...) {
		data = append(data, p[0], p[1])
	}
	for range 4 {
		data = append(data, 0, 255, 0) // green corners
	}

	img, drops := renderShading(t, meshShading(7, data, nil))
	if len(drops) != 0 {
		t.Errorf("drops = %v, want none", drops)
	}
	if got := nrgbaAt(t, img, 10, 10); got.G < 200 || got.R > 60 {
		t.Errorf("tensor patch centre = %v, want green", got)
	}
}

// TestRenderMeshTruncationPoints walks a stream cut at every record boundary
// that a reader can stop at. Each must end the walk and report the loss rather
// than panicking or painting a half-read primitive.
func TestRenderMeshTruncationPoints(t *testing.T) {
	// One complete free-form triangle: three flag-prefixed 8-bit vertices.
	var triangle []byte
	for range 3 {
		triangle = append(triangle, flagVertex(0, 128, 128, 255, 0, 0)...)
	}

	// One complete Coons patch: flag, 12 boundary points, 4 corner colours.
	var patch []byte
	patch = append(patch, 0)
	for range 12 {
		patch = append(patch, 128, 128)
	}
	for range 4 {
		patch = append(patch, 255, 0, 0)
	}

	// One complete tensor patch adds the four interior points.
	tensor := append(append([]byte{}, patch[:25]...), 128, 128, 128, 128, 128, 128, 128, 128)
	tensor = append(tensor, patch[25:]...)

	tests := []struct {
		name  string
		kind  int
		data  []byte
		extra map[string]pdf.PDFValue
	}{
		{"free-form cut after the flag", 4, triangle[:1], nil},
		{"free-form cut inside the coordinates", 4, triangle[:2], nil},
		{"free-form cut inside the colour", 4, triangle[:4], nil},
		{"free-form cut before the second vertex", 4, triangle[:6], nil},
		{"free-form cut inside the second vertex", 4, triangle[:7], nil},
		{"free-form cut inside the third vertex", 4, triangle[:13], nil},
		{"lattice cut inside a row", 5, []byte{
			0, 255, 0, 0, 255, 255, 255, 0, 0, 255, // a full two-vertex row
			0, 0, 0, // then a partial vertex
		}, map[string]pdf.PDFValue{"VerticesPerRow": pdf.PDFInteger(2)}},
		{"patch cut inside the boundary points", 6, patch[:6], nil},
		{"patch cut inside the corner colours", 6, patch[:27], nil},
		{"tensor cut inside the interior points", 7, tensor[:28], nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, drops := renderShading(t, meshShading(tc.kind, tc.data, tc.extra))
			if !hasDrop(drops, dropShading) {
				t.Errorf("drops = %v, want %q", drops, dropShading)
			}
		})
	}
}

// TestRenderMeshFlagCutShort: a leading flag wider than the bytes left cannot
// be read at all. It is the one truncation that stops the walk before any
// vertex data, for both the free-form and the patch walkers.
func TestRenderMeshFlagCutShort(t *testing.T) {
	for _, kind := range []int{4, 6} {
		t.Run(fmt.Sprintf("type%d", kind), func(t *testing.T) {
			sh := meshShading(kind, []byte{0}, map[string]pdf.PDFValue{
				"BitsPerFlag": pdf.PDFInteger(16),
			})
			_, drops := renderShading(t, sh)
			if !hasDrop(drops, dropShading) {
				t.Errorf("drops = %v, want %q", drops, dropShading)
			}
		})
	}
}

// TestRenderMeshUndecodableStream: a mesh whose stream cannot be decoded is a
// dropped shading, not a panic on the raw bytes.
func TestRenderMeshUndecodableStream(t *testing.T) {
	sh := meshShading(4, []byte("not deflate data"), map[string]pdf.PDFValue{
		"Filter": pdf.PDFName{Value: "FlateDecode"},
	})
	_, drops := renderShading(t, sh)
	if !hasDrop(drops, dropShading) {
		t.Errorf("drops = %v, want %q", drops, dropShading)
	}
}

// TestPaintMeshDegenerateState: a mesh under a zero alpha or an empty clip has
// no area to paint, and reports no drop because nothing was lost.
func TestPaintMeshDegenerateState(t *testing.T) {
	var data []byte
	data = append(data, flagVertex(0, 0, 0, 255, 0, 0)...)
	data = append(data, flagVertex(0, 255, 0, 255, 0, 0)...)
	data = append(data, flagVertex(0, 0, 255, 255, 0, 0)...)

	for _, content := range []string{"q /GSNone gs /Sh1 sh Q", "q 5 5 0 0 re W n /Sh1 sh Q"} {
		t.Run(content, func(t *testing.T) {
			img, drops := renderShadingContent(t, meshShading(4, data, nil), content)
			if len(drops) != 0 {
				t.Errorf("drops = %v, want none", drops)
			}
			assertUnpainted(t, img, 3, 17, "a mesh with no area to paint")
		})
	}
}

// TestMeshOverflowStopsPainting: once the primitive cap is hit every painter
// entry point must return immediately, so a hostile mesh cannot keep the
// rasterizer busy after the budget is spent.
func TestMeshOverflowStopsPainting(t *testing.T) {
	newPainter := func() *meshPainter {
		return &meshPainter{
			canvas:   image.NewRGBA(image.Rect(0, 0, 20, 20)),
			toDevice: IdentityMatrix,
			alpha:    1,
			overflow: true,
		}
	}

	mp := newPainter()
	mp.tri(meshVertex{p: Point{0, 0}}, meshVertex{p: Point{20, 0}}, meshVertex{p: Point{0, 20}})
	if mp.primitive != 0 {
		t.Errorf("tri painted %d pieces after overflow, want 0", mp.primitive)
	}

	mp = newPainter()
	mp.flat(meshVertex{}, meshVertex{}, meshVertex{}, 0, 0, 1, false)
	if mp.primitive != 0 {
		t.Errorf("flat painted %d pieces after overflow, want 0", mp.primitive)
	}

	// coonsPatch checks the flag per grid cell, so a painter that overflows on
	// its first cell must abandon the remaining ones.
	mp = newPainter()
	mp.overflow = false
	mp.primitive = meshMaxPrimitives
	pts := make([]Point, 12)
	for i := range pts {
		pts[i] = Point{X: float64(i), Y: float64(i)}
	}
	mp.coonsPatch(pts, [4][3]float64{})
	if !mp.overflow {
		t.Error("coonsPatch past the cap did not set overflow")
	}
	if mp.primitive != meshMaxPrimitives+1 {
		t.Errorf("coonsPatch painted %d pieces past the cap, want 1",
			mp.primitive-meshMaxPrimitives)
	}
}
