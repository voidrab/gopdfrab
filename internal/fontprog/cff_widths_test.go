package fontprog

import (
	"reflect"
	"testing"
)

// widthPrivate declares defaultWidthX 500 and nominalWidthX 600.
var widthPrivate = []byte{248, 136, 20, 248, 236, 21}

// nameKeyedSpec is a four-glyph name-keyed program: .notdef (no width), A
// (nominal+5 via a local subr), B (an operator outside the width grammar, so
// it is skipped) and C (no width, via a global subr).
func nameKeyedSpec() cffSpec {
	return cffSpec{
		sids: []int{sidOf("A"), sidOf("B"), sidOf("C")},
		charstrings: [][]byte{
			{14},
			{32, 10},
			{139, 9},
			{32, 29},
		},
		private: widthPrivate,
		lsubrs:  [][]byte{{144, 14}},      // width operand 5, endchar
		gsubrs:  [][]byte{{139, 139, 21}}, // rmoveto, two args: no width
	}
}

func sidOf(name string) int { return stdSID(name) }

func TestCFFAdvanceWidths(t *testing.T) {
	got, stats := CFFAdvanceWidthsStats(nameKeyedSpec().assemble())
	want := map[string]float64{"A": 605, "C": 500}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("widths = %v, want %v", got, want)
	}
	if stats != (WidthStats{GlyphsTotal: 3, GlyphsSkipped: 1}) {
		t.Errorf("stats = %+v, want 3 total / 1 skipped", stats)
	}
	if plain := CFFAdvanceWidths(nameKeyedSpec().assemble()); !reflect.DeepEqual(plain, want) {
		t.Errorf("CFFAdvanceWidths = %v, want %v", plain, want)
	}
}

func TestCFFAdvanceWidthsFontMatrix(t *testing.T) {
	spec := nameKeyedSpec()
	spec.matrix = []string{"0.002", "0", "0", "0.002", "0", "0"}
	got := CFFAdvanceWidths(spec.assemble())
	if want := (map[string]float64{"A": 1210, "C": 1000}); !reflect.DeepEqual(got, want) {
		t.Errorf("widths under a 0.002 FontMatrix = %v, want %v", got, want)
	}

	// An explicit 1/1000 matrix leaves the widths where they are.
	spec.matrix = []string{"0.001", "0", "0", "0.001", "0", "0"}
	if got := CFFAdvanceWidths(spec.assemble()); got["A"] != 605 {
		t.Errorf("A under the default-equivalent matrix = %v, want 605", got["A"])
	}
}

func TestCFFAdvanceWidthsSkips(t *testing.T) {
	mutate := func(f func(*cffSpec)) []byte {
		s := nameKeyedSpec()
		f(&s)
		return s.assemble()
	}
	cid := cffSpec{cid: true, cids: []int{1}, charstrings: [][]byte{{14}, {14}}, fds: []cffFD{{private: widthPrivate}}, fdSelect: []byte{0, 0, 0}}
	cases := []struct {
		name string
		cff  []byte
		want WidthSkip
	}{
		{"garbage", []byte("garbage"), WidthSkipTopDict},
		{"cid-keyed", cid.assemble(), WidthSkipCIDKeyed},
		{"shear in matrix", mutate(func(s *cffSpec) { s.matrix = []string{"0.001", "0.001"} }), WidthSkipFontMatrix},
		{"no CharStrings", mutate(func(s *cffSpec) { s.noCS = true }), WidthSkipCharStrings},
		{"no Private", mutate(func(s *cffSpec) { s.noPrivate = true }), WidthSkipPrivate},
		{"no charset", mutate(func(s *cffSpec) { s.noCharset = true }), WidthSkipGlyphNames},
		{"unreadable CharStrings INDEX", mutate(func(s *cffSpec) {
			s.sids = []int{sidOf("A")}
			s.csRaw = []byte{0, 2, 1, 1, 200}
		}), WidthSkipCharStrings},
		{"unresolvable SID", mutate(func(s *cffSpec) { s.sids = []int{sidOf("A"), 9999, sidOf("C")} }), WidthSkipGlyphNames},
		{"empty Private", mutate(func(s *cffSpec) { s.private, s.lsubrs = nil, nil }), WidthSkipPrivate},
		{"undecodable Private", mutate(func(s *cffSpec) { s.private, s.lsubrs = []byte{247}, nil }), WidthSkipPrivate},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w, stats := CFFAdvanceWidthsStats(c.cff)
			if w != nil || stats.Skip != c.want {
				t.Errorf("got %v, %+v, want nil widths and skip %q", w, stats, c.want)
			}
		})
	}
}

// cidSpec is a three-glyph CID-keyed program with one Font DICT.
func cidSpec() cffSpec {
	return cffSpec{
		cid:         true,
		cids:        []int{10, 20},
		charstrings: [][]byte{{14}, {144, 14}, {139, 9}},
		fds:         []cffFD{{private: widthPrivate}},
		fdSelect:    []byte{0, 0, 0, 0},
	}
}

func TestCFFCIDAdvanceWidths(t *testing.T) {
	got, stats := CFFCIDAdvanceWidthsStats(cidSpec().assemble())
	want := map[int]float64{0: 500, 10: 605}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("widths = %v, want %v", got, want)
	}
	if stats != (WidthStats{GlyphsTotal: 3, GlyphsSkipped: 1}) {
		t.Errorf("stats = %+v, want 3 total / 1 skipped", stats)
	}
	if plain := CFFCIDAdvanceWidths(cidSpec().assemble()); !reflect.DeepEqual(plain, want) {
		t.Errorf("CFFCIDAdvanceWidths = %v, want %v", plain, want)
	}
}

func TestCFFCIDAdvanceWidthsFontMatrix(t *testing.T) {
	spec := cidSpec()
	spec.matrix = []string{"0.002", "0", "0", "0.002", "0", "0"}
	got := CFFCIDAdvanceWidths(spec.assemble())
	if want := (map[int]float64{0: 1000, 10: 1210}); !reflect.DeepEqual(got, want) {
		t.Errorf("widths under a 0.002 FontMatrix = %v, want %v", got, want)
	}
}

func TestCFFCIDAdvanceWidthsFontDicts(t *testing.T) {
	// A format-3 FDSelect sends gid 0 to FD 0 and gids 1-2 to FD 1, each with
	// its own width defaults; FD 1's nominal width is 100 higher.
	spec := cidSpec()
	spec.fds = []cffFD{
		{private: widthPrivate},
		{private: []byte{248, 136, 20, 249, 80, 21}}, // nominal 700
	}
	spec.fdSelect = []byte{3, 0, 2, 0, 0, 0, 0, 1, 1, 0, 3}
	got := CFFCIDAdvanceWidths(spec.assemble())
	if want := (map[int]float64{0: 500, 10: 705}); !reflect.DeepEqual(got, want) {
		t.Errorf("widths = %v, want %v", got, want)
	}

	// No FDSelect is fine with a single Font DICT.
	one := cidSpec()
	one.fdSelect = nil
	if got := CFFCIDAdvanceWidths(one.assemble()); got[10] != 605 {
		t.Errorf("single Font DICT without FDSelect: widths = %v", got)
	}

	// Font DICTs that cannot be used cost only their own glyphs.
	for name, fd := range map[string]cffFD{
		"own FontMatrix":   {private: widthPrivate, matrix: true},
		"undecodable dict": {private: widthPrivate, garbage: true},
		"no Private":       {noPrivate: true},
	} {
		s := cidSpec()
		s.fds = []cffFD{fd}
		w, stats := CFFCIDAdvanceWidthsStats(s.assemble())
		if len(w) != 0 || stats.Skip != WidthSkipNone || stats.GlyphsSkipped != 3 {
			t.Errorf("%s: got %v, %+v, want every glyph skipped", name, w, stats)
		}
	}

	// An FDSelect naming a Font DICT that does not exist skips that glyph.
	bad := cidSpec()
	bad.fdSelect = []byte{0, 0, 7, 0}
	w, stats := CFFCIDAdvanceWidthsStats(bad.assemble())
	if stats.GlyphsSkipped != 2 || w[0] != 500 {
		t.Errorf("out-of-range FD: got %v, %+v", w, stats)
	}
}

func TestCFFCIDAdvanceWidthsSkips(t *testing.T) {
	mutate := func(f func(*cffSpec)) []byte {
		s := cidSpec()
		f(&s)
		return s.assemble()
	}
	cases := []struct {
		name string
		cff  []byte
		want WidthSkip
	}{
		{"garbage", []byte("garbage"), WidthSkipTopDict},
		{"name-keyed", nameKeyedSpec().assemble(), WidthSkipNotCIDKeyed},
		{"shear in matrix", mutate(func(s *cffSpec) { s.matrix = []string{"0.001", "0.001"} }), WidthSkipFontMatrix},
		{"no CharStrings", mutate(func(s *cffSpec) { s.noCS = true }), WidthSkipCharStrings},
		{"no FDArray", mutate(func(s *cffSpec) { s.noFDArray = true }), WidthSkipFDArray},
		{"empty CharStrings INDEX", mutate(func(s *cffSpec) { s.csRaw = []byte{0, 0} }), WidthSkipCharStrings},
		{"no charset", mutate(func(s *cffSpec) { s.noCharset = true }), WidthSkipCharset},
		{"empty FDArray", mutate(func(s *cffSpec) { s.fdArrayRaw = []byte{0, 0} }), WidthSkipFDArray},
		{"several Font DICTs, no FDSelect", mutate(func(s *cffSpec) {
			s.fds = []cffFD{{private: widthPrivate}, {private: widthPrivate}}
			s.fdSelect = nil
		}), WidthSkipFDSelect},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w, stats := CFFCIDAdvanceWidthsStats(c.cff)
			if w != nil || stats.Skip != c.want {
				t.Errorf("got %v, %+v, want nil widths and skip %q", w, stats, c.want)
			}
		})
	}
}

func TestFontMatrixWidthScale(t *testing.T) {
	inf := 1.0
	for i := 0; i < 2000; i++ {
		inf *= 10
	}
	nan := inf - inf
	cases := []struct {
		a, b   float64
		scale  float64
		wantOK bool
	}{
		{0.001, 0, 1, true},
		{0.0005, 0, 0.5, true},
		{0.001, 0.0001, 0, false}, // tilted advance
		{0, 0, 0, false},
		{-0.001, 0, 0, false},
		{nan, 0, 0, false},
		{inf, 0, 0, false},
	}
	for _, c := range cases {
		scale, ok := fontMatrixWidthScale(c.a, c.b)
		if ok != c.wantOK || (ok && scale != c.scale) {
			t.Errorf("fontMatrixWidthScale(%v, %v) = %v, %v, want %v, %v", c.a, c.b, scale, ok, c.scale, c.wantOK)
		}
	}
}

func TestCffWidthScaleAndScaleWidth(t *testing.T) {
	if s, ok := cffWidthScale(CFFTopDict{}); !ok || s != 1 {
		t.Errorf("no FontMatrix: scale = %v, %v, want 1, true", s, ok)
	}
	td := CFFTopDict{HasFontMatrix: true, FontMatrix: [6]float64{0.002, 0}}
	if s, ok := cffWidthScale(td); !ok || s != 2 {
		t.Errorf("0.002 FontMatrix: scale = %v, %v, want 2, true", s, ok)
	}
	if got := scaleWidth(300, 1); got != 300 {
		t.Errorf("scaleWidth(300, 1) = %v, want 300", got)
	}
	if got := scaleWidth(300, 2.5); got != 750 {
		t.Errorf("scaleWidth(300, 2.5) = %v, want 750", got)
	}
}

func TestCffCharstringWidth(t *testing.T) {
	if got := cffCharstringWidth([]byte{139, 9}, nil, nil, 500, 600); got != -1 {
		t.Errorf("unfollowable charstring = %v, want -1", got)
	}
	if got := cffCharstringWidth([]byte{14}, nil, nil, 500, 600); got != 500 {
		t.Errorf("no width operand = %v, want defaultWidthX 500", got)
	}
	if got := cffCharstringWidth([]byte{144, 14}, nil, nil, 500, 600); got != 605 {
		t.Errorf("width operand 5 = %v, want nominalWidthX+5 = 605", got)
	}
}
