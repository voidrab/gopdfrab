package fontprog

import (
	"bytes"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// widthFont is a Type 1 program with: A (plain hsbw, 600), B (the hsbw sits in
// subr 0, 700), C (calls a subr that merely returns, then has its own hsbw,
// 800), and D (endchar before any width: a glyph with no width).
func widthFont() type1Spec {
	s := newType1()
	s.subrs = []type1Entry{
		{"0", []byte{139, 249, 80, 13, 11}}, // 0 700 hsbw return
		{"1", []byte{11}},                   // return
	}
	s.glyphs = []type1Entry{
		{".notdef", hsbwCS(500)},
		{"A", hsbwCS(600)},
		{"B", []byte{139, 10, 14}},
		{"C", []byte{140, 10, 139, 249, 180, 13, 14}},
		{"D", []byte{14}},
	}
	return s
}

func TestType1Decryption(t *testing.T) {
	plain := []byte("hello charstring")
	enc := type1Encrypt(plain, 55665, 4)
	if got := DecryptType1Block(enc, 55665); !bytes.Equal(got, plain) {
		t.Errorf("DecryptType1Block = %q, want %q", got, plain)
	}
	for _, lenIV := range []int{0, 1, 4} {
		enc := type1Encrypt(plain, 4330, lenIV)
		if got := DecryptType1CharString(enc, lenIV); !bytes.Equal(got, plain) {
			t.Errorf("DecryptType1CharString(lenIV %d) = %q, want %q", lenIV, got, plain)
		}
	}
}

func TestType1LenIV(t *testing.T) {
	cases := []struct {
		plain string
		want  int
	}{
		{"/lenIV 0 def", 0},
		{"/lenIV 7 def", 7},
		{"/Other 1 def", 4},                    // none declared: the spec default
		{"/lenIV 99999999999999999999 def", 4}, // overflows int
	}
	for _, c := range cases {
		if got := type1LenIV([]byte(c.plain)); got != c.want {
			t.Errorf("type1LenIV(%q) = %d, want %d", c.plain, got, c.want)
		}
	}
}

func TestParseType1AdvanceWidthSubrsAndEdges(t *testing.T) {
	// The hsbw lives in a subr the charstring calls.
	subrs := [][]byte{{139, 249, 80, 13, 11}}
	if w, ok := parseType1AdvanceWidth([]byte{139, 10, 14}, subrs); !ok || w != 700 {
		t.Errorf("hsbw in a subr = %v, %v, want 700, true", w, ok)
	}
	// 255 is a 32-bit integer in Type 1.
	if w, ok := parseType1AdvanceWidth([]byte{139, 255, 0, 0, 2, 0x58, 13}, nil); !ok || w != 600 {
		t.Errorf("32-bit operand = %v, %v, want 600, true", w, ok)
	}
	cases := []struct {
		name string
		cs   []byte
		subr [][]byte
	}{
		{"callsubr with an empty stack", []byte{10}, nil},
		{"negative subr index", []byte{138, 10}, [][]byte{{11}}},
		{"subr index past the array", []byte{140, 10}, [][]byte{{11}}},
		{"hsbw with too few operands", []byte{139, 13}, nil},
		{"unterminated charstring", []byte{139, 139}, nil},
		{"truncated 255 operand", []byte{255, 0, 0, 0}, nil},
		{"escape that is not sbw clears the stack", []byte{139, 139, 139, 139, 12, 7, 13}, nil},
		{"subr ends the scan with endchar", []byte{139, 10, 139, 139, 13}, [][]byte{{14}}},
		{"subr recursion is bounded", []byte{139, 10}, [][]byte{{139, 10}}},
	}
	for _, c := range cases {
		if w, ok := parseType1AdvanceWidth(c.cs, c.subr); ok {
			t.Errorf("%s: got width %v, want none", c.name, w)
		}
	}
	// A subr that returns without a width leaves the caller to find its own.
	if w, ok := parseType1AdvanceWidth([]byte{139, 10, 139, 139, 13}, [][]byte{{11}}); !ok || w != 0 {
		t.Errorf("return then hsbw = %v, %v, want 0, true", w, ok)
	}
}

func TestExtractType1Program(t *testing.T) {
	data := widthFont().build()
	p := ExtractType1Program(data, Type1EexecBinStart(data))
	if want := []string{".notdef", "A", "B", "C", "D"}; !reflect.DeepEqual(p.Names, want) {
		t.Errorf("Names = %v, want %v", p.Names, want)
	}
	want := map[string]float64{".notdef": 500, "A": 600, "B": 700, "C": 800}
	if !reflect.DeepEqual(p.Widths, want) {
		t.Errorf("Widths = %v, want %v", p.Widths, want)
	}
	if got := Type1GlyphNames(data); !reflect.DeepEqual(got, p.Names) {
		t.Errorf("Type1GlyphNames = %v, want %v", got, p.Names)
	}
	if got := Type1GlyphWidths(data); !reflect.DeepEqual(got, want) {
		t.Errorf("Type1GlyphWidths = %v, want %v", got, want)
	}
}

func TestExtractType1ProgramVariants(t *testing.T) {
	// lenIV 0 with "-|" as the RD operator: every glyph is still extracted.
	s := newType1()
	s.lenIV, s.rd = 0, "-|"
	s.glyphs = []type1Entry{{"A", hsbwCS(600)}}
	if got := Type1GlyphWidths(s.build()); !reflect.DeepEqual(got, map[string]float64{"A": 600}) {
		t.Errorf("lenIV 0 / -| widths = %v", got)
	}

	// No lenIV declared: the 4-byte default padding applies.
	s = newType1()
	s.noLenIV = true
	s.glyphs = []type1Entry{{"A", hsbwCS(600)}}
	if got := Type1GlyphWidths(s.build()); !reflect.DeepEqual(got, map[string]float64{"A": 600}) {
		t.Errorf("default lenIV widths = %v", got)
	}

	// A 0.0005 FontMatrix halves the widths.
	s = newType1()
	s.header = strings.Replace(type1DefaultHeader, "[0.001 0 0 0.001 0 0]", "[0.0005 0 0 0.0005 0 0]", 1)
	s.glyphs = []type1Entry{{"A", hsbwCS(600)}}
	if got := Type1GlyphWidths(s.build()); !reflect.DeepEqual(got, map[string]float64{"A": 300}) {
		t.Errorf("scaled widths = %v", got)
	}

	// A FontMatrix we cannot use keeps the names but drops the widths.
	s.header = strings.Replace(type1DefaultHeader, "[0.001 0 0 0.001 0 0]", "[0.001 0.5 0 0.001 0 0]", 1)
	data := s.build()
	p := ExtractType1Program(data, Type1EexecBinStart(data))
	if !reflect.DeepEqual(p.Names, []string{"A"}) || len(p.Widths) != 0 {
		t.Errorf("sheared matrix: %+v, want the name and no widths", p)
	}
}

func TestExtractType1ProgramRejectsBadInput(t *testing.T) {
	good := widthFont().build()
	for name, binStart := range map[string]int{"zero": 0, "negative": -1, "past the end": len(good) + 5} {
		if p := ExtractType1Program(good, binStart); p.Names != nil || p.Widths != nil {
			t.Errorf("binStart %s: got %+v, want the zero program", name, p)
		}
	}
	// No eexec at all.
	if names := Type1GlyphNames([]byte("%!PS plain text")); names != nil {
		t.Errorf("no eexec: names = %v", names)
	}
	if widths := Type1GlyphWidths([]byte("%!PS plain text")); widths != nil {
		t.Errorf("no eexec: widths = %v", widths)
	}
	// Encrypted section without /CharStrings.
	s := newType1()
	s.noChars = true
	if sec, iv := Type1CharStringsSection(s.build(), Type1EexecBinStart(s.build())); sec != nil || iv != 4 {
		t.Errorf("no CharStrings: %v, %d, want nil, 4", sec, iv)
	}
	// Garbage after eexec decrypts to something without /CharStrings.
	junk := append([]byte("x eexec\n"), bytes.Repeat([]byte{0x41}, 40)...)
	if p := ExtractType1Program(junk, Type1EexecBinStart(junk)); p.Names != nil {
		t.Errorf("junk: %+v", p)
	}
}

func TestType1CharStringsSection(t *testing.T) {
	s := widthFont()
	s.lenIV = 2
	data := s.build()
	sec, lenIV := Type1CharStringsSection(data, Type1EexecBinStart(data))
	if lenIV != 2 || !bytes.HasPrefix(sec, []byte("/CharStrings")) {
		t.Errorf("section starts %q with lenIV %d, want /CharStrings and 2", sec[:min(12, len(sec))], lenIV)
	}
}

func TestExtractType1ProgramMalformedEntries(t *testing.T) {
	// Entries whose declared length is zero or runs past the data still count
	// as glyph names but yield no width. A sparse, out-of-order and bogus Subrs
	// array leaves gaps rather than shifting the rest.
	s := newType1()
	s.subrs = []type1Entry{
		{"2", []byte{139, 249, 80, 13, 11}},
		{"70000", []byte{11}}, // beyond maxType1Subrs: ignored
	}
	s.glyphs = []type1Entry{
		{"Via2", []byte{141, 10, 14}}, // calls subr 2, the sparse one
		{"Empty", nil},
	}
	data := s.build()
	// Splice in a zero-length and an overrunning entry by text.
	plainStart := Type1EexecBinStart(data)
	plain := DecryptType1Block(data[plainStart:], 55665)
	plain = append(plain, []byte("/Zero 0 RD  ND\n/Over 99999 RD ")...)
	// Re-encrypt the extended private dict.
	ext := append(append([]byte(nil), data[:plainStart]...), type1Encrypt(plain, 55665, 4)...)
	p := ExtractType1Program(ext, plainStart)
	if want := []string{"Via2", "Empty", "Zero", "Over"}; !reflect.DeepEqual(p.Names, want) {
		t.Errorf("Names = %v, want %v", p.Names, want)
	}
	if want := (map[string]float64{"Via2": 700}); !reflect.DeepEqual(p.Widths, want) {
		t.Errorf("Widths = %v, want %v", p.Widths, want)
	}
}

func TestType1SubrArray(t *testing.T) {
	enc := func(cs ...byte) string { return string(type1Encrypt(cs, 4330, 4)) }
	e1, e2 := enc(1, 2, 3), enc(4, 5)
	private := "/Subrs 9 array\n" +
		"dup 1 " + strconv.Itoa(len(e1)) + " RD " + e1 + " NP\n" +
		"dup 4 0 RD  NP\n" + // empty entry
		"dup 99999999999999999999 3 RD abc NP\n" + // number overflows int
		"dup 2 99999999999999999999 RD abc NP\n" + // length overflows int
		"dup 5 " + strconv.Itoa(len(e2)+1000) + " RD " + e2 // runs past the data
	subrs := type1SubrArray([]byte(private), 4)
	if len(subrs) != 2 || subrs[0] != nil || !bytes.Equal(subrs[1], []byte{1, 2, 3}) {
		t.Errorf("subrs = %v, want a gap at 0 and {1 2 3} at 1", subrs)
	}
	if type1SubrArray([]byte("/Other 1 def"), 4) != nil {
		t.Error("no /Subrs should give nil")
	}
}

func TestType1WidthScale(t *testing.T) {
	cases := []struct {
		name   string
		font   string
		scale  float64
		wantOK bool
	}{
		{"no FontMatrix", "%!PS\ncurrentfile eexec\nxxxx", 1, true},
		{"1/1000 matrix", "/FontMatrix [0.001 0 0 0.001 0 0] def eexec", 1, true},
		{"2/1000 matrix", "/FontMatrix[0.002 0 0 0.002 0 0] def eexec", 2, true},
		{"matrix after eexec is ignored", "x eexec /FontMatrix [0.002 0 0 0.002 0 0]", 1, true},
		{"no eexec marker", "/FontMatrix [0.002 0 0 0.002 0 0] def", 2, true},
		{"sheared", "/FontMatrix [0.001 0.001 0 0.001 0 0] def", 0, false},
		{"unparseable number", "/FontMatrix [1.2.3 0 0 1 0 0] def", 0, false},
		{"unparseable second number", "/FontMatrix [0.001 -.- 0 1 0 0] def", 0, false},
	}
	for _, c := range cases {
		scale, ok := Type1WidthScale([]byte(c.font))
		if ok != c.wantOK || (ok && scale != c.scale) {
			t.Errorf("%s: got %v, %v, want %v, %v", c.name, scale, ok, c.scale, c.wantOK)
		}
	}
}

func TestType1EncodingTable(t *testing.T) {
	if enc, ok := Type1EncodingTable(nil, "StandardEncoding"); !ok || enc != StandardEncoding {
		t.Error("StandardEncoding by name")
	}
	if enc, ok := Type1EncodingTable(nil, "WinAnsiEncoding"); !ok || enc != WinAnsiGlyphName {
		t.Error("WinAnsiEncoding by name")
	}
	if enc, ok := Type1EncodingTable(nil, "MacRomanEncoding"); !ok || enc != MacRomanGlyphName {
		t.Error("MacRomanEncoding by name")
	}
	if _, ok := Type1EncodingTable(nil, "MacExpertEncoding"); ok {
		t.Error("MacExpertEncoding is not modelled")
	}

	// From the program: a named encoding, with and without an eexec marker.
	if enc, ok := Type1EncodingTable([]byte("/Encoding StandardEncoding def\neexec"), ""); !ok || enc != StandardEncoding {
		t.Error("built-in StandardEncoding")
	}
	if _, ok := Type1EncodingTable([]byte("/Encoding StandardEncoding def"), ""); !ok {
		t.Error("built-in StandardEncoding without eexec")
	}
	if _, ok := Type1EncodingTable([]byte("/Encoding ISOLatin1Encoding def\neexec"), ""); ok {
		t.Error("an unmodelled built-in name is not ok")
	}
	// From the program: its own array. Out-of-range codes are skipped.
	arr := "/Encoding 256 array\n0 1 255 {1 index exch /.notdef put} for\ndup 65 /Alpha put\ndup 300 /Big put\ndup 66 /Beta put\nreadonly def\neexec"
	enc, ok := Type1EncodingTable([]byte(arr), "")
	if !ok || enc[65] != "Alpha" || enc[66] != "Beta" {
		t.Errorf("array encoding: %q %q %v", enc[65], enc[66], ok)
	}
	if _, ok := Type1EncodingTable([]byte("%!PS\neexec"), ""); ok {
		t.Error("no encoding at all is not ok")
	}
	if _, ok := Type1EncodingTable([]byte("/Encoding 256 array\nreadonly def\neexec"), ""); ok {
		t.Error("an array with no entries is not ok")
	}
}

func TestType1GlyphNameTable(t *testing.T) {
	font := []byte("/Encoding 256 array\ndup 65 /Alpha put\ndup 66 /Beta put\nreadonly def\neexec")
	dict := func(base string, diffs ...pdf.PDFValue) pdf.PDFDict {
		d := pdf.NewPDFDict()
		if base != "" {
			d.Entries.Set("BaseEncoding", pdf.PDFName{Value: base})
		}
		if diffs != nil {
			d.Entries.Set("Differences", pdf.PDFArray(diffs))
		}
		return d
	}

	// A name selects that base outright.
	if enc, ok := Type1GlyphNameTable(font, pdf.PDFName{Value: "WinAnsiEncoding"}); !ok || enc != WinAnsiGlyphName {
		t.Error("named WinAnsiEncoding")
	}
	// No /Encoding entry: the program's own.
	if enc, ok := Type1GlyphNameTable(font, nil); !ok || enc[65] != "Alpha" {
		t.Errorf("default arm: %q %v", enc[65], ok)
	}
	// A dictionary with a modelled BaseEncoding, plus Differences.
	enc, ok := Type1GlyphNameTable(font, dict("WinAnsiEncoding", pdf.PDFInteger(65), pdf.PDFName{Value: "Gamma"}))
	if !ok || enc[65] != "Gamma" || enc[66] != "B" {
		t.Errorf("WinAnsi + Differences: %q %q %v", enc[65], enc[66], ok)
	}
	// A dictionary without BaseEncoding takes the program's built-in.
	enc, ok = Type1GlyphNameTable(font, dict("", pdf.PDFInteger(67), pdf.PDFName{Value: "Gamma"}))
	if !ok || enc[65] != "Alpha" || enc[67] != "Gamma" {
		t.Errorf("built-in + Differences: %q %q %v", enc[65], enc[67], ok)
	}
	// An unmodelled base with Differences still resolves (they may name every code).
	enc, ok = Type1GlyphNameTable(font, dict("MacExpertEncoding", pdf.PDFInteger(1), pdf.PDFName{Value: "Gamma"}))
	if !ok || enc[1] != "Gamma" {
		t.Errorf("unmodelled base + Differences: %q %v", enc[1], ok)
	}
	// An unmodelled base without Differences does not.
	if _, ok := Type1GlyphNameTable(font, dict("MacExpertEncoding")); ok {
		t.Error("unmodelled base without Differences should be not-ok")
	}
}

func TestType1WidthTable(t *testing.T) {
	data := widthFont().build()
	widths := Type1GlyphWidths(data)

	enc, stats := Type1WidthTable(data, pdf.PDFName{Value: "StandardEncoding"}, widths)
	if stats != (WidthStats{GlyphsTotal: len(widths)}) || enc != StandardEncoding {
		t.Errorf("good case: %+v", stats)
	}
	if _, stats := Type1WidthTable(data, pdf.PDFName{Value: "MacExpertEncoding"}, widths); stats.Skip != WidthSkipEncoding {
		t.Errorf("unmodelled encoding: %+v", stats)
	}
	sheared := []byte("/FontMatrix [0.001 0.5 0 0.001 0 0] def\n/Encoding StandardEncoding def\neexec")
	if _, stats := Type1WidthTable(sheared, nil, widths); stats.Skip != WidthSkipFontMatrix {
		t.Errorf("sheared matrix: %+v", stats)
	}
	if _, stats := Type1WidthTable(data, nil, nil); stats.Skip != WidthSkipNoWidths {
		t.Errorf("no widths: %+v", stats)
	}
}

func TestType1EexecBinStart(t *testing.T) {
	cases := []struct {
		name string
		data string
		want int
	}{
		{"no eexec", "%!PS plain", -1},
		{"newline", "x eexec\nBIN", 8},
		{"CRLF and spaces", "x eexec \r\n BIN", 11},
		{"eexec at the very end", "x eexec", 7},
	}
	for _, c := range cases {
		if got := Type1EexecBinStart([]byte(c.data)); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
}
