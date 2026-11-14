package raster

import (
	"image"
	"math"
	"testing"

	"github.com/voidrab/gopdfrab/internal/content"
	"github.com/voidrab/gopdfrab/internal/pdf"
)

// strokeOn strokes contours in black onto a transparent w x h canvas.
func strokeOn(w, h int, contours [][]content.Point, st StrokeStyle) *image.RGBA {
	canvas := image.NewRGBA(image.Rect(0, 0, w, h))
	StrokePathStyled(canvas, contours, st, [3]float64{0, 0, 0}, 1)
	return canvas
}

func line(pts ...float64) []content.Point {
	var out []content.Point
	for i := 0; i+1 < len(pts); i += 2 {
		out = append(out, content.Point{X: pts[i], Y: pts[i+1]})
	}
	return out
}

// wantAlpha checks the alpha at each (x, y) within a tolerance.
func wantAlpha(t *testing.T, c *image.RGBA, name string, cases [][3]float64) {
	t.Helper()
	for _, tc := range cases {
		x, y := int(tc[0]), int(tc[1])
		if got := alphaAt(c, x, y); math.Abs(got-tc[2]) > 0.02 {
			t.Errorf("%s: alpha(%d,%d) = %.3f, want %.3f", name, x, y, got, tc[2])
		}
	}
}

func TestStrokeCaps(t *testing.T) {
	seg := [][]content.Point{line(8, 10, 16, 10)}
	w := 4.0
	wantAlpha(t, strokeOn(24, 20, seg, StrokeStyle{Width: w, Cap: CapButt}), "butt", [][3]float64{
		{7, 10, 0}, {8, 10, 1}, {15, 10, 1}, {16, 10, 0}, {8, 8, 1}, {8, 7, 0},
	})
	wantAlpha(t, strokeOn(24, 20, seg, StrokeStyle{Width: w, Cap: CapSquare}), "square", [][3]float64{
		{5, 10, 0}, {6, 10, 1}, {17, 10, 1}, {18, 10, 0}, {6, 8, 1}, {17, 11, 1},
	})
	round := strokeOn(24, 20, seg, StrokeStyle{Width: w, Cap: CapRound})
	wantAlpha(t, round, "round", [][3]float64{
		{7, 10, 1}, {5, 10, 0}, {16, 10, 1}, {18, 10, 0},
	})
	// The corner of the square cap is cut off by the round one.
	if a := alphaAt(round, 6, 8); a > 0.6 {
		t.Errorf("round cap corner alpha = %.2f, want it mostly empty", a)
	}
}

func TestStrokeJoins(t *testing.T) {
	// An L turning right-then-down; the outer corner is at (17, 3) for a
	// 4-wide line whose centre bends at (15, 5).
	elbow := [][]content.Point{line(5, 5, 15, 5, 15, 15)}
	miter := strokeOn(24, 20, elbow, StrokeStyle{Width: 4, Join: JoinMiter})
	bevel := strokeOn(24, 20, elbow, StrokeStyle{Width: 4, Join: JoinBevel})
	round := strokeOn(24, 20, elbow, StrokeStyle{Width: 4, Join: JoinRound})
	wantAlpha(t, miter, "miter", [][3]float64{{16, 3, 1}, {15, 3, 1}, {16, 4, 1}})
	if a := alphaAt(bevel, 16, 3); a > 0.1 {
		t.Errorf("bevel corner alpha = %.2f, want none", a)
	}
	if a := alphaAt(round, 16, 3); a < 0.05 || a > 0.6 {
		t.Errorf("round corner alpha = %.2f, want partial", a)
	}
	// Away from the outer corner the three agree.
	for _, c := range []*image.RGBA{miter, bevel, round} {
		wantAlpha(t, c, "elbow", [][3]float64{{15, 5, 1}, {14, 6, 1}, {10, 5, 1}, {15, 10, 1}})
	}
}

func TestStrokeMiterLimit(t *testing.T) {
	// A very sharp turn: a miter would spike far beyond the line.
	spike := [][]content.Point{line(2, 10, 30, 10, 2, 12)}
	long := strokeOn(40, 20, spike, StrokeStyle{Width: 2, Join: JoinMiter, MiterLimit: 100})
	limited := strokeOn(40, 20, spike, StrokeStyle{Width: 2, Join: JoinMiter, MiterLimit: 2})
	reach := func(c *image.RGBA) int {
		for x := 39; x >= 0; x-- {
			for y := range 20 {
				if c.RGBAAt(x, y).A != 0 {
					return x
				}
			}
		}
		return -1
	}
	if reach(long) <= reach(limited)+3 {
		t.Errorf("miter reach with limit 100 = %d, with limit 2 = %d; the limit did not bevel", reach(long), reach(limited))
	}
	// A limit below one means the default of ten.
	def := strokeOn(40, 20, spike, StrokeStyle{Width: 2, Join: JoinMiter})
	if reach(def) != reach(strokeOn(40, 20, spike, StrokeStyle{Width: 2, Join: JoinMiter, MiterLimit: 10})) {
		t.Error("zero miter limit did not mean 10")
	}
}

func TestStrokeReversalJoins(t *testing.T) {
	// Doubling back on itself: a round join caps the turn with a half disc.
	back := [][]content.Point{line(6, 10, 14, 10, 6, 10)}
	round := strokeOn(24, 20, back, StrokeStyle{Width: 4, Join: JoinRound})
	wantAlpha(t, round, "round reversal", [][3]float64{{14, 10, 1}, {15, 10, 0.85}, {16, 10, 0}})
	// Miter and bevel joins add nothing for a 180 degree turn.
	for _, j := range []LineJoin{JoinMiter, JoinBevel} {
		c := strokeOn(24, 20, back, StrokeStyle{Width: 4, Join: j})
		wantAlpha(t, c, "flat reversal", [][3]float64{{13, 10, 1}, {14, 10, 0}})
	}
	// A reversal the other way round, to cover both arc directions.
	left := [][]content.Point{line(14, 10, 6, 10, 14, 10)}
	wantAlpha(t, strokeOn(24, 20, left, StrokeStyle{Width: 4, Join: JoinRound}), "round reversal left", [][3]float64{{5, 10, 1}, {4, 10, 0.85}, {3, 10, 0}})
}

func TestStrokeClosedSubpath(t *testing.T) {
	sq := [][]content.Point{line(6, 6, 14, 6, 14, 14, 6, 14, 6, 6)}
	c := strokeOn(24, 24, sq, StrokeStyle{Width: 2})
	// Every corner, including where the path closes, is mitered.
	wantAlpha(t, c, "closed", [][3]float64{{5, 5, 1}, {14, 5, 1}, {14, 14, 1}, {5, 14, 1}, {10, 10, 0}, {10, 5, 1}})
	// A closed path has no caps: butt-capped, its start corner is still whole.
	// A two-point closed contour joins back on itself.
	two := [][]content.Point{line(6, 10, 14, 10, 6, 10)}
	wantAlpha(t, strokeOn(24, 20, two, StrokeStyle{Width: 4, Join: JoinRound}), "two-point", [][3]float64{{10, 10, 1}})
}

func TestStrokeZeroLengthSubpaths(t *testing.T) {
	dot := [][]content.Point{line(10, 10)}
	wantAlpha(t, strokeOn(20, 20, dot, StrokeStyle{Width: 6, Cap: CapRound}), "round dot", [][3]float64{{10, 10, 1}, {9, 9, 1}, {13, 10, 0}})
	wantAlpha(t, strokeOn(20, 20, dot, StrokeStyle{Width: 6, Cap: CapSquare}), "square dot", [][3]float64{{7, 7, 1}, {12, 12, 1}, {13, 10, 0}})
	if n, _, _, _, _ := ink(strokeOnWhite(dot, StrokeStyle{Width: 6, Cap: CapButt})); n != 0 {
		t.Errorf("butt-capped dot painted %d pixels", n)
	}
	// Repeated points collapse to the same dot.
	same := [][]content.Point{line(10, 10, 10, 10, 10, 10)}
	wantAlpha(t, strokeOn(20, 20, same, StrokeStyle{Width: 6, Cap: CapSquare}), "repeated points", [][3]float64{{10, 10, 1}})
}

// strokeOnWhite is strokeOn onto paper white, so ink can count the result.
func strokeOnWhite(contours [][]content.Point, st StrokeStyle) *image.RGBA {
	canvas := image.NewRGBA(image.Rect(0, 0, 20, 20))
	for i := 0; i < len(canvas.Pix); i++ {
		canvas.Pix[i] = 255
	}
	StrokePathStyled(canvas, contours, st, [3]float64{0, 0, 0}, 1)
	return canvas
}

func TestStrokeDashes(t *testing.T) {
	seg := [][]content.Point{line(0, 10, 40, 10)}
	on := func(c *image.RGBA, xs ...int) []bool {
		var out []bool
		for _, x := range xs {
			out = append(out, alphaAt(c, x, 10) > 0.5)
		}
		return out
	}
	eq := func(a, b []bool) bool {
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if a[i] != b[i] {
				return false
			}
		}
		return true
	}
	probe := []int{2, 12, 22, 32}

	c := strokeOn(40, 20, seg, StrokeStyle{Width: 2, Dash: []float64{10, 10}})
	if got := on(c, probe...); !eq(got, []bool{true, false, true, false}) {
		t.Errorf("dash [10 10]: %v", got)
	}
	// A phase of 5 starts halfway through the first dash.
	c = strokeOn(40, 20, seg, StrokeStyle{Width: 2, Dash: []float64{10, 10}, DashPhase: 5})
	if got := on(c, 2, 7, 12, 17, 22); !eq(got, []bool{true, false, false, true, true}) {
		t.Errorf("dash phase 5: %v", got)
	}
	// A negative phase wraps; a phase past the period too.
	neg := strokeOn(40, 20, seg, StrokeStyle{Width: 2, Dash: []float64{10, 10}, DashPhase: -15})
	big := strokeOn(40, 20, seg, StrokeStyle{Width: 2, Dash: []float64{10, 10}, DashPhase: 25})
	if !eq(on(neg, 2, 7, 12, 17), on(big, 2, 7, 12, 17)) {
		t.Error("equivalent phases differ")
	}
	// A single length is on, then off for the same length.
	c = strokeOn(40, 20, seg, StrokeStyle{Width: 2, Dash: []float64{10}})
	if got := on(c, probe...); !eq(got, []bool{true, false, true, false}) {
		t.Errorf("dash [10]: %v", got)
	}
	// Three lengths alternate on/off across repeats: on 4, off 4, on 4, off 4...
	c = strokeOn(40, 20, seg, StrokeStyle{Width: 2, Dash: []float64{4, 4, 4}})
	if got := on(c, 2, 6, 10, 14, 18, 22); !eq(got, []bool{true, false, true, false, true, false}) {
		t.Errorf("dash [4 4 4]: %v", got)
	}
	// A dash crossing a corner bends with it.
	bend := [][]content.Point{line(2, 4, 12, 4, 12, 14)}
	cb := strokeOn(20, 20, bend, StrokeStyle{Width: 2, Dash: []float64{16, 100}})
	wantAlpha(t, cb, "dash across a corner", [][3]float64{{5, 4, 1}, {12, 8, 1}, {12, 12, 0}})
	// A closed contour is dashed from its start.
	sq := [][]content.Point{line(2, 2, 18, 2, 18, 18, 2, 18, 2, 2)}
	cs := strokeOn(20, 20, sq, StrokeStyle{Width: 2, Dash: []float64{8, 8}})
	wantAlpha(t, cs, "dashed square", [][3]float64{{5, 2, 1}, {14, 2, 0}})
}

func TestStrokeDashEdgeCases(t *testing.T) {
	seg := [][]content.Point{line(0, 10, 40, 10)}
	solid := strokeOn(40, 20, seg, StrokeStyle{Width: 2})
	for name, dash := range map[string][]float64{
		"all zero":   {0, 0},
		"negative":   {5, -1},
		"not finite": {5, math.NaN()},
		"infinite":   {math.Inf(1), 5},
		"too fine":   {0.001, 0.001},
	} {
		c := strokeOn(40, 20, seg, StrokeStyle{Width: 2, Dash: dash})
		if string(c.Pix) != string(solid.Pix) {
			t.Errorf("%s dash did not fall back to a solid line", name)
		}
	}
	// Zero-length dashes with round caps draw dots along the line.
	dots := strokeOn(40, 20, seg, StrokeStyle{Width: 4, Cap: CapRound, Dash: []float64{0, 10}})
	wantAlpha(t, dots, "dots", [][3]float64{{0, 10, 1}, {10, 10, 1}, {5, 10, 0}, {15, 10, 0}})
	// A dash with a zero entry before the phase is skipped over.
	skip := strokeOn(40, 20, seg, StrokeStyle{Width: 2, Dash: []float64{0, 10}, DashPhase: 3})
	wantAlpha(t, skip, "skipped zero", [][3]float64{{12, 10, 0}})
	// A single point cannot be dashed.
	if got := dashed([]content.Point{{X: 1, Y: 1}}, false, StrokeStyle{Dash: []float64{1, 1}}); len(got) != 1 {
		t.Errorf("dashed(point) = %v", got)
	}
}

func TestStrokeIgnoresBadPoints(t *testing.T) {
	bad := [][]content.Point{{{X: 2, Y: 10}, {X: math.NaN(), Y: 3}, {X: 8, Y: 10}, {X: math.Inf(1), Y: 0}, {X: 14, Y: 10}}}
	c := strokeOn(20, 20, bad, StrokeStyle{Width: 2})
	wantAlpha(t, c, "bad points", [][3]float64{{3, 10, 1}, {10, 10, 1}, {13, 10, 1}})
	if outline := strokeOutline([][]content.Point{{{X: math.NaN(), Y: 1}}}, StrokeStyle{Width: 2}); len(outline) != 0 {
		t.Errorf("outline of only bad points = %v", outline)
	}
}

func TestStrokeDirectionDoesNotMatter(t *testing.T) {
	fwd := strokeOn(30, 30, [][]content.Point{line(4, 6, 20, 8, 12, 24, 26, 26)}, StrokeStyle{Width: 3, Join: JoinRound, Cap: CapSquare})
	rev := strokeOn(30, 30, [][]content.Point{line(26, 26, 12, 24, 20, 8, 4, 6)}, StrokeStyle{Width: 3, Join: JoinRound, Cap: CapSquare})
	diff := 0
	for i := range fwd.Pix {
		if d := int(fwd.Pix[i]) - int(rev.Pix[i]); d > 3 || d < -3 {
			diff++
		}
	}
	if diff != 0 {
		t.Errorf("%d samples differ between a path and its reverse", diff)
	}
}

func TestOrientedDropsDegeneratePolygons(t *testing.T) {
	if oriented([]content.Point{{X: 0, Y: 0}, {X: 1, Y: 1}, {X: 2, Y: 2}}) {
		t.Error("a collinear polygon has no area")
	}
	cw := []content.Point{{X: 0, Y: 0}, {X: 0, Y: 1}, {X: 1, Y: 1}, {X: 1, Y: 0}}
	if !oriented(cw) || cw[1].X != 1 { // reversed in place to wind positively
		t.Errorf("polygon not reversed: %v", cw)
	}
}

// strokeContent renders a content stream on the 20x20 test page.
func strokeContent(t *testing.T, content string, resources pdf.PDFDict) *image.RGBA {
	t.Helper()
	return render20(t, content, resources)
}

func TestStrokeStateOperators(t *testing.T) {
	// User y is up: a horizontal line at user y=10 is device row 9..10.
	rowOn := func(img *image.RGBA, y int, xs ...int) []bool {
		var out []bool
		for _, x := range xs {
			out = append(out, nrgbaAt(t, img, x, y).A != 0 && !isWhite(t, img, x, y))
		}
		return out
	}
	all := func(b []bool, want bool) bool {
		for _, v := range b {
			if v != want {
				return false
			}
		}
		return true
	}

	// d: dash [4 4] gives on at 0..3, off at 4..7.
	img := strokeContent(t, "0 0 0 RG 2 w [4 4] 0 d 0 10 m 20 10 l S", pdf.PDFDict{})
	if got := rowOn(img, 10, 1, 2); !all(got, true) {
		t.Errorf("dash on part: %v", got)
	}
	if got := rowOn(img, 10, 5, 6); !all(got, false) {
		t.Errorf("dash off part: %v", got)
	}
	// J: a round or projecting cap extends past the endpoint, butt does not.
	for op, wantReach := range map[string]bool{"0": false, "1": true, "2": true} {
		img := strokeContent(t, "0 0 0 RG 6 w "+op+" J 6 10 m 14 10 l S", pdf.PDFDict{})
		if got := rowOn(img, 10, 4)[0]; got != wantReach {
			t.Errorf("cap %s: pixel past the end painted = %v, want %v", op, got, wantReach)
		}
	}
	// j and M: the outer corner of a right angle (device pixel 16,18) is filled
	// by a miter, not a bevel.
	miter := strokeContent(t, "0 0 0 RG 6 w 0 j 4 4 m 14 4 l 14 14 l S", pdf.PDFDict{})
	bevel := strokeContent(t, "0 0 0 RG 6 w 2 j 4 4 m 14 4 l 14 14 l S", pdf.PDFDict{})
	if isWhite(t, miter, 16, 18) || !isWhite(t, bevel, 16, 18) {
		t.Errorf("miter corner white=%v, bevel corner white=%v", isWhite(t, miter, 16, 18), isWhite(t, bevel, 16, 18))
	}
	limited := strokeContent(t, "0 0 0 RG 6 w 0 j 1 M 4 4 m 14 4 l 14 14 l S", pdf.PDFDict{})
	if !isWhite(t, limited, 16, 18) {
		t.Error("a miter limit of 1 should bevel every corner")
	}
	// d with a missing operand is ignored; q/Q restores the dash.
	if got := rowOn(strokeContent(t, "0 0 0 RG 2 w [4 4] d 0 10 m 20 10 l S", pdf.PDFDict{}), 10, 5, 6); !all(got, true) {
		t.Errorf("malformed d changed the stroke: %v", got)
	}
	img = strokeContent(t, "0 0 0 RG 2 w q [4 4] 0 d Q 0 10 m 20 10 l S", pdf.PDFDict{})
	if got := rowOn(img, 10, 5, 6); !all(got, true) {
		t.Errorf("Q did not restore the solid line: %v", got)
	}
	// d with a non-array first operand strokes solid.
	if got := rowOn(strokeContent(t, "0 0 0 RG 2 w 3 0 d 0 10 m 20 10 l S", pdf.PDFDict{}), 10, 5, 6); !all(got, true) {
		t.Errorf("non-array dash: %v", got)
	}
}

func TestStrokeExtGState(t *testing.T) {
	egs := func(entries map[string]pdf.PDFValue) pdf.PDFDict {
		return resourcesWith("ExtGState", map[string]pdf.PDFValue{"G": pdf.PDFDict{Entries: pdf.DictOf(entries)}})
	}
	rowOn := func(img *image.RGBA, x int) bool { return !isWhite(t, img, x, 10) }

	dashed := render20(t, "0 0 0 RG /G gs 0 10 m 20 10 l S", egs(map[string]pdf.PDFValue{
		"LW": pdf.PDFInteger(2),
		"D":  pdf.PDFArray{numArray(4, 4), pdf.PDFInteger(0)},
	}))
	if !rowOn(dashed, 1) || rowOn(dashed, 5) {
		t.Error("ExtGState /D and /LW were not applied")
	}
	capped := render20(t, "0 0 0 RG /G gs 6 10 m 14 10 l S", egs(map[string]pdf.PDFValue{
		"LW": pdf.PDFInteger(6), "LC": pdf.PDFInteger(2),
	}))
	if !rowOn(capped, 4) {
		t.Error("ExtGState /LC square cap was not applied")
	}
	joined := render20(t, "0 0 0 RG /G gs 4 4 m 14 4 l 14 14 l S", egs(map[string]pdf.PDFValue{
		"LW": pdf.PDFInteger(6), "LJ": pdf.PDFInteger(2), "ML": pdf.PDFInteger(3),
	}))
	if !isWhite(t, joined, 16, 18) {
		t.Error("ExtGState /LJ bevel join was not applied")
	}
	// A malformed /D (wrong length) is ignored.
	ignored := render20(t, "0 0 0 RG 2 w /G gs 0 10 m 20 10 l S", egs(map[string]pdf.PDFValue{
		"D": pdf.PDFArray{numArray(4, 4)},
	}))
	if !rowOn(ignored, 5) {
		t.Error("a malformed /D changed the stroke")
	}
}

func TestLineStyleConversions(t *testing.T) {
	if lineCapOf(0) != CapButt || lineCapOf(1) != CapRound || lineCapOf(2) != CapSquare || lineCapOf(9) != CapButt {
		t.Error("lineCapOf")
	}
	if lineJoinOf(0) != JoinMiter || lineJoinOf(1) != JoinRound || lineJoinOf(2) != JoinBevel || lineJoinOf(9) != JoinMiter {
		t.Error("lineJoinOf")
	}
	if dashArray(pdf.PDFInteger(3)) != nil {
		t.Error("dashArray of a non-array")
	}
	if got := dashArray(pdf.PDFArray{pdf.PDFInteger(3), pdf.PDFReal(1.5)}); len(got) != 2 || got[1] != 1.5 {
		t.Errorf("dashArray = %v", got)
	}
	gs := newRenderState(content.Matrix{A: 2, D: 2}, [4]float64{})
	gs.lineWidth, gs.dash, gs.dashPhase = 3, []float64{1, 2}, 0.5
	st := gs.strokeStyle()
	if st.Width != 6 || st.Dash[1] != 4 || st.DashPhase != 1 || st.MiterLimit != 10 {
		t.Errorf("strokeStyle = %+v", st)
	}
	if r := gs.strokeReach(); r != 30 { // half of 6, times the default limit of 10
		t.Errorf("miter reach = %v", r)
	}
	gs.lineJoin = JoinRound
	if r := gs.strokeReach(); math.Abs(r-3*math.Sqrt2) > 1e-9 {
		t.Errorf("round reach = %v", r)
	}
}
