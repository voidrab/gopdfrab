package fontprog

import (
	"testing"
)

// TestHexToUnicode covers the direct-value and too-short-to-decode paths.
func TestHexToUnicode(t *testing.T) {
	if got, ok := hexToUnicode("0041"); !ok || got != 0x0041 {
		t.Errorf("hexToUnicode(0041) = (%04X, %v), want (0041, true)", got, ok)
	}
	if _, ok := hexToUnicode("41"); ok {
		t.Error("hexToUnicode(41) (single byte) ok = true, want false")
	}
}

// TestParseToUnicodeCMap covers all three bfchar/bfrange forms the parser
// supports (single bfchar entries are already exercised via
// assertSymbolicSubstitute): a bfrange with a single hex base (incrementing
// per code), a bfrange with a destination array (one hex value per code),
// and the malformed entries that must be skipped rather than panic.
func TestParseToUnicodeCMap(t *testing.T) {
	cmap := []byte(`
1 beginbfchar
<41> <0041>
endbfchar
2 beginbfrange
<42> <44> <0042>
<61> <63> [<0061> <0062> <0063>]
<FF> <10> <2000>
endbfrange
`)
	got := ParseToUnicodeCMap(cmap)

	want := map[int]uint16{
		0x41: 0x0041, // bfchar
		0x42: 0x0042, // bfrange, base hex
		0x43: 0x0043,
		0x44: 0x0044,
		0x61: 0x0061, // bfrange, destination array
		0x62: 0x0062,
		0x63: 0x0063,
	}
	for code, u := range want {
		if got[code] != u {
			t.Errorf("ParseToUnicodeCMap[%02X] = %04X, want %04X", code, got[code], u)
		}
	}
	// <FF> <10> has hi < lo and must be skipped, not produce entries.
	if _, ok := got[0xFF]; ok {
		t.Errorf("ParseToUnicodeCMap produced an entry for a hi<lo range: %v", got)
	}
}

func TestCmapTokenizeAndParseInt(t *testing.T) {
	toks := CmapTokenize([]byte("% comment\n<0041> 65 (lit\\)eral) endcidrange"))
	var texts []string
	for _, tok := range toks {
		texts = append(texts, tok.Text)
	}
	want := []string{"<0041>", "65", "(lit\\)eral)", "endcidrange"}
	if len(texts) != len(want) {
		t.Fatalf("CmapTokenize = %v, want %v", texts, want)
	}
	for i := range want {
		if texts[i] != want[i] {
			t.Errorf("token[%d] = %q, want %q", i, texts[i], want[i])
		}
	}

	if n, ok := CmapParseInt("12345"); !ok || n != 12345 {
		t.Errorf("CmapParseInt(12345) = %d, %v", n, ok)
	}
	if _, ok := CmapParseInt("12a"); ok {
		t.Error("CmapParseInt should reject non-digit characters")
	}
	if _, ok := CmapParseInt(""); ok {
		t.Error("CmapParseInt should reject an empty token")
	}
}

func TestParseToUnicodeCMapSkipsUnusableEntries(t *testing.T) {
	cmap := []byte(`
2 beginbfchar
<FFFFFFFFFF> <0041>
<42> <43>
endbfchar
2 beginbfrange
<FFFFFFFFFF> <FFFFFFFFFE> <0041>
<50> <51> <41>
<60> <61> <>
endbfrange
`)
	// Code overflowing 32 bits, a one-byte destination, and range forms that
	// lack a usable destination are all dropped.
	if got := ParseToUnicodeCMap(cmap); len(got) != 0 {
		t.Errorf("ParseToUnicodeCMap = %v, want nothing", got)
	}

	// A destination array shorter than the range maps only what it has, and
	// one with an unusable entry skips just that code.
	got := ParseToUnicodeCMap([]byte("1 beginbfrange\n<10> <14> [<0041> <42> <0043>]\nendbfrange"))
	want := map[int]uint16{0x10: 0x41, 0x12: 0x43}
	if len(got) != len(want) || got[0x10] != 0x41 || got[0x12] != 0x43 {
		t.Errorf("ParseToUnicodeCMap(array) = %v, want %v", got, want)
	}
}

func TestCmapTokenizeEdges(t *testing.T) {
	texts := func(src string) []string {
		var out []string
		for _, tok := range CmapTokenize([]byte(src)) {
			out = append(out, tok.Text)
		}
		return out
	}
	// Trailing whitespace ends the scan; unterminated hex and nested literal
	// strings are taken to the end or the matching parenthesis.
	if got := texts("a  \n "); len(got) != 1 || got[0] != "a" {
		t.Errorf("trailing whitespace: %q", got)
	}
	if got := texts("<00 41"); len(got) != 1 || got[0] != "<00 41" {
		t.Errorf("unterminated hex: %q", got)
	}
	if got := texts("(a(b)c) d"); len(got) != 2 || got[0] != "(a(b)c)" || got[1] != "d" {
		t.Errorf("nested literal: %q", got)
	}
	toks := CmapTokenize([]byte("ab <cd>"))
	if len(toks) != 2 || toks[1].Start != 3 || toks[1].End != 7 {
		t.Errorf("token ranges = %+v", toks)
	}
}
