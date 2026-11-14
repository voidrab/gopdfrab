package raster

import (
	"image"
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
	// Two shape clips multiply: a second triangle covering the right half.
	two := render20(t, triClip+"10 0 m 20 0 l 20 20 l h W n 0 0 0 rg 0 0 20 20 re f", pdf.PDFDict{})
	if !isBlack(t, two, 13, 14) || !isWhite(t, two, 6, 16) || !isWhite(t, two, 3, 5) {
		t.Error("two shape clips did not intersect")
	}
	// Q brings the unclipped state back.
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
	// Axial and mesh shadings painted with sh.
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
	m := &clipMask{rect: image.Rect(2, 3, 4, 5), cov: []uint8{0, 255, 51, 255}}
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

func TestNewClipMaskBounds(t *testing.T) {
	bounds := image.Rect(0, 0, 10, 10)
	sq := [][]content.Point{{{X: -5, Y: 2}, {X: 4, Y: 2}, {X: 4, Y: 20}, {X: -5, Y: 20}}}
	m := newClipMask(bounds, sq, false, nil)
	if m.rect != image.Rect(0, 2, 4, 10) || len(m.cov) != 32 {
		t.Fatalf("mask rect %v with %d samples", m.rect, len(m.cov))
	}
	if m.at(0, 2) != 1 || m.at(5, 5) != 0 {
		t.Errorf("mask coverage: %v %v", m.at(0, 2), m.at(5, 5))
	}
	// A parent narrows both the rectangle and the coverage.
	parent := &clipMask{rect: image.Rect(1, 0, 10, 6), cov: make([]uint8, 9*6)}
	for i := range parent.cov {
		parent.cov[i] = 128
	}
	child := newClipMask(bounds, sq, false, parent)
	if child.rect != image.Rect(1, 2, 4, 6) {
		t.Errorf("child rect = %v", child.rect)
	}
	if a := child.at(2, 3); a < 0.49 || a > 0.51 {
		t.Errorf("child coverage = %v, want half", a)
	}
	// A path off the bounds leaves an empty mask that hides everything.
	off := newClipMask(bounds, [][]content.Point{{{X: 20, Y: 20}, {X: 30, Y: 20}, {X: 25, Y: 30}}}, false, nil)
	if !off.rect.Empty() || off.at(5, 5) != 0 {
		t.Errorf("off-bounds mask: %v", off.rect)
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
