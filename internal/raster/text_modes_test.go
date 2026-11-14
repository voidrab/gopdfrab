package raster

import (
	"image"
	"testing"

	"github.com/voidrab/gopdfrab/internal/content"
	"github.com/voidrab/gopdfrab/internal/pdf"
)

// modePage renders content on a 60x60 page that has the Helvetica font F1.
func modePage(t *testing.T, content string) *image.RGBA {
	t.Helper()
	img, _ := renderSized(t, 60, 60, content, fontResources(helvetica(nil)))
	return img
}

// countColour counts pixels whose colour is within tol of (r, g, b).
func countColour(img *image.RGBA, r, g, b, tol int) int {
	n := 0
	for i := 0; i+3 < len(img.Pix); i += 4 {
		d := func(a uint8, want int) bool { return int(a) >= want-tol && int(a) <= want+tol }
		if d(img.Pix[i], r) && d(img.Pix[i+1], g) && d(img.Pix[i+2], b) {
			n++
		}
	}
	return n
}

func TestTextRenderModesFillAndStroke(t *testing.T) {
	show := func(mode string) *image.RGBA {
		return modePage(t, "1 0 0 rg 0 0 1 RG 2 w BT "+mode+" Tr /F1 48 Tf 5 8 Td (H) Tj ET")
	}
	fill, stroke, both := show("0"), show("1"), show("2")
	red := func(img *image.RGBA) int { return countColour(img, 255, 0, 0, 8) }
	blue := func(img *image.RGBA) int { return countColour(img, 0, 0, 255, 8) }

	if red(fill) < 100 || blue(fill) != 0 {
		t.Errorf("mode 0: %d red, %d blue pixels; want a red fill only", red(fill), blue(fill))
	}
	if red(stroke) != 0 || blue(stroke) < 50 {
		t.Errorf("mode 1: %d red, %d blue pixels; want a blue outline only", red(stroke), blue(stroke))
	}
	if red(both) < 50 || blue(both) < 50 {
		t.Errorf("mode 2: %d red, %d blue pixels; want both", red(both), blue(both))
	}
	// An outline leaves the inside of a stem unpainted; the fill paints it.
	interior := image.Point{-1, -1}
	for y := 1; y < 59 && interior.X < 0; y++ {
		for x := 1; x < 59; x++ {
			solid := true
			for dy := -1; dy <= 1 && solid; dy++ {
				for dx := -1; dx <= 1; dx++ {
					if c := fill.RGBAAt(x+dx, y+dy); c.R != 255 || c.G != 0 || c.B != 0 {
						solid = false
						break
					}
				}
			}
			if solid {
				interior = image.Pt(x, y)
				break
			}
		}
	}
	if interior.X < 0 {
		t.Fatal("the filled glyph has no interior pixel to test")
	}
	if !isWhite(t, stroke, interior.X, interior.Y) {
		t.Errorf("the outline painted the inside of the glyph at %v: %v", interior, stroke.RGBAAt(interior.X, interior.Y))
	}
	// An out-of-range mode is read as fill.
	if bad := modePage(t, "1 0 0 rg BT 9 Tr /F1 48 Tf 5 8 Td (H) Tj ET"); red(bad) != red(fill) {
		t.Errorf("Tr 9 painted %d red pixels, want the fill's %d", red(bad), red(fill))
	}
	// A negative or missing operand also means fill.
	if bad := modePage(t, "1 0 0 rg BT -2 Tr /F1 48 Tf 5 8 Td (H) Tj ET"); red(bad) != red(fill) {
		t.Errorf("Tr -2 painted %d red pixels", red(bad))
	}
}

func TestTextClipModes(t *testing.T) {
	// The glyph alone, filled black, for comparison.
	plain := modePage(t, "BT /F1 48 Tf 5 8 Td (H) Tj ET")
	wantInk, minX, minY, maxX, maxY := ink(plain)

	// Mode 7 paints nothing itself. The ET operator clips the following red
	// page-wide fill to the glyph shape.
	clipped := modePage(t, "BT 7 Tr /F1 48 Tf 5 8 Td (H) Tj ET 1 0 0 rg 0 0 60 60 re f")
	n, cMinX, cMinY, cMaxX, cMaxY := ink(clipped)
	if n < wantInk*9/10 || n > wantInk*11/10 {
		t.Errorf("clipped fill covers %d pixels, the glyph %d", n, wantInk)
	}
	if cMinX != minX || cMinY != minY || cMaxX != maxX || cMaxY != maxY {
		t.Errorf("clipped bbox (%d,%d)-(%d,%d), glyph (%d,%d)-(%d,%d)", cMinX, cMinY, cMaxX, cMaxY, minX, minY, maxX, maxY)
	}
	// The counter between the stems stays unpainted.
	mid := (minX + maxX) / 2
	if !isWhite(t, clipped, mid, minY+2) {
		t.Errorf("(%d,%d) between the stems was painted", mid, minY+2)
	}

	// Mode 4 fills and clips: the black glyph, then red over it.
	over := modePage(t, "BT 4 Tr /F1 48 Tf 5 8 Td (H) Tj ET 1 0 0 rg 0 0 60 60 re f")
	if countColour(over, 255, 0, 0, 8) < wantInk/2 || countColour(over, 0, 0, 0, 8) != 0 {
		t.Errorf("mode 4: %d red, %d black pixels", countColour(over, 255, 0, 0, 8), countColour(over, 0, 0, 0, 8))
	}
	// Modes 5 and 6 stroke and clip.
	for _, mode := range []string{"5", "6"} {
		img := modePage(t, "BT "+mode+" Tr /F1 48 Tf 5 8 Td (H) Tj ET 0 1 0 rg 0 0 60 60 re f")
		if countColour(img, 0, 255, 0, 8) < wantInk/2 {
			t.Errorf("mode %s clip left only %d green pixels", mode, countColour(img, 0, 255, 0, 8))
		}
	}

	// q and Q bring the unclipped state back.
	after := modePage(t, "q BT 7 Tr /F1 48 Tf 5 8 Td (H) Tj ET Q 1 0 0 rg 0 0 60 60 re f")
	if countColour(after, 255, 0, 0, 8) != 60*60 {
		t.Errorf("Q left a text clip: %d of %d pixels red", countColour(after, 255, 0, 0, 8), 60*60)
	}
	// Glyphs without outlines add nothing, so the clip is left alone rather
	// than closing on an empty path.
	empty := modePage(t, "BT 7 Tr /F1 12 Tf (\x01) Tj ET 1 0 0 rg 0 0 60 60 re f")
	if countColour(empty, 255, 0, 0, 8) != 60*60 {
		t.Errorf("an outline-less text clip hid the page: %d red pixels", countColour(empty, 255, 0, 0, 8))
	}
	// A text object that never ends applies no clip.
	open := modePage(t, "BT 7 Tr /F1 48 Tf 5 8 Td (H) Tj 1 0 0 rg 0 0 60 60 re f")
	if countColour(open, 255, 0, 0, 8) != 60*60 {
		t.Error("a clip applied without ET")
	}
}

// TestTextClipBelongsToItsStream verifies that executing another content
// stream, as a Type 3 glyph does, neither applies nor loses the text clip of
// the text object that invoked it.
func TestTextClipBelongsToItsStream(t *testing.T) {
	canvas := image.NewRGBA(image.Rect(0, 0, 20, 20))
	r := &renderer{canvas: canvas, fontCache: map[uintptr]*fontInfo{}}
	pending := [][]content.Point{{{X: 1, Y: 1}, {X: 5, Y: 1}, {X: 3, Y: 4}}}
	r.textClip = pending
	r.execContent([]byte("BT ET"), pdf.PDFDict{}, newRenderState(content.IdentityMatrix, [4]float64{0, 0, 20, 20}))
	if len(r.textClip) != 1 || &r.textClip[0][0] != &pending[0][0] {
		t.Errorf("textClip after a nested stream = %v", r.textClip)
	}
}

func TestClosedContoursRepeatTheFirstPoint(t *testing.T) {
	in := [][]content.Point{{{X: 1, Y: 2}, {X: 3, Y: 4}}, {{X: 5, Y: 6}}}
	out := closedContours(in)
	if len(out[0]) != 3 || out[0][2] != in[0][0] || len(out[1]) != 2 || out[1][1] != in[1][0] {
		t.Errorf("closedContours = %v", out)
	}
	if len(in[0]) != 2 {
		t.Error("the input was modified")
	}
}
