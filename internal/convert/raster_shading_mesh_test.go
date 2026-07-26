package convert

import (
	"image"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// meshShading builds a mesh shading stream dict over a 0..20 coordinate range
// with 8-bit fields and DeviceRGB colours, so a test's bytes map linearly:
// a raw 0 is 0 and a raw 255 is 20 (or full intensity for a colour).
func meshShading(kind int, data []byte, extra map[string]pdf.PDFValue) pdf.PDFDict {
	entries := map[string]pdf.PDFValue{
		"ShadingType":       pdf.PDFInteger(kind),
		"ColorSpace":        pdf.PDFName{Value: "DeviceRGB"},
		"BitsPerCoordinate": pdf.PDFInteger(8),
		"BitsPerComponent":  pdf.PDFInteger(8),
		"BitsPerFlag":       pdf.PDFInteger(8),
		"Decode":            numArray(0, 20, 0, 20, 0, 1, 0, 1, 0, 1),
	}
	for k, v := range extra {
		entries[k] = v
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
