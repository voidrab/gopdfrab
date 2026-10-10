package fontprog

import (
	"bytes"
	"testing"
)

// widthSubr is a subr that begins with hsbw, as subroutinized programs put it:
// "hsbw <hint> return". The caller pushes sbx and wx first.
var widthSubr = []byte{13, 139, 139, 1, 11} // hsbw, 0 0 hstem, return

// callsubr32 is "idx callsubr" with idx in the 5-byte form real programs use.
func callsubr32(idx byte) []byte { return []byte{255, 0, 0, 0, idx, 10} }

// glyphPlain returns the decrypted charstring of glyph name in program p,
// without its lenIV padding.
func glyphPlain(t *testing.T, p []byte, name string) []byte {
	t.Helper()
	section, lenIV := Type1CharStringsSection(p, Type1EexecBinStart(p))
	for _, m := range Type1CharStringRe.FindAllSubmatchIndex(section, -1) {
		if string(section[m[2]:m[3]]) != name {
			continue
		}
		n := 0
		for _, c := range section[m[4]:m[5]] {
			n = n*10 + int(c-'0')
		}
		return DecryptType1CharString(section[m[1]:m[1]+n], lenIV)
	}
	t.Fatalf("glyph /%s not found", name)
	return nil
}

func inline(t *testing.T, p []byte) ([]byte, bool) {
	t.Helper()
	length1 := Type1EexecBinStart(p)
	out, length2, changed := InlineType1WidthSubrs(p, length1, len(p)-length1)
	if changed && length1+length2 != len(out) {
		t.Errorf("Length2 = %d, want %d", length2, len(out)-length1)
	}
	return out, changed
}

func TestInlineType1WidthSubrs(t *testing.T) {
	for _, lenIV := range []int{0, 4} {
		spec := type1Spec{lenIV: lenIV}
		spec.subrs = []type1Entry{{"0", widthSubr}}
		viaSubr := append(append([]byte{139, 247, 172}, callsubr32(0)...), 14) // 0 280 0 callsubr endchar
		spec.glyphs = []type1Entry{{"f", viaSubr}, {"A", hsbwCS(500)}}
		p := spec.build()

		out, changed := inline(t, p)
		if !changed {
			t.Fatalf("lenIV %d: nothing inlined", lenIV)
		}
		want := []byte{139, 247, 172, 13, 139, 139, 1, 14} // 0 280 hsbw 0 0 hstem endchar
		if got := glyphPlain(t, out, "f"); !bytes.Equal(got, want) {
			t.Errorf("lenIV %d: /f = %v, want %v", lenIV, got, want)
		}
		if got := glyphPlain(t, out, "A"); !bytes.Equal(got, hsbwCS(500)) {
			t.Errorf("lenIV %d: untouched /A = %v", lenIV, got)
		}
		if w := Type1GlyphWidths(out); w["f"] != 280 || w["A"] != 500 {
			t.Errorf("lenIV %d: widths = %v", lenIV, w)
		}
		if !bytes.HasPrefix(out, p[:Type1EexecBinStart(p)]) {
			t.Errorf("lenIV %d: clear-text part changed", lenIV)
		}
	}
}

func TestInlineType1WidthSubrsNested(t *testing.T) {
	spec := newType1()
	spec.subrs = []type1Entry{{"0", widthSubr}, {"1", append(callsubr32(0), 11)}}
	spec.glyphs = []type1Entry{{"f", append(append([]byte{139, 247, 172}, callsubr32(1)...), 14)}}
	out, changed := inline(t, spec.build())
	want := []byte{139, 247, 172, 13, 139, 139, 1, 14}
	if got := glyphPlain(t, out, "f"); !changed || !bytes.Equal(got, want) {
		t.Errorf("nested: changed=%v /f = %v, want %v", changed, got, want)
	}
}

func TestInlineType1WidthSubrsLeavesAlone(t *testing.T) {
	direct := newType1()
	direct.glyphs = []type1Entry{{"A", hsbwCS(500)}}

	missingSubr := newType1()
	missingSubr.subrs = []type1Entry{{"0", widthSubr}}
	missingSubr.glyphs = []type1Entry{{"f", append(append([]byte{139, 247, 172}, callsubr32(7)...), 14)}}

	noWidth := newType1() // the subr draws but never sets the width
	noWidth.subrs = []type1Entry{{"0", []byte{139, 139, 1, 11}}}
	noWidth.glyphs = []type1Entry{{"f", append(append([]byte{139, 247, 172}, callsubr32(0)...), 14)}}

	cyclic := newType1() // subr 0 calls itself
	cyclic.subrs = []type1Entry{{"0", append(callsubr32(0), 11)}}
	cyclic.glyphs = []type1Entry{{"f", append(append([]byte{139, 247, 172}, callsubr32(0)...), 14)}}

	noIndex := newType1() // callsubr with an empty stack
	noIndex.glyphs = []type1Entry{{"f", []byte{10, 14}}}

	for name, p := range map[string][]byte{
		"direct hsbw":    direct.build(),
		"missing subr":   missingSubr.build(),
		"subr sets none": noWidth.build(),
		"cyclic subrs":   cyclic.build(),
		"no subr index":  noIndex.build(),
	} {
		if out, changed := inline(t, p); changed || !bytes.Equal(out, p) {
			t.Errorf("%s: changed=%v", name, changed)
		}
	}

	p := direct.build()
	for name, lengths := range map[string][2]int{
		"no clear text":    {0, len(p)},
		"past the end":     {Type1EexecBinStart(p), len(p)},
		"negative Length2": {Type1EexecBinStart(p), -1},
	} {
		if _, _, changed := InlineType1WidthSubrs(p, lengths[0], lengths[1]); changed {
			t.Errorf("%s: changed", name)
		}
	}
	// Hex-encoded eexec sections are not rewritten.
	hex := []byte("%!PS\ncurrentfile eexec\n0123456789abcdef")
	if _, _, changed := InlineType1WidthSubrs(hex, 23, len(hex)-23); changed {
		t.Error("hex eexec rewritten")
	}
}

// TestInlineWidthSubrEncodings drives inlineWidthSubr directly: every number
// encoding as the subr index, and charstrings it must leave alone.
func TestInlineWidthSubrEncodings(t *testing.T) {
	subrs := make([][]byte, 1200)
	subrs[5] = widthSubr
	subrs[1100] = widthSubr
	wx := []byte{139, 247, 172} // 0 280
	want := []byte{139, 247, 172, 13, 139, 139, 1, 14}
	for name, idx := range map[string][]byte{
		"1-byte":      {139 + 5},
		"2-byte":      {250, 1100 - 108 - 3*256}, // (250-247)*256 + b + 108
		"shortint 28": {28, 0, 5},
		"5-byte":      {255, 0, 0, 0, 5},
	} {
		cs := append(append(append(append([]byte{}, wx...), idx...), 10), 14)
		got, ok := inlineWidthSubr(cs, subrs)
		if !ok || !bytes.Equal(got, want) {
			t.Errorf("%s index: ok=%v %v, want %v", name, ok, got, want)
		}
	}
	for name, cs := range map[string][]byte{
		"negative index":      {139, 247, 172, 251, 0, 10, 14},
		"truncated number":    {139, 255, 0},
		"operands only":       {139, 139},
		"other command first": {139, 139, 1, 14},
		"sbw-less escape":     {139, 12, 6, 14},
	} {
		if _, ok := inlineWidthSubr(cs, subrs); ok {
			t.Errorf("%s: inlined", name)
		}
	}
	// A subr that begins with sbw (12 7) counts as a width command too:
	// "0 0 280 0 0 callsubr" keeps sbx sby wx wy and drops the index.
	sbwSubrs := [][]byte{{12, 7, 11}}
	got, ok := inlineWidthSubr([]byte{139, 139, 247, 172, 139, 139, 10, 14}, sbwSubrs)
	if !ok || !bytes.Equal(got, []byte{139, 139, 247, 172, 139, 12, 7, 14}) {
		t.Errorf("sbw: ok=%v %v", ok, got)
	}
}

// type1Raw builds a program around a hand-written Private dict text, for
// shapes type1Spec cannot express.
func type1Raw(private string) []byte {
	return append([]byte(type1DefaultHeader), type1Encrypt([]byte(private), 55665, 4)...)
}

func TestInlineType1WidthSubrsMalformedPrograms(t *testing.T) {
	for name, p := range map[string][]byte{
		"no CharStrings":       type1Raw("dup /Private 8 dict dup begin\n"),
		"length past the end":  type1Raw("/CharStrings 1 dict dup begin\n/f 9999 RD xx ND\n"),
		"shorter than lenIV":   type1Raw("/CharStrings 1 dict dup begin\n/f 2 RD xx ND\n"),
		"length overflows int": type1Raw("/CharStrings 1 dict dup begin\n/f 99999999999999999999 RD xx ND\n"),
	} {
		if _, changed := inline(t, p); changed {
			t.Errorf("%s: changed", name)
		}
	}
}
