package fontprog

import (
	"reflect"
	"testing"
)

func TestType2CharstringWidthOperandEncodings(t *testing.T) {
	// Each program pushes the width operand with a different encoding and ends
	// in endchar, whose single-argument form carries the width.
	cases := []struct {
		name string
		cs   []byte
		want float64
	}{
		{"positive two-byte (247-250)", []byte{248, 236, 14}, 600},
		{"negative two-byte (251-254)", []byte{251, 0, 14}, -108},
		{"16-bit integer (28)", []byte{28, 0x01, 0x2C, 14}, 300},
		{"negative 16-bit integer (28)", []byte{28, 0xFF, 0x38, 14}, -200},
		{"16.16 fixed (255)", []byte{255, 0x00, 0x02, 0x80, 0x00, 14}, 2.5},
	}
	for _, c := range cases {
		w, hasWidth, ok := Type2CharstringWidth(c.cs, nil, nil)
		if !ok || !hasWidth || w != c.want {
			t.Errorf("%s: got %v, %v, %v, want %v, true, true", c.name, w, hasWidth, ok, c.want)
		}
	}

	// Truncated operands cannot be followed.
	for name, cs := range map[string][]byte{
		"247 truncated": {247},
		"251 truncated": {251},
		"28 truncated":  {28, 0},
		"255 truncated": {255, 0, 0, 0},
	} {
		if _, _, ok := Type2CharstringWidth(cs, nil, nil); ok {
			t.Errorf("%s: should not be followable", name)
		}
	}
}

func TestType2CharstringWidthShapes(t *testing.T) {
	cases := []struct {
		name     string
		cs       []byte
		hasWidth bool
		ok       bool
	}{
		{"ends without a terminator", []byte{144}, false, false},
		{"endchar with 2 args is malformed", []byte{139, 139, 14}, false, false},
		{"endchar with 3 args is malformed", []byte{139, 139, 139, 14}, false, false},
		{"hstem with even args has no width", []byte{139, 139, 1}, false, true},
		{"hmoveto with one arg has no width", []byte{139, 22}, false, true},
		{"hmoveto with two args has a width", []byte{139, 139, 22}, true, true},
		{"vmoveto with one arg has no width", []byte{139, 4}, false, true},
	}
	for _, c := range cases {
		_, hasWidth, ok := Type2CharstringWidth(c.cs, nil, nil)
		if hasWidth != c.hasWidth || ok != c.ok {
			t.Errorf("%s: hasWidth=%v ok=%v, want %v %v", c.name, hasWidth, ok, c.hasWidth, c.ok)
		}
	}
}

func TestCffPrivateInfoOperatorWithoutOperands(t *testing.T) {
	// A bare defaultWidthX operator is ignored rather than read as zero.
	d, n, _, ok := CFFPrivateInfo([]byte{20, 144, 21}, 0, 3)
	if !ok || d != 0 || n != 5 {
		t.Errorf("cffPrivateInfo = %v, %v, %v, want 0, 5, true", d, n, ok)
	}
}

func TestCffDictNumbersNegativeTwoByte(t *testing.T) {
	var got []float64
	ok := CFFDictNumbers([]byte{251, 0, 20}, func(_ int, operands []float64) { got = operands })
	if !ok || !reflect.DeepEqual(got, []float64{-108}) {
		t.Errorf("cffDictNumbers(251 0) = %v, %v, want [-108] true", got, ok)
	}
}

func TestCffRealNumberExponents(t *testing.T) {
	// "1E2" -> nibbles 1 B 2 F; "1E-2" -> 1 C 2 F.
	if v, _, ok := cffRealNumber([]byte{0x1B, 0x2F}); !ok || v != 100 {
		t.Errorf("1E2 = %v, %v, want 100", v, ok)
	}
	if v, _, ok := cffRealNumber([]byte{0x1C, 0x2F}); !ok || v != 0.01 {
		t.Errorf("1E-2 = %v, %v, want 0.01", v, ok)
	}
}

func TestParseCFFFDSelectMalformed(t *testing.T) {
	good3 := []byte{3, 0, 1, 0, 0, 0, 0, 2}
	cases := []struct {
		name string
		data []byte
		off  int
		n    int
	}{
		{"offset past data", []byte{0}, 5, 1},
		{"no glyphs", []byte{0, 0}, 0, 0},
		{"format 0 shorter than the glyph count", []byte{0, 1}, 0, 3},
		{"format 3 without room for its header", []byte{3, 0, 1}, 0, 2},
		{"format 3 ranges run past the data", []byte{3, 0, 9, 0, 0, 0, 0, 2}, 0, 2},
		{"format 3 sentinel disagrees with the glyph count", good3, 0, 3},
		{"format 3 range starts after the next one", []byte{3, 0, 2, 0, 5, 0, 0, 1, 1, 0, 2}, 0, 2},
		{"format 3 range overruns the sentinel", []byte{3, 0, 1, 0, 0, 0, 0, 2}, 0, 1},
	}
	for _, c := range cases {
		if got := ParseCFFFDSelect(c.data, c.off, c.n); got != nil {
			t.Errorf("%s: got %v, want nil", c.name, got)
		}
	}
	if got := ParseCFFFDSelect(good3, 0, 2); !reflect.DeepEqual(got, []int{0, 0}) {
		t.Errorf("well-formed format 3 = %v, want [0 0]", got)
	}
}

// cffWithTopDict wraps raw Top DICT bytes in an otherwise empty CFF header,
// Name INDEX and Top DICT INDEX.
func cffWithTopDict(top []byte) []byte {
	out := []byte{1, 0, 4, 1}
	out = append(out, cffIndex([][]byte{[]byte("Font")})...)
	return append(out, cffIndex([][]byte{top})...)
}

func TestParseCFFTopDictOperators(t *testing.T) {
	top := []byte{}
	top = append(top, cffRealOperand("0.001")...)
	top = append(top, cffRealOperand("0")...)
	top = append(top, 12, 7) // FontMatrix
	top = append(top, 17)    // CharStrings with no operand: ignored
	top = append(top, cffSmall(5), 15)
	top = append(top, cffSmall(3), cffSmall(4), 18)
	top = append(top, cffSmall(1), 12, 36)
	top = append(top, cffSmall(2), 12, 37)
	top = append(top, 12, 30) // ROS
	td, ok := ParseCFFTopDict(cffWithTopDict(top))
	if !ok {
		t.Fatal("ParseCFFTopDict failed")
	}
	want := CFFTopDict{
		CSOffset: -1, CharsetOffset: 5, PrivateSize: 3, PrivateOffset: 4,
		FDArrayOffset: 1, FDSelect: 2, FontMatrix: [6]float64{0.001, 0},
		HasFontMatrix: true, IsCIDKeyed: true,
	}
	if td != want {
		t.Errorf("ParseCFFTopDict = %+v, want %+v", td, want)
	}

	// Operators that need an operand are ignored without one.
	td, ok = ParseCFFTopDict(cffWithTopDict([]byte{15, 18, 12, 36, 12, 37}))
	if !ok || td.CharsetOffset != -1 || td.PrivateOffset != -1 || td.FDArrayOffset != -1 || td.FDSelect != -1 {
		t.Errorf("operand-less operators: %+v, %v", td, ok)
	}

	// A Top DICT that does not decode reports not-ok.
	if _, ok := ParseCFFTopDict(cffWithTopDict([]byte{247})); ok {
		t.Error("undecodable Top DICT should report not-ok")
	}
}

func TestParseCFFTopDictMalformedIndexes(t *testing.T) {
	hdr := []byte{1, 0, 4, 1}
	name := cffIndex([][]byte{[]byte("Font")})
	cat := func(parts ...[]byte) []byte {
		var out []byte
		for _, p := range parts {
			out = append(out, p...)
		}
		return out
	}
	cases := []struct {
		name string
		cff  []byte
	}{
		{"empty Name INDEX leaves no Top DICT", cat(hdr, []byte{0, 0})},
		{"Name INDEX count without offset size", cat(hdr, []byte{0, 1})},
		{"Name INDEX with a bad offset size", cat(hdr, []byte{0, 1, 9, 1, 1})},
		{"Name INDEX offsets past the data", cat(hdr, []byte{0, 1, 1, 1})},
		{"no room for the Top DICT count", cat(hdr, name)},
		{"empty Top DICT INDEX", cat(hdr, name, []byte{0, 0})},
		{"Top DICT with a bad offset size", cat(hdr, name, []byte{0, 1, 0, 1, 1})},
		{"Top DICT data past the end", cat(hdr, name, []byte{0, 1, 1, 1, 50})},
	}
	for _, c := range cases {
		if _, ok := ParseCFFTopDict(c.cff); ok {
			t.Errorf("%s: should report not-ok", c.name)
		}
	}
}

func TestParseCFFCharsetCIDsMalformed(t *testing.T) {
	pad := []byte{0, 0, 0}
	cases := []struct {
		name  string
		table []byte
		n     int
	}{
		{"format 0 runs out", []byte{0, 0, 1}, 3},
		{"format 1 range runs out", []byte{1, 0, 5}, 3},
		{"format 1 without nLeft", []byte{1, 0, 5}, 2},
		{"format 2 without nLeft", []byte{2, 0, 5, 0}, 2},
	}
	for _, c := range cases {
		if got := ParseCFFCharsetCIDs(append(append([]byte{}, pad...), c.table...), 3, c.n); got != nil {
			t.Errorf("%s: got %v, want nil", c.name, got)
		}
	}
	if ParseCFFCharsetCIDs(pad, 9, 2) != nil {
		t.Error("an offset past the data should be nil")
	}
	f0 := append(append([]byte{}, pad...), 0, 0, 7, 0, 9)
	if got := ParseCFFCharsetCIDs(f0, 3, 3); !reflect.DeepEqual(got, []int{0, 7, 9}) {
		t.Errorf("format 0 = %v, want [0 7 9]", got)
	}
}

func TestParseCFFCharStringLengths(t *testing.T) {
	cff := append([]byte{9, 9}, cffIndex([][]byte{{1, 2, 3}, {}, {4}})...)
	if got := ParseCFFCharStringLengths(cff, 2); !reflect.DeepEqual(got, []int{3, 0, 1}) {
		t.Errorf("lengths = %v, want [3 0 1]", got)
	}
	// Two-byte offsets.
	big := make([]byte, 300)
	cff2 := append([]byte{9}, cffIndex([][]byte{big, {1}})...)
	if got := ParseCFFCharStringLengths(cff2, 1); !reflect.DeepEqual(got, []int{300, 1}) {
		t.Errorf("two-byte-offset lengths = %v, want [300 1]", got)
	}
	for name, c := range map[string]struct {
		data []byte
		off  int
	}{
		"negative offset":  {cff, -1},
		"offset past data": {cff, 200},
		"empty INDEX":      {[]byte{0, 0, 1}, 0},
		"bad offset size":  {[]byte{0, 1, 9, 1, 1}, 0},
		"truncated":        {[]byte{0, 5, 1, 1}, 0},
	} {
		if got := ParseCFFCharStringLengths(c.data, c.off); got != nil {
			t.Errorf("%s: got %v, want nil", name, got)
		}
	}
}

func TestParseCFFIndexMalformedEntries(t *testing.T) {
	cases := []struct {
		name string
		data []byte
	}{
		{"count without offset size", []byte{0, 1}},
		{"bad offset size", []byte{0, 1, 9, 1, 1}},
		{"data past the end", []byte{0, 1, 1, 1, 9, 'x'}},
		{"last offset below 1", []byte{0, 1, 1, 0, 0}},
		{"entry starts below the data", []byte{0, 1, 1, 0, 2, 'x'}},
		{"entries out of order", []byte{0, 2, 1, 1, 3, 2, 'a', 'b'}},
	}
	for _, c := range cases {
		if got, end := ParseCFFIndex(c.data, 0); got != nil || end != 0 {
			t.Errorf("%s: got %v, %d, want nil, 0", c.name, got, end)
		}
	}
}

func TestCFFGlyphNames(t *testing.T) {
	spec := nameKeyedSpec()
	spec.sids = []int{sidOf("A"), 391, sidOf("C")} // 391 is the first custom string
	spec.customStrings = []string{"alpha"}
	got := CFFGlyphNames(spec.assemble())
	if want := []string{".notdef", "A", "alpha", "C"}; !reflect.DeepEqual(got, want) {
		t.Errorf("CFFGlyphNames = %v, want %v", got, want)
	}

	if CFFGlyphNames(cidSpec().assemble()) != nil {
		t.Error("a CID-keyed program has no glyph names")
	}
	noCS := nameKeyedSpec()
	noCS.csRaw = []byte{}
	if CFFGlyphNames(noCS.assemble()) != nil {
		t.Error("a CharStrings offset at the end of the data should give nil")
	}
	if CFFGlyphNames([]byte("nope")) != nil {
		t.Error("garbage should give nil")
	}
}

func TestCffStringIndexEntries(t *testing.T) {
	if cffStringIndexEntries([]byte{1, 2}) != nil {
		t.Error("short input should give nil")
	}
	spec := nameKeyedSpec()
	spec.customStrings = []string{"one", "two"}
	got := cffStringIndexEntries(spec.assemble())
	if len(got) != 2 || string(got[0]) != "one" || string(got[1]) != "two" {
		t.Errorf("cffStringIndexEntries = %q", got)
	}
}
