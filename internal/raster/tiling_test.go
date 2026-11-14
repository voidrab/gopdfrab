package raster

import (
	"image"
	"math"
	"testing"

	"github.com/voidrab/gopdfrab/internal/content"
	"github.com/voidrab/gopdfrab/internal/pdf"
)

// tilingPattern builds a tiling pattern with the given cell geometry and
// content; extra entries override the defaults.
func tilingPattern(paint int, bbox pdf.PDFArray, step float64, body string, extra map[string]pdf.PDFValue) pdf.PDFDict {
	e := map[string]pdf.PDFValue{
		"PatternType": pdf.PDFInteger(1), "PaintType": pdf.PDFInteger(paint), "TilingType": pdf.PDFInteger(1),
		"BBox": bbox, "XStep": pdf.PDFReal(step), "YStep": pdf.PDFReal(step),
	}
	for k, v := range extra {
		e[k] = v
	}
	return pdf.NewStreamDict(pdf.DictOf(e), []byte(body))
}

// checker is a 4x4 cell with a red square at its lower left and a blue one at
// its upper right.
func checker() pdf.PDFDict {
	return tilingPattern(1, numArray(0, 0, 4, 4), 4, "1 0 0 rg 0 0 2 2 re f 0 0 1 rg 2 2 2 2 re f", nil)
}

// renderTiled paints content over a 20x20 page whose resources hold the given
// patterns, an uncoloured-pattern colour space CS1, and an ExtGState G.
func renderTiled(t *testing.T, content string, patterns map[string]pdf.PDFValue) (*image.RGBA, []string) {
	t.Helper()
	page := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Contents": pdf.NewStreamDict(nil, []byte(content)),
	})}
	pat := map[string]pdf.PDFValue{}
	for k, v := range patterns {
		pat[k] = v
	}
	resources := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Pattern":    pdf.PDFDict{Entries: pdf.DictOf(pat)},
		"ColorSpace": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"CS1": pdf.PDFArray{pdf.PDFName{Value: "Pattern"}, pdf.PDFName{Value: "DeviceRGB"}}})},
		"ExtGState":  pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"G": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"ca": pdf.PDFReal(0.5)})}})},
		"Font":       pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"F1": helvetica(nil)})},
	})}
	img, drops, err := RenderPage(page, resources, [4]float64{0, 0, 20, 20}, 72, nil)
	if err != nil {
		t.Fatalf("RenderPage: %v", err)
	}
	return img, drops
}

func TestTilingPatternFill(t *testing.T) {
	img, drops := renderTiled(t, "/Pattern cs /P1 scn 5 5 10 10 re f", map[string]pdf.PDFValue{"P1": checker()})
	if len(drops) != 0 {
		t.Errorf("drops = %v, want none", drops)
	}
	for y := range 20 {
		for x := range 20 {
			inside := x >= 5 && x < 15 && y >= 5 && y < 15
			// Device pixel centre to pattern space, then into its 4x4 cell.
			u, v := math.Mod(float64(x)+0.5, 4), math.Mod(20-float64(y)-0.5, 4)
			switch {
			case !inside:
				if !isWhite(t, img, x, y) {
					t.Errorf("(%d,%d) outside the fill was painted", x, y)
				}
			case u < 2 && v < 2:
				if c := nrgbaAt(t, img, x, y); c.R != 255 || c.G != 0 || c.B != 0 {
					t.Errorf("(%d,%d) = %v, want red", x, y, c)
				}
			case u >= 2 && v >= 2:
				if c := nrgbaAt(t, img, x, y); c.R != 0 || c.G != 0 || c.B != 255 {
					t.Errorf("(%d,%d) = %v, want blue", x, y, c)
				}
			default:
				if !isWhite(t, img, x, y) {
					t.Errorf("(%d,%d) in the gap between squares was painted", x, y)
				}
			}
		}
	}
}

func TestUncolouredTilingPatternTakesTheSelectedColour(t *testing.T) {
	// The cell's own red is ignored: the pattern paints in the colour given to scn.
	p := tilingPattern(2, numArray(0, 0, 4, 4), 4, "1 0 0 rg 0 0 2 2 re f", nil)
	img, drops := renderTiled(t, "/CS1 cs 0 1 0 /P1 scn 0 0 20 20 re f", map[string]pdf.PDFValue{"P1": p})
	if len(drops) != 0 {
		t.Errorf("drops = %v", drops)
	}
	// Pixel (0, 19) has its centre at pattern space (0.5, 0.5): inside the square.
	if c := nrgbaAt(t, img, 0, 19); c.R != 0 || c.G != 255 || c.B != 0 {
		t.Errorf("pixel in the square = %v, want green", c)
	}
	if !isWhite(t, img, 3, 16) { // (3.5, 3.5) is outside it
		t.Errorf("pixel outside the square = %v", nrgbaAt(t, img, 3, 16))
	}
	// A different colour is a different tile.
	img, _ = renderTiled(t, "/CS1 cs 0 0 1 /P1 scn 0 0 10 20 re f /CS1 cs 1 0 0 /P1 scn 10 0 10 20 re f", map[string]pdf.PDFValue{"P1": p})
	if c := nrgbaAt(t, img, 0, 19); c.B != 255 || c.R != 0 {
		t.Errorf("left half = %v, want blue", c)
	}
	if c := nrgbaAt(t, img, 12, 19); c.R != 255 || c.B != 0 {
		t.Errorf("right half = %v, want red", c)
	}
}

func TestTilingPatternMatrix(t *testing.T) {
	// Scaled by two, the 4-unit cell is 8 pixels: the picture repeats every 8.
	scaled := tilingPattern(1, numArray(0, 0, 4, 4), 4, "1 0 0 rg 0 0 2 2 re f", map[string]pdf.PDFValue{
		"Matrix": numArray(2, 0, 0, 2, 0, 0),
	})
	img, _ := renderTiled(t, "/Pattern cs /P1 scn 0 0 20 20 re f", map[string]pdf.PDFValue{"P1": scaled})
	for y := range 12 {
		for x := range 12 {
			if nrgbaAt(t, img, x, y) != nrgbaAt(t, img, x+8, y) {
				t.Fatalf("pixels (%d,%d) and (%d,%d) differ: not 8 pixels periodic", x, y, x+8, y)
			}
		}
	}
	if c := nrgbaAt(t, img, 1, 19); c.R != 255 || c.G != 0 { // inside the 4x4 pixel square
		t.Errorf("square = %v", c)
	}
	if !isWhite(t, img, 5, 19) { // past its 4 pixels
		t.Errorf("gap = %v", nrgbaAt(t, img, 5, 19))
	}

	// Rotated by 45 degrees the cell no longer lines up with the pixels, but
	// the fill is still confined to its path and shows both ink and gaps.
	c, s := math.Cos(math.Pi/4), math.Sin(math.Pi/4)
	rot := tilingPattern(1, numArray(0, 0, 4, 4), 4, "1 0 0 rg 0 0 2 2 re f", map[string]pdf.PDFValue{
		"Matrix": numArray(c, s, -s, c, 0, 0),
	})
	img, _ = renderTiled(t, "/Pattern cs /P1 scn 4 4 12 12 re f", map[string]pdf.PDFValue{"P1": rot})
	red, white := 0, 0
	for y := 4; y < 16; y++ {
		for x := 4; x < 16; x++ {
			if isWhite(t, img, x, y) {
				white++
			} else {
				red++
			}
		}
	}
	if red < 20 || white < 20 {
		t.Errorf("rotated pattern: %d painted, %d unpainted pixels inside the fill", red, white)
	}
	if !isWhite(t, img, 2, 2) || !isWhite(t, img, 17, 17) {
		t.Error("the rotated pattern spilled outside the path")
	}
}

func TestTilingBoxLargerThanTheStepOverlaps(t *testing.T) {
	// Each cell paints a rectangle 6 wide, 2 wider than the step: with the
	// neighbours' overflow the plane is covered edge to edge.
	p := tilingPattern(1, numArray(0, 0, 6, 4), 4, "1 0 0 rg 0 0 6 4 re f", nil)
	img, _ := renderTiled(t, "/Pattern cs /P1 scn 0 0 20 20 re f", map[string]pdf.PDFValue{"P1": p})
	if n, _, _, _, _ := ink(img); n != 400 {
		t.Errorf("%d of 400 pixels painted", n)
	}
	// A box taller than the step, covering the same way vertically.
	q := tilingPattern(1, numArray(0, 0, 4, 6), 4, "0 0 1 rg 0 0 4 6 re f", nil)
	img, _ = renderTiled(t, "/Pattern cs /P1 scn 0 0 20 20 re f", map[string]pdf.PDFValue{"P1": q})
	if n, _, _, _, _ := ink(img); n != 400 {
		t.Errorf("%d of 400 pixels painted with a tall box", n)
	}
	// Content outside the box is clipped to it.
	clipped := tilingPattern(1, numArray(0, 0, 2, 2), 4, "1 0 0 rg -5 -5 20 20 re f", nil)
	img, _ = renderTiled(t, "/Pattern cs /P1 scn 0 0 20 20 re f", map[string]pdf.PDFValue{"P1": clipped})
	if n, _, _, _, _ := ink(img); n != 25*4 {
		t.Errorf("%d pixels painted, want the 2x2 box of each of 25 cells", n)
	}
}

func TestTilingPatternStrokeTextAndAlpha(t *testing.T) {
	pats := map[string]pdf.PDFValue{"P1": tilingPattern(1, numArray(0, 0, 2, 2), 2, "1 0 0 rg 0 0 2 2 re f", nil)}
	// A stroke through a Pattern colour space.
	img, _ := renderTiled(t, "/Pattern CS /P1 SCN 4 w 2 10 m 18 10 l S", pats)
	if c := nrgbaAt(t, img, 10, 9); c.R != 255 || c.G != 0 {
		t.Errorf("stroke = %v, want the pattern's red", c)
	}
	if !isWhite(t, img, 10, 3) {
		t.Error("the stroke spilled")
	}
	// Text filled with the pattern.
	img, _ = renderTiled(t, "/Pattern cs /P1 scn BT /F1 20 Tf 2 4 Td (H) Tj ET", pats)
	redPixels := countColour(img, 255, 0, 0, 8)
	if redPixels < 20 || countColour(img, 0, 0, 0, 60) != 0 {
		t.Errorf("pattern-filled text: %d red pixels, %d dark", redPixels, countColour(img, 0, 0, 0, 60))
	}
	// A fill alpha scales the pattern's own.
	img, _ = renderTiled(t, "/G gs /Pattern cs /P1 scn 0 0 20 20 re f", pats)
	if c := nrgbaAt(t, img, 5, 5); c.R != 255 || c.G < 120 || c.G > 135 {
		t.Errorf("half-transparent pattern = %v", c)
	}
}

func TestNestedTilingPattern(t *testing.T) {
	// The outer cell is filled with an inner pattern that paints green squares.
	inner := tilingPattern(1, numArray(0, 0, 2, 2), 2, "0 1 0 rg 0 0 1 1 re f", nil)
	outer := tilingPattern(1, numArray(0, 0, 8, 8), 8, "/Pattern cs /Q scn 0 0 8 8 re f", map[string]pdf.PDFValue{
		"Resources": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"Pattern": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Q": inner})},
		})},
	})
	img, drops := renderTiled(t, "/Pattern cs /P1 scn 0 0 20 20 re f", map[string]pdf.PDFValue{"P1": outer})
	if len(drops) != 0 {
		t.Errorf("drops = %v", drops)
	}
	if n := countColour(img, 0, 255, 0, 8); n < 80 {
		t.Errorf("only %d green pixels from the nested pattern", n)
	}
}

func TestSelfReferencingTilingPatternTerminates(t *testing.T) {
	// The pattern's own content paints with itself: recursion stops at the
	// nesting limit instead of overflowing the stack.
	p := tilingPattern(1, numArray(0, 0, 4, 4), 4, "/Pattern cs /P1 scn 0 0 4 4 re f 1 0 0 rg 0 0 1 1 re f", nil)
	e := p.Entries
	e.Set("Resources", pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Pattern": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"P1": p})},
	})})
	img, _ := renderTiled(t, "/Pattern cs /P1 scn 0 0 20 20 re f", map[string]pdf.PDFValue{"P1": p})
	if n := countColour(img, 255, 0, 0, 8); n == 0 {
		t.Error("the outermost cell painted nothing")
	}
}

func TestUnusableTilingPatternsReportADrop(t *testing.T) {
	cases := map[string]pdf.PDFDict{
		"zero step":      tilingPattern(1, numArray(0, 0, 4, 4), 0, "0 0 1 1 re f", nil),
		"huge step":      tilingPattern(1, numArray(0, 0, 4, 4), math.Inf(1), "0 0 1 1 re f", nil),
		"bad paint type": tilingPattern(3, numArray(0, 0, 4, 4), 4, "0 0 1 1 re f", nil),
		"empty box":      tilingPattern(1, numArray(0, 0, 0, 4), 4, "0 0 1 1 re f", nil),
		"short box":      tilingPattern(1, numArray(0, 0, 4), 4, "0 0 1 1 re f", nil),
		"no box":         tilingPattern(1, nil, 4, "0 0 1 1 re f", map[string]pdf.PDFValue{"BBox": pdf.PDFInteger(1)}),
		"missing step":   tilingPattern(1, numArray(0, 0, 4, 4), 4, "0 0 1 1 re f", map[string]pdf.PDFValue{"XStep": pdf.PDFName{Value: "x"}}),
		"not a stream": {Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"PatternType": pdf.PDFInteger(1), "PaintType": pdf.PDFInteger(1), "BBox": numArray(0, 0, 4, 4),
			"XStep": pdf.PDFInteger(4), "YStep": pdf.PDFInteger(4),
		})},
		"undecodable": tilingPattern(1, numArray(0, 0, 4, 4), 4, "garbage", map[string]pdf.PDFValue{"Filter": pdf.PDFName{Value: "FlateDecode"}}),
	}
	for name, p := range cases {
		img, drops := renderTiled(t, "/Pattern cs /P1 scn 5 5 10 10 re f", map[string]pdf.PDFValue{"P1": p})
		if !hasDrop(drops, dropTilingPattern) {
			t.Errorf("%s: drops = %v, want %q", name, drops, dropTilingPattern)
		}
		if n, _, _, _, _ := ink(img); n != 0 {
			t.Errorf("%s: painted %d pixels", name, n)
		}
	}
	// A singular pattern matrix cannot be inverted: nothing is painted.
	flat := tilingPattern(1, numArray(0, 0, 4, 4), 4, "0 0 4 4 re f", map[string]pdf.PDFValue{"Matrix": numArray(0, 0, 0, 0, 0, 0)})
	if img, _ := renderTiled(t, "/Pattern cs /P1 scn 5 5 10 10 re f", map[string]pdf.PDFValue{"P1": flat}); func() bool { n, _, _, _, _ := ink(img); return n != 0 }() {
		t.Error("a pattern with a singular matrix painted")
	}
}

func TestHugeTilingCellsAreCapped(t *testing.T) {
	// A cell far larger than the page is rendered at reduced resolution rather
	// than allocating tens of megapixels.
	p := tilingPattern(1, numArray(0, 0, 5000, 5000), 5000, "1 0 0 rg 0 0 5000 5000 re f", nil)
	img, _ := renderTiled(t, "/Pattern cs /P1 scn 0 0 20 20 re f", map[string]pdf.PDFValue{"P1": p})
	if n, _, _, _, _ := ink(img); n != 400 {
		t.Errorf("%d of 400 pixels painted", n)
	}
}

func TestTileCacheReusesAndResets(t *testing.T) {
	canvas := image.NewRGBA(image.Rect(0, 0, 20, 20))
	r := &renderer{canvas: canvas, fontCache: map[uintptr]*fontInfo{}, patternBase: content.IdentityMatrix}
	p := checker()
	tl, _ := parseTiling(p)
	first := r.tileImage(p, tl, 4, 4, [3]float64{})
	if again := r.tileImage(p, tl, 4, 4, [3]float64{}); again != first {
		t.Error("the same cell was rendered twice")
	}
	if other := r.tileImage(p, tl, 8, 8, [3]float64{}); other == first || other.Rect.Dx() != 8 {
		t.Error("a different size shared a tile")
	}
	// Past the byte budget the cache starts over.
	r.tileBytes = maxTileCacheBytes
	r.tileImage(p, tl, 2, 2, [3]float64{})
	if len(r.tiles) != 1 {
		t.Errorf("the cache holds %d tiles after a reset, want 1", len(r.tiles))
	}
	// Past the nesting limit nothing is rendered.
	r.depth = maxFormDepth + 1
	if r.tileImage(p, tl, 3, 3, [3]float64{}) != nil {
		t.Error("a tile was rendered past the nesting limit")
	}
}

func TestParseTilingNormalisesTheBox(t *testing.T) {
	p := tilingPattern(2, numArray(4, 6, 0, 2), -3, "", map[string]pdf.PDFValue{"YStep": pdf.PDFInteger(-5)})
	tl, ok := parseTiling(p)
	if !ok || tl.bbox != [4]float64{0, 2, 4, 6} || tl.xstep != 3 || tl.ystep != 5 || !tl.uncoloured {
		t.Errorf("parseTiling = %+v, %v", tl, ok)
	}
	// Shading patterns are not tiling patterns.
	if _, ok := parseTiling(axialPattern()); ok {
		t.Error("a shading pattern parsed as a tiling pattern")
	}
}

func TestTilingSamplerRejectsWhatItCannotDraw(t *testing.T) {
	r := &renderer{canvas: image.NewRGBA(image.Rect(0, 0, 4, 4)), patternBase: content.IdentityMatrix}
	if _, ok := r.tilingSampler(axialPattern(), [3]float64{}, 1); ok {
		t.Error("a shading pattern was sampled as a tiling pattern")
	}
	if _, ok := r.patternSampler(tilingPattern(1, numArray(0, 0, 4, 4), 0, "", nil), [3]float64{}, 1); ok {
		t.Error("a pattern with no step was sampled")
	}
}
