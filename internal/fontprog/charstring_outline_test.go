package fontprog

import "testing"

// Point is a point of a recorded outline.
type Point struct{ X, Y float64 }

// recSink records an outline as contours of the points it passes through:
// each MoveTo starts a contour, and lines and curves add their end point.
// Curves are not flattened, so a curve adds one point.
type recSink struct {
	contours [][]Point
	cur      []Point
}

func (s *recSink) MoveTo(x, y float64) {
	s.closeContour()
	s.cur = []Point{{x, y}}
}

func (s *recSink) LineTo(x, y float64) { s.cur = append(s.cur, Point{x, y}) }

func (s *recSink) CurveTo(_, _, _, _, x3, y3 float64) { s.cur = append(s.cur, Point{x3, y3}) }

// closeContour keeps the open contour if it draws anything.
func (s *recSink) closeContour() {
	if len(s.cur) > 1 {
		s.contours = append(s.contours, s.cur)
	}
	s.cur = nil
}

func (s *recSink) result() [][]Point {
	s.closeContour()
	return s.contours
}

// type2Contours draws a Type 2 charstring calling into gsubrs and lsubrs.
func type2Contours(cs []byte, gsubrs, lsubrs [][]byte) [][]Point {
	var s recSink
	Type2Outline(cs, gsubrs, lsubrs, &s)
	return s.result()
}

// interpretType2Charstring draws a Type 2 charstring that calls no
// subroutines.
func interpretType2Charstring(cs []byte) [][]Point { return type2Contours(cs, nil, nil) }

// interpretType1Charstring draws a lone Type 1 charstring: one with no
// Subrs to call and no glyphs for seac to compose.
func interpretType1Charstring(cs []byte) [][]Point {
	return t1outlineOK(t1(map[string][]byte{"g": cs}), "g")
}

func t1outlineOK(p *Type1Charstrings, name string) [][]Point {
	var s recSink
	p.Outline(name, &s)
	return s.result()
}

// cs concatenates charstring fragments.
func cs(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// nums encodes small charstring operands (-107..107), valid in both Type1
// and Type2.
func nums(vs ...int) []byte {
	out := make([]byte, len(vs))
	for i, v := range vs {
		out[i] = b139(v)
	}
	return out
}

func lastPoint(t *testing.T, contours [][]Point) Point {
	t.Helper()
	if len(contours) == 0 {
		t.Fatal("no contours")
	}
	c := contours[len(contours)-1]
	return c[len(c)-1]
}

// TestType2Subroutines pins that callsubr and callgsubr apply the subroutine
// bias to their operand, that return resumes the caller, and that endchar
// inside a subroutine ends the glyph. Subset CFF fonts draw most of their
// outlines through subroutines.
func TestType2Subroutines(t *testing.T) {
	// Fewer than 1240 subrs: an operand of n-107 names subr n.
	lsubrs := [][]byte{cs(nums(50, 0), []byte{5, 11})} // 50 0 rlineto return
	gsubrs := [][]byte{cs(nums(0, 50), []byte{5, 11})} // 0 50 rlineto return
	run := func(prog []byte) [][]Point {
		return type2Contours(prog, gsubrs, lsubrs)
	}

	got := run(cs(nums(10, 10), []byte{21}, // rmoveto
		nums(-107), []byte{10}, // callsubr 0
		nums(-107), []byte{29}, // callgsubr 0
		nums(-50, 0), []byte{5, 14}, // rlineto, endchar
		nums(0, -50), []byte{5})) // after endchar: never drawn
	want := []Point{{10, 10}, {60, 10}, {60, 60}, {10, 60}}
	if len(got) != 1 || len(got[0]) != len(want) {
		t.Fatalf("contours = %v, want %v", got, want)
	}
	for i, p := range want {
		if got[0][i] != p {
			t.Errorf("point %d = %v, want %v", i, got[0][i], p)
		}
	}

	// endchar inside a subroutine ends the glyph, not just the subroutine.
	lsubrs = [][]byte{cs(nums(50, 0), []byte{5, 14})}
	if got := run(cs(nums(0, 0), []byte{21}, nums(-107), []byte{10}, nums(0, 50), []byte{5})); lastPoint(t, got) != (Point{50, 0}) {
		t.Errorf("drawing continued after endchar in a subroutine: %v", got)
	}

	// A call that cannot be followed ends the glyph: no operand, an index
	// outside the INDEX, or nesting past the limit (a subr calling itself).
	lsubrs = [][]byte{cs(nums(-107), []byte{10})}
	for name, call := range map[string][]byte{
		"empty stack":  {10},
		"out of range": cs(nums(-100), []byte{10}),
		"recursion":    cs(nums(-107), []byte{10}),
	} {
		got := run(cs(nums(0, 0), []byte{21}, nums(50, 0), []byte{5}, call, nums(0, 50), []byte{5}))
		if lastPoint(t, got) != (Point{50, 0}) {
			t.Errorf("%s: drawing continued past the failed call: %v", name, got)
		}
	}
}

func TestCFFCharstringsOutline(t *testing.T) {
	p := &CFFCharstrings{
		charStrings: [][]byte{cs(nums(0, 0), []byte{21}, nums(-107), []byte{29}, []byte{14})},
		subrs:       CFFSubrs{Global: [][]byte{cs(nums(30, 0), []byte{5, 11})}},
	}
	var s recSink
	if ok := p.Outline(0, &s); !ok || lastPoint(t, s.result()) != (Point{30, 0}) {
		t.Errorf("Outline(0) = %v %v, want the global subr's line", s.contours, ok)
	}
	for _, gid := range []int{-1, 1} {
		if ok := p.Outline(gid, &recSink{}); ok {
			t.Errorf("outline(%d) accepted a glyph ID outside the CharStrings", gid)
		}
	}
}

// TestType2Flex pins the four flex operators, each of which draws two curves.
func TestType2Flex(t *testing.T) {
	cases := []struct {
		name string
		op   byte
		args []int
		mid  Point // end of the first curve
		end  Point
	}{
		{"flex", 35, []int{10, 0, 10, 10, 10, 0, 10, 0, 10, -10, 10, 0, 50}, Point{30, 10}, Point{60, 0}},
		{"hflex", 34, []int{10, 10, 10, 10, 10, 10, 10}, Point{30, 10}, Point{60, 0}},
		{"hflex1", 36, []int{10, 5, 10, 5, 10, 10, 10, 5, 10}, Point{30, 10}, Point{60, 0}},
		{"flex1 horizontal", 37, []int{10, 5, 10, 5, 10, 0, 10, -5, 10, 0, 10}, Point{30, 10}, Point{60, 0}},
		{"flex1 vertical", 37, []int{5, 10, 5, 10, 0, 10, -5, 10, 0, 10, 10}, Point{10, 30}, Point{0, 60}},
	}
	for _, tc := range cases {
		got := interpretType2Charstring(cs(nums(0, 0), []byte{21}, nums(tc.args...), []byte{12, tc.op, 14}))
		if lastPoint(t, got) != tc.end {
			t.Errorf("%s: ends at %v, want %v", tc.name, lastPoint(t, got), tc.end)
		}
		hasMid := false
		for _, p := range got[0] {
			hasMid = hasMid || p == tc.mid
		}
		if !hasMid {
			t.Errorf("%s: no point at the joint %v: %v", tc.name, tc.mid, got[0])
		}
		// Too few operands draw nothing. Two are dropped, because flex's
		// last operand, the flex depth, is not needed to draw.
		if got := interpretType2Charstring(cs(nums(0, 0), []byte{21}, nums(tc.args[2:]...), []byte{12, tc.op, 14})); len(got) != 0 {
			t.Errorf("%s with too few operands drew %v", tc.name, got)
		}
	}
	// Other escapes and a truncated escape draw nothing.
	for _, prog := range [][]byte{cs(nums(0, 0), []byte{21}, nums(1, 2), []byte{12, 3}), cs(nums(0, 0), []byte{21, 12})} {
		if got := interpretType2Charstring(prog); len(got) != 0 {
			t.Errorf("%v drew %v", prog, got)
		}
	}
}

// t1 builds a Type1 program from glyph and subr charstrings.
func t1(glyphs map[string][]byte, subrs ...[]byte) *Type1Charstrings {
	return &Type1Charstrings{glyphs: glyphs, subrs: subrs}
}

const (
	t1CallSubr        = 10
	t1Return          = 11
	t1Endchar         = 14
	t1Rmoveto         = 21
	t1Rlineto         = 5
	t1Vlineto         = 7
	t1Hsbw            = 13
	t1Esc             = 12
	t1Seac            = 6
	t1CallOtherSubr   = 16
	t1Pop             = 17
	t1SetCurrentPoint = 33
)

func t1outline(t *testing.T, p *Type1Charstrings, name string) [][]Point {
	t.Helper()
	var s recSink
	if !p.Outline(name, &s) {
		t.Fatalf("Outline(%q) failed", name)
	}
	return s.result()
}

// TestType1Subroutines pins callsubr and return against the font's Subrs,
// and that a call which cannot be followed ends the glyph.
func TestType1Subroutines(t *testing.T) {
	line := cs(nums(50, 0), []byte{t1Rlineto, t1Return})
	end := cs(nums(50, 0), []byte{t1Rlineto, t1Endchar})
	self := cs(nums(3), []byte{t1CallSubr})
	head := cs(nums(0, 100), []byte{t1Hsbw}, nums(0, 0), []byte{t1Rmoveto})
	tail := cs(nums(50), []byte{t1Vlineto, t1Endchar})
	glyph := func(call []byte) []byte { return cs(head, call, tail) }

	p := t1(map[string][]byte{
		"ok":    glyph(cs(nums(0), []byte{t1CallSubr})),
		"ender": glyph(cs(nums(1), []byte{t1CallSubr})),
		"none":  glyph([]byte{t1CallSubr}),
		"high":  glyph(cs(nums(9), []byte{t1CallSubr})),
		"low":   glyph(cs(nums(-1), []byte{t1CallSubr})),
		"hole":  glyph(cs(nums(2), []byte{t1CallSubr})),
		"cycle": glyph(cs(nums(3), []byte{t1CallSubr})),
		"fall":  glyph(cs(nums(4), []byte{t1CallSubr})),
	}, line, end, nil, self, cs(nums(50, 0), []byte{t1Rlineto}))

	// A subr that runs off its end without return resumes the caller too.
	for _, name := range []string{"ok", "fall"} {
		if got := lastPoint(t, t1outline(t, p, name)); got != (Point{50, 50}) {
			t.Errorf("%s: ends at %v, want {50 50} (subr's line, then the caller's)", name, got)
		}
	}
	for _, name := range []string{"ender", "none", "high", "low", "hole", "cycle"} {
		want := Point{0, 0}
		if name == "ender" {
			want = Point{50, 0}
		}
		got := t1outline(t, p, name)
		if len(got) > 0 && lastPoint(t, got) != want {
			t.Errorf("%s: drawing continued past the call: %v", name, got)
		}
	}
	if p.Outline("absent", &recSink{}) {
		t.Error("an undefined glyph name yielded an outline")
	}
}

// callOther encodes "args... n which callothersubr".
func callOther(which int, args ...int) []byte {
	return cs(nums(args...), nums(len(args), which), []byte{t1Esc, t1CallOtherSubr})
}

// TestType1Flex pins the OtherSubrs flex protocol: inside a flex, rmoveto
// records points instead of moving, and OtherSubr 0 draws the two curves
// from where the flex started, then hands the end point to setcurrentpoint.
func TestType1Flex(t *testing.T) {
	rm := func(dx, dy int) []byte {
		return cs(nums(dx, dy), []byte{t1Rmoveto}, callOther(2))
	}
	prog := cs(nums(0, 100), []byte{t1Hsbw}, nums(0, 0), []byte{t1Rmoveto},
		callOther(1),
		rm(30, 10),  // reference point (30,10)
		rm(-20, 0),  // (10,10)
		rm(10, 10),  // (20,20)
		rm(10, 0),   // (30,20): joint
		rm(10, 0),   // (40,20)
		rm(10, -10), // (50,10)
		rm(10, -10), // (60,0): end
		callOther(0, 50, 60, 0),
		[]byte{t1Esc, t1Pop, t1Esc, t1Pop, t1Esc, t1SetCurrentPoint},
		nums(30), []byte{t1Vlineto, t1Endchar})
	got := t1outline(t, t1(map[string][]byte{"g": prog}), "g")
	if len(got) != 1 {
		t.Fatalf("contours = %v, want one", got)
	}
	c := got[0]
	if c[0] != (Point{0, 0}) {
		t.Errorf("starts at %v, want the point the flex started from", c[0])
	}
	hasJoint := false
	for _, p := range c {
		hasJoint = hasJoint || p == (Point{30, 20})
		if p == (Point{30, 10}) {
			t.Error("the reference point was drawn")
		}
	}
	if !hasJoint {
		t.Errorf("no point at the joint {30 20}: %v", c)
	}
	if last := c[len(c)-1]; last != (Point{60, 30}) {
		t.Errorf("ends at %v, want {60 30}: the line after setcurrentpoint(60, 0)", last)
	}

	// A flex end without its seven points draws nothing and pushes no
	// result for pop.
	short := cs(nums(0, 100), []byte{t1Hsbw}, nums(0, 0), []byte{t1Rmoveto},
		callOther(1), rm(5, 5), callOther(0, 50, 60), nums(10, 0), []byte{t1Rlineto, t1Endchar})
	got = t1outline(t, t1(map[string][]byte{"g": short}), "g")
	if lastPoint(t, got) != (Point{15, 5}) {
		t.Errorf("short flex: ends at %v, want {15 5} (the recorded point, then a plain line)", lastPoint(t, got))
	}
}

// TestType1OtherSubrResults pins what pop returns: hint replacement
// (OtherSubr 3) hands back its subr number, an unknown OtherSubr its
// arguments in order, and an empty result stack 0.
func TestType1OtherSubrResults(t *testing.T) {
	hint := cs(nums(5, 5), []byte{1, t1Return}) // hstem; return
	head := cs(nums(0, 100), []byte{t1Hsbw}, nums(0, 0), []byte{t1Rmoveto})
	p := t1(map[string][]byte{
		"hint":    cs(head, callOther(3, 3), []byte{t1Esc, t1Pop, t1CallSubr}, nums(10, 0), []byte{t1Rlineto, t1Endchar}),
		"unknown": cs(head, callOther(7, 11, 22), []byte{t1Esc, t1Pop, t1Esc, t1Pop, t1Rlineto, t1Endchar}),
		"empty":   cs(head, []byte{t1Esc, t1Pop}, nums(4), []byte{t1Rlineto, t1Endchar}),
		"short":   cs(head, nums(1), []byte{t1Esc, t1CallOtherSubr}, nums(10, 0), []byte{t1Rlineto, t1Endchar}),
		"badN":    cs(head, nums(9, 7), []byte{t1Esc, t1CallOtherSubr}, nums(10, 0), []byte{t1Rlineto, t1Endchar}),
		"setpt":   cs(head, nums(1), []byte{t1Esc, t1SetCurrentPoint}, nums(10, 0), []byte{t1Rlineto, t1Endchar}),
	}, nil, nil, nil, hint)
	for name, want := range map[string]Point{
		"hint":    {10, 0},  // pop gave 3, callsubr 3 ran the hint subr, drawing resumed
		"unknown": {11, 22}, // the arguments came back in order
		"empty":   {0, 4},   // pop gave 0: rlineto 0 4
		"short":   {10, 0},  // too few operands: ignored
		"badN":    {10, 0},  // more arguments claimed than present: ignored
		"setpt":   {10, 0},  // setcurrentpoint with one operand: ignored
	} {
		if got := lastPoint(t, t1outline(t, p, name)); got != want {
			t.Errorf("%s: ends at %v, want %v", name, got, want)
		}
	}
}

// TestType1Seac pins standard accented-character composition: the base and
// accent glyphs, named through StandardEncoding, with the accent offset by
// (adx-asb, ady).
func TestType1Seac(t *testing.T) {
	const acute = 0xC2 // StandardEncoding "acute"
	box := func(sbx int) []byte {
		return cs(nums(sbx, 100), []byte{t1Hsbw}, nums(0, 0), []byte{t1Rmoveto}, nums(10, 0), []byte{t1Rlineto}, nums(10), []byte{t1Vlineto, t1Endchar})
	}
	// achar 194 needs the two-byte operand form: 247 v1 = 108 + v1.
	seac := func(bchar int, achar int) []byte {
		return cs(nums(0, 100), []byte{t1Hsbw}, nums(20, 60, 70, bchar), []byte{247, byte(achar - 108)}, []byte{t1Esc, t1Seac})
	}
	p := t1(map[string][]byte{
		"A":       box(0),
		"acute":   box(20),
		"Aacute":  seac('A', acute),
		"noBase":  seac('Z', acute),
		"nested":  seac('B', acute),
		"B":       seac('A', acute),
		"short":   cs(nums(1, 2), []byte{t1Esc, t1Seac}),
		"badCode": cs(nums(0, 100), []byte{t1Hsbw}, nums(20, 60, 70, -1, 65), []byte{t1Esc, t1Seac}),
	})

	got := t1outline(t, p, "Aacute")
	if len(got) != 2 {
		t.Fatalf("Aacute contours = %v, want base and accent", got)
	}
	if got[0][0] != (Point{0, 0}) {
		t.Errorf("base starts at %v, want {0 0}", got[0][0])
	}
	// The accent's own hsbw sbx (20) lands it at adx-asb+sbx = 60-20+20.
	if got[1][0] != (Point{60, 70}) {
		t.Errorf("accent starts at %v, want {60 70}", got[1][0])
	}

	if got := t1outline(t, p, "noBase"); len(got) != 1 || got[0][0] != (Point{60, 70}) {
		t.Errorf("noBase = %v, want just the accent", got)
	}
	// A seac component that is itself a seac is not composed again.
	if got := t1outline(t, p, "nested"); len(got) != 1 || got[0][0] != (Point{60, 70}) {
		t.Errorf("nested = %v, want just the accent", got)
	}
	for _, name := range []string{"short", "badCode"} {
		if got := t1outline(t, p, name); len(got) != 0 {
			t.Errorf("%s drew %v", name, got)
		}
	}
	// A lone charstring has no glyphs to compose from.
	if got := interpretType1Charstring(seac('A', acute)); len(got) != 0 {
		t.Errorf("seac without a program drew %v", got)
	}
}

func TestType1DivShortStack(t *testing.T) {
	// div with one operand leaves it; div by zero drops both.
	got := interpretType1Charstring(cs(nums(0, 100), []byte{t1Hsbw}, nums(0, 0), []byte{t1Rmoveto},
		nums(7), []byte{t1Esc, 12}, []byte{6}, // hlineto 7
		nums(3, 0), []byte{t1Esc, 12}, nums(5), []byte{t1Vlineto, t1Endchar}))
	if lastPoint(t, got) != (Point{7, 5}) {
		t.Errorf("ends at %v, want {7 5}", lastPoint(t, got))
	}
}

func TestType1HsbwShortStack(t *testing.T) {
	// Drawing goes on past an hsbw that lacks its sbx, from the origin.
	got := interpretType1Charstring(cs(nums(100), []byte{t1Hsbw}, nums(10, 10), []byte{t1Rmoveto},
		nums(7), []byte{6, t1Endchar})) // hlineto 7
	if lastPoint(t, got) != (Point{17, 10}) {
		t.Errorf("ends at %v, want {17 10}", lastPoint(t, got))
	}
}

func TestInterpretType2CharstringHVCurve(t *testing.T) {
	enc := func(v int) byte { return byte(v + 139) }
	// 0 0 moveto, then a hvcurveto: dx1 dx2 dy2 dy3 (horizontal start) from (0,0).
	program := []byte{
		enc(0), enc(0), 21, // 0 0 rmoveto
		enc(50), enc(20), enc(20), enc(50), 31, // hvcurveto
		14,
	}
	contours := interpretType2Charstring(program)
	if len(contours) != 1 {
		t.Fatalf("got %d contours, want 1", len(contours))
	}
	pts := contours[0]
	last := pts[len(pts)-1]
	// Curve ends at x3=x2 (=50+20=70), y3=y2+50=20+50=70 for the horizontal-start 4-arg case.
	if last.X != 70 || last.Y != 70 {
		t.Errorf("curve endpoint = %+v, want (70,70)", last)
	}
}

func b139(v int) byte { return byte(v + 139) }

// TestType2CharstringOperators exercises the Type2 interpreter across the
// curve operators and every number encoding (single-byte, 247/251 ranges, the
// 28 int16 form, and the 255 16.16 fixed form).
func TestType2CharstringOperators(t *testing.T) {
	var p []byte
	add := func(bs ...byte) { p = append(p, bs...) }

	add(b139(100), b139(10), b139(20), 18)                                          // width=100, hstemhm 10 20
	add(b139(5), b139(5), 21)                                                       // rmoveto
	add(247, 0, 251, 0, 5)                                                          // rlineto with 247/251-range numbers
	add(28, 0x00, 0x0A, 28, 0x00, 0x0A, 5)                                          // rlineto with int16 numbers
	add(255, 0, 1, 0, 0, 255, 0, 1, 0, 0, 5)                                        // rlineto with 16.16 fixed numbers
	add(b139(10), 6)                                                                // hlineto
	add(b139(10), 7)                                                                // vlineto
	add(b139(5), b139(5), b139(5), b139(5), b139(5), b139(5), 8)                    // rrcurveto
	add(b139(5), b139(5), b139(5), b139(5), 26)                                     // vvcurveto
	add(b139(5), b139(5), b139(5), b139(5), 27)                                     // hhcurveto
	add(b139(5), b139(5), b139(5), b139(5), 30)                                     // vhcurveto
	add(b139(5), b139(5), b139(5), b139(5), 31)                                     // hvcurveto
	add(b139(5), b139(5), b139(5), b139(5), b139(5), b139(5), b139(5), b139(5), 24) // rcurveline
	add(b139(5), b139(5), b139(5), b139(5), b139(5), b139(5), b139(5), b139(5), 25) // rlinecurve
	add(14)                                                                         // endchar

	contours := interpretType2Charstring(p)
	if len(contours) == 0 {
		t.Fatal("interpretType2Charstring produced no contours")
	}
}

// TestType1CharstringOperators exercises the Type1 interpreter's moveto/curve
// operators and its number encodings (247/251 ranges and the 255 int32 form).
func TestType1CharstringOperators(t *testing.T) {
	var p []byte
	add := func(bs ...byte) { p = append(p, bs...) }

	add(b139(0), b139(100), 13)                                  // hsbw
	add(b139(20), 4)                                             // vmoveto
	add(b139(30), 22)                                            // hmoveto
	add(247, 0, 251, 0, 5)                                       // rlineto with 247/251-range numbers
	add(b139(5), b139(5), b139(5), b139(5), b139(5), b139(5), 8) // rrcurveto
	add(b139(5), b139(5), b139(5), b139(5), 30)                  // vhcurveto
	add(b139(5), b139(5), b139(5), b139(5), 31)                  // hvcurveto
	add(255, 0, 0, 0, 50, b139(0), 21)                           // rmoveto with int32 number
	add(9, 14)                                                   // closepath, endchar

	contours := interpretType1Charstring(p)
	if len(contours) == 0 {
		t.Fatal("interpretType1Charstring produced no contours")
	}
}

// TestType2CharstringEscapeAndHintmask covers the two-byte escape operator
// (12, a no-op on outline geometry) and the hintmask/cntrmask mask-byte skip.
func TestType2CharstringEscapeAndHintmask(t *testing.T) {
	var p []byte
	add := func(bs ...byte) { p = append(p, bs...) }

	add(b139(0), b139(4), b139(0), b139(4), 18) // hstemhm: 2 stems, no leading width
	add(19, 0xFF)                               // hintmask + 1 mask byte (ceil(2 stems/8))
	add(12, 0)                                  // two-byte escape, ignored
	add(b139(10), b139(10), 21)                 // rmoveto
	add(b139(20), b139(0), 5)                   // rlineto
	add(14)                                     // endchar

	contours := interpretType2Charstring(p)
	if len(contours) == 0 {
		t.Fatal("interpretType2Charstring produced no contours")
	}
}

// TestType1CharstringSubrEscapes covers the div and sbw escapes, and that a
// callsubr with no Subrs to resolve against ends the glyph.
func TestType1CharstringSubrEscapes(t *testing.T) {
	var p []byte
	add := func(bs ...byte) { p = append(p, bs...) }

	add(b139(0), b139(100), 13)                     // hsbw: sbx=0 wx=100
	add(b139(10), b139(10), 21)                     // rmoveto (10,10)
	add(b139(10), b139(2), 12, 12)                  // div: 10 2 -> 5.0
	add(22)                                         // hmoveto consumes the div result
	add(b139(0), b139(0), b139(50), b139(0), 12, 7) // sbw: sbx=0 sby=0 -> x,y=(0,0)
	add(b139(20), b139(20), 21)                     // rmoveto for a real contour
	add(b139(50), 6)                                // hlineto
	add(9)                                          // closepath
	add(b139(5), 10)                                // callsubr: a lone charstring has no Subrs, so the glyph ends
	add(b139(50), 7)                                // never drawn

	contours := interpretType1Charstring(p)
	if len(contours) != 1 || len(contours[0]) != 2 {
		t.Fatalf("contours = %v, want the one 2-point contour drawn before the unresolvable callsubr", contours)
	}
}

func TestInterpretType2CharstringSquare(t *testing.T) {
	// A hand-built Type2 charstring tracing a 100x100 square via rmoveto +
	// rlineto, then endchar: moveto (10,10); lineto (110,10),(110,110),(10,110).
	enc := func(v int) byte { return byte(v + 139) }
	program := []byte{
		enc(10), enc(10), 21, // 10 10 rmoveto
		enc(100), enc(0), 5, // 100 0 rlineto
		enc(0), enc(100), 5, // 0 100 rlineto
		enc(-100), enc(0), 5, // -100 0 rlineto
		14, // endchar
	}
	contours := interpretType2Charstring(program)
	if len(contours) != 1 {
		t.Fatalf("got %d contours, want 1", len(contours))
	}
	pts := contours[0]
	if len(pts) != 4 {
		t.Fatalf("got %d points, want 4 (square corners)", len(pts))
	}
	want := []Point{{10, 10}, {110, 10}, {110, 110}, {10, 110}}
	for i, w := range want {
		if pts[i].X != w.X || pts[i].Y != w.Y {
			t.Errorf("point %d = %+v, want %+v", i, pts[i], w)
		}
	}
}

func TestInterpretType1CharstringSquare(t *testing.T) {
	// hsbw sbx=0 wx=100, then rmoveto (10,10), rlineto x3 tracing a square,
	// closepath, endchar -- the Type1 counterpart of the Type2 square test.
	enc := func(v int) byte { return byte(v + 139) }
	program := []byte{
		enc(0), enc(100), 13, // 0 100 hsbw
		enc(10), enc(10), 21, // 10 10 rmoveto
		enc(100), 6, // 100 hlineto
		enc(100), 7, // 100 vlineto
		enc(-100), 6, // -100 hlineto
		9,  // closepath
		14, // endchar
	}
	contours := interpretType1Charstring(program)
	if len(contours) != 1 {
		t.Fatalf("got %d contours, want 1", len(contours))
	}
	pts := contours[0]
	want := []Point{{10, 10}, {110, 10}, {110, 110}, {10, 110}}
	if len(pts) != len(want) {
		t.Fatalf("got %d points, want %d", len(pts), len(want))
	}
	for i, w := range want {
		if pts[i].X != w.X || pts[i].Y != w.Y {
			t.Errorf("point %d = %+v, want %+v", i, pts[i], w)
		}
	}
}

func TestType2CharstringMovesAndOddCurveArgs(t *testing.T) {
	p := func(bs ...byte) []byte { return bs }
	n := b139
	// hmoveto, vmoveto, a stem operator, an unassigned operator (2) and an
	// endchar with a stray operand all fall through without drawing.
	got := interpretType2Charstring(p(
		n(10), 22, n(20), 4, // hmoveto 10; vmoveto 20 -> (10, 20)
		n(5), n(5), 1, // hstem
		n(3), 6, // hlineto 3
		2,        // unassigned operator
		n(1), 14, // endchar
	))
	if len(got) != 1 || len(got[0]) != 2 || got[0][0] != (Point{10, 20}) || got[0][1] != (Point{13, 20}) {
		t.Fatalf("contours = %v, want one line {10 20}-{13 20}", got)
	}

	// Odd argument counts: the leading dx1/dy1 of vv/hh curves, and the final
	// extra delta of vh/hv curves.
	for _, tc := range []struct {
		name string
		prog []byte
		end  Point
	}{
		{"vvcurveto dx1", p(n(0), n(0), 21, n(7), n(1), n(2), n(3), n(4), 26), Point{9, 8}},
		{"hhcurveto dy1", p(n(0), n(0), 21, n(7), n(1), n(2), n(3), n(4), 27), Point{7, 10}},
		{"hvcurveto tail", p(n(0), n(0), 21, n(1), n(2), n(3), n(4), n(5), 31), Point{8, 7}},
		{"vhcurveto tail", p(n(0), n(0), 21, n(1), n(2), n(3), n(4), n(5), 30), Point{6, 9}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := interpretType2Charstring(tc.prog)
			if len(c) != 1 {
				t.Fatalf("contours = %v, want one curve", c)
			}
			if end := c[0][len(c[0])-1]; end != tc.end {
				t.Errorf("curve ends at %v, want %v", end, tc.end)
			}
		})
	}
}

func TestType1CharstringEdgeOperators(t *testing.T) {
	n := b139
	cs := []byte{
		n(0), n(100), 13, // hsbw
		n(5), n(5), 1, // hstem
		n(5), n(5), 3, // vstem
		n(10), n(10), 21, // rmoveto
		n(1), n(0), 12, 12, // div by zero drops both operands
		n(4), n(2), 12, 12, // 4 2 div -> 2 (hlineto length)
		6,            // hlineto
		n(1), 12, 99, // unknown escape
		2,                             // unassigned operator
		n(5), n(5), n(5), n(5), 12, 7, // sbw
		n(5), 7, // vlineto
		14,
	}
	got := interpretType1Charstring(cs)
	if len(got) != 1 || len(got[0]) != 3 {
		t.Fatalf("contours = %v, want one contour of 3 points", got)
	}
	if got[0][1] != (Point{12, 10}) {
		t.Errorf("hlineto landed at %v, want {12 10} (div result used as length)", got[0][1])
	}

	// A lone escape byte at the end of the program ends interpretation.
	if got := interpretType1Charstring([]byte{n(0), n(0), 21, n(5), 6, 12}); len(got) != 1 {
		t.Errorf("contours = %v, want the one drawn before the truncated escape", got)
	}
}

// TestCharstringNumbers covers every operand encoding, the one place the two
// formats differ (byte 255: 16.16 fixed in Type 2, an integer in Type 1), and
// truncated or non-operand input.
func TestCharstringNumbers(t *testing.T) {
	tests := []struct {
		name         string
		in           []byte
		type2, type1 float64
		n            int
	}{
		{"single byte", []byte{139 + 7}, 7, 7, 1},
		{"247-range", []byte{247, 1}, 109, 109, 2},
		{"251-range", []byte{251, 1}, -109, -109, 2},
		{"28-form", []byte{28, 0xFF, 0xFE}, -2, -2, 3},
		{"255-form", []byte{255, 0, 1, 0, 0}, 1, 65536, 5},
		{"247-range truncated", []byte{247}, 0, 0, 0},
		{"251-range truncated", []byte{251}, 0, 0, 0},
		{"28-form truncated", []byte{28, 0x00}, 0, 0, 0},
		{"255-form truncated", []byte{255, 0, 1}, 0, 0, 0},
		{"operator byte", []byte{0}, 0, 0, 0},
		{"empty", nil, 0, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, n, ok := Type2Number(tt.in)
			if v != tt.type2 || n != tt.n || ok != (tt.n > 0) {
				t.Errorf("Type2Number(%v) = %v, %v, %v, want %v, %v, %v", tt.in, v, n, ok, tt.type2, tt.n, tt.n > 0)
			}
			v, n, ok = Type1Number(tt.in)
			if v != tt.type1 || n != tt.n || ok != (tt.n > 0) {
				t.Errorf("Type1Number(%v) = %v, %v, %v, want %v, %v, %v", tt.in, v, n, ok, tt.type1, tt.n, tt.n > 0)
			}
		})
	}
}
