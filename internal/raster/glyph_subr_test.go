package raster

import (
	"testing"

	"github.com/voidrab/gopdfrab/internal/fontprog"
)

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

// TestType2Subroutines pins that callsubr and callgsubr run the biased
// local and global subroutine, that return resumes the caller, and that
// endchar inside a subroutine ends the glyph. Subset CFF fonts route most
// of their outlines through subroutines.
func TestType2Subroutines(t *testing.T) {
	// Fewer than 1240 subrs: an operand of n-107 names subr n.
	lsubrs := [][]byte{cs(nums(50, 0), []byte{5, 11})} // 50 0 rlineto return
	gsubrs := [][]byte{cs(nums(0, 50), []byte{5, 11})} // 0 50 rlineto return
	run := func(prog []byte) [][]Point {
		in := &type2Interp{lsubrs: lsubrs, gsubrs: gsubrs}
		in.run(prog, 0)
		in.closeContour()
		return in.contours
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

func TestCFFProgramOutline(t *testing.T) {
	p := &cffProgram{
		charStrings: [][]byte{cs(nums(0, 0), []byte{21}, nums(-107), []byte{29}, []byte{14})},
		subrs:       fontprog.CFFSubrs{Global: [][]byte{cs(nums(30, 0), []byte{5, 11})}},
	}
	path, ok := p.outline(0)
	if !ok || lastPoint(t, path.Contours) != (Point{30, 0}) {
		t.Errorf("outline(0) = %v %v, want the global subr's line", path.Contours, ok)
	}
	for _, gid := range []int{-1, 1} {
		if _, ok := p.outline(gid); ok {
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
		// Short of operands draws nothing. (flex's last operand, the flex
		// depth, only matters for hinting, so two are dropped.)
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
func t1(glyphs map[string][]byte, subrs ...[]byte) *type1Program {
	return &type1Program{glyphs: glyphs, subrs: subrs}
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

func t1outline(t *testing.T, p *type1Program, name string) [][]Point {
	t.Helper()
	path, ok := p.outline(name)
	if !ok {
		t.Fatalf("outline(%q) failed", name)
	}
	return path.Contours
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
	if _, ok := p.outline("absent"); ok {
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

	// A flex end without its seven points draws nothing and leaves no
	// result for pop beyond what it was given.
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
