package raster

import (
	"image"
	"math"
	"math/rand"
	"testing"

	"github.com/voidrab/gopdfrab/internal/content"
	"github.com/voidrab/gopdfrab/internal/pdf"
)

// triClip clips to a triangle whose device corners are (2,18), (18,18) and
// (10,2); user y is up, so these are user (2,2), (18,2) and (10,18).
const triClip = "2 2 m 18 2 l 10 18 l h W n "

func TestClipToAShape(t *testing.T) {
	img := render20(t, triClip+"0 0 0 rg 0 0 20 20 re f", pdf.PDFDict{})
	for _, p := range [][2]int{{10, 12}, {10, 16}, {6, 16}} {
		if !isBlack(t, img, p[0], p[1]) {
			t.Errorf("(%d,%d) inside the triangle was not painted", p[0], p[1])
		}
	}
	for _, p := range [][2]int{{3, 5}, {17, 5}, {10, 19}, {1, 17}} {
		if !isWhite(t, img, p[0], p[1]) {
			t.Errorf("(%d,%d) outside the triangle was painted", p[0], p[1])
		}
	}
	// The slanted edges are anti-aliased, not stair-stepped.
	grey := 0
	for y := range 20 {
		for x := range 20 {
			if c := nrgbaAt(t, img, x, y); c.R > 30 && c.R < 225 {
				grey++
			}
		}
	}
	if grey < 8 {
		t.Errorf("only %d partially covered pixels along the clip edges", grey)
	}
}

func TestClipWindingRules(t *testing.T) {
	// Two rectangles wound the same way: the nonzero rule keeps the inner one
	// inside the clip, the even-odd rule cuts it out.
	rects := "4 4 12 12 re 8 8 4 4 re "
	nonzero := render20(t, rects+"W n 0 0 0 rg 0 0 20 20 re f", pdf.PDFDict{})
	evenOdd := render20(t, rects+"W* n 0 0 0 rg 0 0 20 20 re f", pdf.PDFDict{})
	if !isBlack(t, nonzero, 10, 10) || !isBlack(t, nonzero, 6, 10) {
		t.Error("nonzero clip left a hole")
	}
	if !isWhite(t, evenOdd, 10, 10) || !isBlack(t, evenOdd, 6, 10) || !isWhite(t, evenOdd, 2, 10) {
		t.Error("even-odd clip is not a ring")
	}
}

func TestClipsIntersectAndRestore(t *testing.T) {
	// A rectangle clip after a shape clip keeps the overlap only.
	both := render20(t, triClip+"0 0 10 20 re W n 0 0 0 rg 0 0 20 20 re f", pdf.PDFDict{})
	if !isBlack(t, both, 6, 14) || !isWhite(t, both, 14, 14) || !isWhite(t, both, 3, 5) {
		t.Error("shape and rectangle clips did not intersect")
	}
	// Nested shape clips intersect: the second triangle covers the lower right
	// of the page.
	two := render20(t, triClip+"10 0 m 20 0 l 20 20 l h W n 0 0 0 rg 0 0 20 20 re f", pdf.PDFDict{})
	if !isBlack(t, two, 13, 14) || !isWhite(t, two, 6, 16) || !isWhite(t, two, 3, 5) {
		t.Error("two shape clips did not intersect")
	}
	// The Q operator restores the clip that q saved.
	restored := render20(t, "q "+triClip+"Q 0 0 0 rg 0 0 20 20 re f", pdf.PDFDict{})
	if !isBlack(t, restored, 1, 1) || !isBlack(t, restored, 18, 18) {
		t.Error("Q did not restore the clip")
	}
	// A shape wholly outside the page clips everything.
	gone := render20(t, "30 30 m 40 30 l 35 40 l h W n 0 0 0 rg 0 0 20 20 re f", pdf.PDFDict{})
	if n, _, _, _, _ := ink(gone); n != 0 {
		t.Errorf("an off-page clip let %d pixels through", n)
	}
}

func TestClipAppliesToEveryPaintingOperation(t *testing.T) {
	// A stroke across the page, cut to the triangle (which is 8 wide at row 10).
	stroke := render20(t, triClip+"0 0 0 RG 4 w 0 10 m 20 10 l S", pdf.PDFDict{})
	if !isBlack(t, stroke, 10, 10) || !isWhite(t, stroke, 2, 10) || !isWhite(t, stroke, 17, 10) {
		t.Error("stroke ignored the shape clip")
	}
	// A translucent fill keeps its alpha inside the clip.
	half := render20(t, triClip+"0 0 0 rg /G gs 0 0 20 20 re f", resourcesWith("ExtGState", map[string]pdf.PDFValue{
		"G": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"ca": pdf.PDFReal(0.5)})},
	}))
	if c := nrgbaAt(t, half, 10, 12); c.R < 120 || c.R > 135 {
		t.Errorf("translucent fill inside the clip = %v", c)
	}
	// A stencil image mask painted with the fill colour.
	image := render20(t, triClip+"0 0 0 rg q 20 0 0 20 0 0 cm BI /IM true /W 1 /H 1 /BPC 1 ID \x00 EI Q", pdf.PDFDict{})
	if !isBlack(t, image, 10, 12) || !isWhite(t, image, 3, 5) {
		t.Error("inline image ignored the shape clip")
	}
	// Axial and mesh shadings painted with the sh operator.
	axial := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"ShadingType": pdf.PDFInteger(2), "ColorSpace": pdf.PDFName{Value: "DeviceRGB"},
		"Coords": numArray(0, 0, 20, 0), "Function": expFunction([]float64{0, 0, 0}, []float64{0, 0, 0}),
		"Extend": pdf.PDFArray{pdf.PDFBoolean(true), pdf.PDFBoolean(true)},
	})}
	img, _ := renderShadingContent(t, axial, "q "+triClip+"/Sh1 sh Q")
	if isWhite(t, img, 10, 12) {
		t.Error("axial shading missing inside the clip")
	}
	assertUnpainted(t, img, 3, 5, "outside the clip")

	var data []byte
	data = append(data, vertex(0, 255, 0, 0, 255)...)
	data = append(data, vertex(255, 255, 0, 0, 255)...)
	data = append(data, vertex(0, 0, 0, 0, 255)...)
	data = append(data, vertex(255, 0, 0, 0, 255)...)
	mesh := meshShading(5, data, map[string]pdf.PDFValue{"VerticesPerRow": pdf.PDFInteger(2)})
	img, _ = renderShadingContent(t, mesh, "q "+triClip+"/Sh1 sh Q")
	if got := nrgbaAt(t, img, 10, 12); got.B < 200 {
		t.Errorf("mesh inside the clip = %v, want blue", got)
	}
	assertUnpainted(t, img, 3, 5, "outside the clip")
}

func TestClipMaskLookup(t *testing.T) {
	var none *clipMask
	if none.at(5, 5) != 1 {
		t.Error("a nil mask must let everything through")
	}
	// Row 3 lets pixel 3 through, row 4 lets pixel 2 through at a fifth and
	// pixel 3 fully.
	m := &clipMask{rect: image.Rect(2, 3, 4, 5), rows: [][]clipSpan{
		{{3, 4, 255}},
		{{2, 3, 51}, {3, 4, 255}},
	}}
	for _, tc := range []struct {
		x, y int
		want float64
	}{{2, 3, 0}, {3, 3, 1}, {2, 4, 0.2}, {3, 4, 1}, {1, 3, 0}, {4, 3, 0}, {2, 5, 0}, {2, 2, 0}} {
		if got := m.at(tc.x, tc.y); got < tc.want-0.001 || got > tc.want+0.001 {
			t.Errorf("at(%d,%d) = %v, want %v", tc.x, tc.y, got, tc.want)
		}
	}
	paint := constant([3]float64{1, 0, 0}, 0.5)
	if rgb, a := none.wrap(paint)(0, 0); a != 0.5 || rgb != [3]float64{1, 0, 0} {
		t.Errorf("nil wrap changed the colour: %v %v", rgb, a)
	}
	if _, a := m.wrap(paint)(2, 4); a < 0.099 || a > 0.101 {
		t.Errorf("wrapped alpha = %v, want 0.1", a)
	}
	if _, a := m.wrap(constant([3]float64{}, 0))(3, 3); a != 0 {
		t.Errorf("a transparent colour became %v", a)
	}
}

// TestClipMaskLookupInLongRows checks the search in rows with many spans:
// every second pixel of a 200 pixel row is let through, each at its own level.
func TestClipMaskLookupInLongRows(t *testing.T) {
	var row []clipSpan
	for x := 0; x < 200; x += 2 {
		row = append(row, clipSpan{int32(x), int32(x + 1), uint8(x + 1)})
	}
	m := &clipMask{rect: image.Rect(0, 0, 200, 1), rows: [][]clipSpan{row}}
	for x := range 200 {
		want := 0.0
		if x%2 == 0 {
			want = float64(x+1) / 255
		}
		if got := m.at(x, 0); got != want {
			t.Fatalf("at(%d) = %v, want %v", x, got, want)
		}
	}
}

func TestNewClipMaskBounds(t *testing.T) {
	bounds := image.Rect(0, 0, 10, 10)
	sq := [][]content.Point{{{X: -5, Y: 2}, {X: 4, Y: 2}, {X: 4, Y: 20}, {X: -5, Y: 20}}}
	m := newClipMask(bounds, sq, false, nil)
	if m.rect != image.Rect(0, 2, 4, 10) || len(m.rows) != 8 {
		t.Fatalf("mask rect %v with %d rows", m.rect, len(m.rows))
	}
	// A solid rectangle is one span per row, however wide.
	for y, row := range m.rows {
		if len(row) != 1 || row[0] != (clipSpan{0, 4, 255}) {
			t.Errorf("row %d = %v, want one full span", y, row)
		}
	}
	if m.at(0, 2) != 1 || m.at(5, 5) != 0 {
		t.Errorf("mask coverage: %v %v", m.at(0, 2), m.at(5, 5))
	}
	// A parent narrows both the rectangle and the coverage.
	parent := &clipMask{rect: image.Rect(1, 0, 10, 6)}
	for range 6 {
		parent.rows = append(parent.rows, []clipSpan{{1, 10, 128}})
	}
	child := newClipMask(bounds, sq, false, parent)
	if child.rect != image.Rect(1, 2, 4, 6) {
		t.Errorf("child rect = %v", child.rect)
	}
	if a := child.at(2, 3); a < 0.49 || a > 0.51 {
		t.Errorf("child coverage = %v, want half", a)
	}
	// A parent with gaps and levels of its own: the child is their product.
	patchy := &clipMask{rect: image.Rect(0, 0, 10, 10)}
	for range 10 {
		patchy.rows = append(patchy.rows, []clipSpan{{1, 3, 255}, {5, 8, 128}})
	}
	both := newClipMask(bounds, [][]content.Point{rectContour(0, 0, 10, 10)}, false, patchy)
	for x, want := range []float64{0, 1, 1, 0, 0, 0.5, 0.5, 0.5, 0, 0} {
		if got := both.at(x, 4); got < want-0.01 || got > want+0.01 {
			t.Errorf("patchy parent at x=%d = %v, want %v", x, got, want)
		}
	}
	// A path off the bounds leaves an empty mask that hides everything.
	off := newClipMask(bounds, [][]content.Point{{{X: 20, Y: 20}, {X: 30, Y: 20}, {X: 25, Y: 30}}}, false, nil)
	if !off.rect.Empty() || off.at(5, 5) != 0 {
		t.Errorf("off-bounds mask: %v", off.rect)
	}
}

// TestClipMaskMatchesADenseRasterization verifies the spans against a plain
// pixel-by-pixel coverage of the same shapes, with and without a parent mask:
// merging and intersecting runs must not change what a pixel lets through.
func TestClipMaskMatchesADenseRasterization(t *testing.T) {
	bounds := image.Rect(0, 0, 60, 50)
	rng := rand.New(rand.NewSource(7))
	shape := func() [][]content.Point {
		n := 3 + rng.Intn(5)
		poly := make([]content.Point, n)
		for i := range poly {
			poly[i] = content.Point{X: rng.Float64()*70 - 5, Y: rng.Float64()*60 - 5}
		}
		return [][]content.Point{poly}
	}
	for trial := range 40 {
		evenOdd := trial%2 == 0
		first, second := shape(), shape()
		parent := newClipMask(bounds, first, evenOdd, nil)
		child := newClipMask(bounds, second, !evenOdd, parent)

		// The reference, built the dumb way.
		dense := func(contours [][]content.Point, eo bool) map[image.Point]float64 {
			out := map[image.Point]float64{}
			scanCoverage(bounds, contours, eo, true, func(x, y int, cov float32) { out[image.Pt(x, y)] = float64(cov) })
			return out
		}
		d1, d2 := dense(first, evenOdd), dense(second, !evenOdd)
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			for x := bounds.Min.X; x < bounds.Max.X; x++ {
				p := image.Pt(x, y)
				if got, want := parent.at(x, y), d1[p]; math.Abs(got-want) > 1.0/255 {
					t.Fatalf("trial %d parent (%d,%d) = %v, want %v", trial, x, y, got, want)
				}
				if got, want := child.at(x, y), d2[p]*d1[p]; math.Abs(got-want) > 2.0/255 {
					t.Fatalf("trial %d child (%d,%d) = %v, want %v", trial, x, y, got, want)
				}
			}
		}
	}
}

func TestAxisAlignedRect(t *testing.T) {
	pt := func(x, y float64) content.Point { return content.Point{X: x, Y: y} }
	closed := []content.Point{pt(1, 2), pt(5, 2), pt(5, 6), pt(1, 6), pt(1, 2)}
	if r, ok := axisAlignedRect([][]content.Point{closed}); !ok || r != [4]float64{1, 2, 5, 6} {
		t.Errorf("closed rectangle = %v, %v", r, ok)
	}
	if _, ok := axisAlignedRect([][]content.Point{closed[:4]}); !ok {
		t.Error("an unclosed four-point rectangle is still a rectangle")
	}
	for name, contours := range map[string][][]content.Point{
		"none":      nil,
		"two":       {closed, closed},
		"triangle":  {{pt(0, 0), pt(4, 0), pt(2, 3)}},
		"diamond":   {{pt(2, 0), pt(4, 2), pt(2, 4), pt(0, 2)}},
		"five open": {{pt(0, 0), pt(4, 0), pt(4, 4), pt(0, 4), pt(1, 1)}},
	} {
		if _, ok := axisAlignedRect(contours); ok {
			t.Errorf("%s was taken for a rectangle", name)
		}
	}
}
