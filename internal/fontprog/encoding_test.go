package fontprog

import (
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

func TestGlyphNameToUnicode(t *testing.T) {
	cases := []struct {
		name string
		want uint16
		ok   bool
	}{
		{"space", 0x0020, true},
		{"A", 0x0041, true},
		{"fi", 0xFB01, true},
		{"fl", 0xFB02, true},
		{"Euro", 0x20AC, true},
		{"uni0041", 0x0041, true},
		{"uni20AC", 0x20AC, true},
		{"notexist", 0, false},
	}
	for _, c := range cases {
		got, ok := GlyphNameToUnicode(c.name)
		if ok != c.ok || got != c.want {
			t.Errorf("GlyphNameToUnicode(%q) = (%04X, %v), want (%04X, %v)", c.name, got, ok, c.want, c.ok)
		}
	}
}

func TestSimpleFontCodeToUnicodeWinAnsi(t *testing.T) {
	table := SimpleFontCodeToUnicode(pdf.PDFName{Value: "WinAnsiEncoding"})
	// code 65 = 'A' in WinAnsi
	if table[65] != 0x0041 {
		t.Errorf("WinAnsi code 65 = %04X, want 0041", table[65])
	}
	// code 0x80 = Euro sign in Windows-1252
	if table[0x80] != 0x20AC {
		t.Errorf("WinAnsi code 0x80 = %04X, want 20AC", table[0x80])
	}
}

// TestSimpleFontCodeToUnicodeDifferences verifies that /Differences entries
// override the base encoding, which is the path the verifier missed before.
func TestSimpleFontCodeToUnicodeDifferences(t *testing.T) {
	// Encoding: WinAnsiEncoding with code 32 remapped to "fi" (U+FB01)
	diffs := pdf.PDFArray{
		pdf.PDFInteger(32),
		pdf.PDFName{Value: "fi"},
	}
	enc := pdf.NewPDFDict()
	enc.Entries.Set("Type", pdf.PDFName{Value: "Encoding"})
	enc.Entries.Set("BaseEncoding", pdf.PDFName{Value: "WinAnsiEncoding"})
	enc.Entries.Set("Differences", diffs)

	table := SimpleFontCodeToUnicode(enc)

	// code 32 was remapped from U+0020 (space) to U+FB01 (fi)
	if table[32] != 0xFB01 {
		t.Errorf("code 32 with /Differences fi = %04X, want FB01", table[32])
	}
	// code 33 should still be WinAnsi 0x0021 (!)
	if table[33] != 0x0021 {
		t.Errorf("code 33 unchanged = %04X, want 0021", table[33])
	}
}

// TestSimpleFontCodeToUnicodeUnknownGlyphName verifies that an unrecognised
// glyph name in /Differences maps the code to 0 (conservatively skipped).
func TestSimpleFontCodeToUnicodeUnknownGlyphName(t *testing.T) {
	diffs := pdf.PDFArray{
		pdf.PDFInteger(65),
		pdf.PDFName{Value: ".unimappedglyph"},
	}
	enc := pdf.NewPDFDict()
	enc.Entries.Set("BaseEncoding", pdf.PDFName{Value: "WinAnsiEncoding"})
	enc.Entries.Set("Differences", diffs)

	table := SimpleFontCodeToUnicode(enc)
	// Unknown glyph name must map to 0 so the check skips conservatively.
	if table[65] != 0 {
		t.Errorf("unknown glyph name code 65 = %04X, want 0000", table[65])
	}
}

// TestGlyphNameAndEncoding covers GlyphNameToUnicode and SimpleFontCodeToUnicode.
func TestGlyphNameAndEncoding(t *testing.T) {
	if u, ok := GlyphNameToUnicode("A"); !ok || u != 'A' {
		t.Errorf("GlyphNameToUnicode(A) = %d, %v", u, ok)
	}
	if _, ok := GlyphNameToUnicode("no_such_glyph_xyz"); ok {
		t.Error("GlyphNameToUnicode(unknown) should be false")
	}
	table := SimpleFontCodeToUnicode(pdf.PDFName{Value: "WinAnsiEncoding"})
	if table[65] != 'A' {
		t.Errorf("WinAnsi code 65 -> U+%04X, want 'A'", table[65])
	}
}

func TestSimpleFontGlyphNameTableCustomEncoding(t *testing.T) {
	v := pdf.NewPDFDict()
	encDict := pdf.NewPDFDict()
	encDict.Entries.Set("BaseEncoding", pdf.PDFName{Value: "WinAnsiEncoding"})
	encDict.Entries.Set("Differences", pdf.PDFArray{pdf.PDFInteger(65), pdf.PDFName{Value: "Agrave"}})
	v.Entries.Set("Encoding", encDict)

	names, ok := SimpleFontGlyphNameTable(v)
	if !ok || names[65] != "Agrave" {
		t.Errorf("SimpleFontGlyphNameTable custom Differences: names[65] = %q, ok=%v", names[65], ok)
	}

	v2 := pdf.NewPDFDict()
	v2.Entries.Set("Encoding", pdf.PDFName{Value: "MacRomanEncoding"})
	if _, ok := SimpleFontGlyphNameTable(v2); ok {
		t.Error("SimpleFontGlyphNameTable(MacRomanEncoding) should be ok=false (unmodeled)")
	}
}

func TestMacRomanToUnicode(t *testing.T) {
	cases := []struct {
		code int
		want uint16
	}{
		{65, 0x0041},   // A
		{0x8E, 0x00E9}, // eacute
		{0xA0, 0x2020}, // dagger
		{0xD5, 0x2019}, // quoteright
		{0xDB, 0x00A4}, // currency
		{0xF5, 0x0131}, // dotlessi
		{0xFF, 0x02C7}, // caron
		{0x00, 0},
	}
	for _, c := range cases {
		if got := MacRomanToUnicode[c.code]; got != c.want {
			t.Errorf("MacRomanToUnicode[%d] = %04X, want %04X", c.code, got, c.want)
		}
	}
}

func TestStandardToUnicode(t *testing.T) {
	cases := []struct {
		code int
		want uint16
	}{
		{65, 0x0041},   // A
		{0x27, 0x2019}, // quoteright, not apostrophe
		{0x60, 0x2018}, // quoteleft, not grave
		{0xA4, 0x2044}, // fraction
		{0x80, 0},
	}
	for _, c := range cases {
		if got := StandardToUnicode[c.code]; got != c.want {
			t.Errorf("StandardToUnicode[%d] = %04X, want %04X", c.code, got, c.want)
		}
	}
}

func TestSymbolToUnicode(t *testing.T) {
	cases := []struct {
		code int
		want uint16
	}{
		{32, 0x0020},  // space
		{34, 0x2200},  // universal
		{67, 0x03A7},  // Chi
		{97, 0x03B1},  // alpha
		{165, 0x221E}, // infinity
		{229, 0x2211}, // summation
		{240, 0xF8FF}, // apple (PUA)
		{254, 0xF8FE}, // bracerightbt (PUA)
		{127, 0},      // undefined
	}
	for _, c := range cases {
		if got := SymbolToUnicode[c.code]; got != c.want {
			t.Errorf("SymbolToUnicode[%d] = %04X, want %04X", c.code, got, c.want)
		}
	}
	if SymbolGlyphNameUnicode["universal"] != 0x2200 || SymbolGlyphNameUnicode["alpha"] != 0x03B1 {
		t.Errorf("SymbolGlyphNameUnicode missing universal/alpha")
	}
}

func TestZapfDingbatsToUnicode(t *testing.T) {
	cases := []struct {
		code int
		want uint16
	}{
		{32, 0x0020},  // space
		{33, 0x2701},  // a1 upper blade scissors
		{37, 0x260E},  // a4 black telephone
		{51, 0x2713},  // a19 check mark
		{52, 0x2714},  // a20 heavy check mark
		{72, 0x2605},  // a35 black star
		{126, 0x275E}, // a100
		{128, 0x2768}, // a89 medium left parenthesis ornament
		{141, 0x2775}, // a96 medium right curly bracket ornament
		{168, 0x2663}, // a112 club suit
		{172, 0x2460}, // a120 circled digit one
		{181, 0x2469}, // a129 circled number ten
		{182, 0x2776}, // a130 negative circled digit one
		{213, 0x2192}, // a161 rightwards arrow
		{254, 0x27BE}, // a191
		{240, 0},      // undefined
		{160, 0},      // undefined
	}
	for _, c := range cases {
		if got := ZapfDingbatsToUnicode[c.code]; got != c.want {
			t.Errorf("ZapfDingbatsToUnicode[%d] = %04X, want %04X", c.code, got, c.want)
		}
	}
	nameCases := map[string]uint16{
		"a19": 0x2713, "a20": 0x2714, "a89": 0x2768, "a205": 0x276E, "a191": 0x27BE,
	}
	for name, want := range nameCases {
		if got := ZapfDingbatsGlyphNameUnicode[name]; got != want {
			t.Errorf("ZapfDingbatsGlyphNameUnicode[%q] = %04X, want %04X", name, got, want)
		}
	}
}

func TestSimpleFontCodeToUnicodeMacRomanExact(t *testing.T) {
	table := SimpleFontCodeToUnicode(pdf.PDFName{Value: "MacRomanEncoding"})
	if table[0x8E] != 0x00E9 {
		t.Errorf("MacRoman code 0x8E = %04X, want 00E9 (eacute)", table[0x8E])
	}
	if table[0xD5] != 0x2019 {
		t.Errorf("MacRoman code 0xD5 = %04X, want 2019 (quoteright)", table[0xD5])
	}
}

func simpleFontDict(baseFont string, flags int) pdf.PDFDict {
	d := pdf.NewPDFDict()
	d.Entries.Set("Type", pdf.PDFName{Value: "Font"})
	d.Entries.Set("Subtype", pdf.PDFName{Value: "Type1"})
	d.Entries.Set("BaseFont", pdf.PDFName{Value: baseFont})
	if flags != 0 {
		desc := pdf.NewPDFDict()
		desc.Entries.Set("Flags", pdf.PDFInteger(flags))
		d.Entries.Set("FontDescriptor", desc)
	}
	return d
}

func TestOriginalCodeToUnicodeStandardSymbolFonts(t *testing.T) {
	zapf := simpleFontDict("ZapfDingbats", 4)
	table := mustTable(zapf)
	if table[52] != 0x2714 {
		t.Errorf("ZapfDingbats code 52 = %04X, want 2714 (heavy check mark)", table[52])
	}

	subset := simpleFontDict("ABCDEF+Symbol", 4)
	table = mustTable(subset)
	if table[97] != 0x03B1 {
		t.Errorf("Symbol code 97 = %04X, want 03B1 (alpha)", table[97])
	}
}

func TestOriginalCodeToUnicodeDifferencesWithDingbatNames(t *testing.T) {
	d := simpleFontDict("ZapfDingbats", 4)
	enc := pdf.NewPDFDict()
	enc.Entries.Set("Differences", pdf.PDFArray{
		pdf.PDFInteger(52), pdf.PDFName{Value: "a20"},
		pdf.PDFName{Value: "a19"},
	})
	d.Entries.Set("Encoding", enc)

	table := mustTable(d)
	if table[52] != 0x2714 {
		t.Errorf("Differences code 52 = %04X, want 2714 (a20)", table[52])
	}
	if table[53] != 0x2713 {
		t.Errorf("Differences code 53 = %04X, want 2713 (a19)", table[53])
	}
	// Codes outside Differences keep the dingbats built-in meaning.
	if table[33] != 0x2701 {
		t.Errorf("code 33 = %04X, want 2701 (a1 from builtin)", table[33])
	}
}

func TestOriginalCodeToUnicodeSymbolicCustomIsUnknown(t *testing.T) {
	d := simpleFontDict("MyDingFont", 4)
	table := mustTable(d)
	for cc, u := range table {
		if u != 0 {
			t.Fatalf("symbolic custom font code %d resolved to %04X, want unknown", cc, u)
		}
	}
}

func TestOriginalCodeToUnicodeNonSymbolicDefaults(t *testing.T) {
	// No Encoding at all: matches the fixer's WinAnsi assumption.
	d := simpleFontDict("Helvetica", 32)
	table := mustTable(d)
	if table[0x80] != 0x20AC {
		t.Errorf("no-encoding code 0x80 = %04X, want 20AC (WinAnsi Euro)", table[0x80])
	}

	// Encoding dict without BaseEncoding: the implicit standard base.
	enc := pdf.NewPDFDict()
	enc.Entries.Set("Differences", pdf.PDFArray{pdf.PDFInteger(65), pdf.PDFName{Value: "eacute"}})
	d.Entries.Set("Encoding", enc)
	table = mustTable(d)
	if table[65] != 0x00E9 {
		t.Errorf("Differences code 65 = %04X, want 00E9 (eacute)", table[65])
	}
	if table[0x27] != 0x2019 {
		t.Errorf("dict-without-base code 0x27 = %04X, want 2019 (Standard quoteright)", table[0x27])
	}
}

func TestOriginalCodeToUnicodeToUnicodeFallback(t *testing.T) {
	d := simpleFontDict("MySymbols", 4)
	toUni := pdf.NewPDFDict()
	toUni.HasStream = true
	toUni.RawStream = []byte("beginbfchar\n<34> <2714>\nendbfchar")
	d.Entries.Set("ToUnicode", toUni)

	table := mustTable(d)
	if table[0x34] != 0x2714 {
		t.Errorf("ToUnicode code 0x34 = %04X, want 2714", table[0x34])
	}
	if table[0x35] != 0 {
		t.Errorf("unmapped code 0x35 = %04X, want unknown", table[0x35])
	}
}

func mustTable(d pdf.PDFDict) [256]uint16 {
	table, _ := OriginalSimpleFontCodeToUnicode(d)
	return table
}

func TestMacRomanGlyphNameMatchesUnicodeTable(t *testing.T) {
	// Every defined name must map back to the code's MacRomanToUnicode
	// codepoint. 0xCA is the exception: Annex D names it space, the Mac OS
	// set maps it to no-break space.
	defined := 0
	for code, name := range MacRomanGlyphName {
		if name == "" {
			continue
		}
		defined++
		u, ok := GlyphNameToUnicode(name)
		want := MacRomanToUnicode[code]
		if code == 0xCA {
			want = 0x20
		}
		if !ok || u != want {
			t.Errorf("MacRomanGlyphName[%#x] = %q -> U+%04X (ok=%v), want U+%04X", code, name, u, ok, want)
		}
	}
	// 95 printable ASCII codes plus 128 high codes, less the 15 Annex D
	// leaves undefined.
	if want := 95 + 128 - 15; defined != want {
		t.Errorf("%d defined codes, want %d", defined, want)
	}
}

func TestWinAnsiGlyphNameMatchesUnicodeTable(t *testing.T) {
	// 0xAD is the exception: Annex D names the soft hyphen hyphen, which
	// maps back to U+002D.
	for code, name := range WinAnsiGlyphName {
		if name == "" || code == 0xAD {
			continue
		}
		if u, ok := GlyphNameToUnicode(name); !ok || u != WinAnsiToUnicode[code] {
			t.Errorf("WinAnsiGlyphName[%#x] = %q -> U+%04X (ok=%v), want U+%04X", code, name, u, ok, WinAnsiToUnicode[code])
		}
	}
}

func TestMacOSRomanCode(t *testing.T) {
	for u, want := range map[uint16]byte{'A': 0x41, 0x00E9: 0x8E, 0x2260: 0xAD, 0x00A0: 0xCA} {
		if got, ok := MacOSRomanCode(u); !ok || got != want {
			t.Errorf("MacOSRomanCode(U+%04X) = %#x, %v; want %#x", u, got, ok, want)
		}
	}
	if _, ok := MacOSRomanCode(0x0141); ok { // Lslash
		t.Error("U+0141 has no Mac OS Roman code")
	}
}
