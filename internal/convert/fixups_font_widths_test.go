package convert

import (
	"bytes"
	"strconv"
	"testing"

	"github.com/voidrab/gopdfrab/internal/fontprog"
	"github.com/voidrab/gopdfrab/internal/fontsubst"
	"github.com/voidrab/gopdfrab/internal/pdf"
)

// dictOf is a terse pdf.PDFDict literal for the font fixtures below.
func dictOf(entries map[string]pdf.PDFValue) pdf.PDFDict {
	return pdf.PDFDict{Entries: pdf.DictOf(entries)}
}

// streamOf is a decoded (unfiltered) stream dict carrying raw.
func streamOf(raw []byte) pdf.PDFDict {
	d := dictOf(map[string]pdf.PDFValue{})
	d.SetStream(raw)
	return d
}

// undecodableStream is a stream whose declared Flate filter cannot decode.
func undecodableStream() pdf.PDFDict {
	d := streamOf([]byte("not zlib"))
	d.Entries.Set("Filter", pdf.PDFName{Value: "FlateDecode"})
	return d
}

func ints(vs ...int) pdf.PDFArray {
	out := make(pdf.PDFArray, len(vs))
	for i, v := range vs {
		out[i] = pdf.PDFInteger(v)
	}
	return out
}

func widthsOf(t *testing.T, d pdf.PDFDict) []int {
	t.Helper()
	arr, ok := d.Entries.Get("Widths").(pdf.PDFArray)
	if !ok {
		t.Fatalf("Widths = %v, want an array", d.Entries.Get("Widths"))
	}
	out := make([]int, len(arr))
	for i, v := range arr {
		n, ok := pdf.PDFNumberToInt(v)
		if !ok {
			t.Fatalf("Widths[%d] = %v, want a number", i, v)
		}
		out[i] = n
	}
	return out
}

func equalInts(a, b []int) bool {
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

// t1Int encodes n as a Type 1 charstring number.
func t1Int(n int) []byte {
	switch {
	case n >= -107 && n <= 107:
		return []byte{byte(n + 139)}
	case n >= 108 && n <= 1131:
		n -= 108
		return []byte{byte(247 + n/256), byte(n % 256)}
	}
	panic("t1Int: out of range")
}

// type1Program builds a minimal Type 1 font with StandardEncoding whose glyphs
// each consist of "0 <width> hsbw endchar", in name order.
func type1Program(names []string, widths map[string]int) []byte {
	private := "dup /Private 8 dict dup begin\n/CharStrings " + strconv.Itoa(len(names)) + " dict dup begin\n"
	for _, n := range names {
		cs := append(t1Int(0), t1Int(widths[n])...)
		cs = append(cs, 13, 14)
		enc := type1Encrypt(cs, 4330, 4)
		private += "/" + n + " " + strconv.Itoa(len(enc)) + " RD " + string(enc) + " ND\n"
	}
	private += "end\n"
	header := "%!PS-AdobeFont-1.0: T\n/Encoding StandardEncoding def\ncurrentfile eexec\n"
	return append([]byte(header), type1Encrypt([]byte(private), 55665, 4)...)
}

// type1Font is a Type1 font dict embedding program under FontFile, with the
// given widths starting at code 65 ("A") and an optional /MissingWidth.
func type1Font(program []byte, widths pdf.PDFArray, missingWidth int) pdf.PDFDict {
	desc := dictOf(map[string]pdf.PDFValue{"FontFile": streamOf(program)})
	if missingWidth != 0 {
		desc.Entries.Set("MissingWidth", pdf.PDFInteger(missingWidth))
	}
	return dictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Font"}, "Subtype": pdf.PDFName{Value: "Type1"},
		"FontDescriptor": desc, "FirstChar": pdf.PDFInteger(65), "Widths": widths,
	})
}

func TestFixType1WidthsCorrectsMismatches(t *testing.T) {
	prog := type1Program([]string{"A", "B"}, map[string]int{"A": 600, "B": 700})

	// A is wrong, B is within tolerance, and the non-number, out-of-range and
	// unnamed entries are skipped without disturbing their neighbours.
	d := type1Font(prog, pdf.PDFArray{pdf.PDFInteger(500), pdf.PDFReal(700.5), pdf.PDFName{Value: "x"}}, 0)
	if !fixFontMetricsDict(d) {
		t.Fatal("fixFontMetricsDict = false, want true (A is 500, program says 600)")
	}
	w := d.Entries.Get("Widths").(pdf.PDFArray)
	if w[0] != pdf.PDFInteger(600) || w[1] != pdf.PDFReal(700.5) || w[2] != (pdf.PDFName{Value: "x"}) {
		t.Errorf("Widths = %v, want [600 700.5 /x]", w)
	}
	if fixFontMetricsDict(d) {
		t.Error("second pass changed the fixed font")
	}

	// A zero width means "none declared" and is never compared; codes beyond 255
	// are skipped.
	d = type1Font(prog, ints(0), 0)
	d.Entries.Set("FirstChar", pdf.PDFInteger(255))
	d.Entries.Set("Widths", ints(500, 500))
	if fixFontMetricsDict(d) {
		t.Error("zero width or out-of-range code was rewritten")
	}
}

func TestFixType1WidthsBailsOut(t *testing.T) {
	prog := type1Program([]string{"A"}, map[string]int{"A": 600})
	noWidthProg := type1Program(nil, nil)

	cases := map[string]pdf.PDFDict{
		"no widths":          type1Font(prog, ints(500), 0),
		"undecodable":        type1Font(prog, ints(500), 0),
		"empty program":      type1Font(nil, ints(500), 0),
		"unknown encoding":   type1Font(prog, ints(500), 0),
		"program has glyphs": type1Font(noWidthProg, ints(500), 0),
	}
	cases["no widths"].Entries.Del("Widths")
	cases["undecodable"].Entries.Get("FontDescriptor").(pdf.PDFDict).Entries.Set("FontFile", undecodableStream())
	cases["unknown encoding"].Entries.Set("Encoding", pdf.PDFName{Value: "NoSuchEncoding"})
	for name, d := range cases {
		if fixFontMetricsDict(d) {
			t.Errorf("%s: fixFontMetricsDict = true, want false", name)
		}
	}
}

func TestGrowType1Widths(t *testing.T) {
	// A, C defined; B is not, so it falls back to the missing width.
	prog := type1Program([]string{"A", "C"}, map[string]int{"A": 600, "C": 700})

	d := type1Font(prog, ints(600), 250)
	if !fixFontMetricsDict(d) {
		t.Fatal("fixFontMetricsDict = false, want true (C's width differs from /MissingWidth 250)")
	}
	if got, want := widthsOf(t, d), []int{600, 250, 700}; !equalInts(got, want) {
		t.Errorf("Widths = %v, want %v", got, want)
	}
	if d.Entries.Get("FirstChar") != pdf.PDFInteger(65) || d.Entries.Get("LastChar") != pdf.PDFInteger(67) {
		t.Errorf("FirstChar/LastChar = %v/%v, want 65/67", d.Entries.Get("FirstChar"), d.Entries.Get("LastChar"))
	}
	if fixFontMetricsDict(d) {
		t.Error("second pass grew the widths again")
	}

	// Without a declared /MissingWidth, or with no widths at all, nothing grows.
	if fixFontMetricsDict(type1Font(prog, ints(600), 0)) {
		t.Error("grew /Widths without a /MissingWidth")
	}
	if growType1Widths(dictOf(nil), dictOf(map[string]pdf.PDFValue{"MissingWidth": pdf.PDFInteger(250)}),
		[256]string{}, nil, 65, nil) {
		t.Error("grew an empty /Widths")
	}
}

// cffWithDefaultWidth is buildMinimalCFF with defaultWidthX set to 600, so its
// glyph "A" (a lone endchar) has advance width 600.
func cffWithDefaultWidth() []byte {
	cff := buildMinimalCFF()
	i32 := func(v int) []byte { return []byte{29, 0, 0, 0, byte(v)} }
	old := append(i32(4), i32(56)...)
	patched := append(i32(5), i32(56)...)
	cff = bytes.Replace(cff, old, patched, 1)
	return bytes.Replace(cff, []byte{0x8b, 20, 0x8b, 21}, []byte{248, 236, 20, 0x8b, 21}, 1)
}

func type1CFont(program pdf.PDFDict, widths pdf.PDFArray) pdf.PDFDict {
	return dictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Font"}, "Subtype": pdf.PDFName{Value: "Type1"},
		"FontDescriptor": dictOf(map[string]pdf.PDFValue{"FontFile3": program}),
		"FirstChar":      pdf.PDFInteger(65), "Widths": widths,
		"Encoding": pdf.PDFName{Value: "WinAnsiEncoding"},
	})
}

func TestFixType1CWidths(t *testing.T) {
	prog := streamOf(cffWithDefaultWidth())

	// A is wrong; B has no glyph in the program; the name, zero and
	// out-of-range entries are skipped.
	d := type1CFont(prog, pdf.PDFArray{pdf.PDFInteger(500), pdf.PDFInteger(500), pdf.PDFName{Value: "x"}, pdf.PDFInteger(0)})
	if !fixFontMetricsDict(d) {
		t.Fatal("fixFontMetricsDict = false, want true")
	}
	w := d.Entries.Get("Widths").(pdf.PDFArray)
	if w[0] != pdf.PDFInteger(600) || w[1] != pdf.PDFInteger(500) {
		t.Errorf("Widths = %v, want A corrected to 600 and B untouched", w)
	}
	if fixFontMetricsDict(d) {
		t.Error("second pass changed the fixed font")
	}

	d = type1CFont(prog, ints(500, 500))
	d.Entries.Set("FirstChar", pdf.PDFInteger(255))
	d.Entries.Set("Widths", ints(500, 500))
	if fixFontMetricsDict(d) {
		t.Error("out-of-range code was rewritten")
	}
	d = type1CFont(prog, ints(500))
	d.Entries.Set("FirstChar", pdf.PDFInteger(1)) // code 1 names no glyph
	if fixFontMetricsDict(d) {
		t.Error("code without a glyph name was rewritten")
	}

	cases := map[string]pdf.PDFDict{
		"no widths":     type1CFont(prog, ints(500)),
		"undecodable":   type1CFont(undecodableStream(), ints(500)),
		"empty program": type1CFont(streamOf(nil), ints(500)),
		"not a CFF":     type1CFont(streamOf([]byte("garbage garbage")), ints(500)),
		"no encoding":   type1CFont(prog, ints(500)),
	}
	cases["no widths"].Entries.Del("Widths")
	cases["no encoding"].Entries.Del("Encoding")
	for name, d := range cases {
		if fixFontMetricsDict(d) {
			t.Errorf("%s: fixFontMetricsDict = true, want false", name)
		}
	}
}

func cidFont(subtype string, key string, program pdf.PDFDict, w pdf.PDFValue) pdf.PDFDict {
	d := dictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Font"}, "Subtype": pdf.PDFName{Value: subtype},
		"FontDescriptor": dictOf(map[string]pdf.PDFValue{key: program}),
	})
	if w != nil {
		d.Entries.Set("W", w)
	}
	return d
}

func TestFixCIDCFFWidthsBailsOut(t *testing.T) {
	w := pdf.PDFArray{pdf.PDFInteger(1), ints(500)}
	cases := map[string]pdf.PDFDict{
		"undecodable":     cidFont("CIDFontType0", "FontFile3", undecodableStream(), w),
		"not a CID CFF":   cidFont("CIDFontType0", "FontFile3", streamOf([]byte("garbage")), w),
		"already correct": cidFont("CIDFontType0", "FontFile3", streamOf(buildMinimalCIDCFF()), pdf.PDFArray{pdf.PDFInteger(1), ints(600, 700)}),
	}
	for name, d := range cases {
		if fixFontMetricsDict(d) {
			t.Errorf("%s: fixFontMetricsDict = true, want false", name)
		}
	}
}

func TestFixCIDTrueTypeWidths(t *testing.T) {
	ttf := streamOf(fontsubst.LiberationSansRegular)
	// CID 36 is "A" in Liberation Sans; CID 60000 is beyond its hmtx.
	d := cidFont("CIDFontType2", "FontFile2", ttf, pdf.PDFArray{pdf.PDFInteger(36), ints(100)})
	if !fixFontMetricsDict(d) {
		t.Fatal("fixFontMetricsDict = false, want true")
	}
	if fixFontMetricsDict(d) {
		t.Error("second pass changed the fixed font")
	}

	cases := map[string]pdf.PDFDict{
		"no W":        cidFont("CIDFontType2", "FontFile2", ttf, nil),
		"undecodable": cidFont("CIDFontType2", "FontFile2", undecodableStream(), pdf.PDFArray{pdf.PDFInteger(36), ints(100)}),
		"not sfnt":    cidFont("CIDFontType2", "FontFile2", streamOf([]byte("garbage")), pdf.PDFArray{pdf.PDFInteger(36), ints(100)}),
	}
	for name, d := range cases {
		if fixFontMetricsDict(d) {
			t.Errorf("%s: fixFontMetricsDict = true, want false", name)
		}
	}
}

func TestFixSimpleTrueTypeWidths(t *testing.T) {
	font := func(program pdf.PDFDict, widths pdf.PDFArray) pdf.PDFDict {
		return dictOf(map[string]pdf.PDFValue{
			"Type": pdf.PDFName{Value: "Font"}, "Subtype": pdf.PDFName{Value: "TrueType"},
			"FontDescriptor": dictOf(map[string]pdf.PDFValue{"FontFile2": program}),
			"FirstChar":      pdf.PDFInteger(65), "Widths": widths,
			"Encoding": pdf.PDFName{Value: "WinAnsiEncoding"},
		})
	}
	ttf := streamOf(fontsubst.LiberationSansRegular)

	// A (wrong), a zero width, a name and a code beyond 255 sit side by side.
	d := font(ttf, pdf.PDFArray{pdf.PDFInteger(100), pdf.PDFInteger(0), pdf.PDFName{Value: "x"}})
	if !fixFontMetricsDict(d) {
		t.Fatal("fixFontMetricsDict = false, want true")
	}
	if got := widthsOf2(d); got[0] != 667 || got[1] != 667 {
		t.Errorf("Widths = %v, want A corrected to 667 and B's zero filled in", got)
	}
	if fixFontMetricsDict(d) {
		t.Error("second pass changed the fixed font")
	}

	d = font(ttf, ints(100, 100))
	d.Entries.Set("FirstChar", pdf.PDFInteger(256))
	if fixFontMetricsDict(d) {
		t.Error("out-of-range code was rewritten")
	}
	// Code 0x81 is undefined in WinAnsi: no glyph, so the width stays.
	d = font(ttf, ints(100))
	d.Entries.Set("FirstChar", pdf.PDFInteger(0x81))
	if fixFontMetricsDict(d) {
		t.Error("code without a glyph was rewritten")
	}

	cases := map[string]pdf.PDFDict{
		"no widths":   font(ttf, ints(100)),
		"undecodable": font(undecodableStream(), ints(100)),
		"not sfnt":    font(streamOf([]byte("garbage")), ints(100)),
	}
	cases["no widths"].Entries.Del("Widths")
	for name, d := range cases {
		if fixFontMetricsDict(d) {
			t.Errorf("%s: fixFontMetricsDict = true, want false", name)
		}
	}
}

func widthsOf2(d pdf.PDFDict) []int {
	arr := d.Entries.Get("Widths").(pdf.PDFArray)
	out := make([]int, len(arr))
	for i, v := range arr {
		out[i], _ = pdf.PDFNumberToInt(v)
	}
	return out
}

func TestFixFontMetricsDictRouting(t *testing.T) {
	// Anything that is not a font, or whose subtype has no program to compare
	// against, is left alone.
	for name, d := range map[string]pdf.PDFDict{
		"not a font":          dictOf(map[string]pdf.PDFValue{"Type": pdf.PDFName{Value: "Page"}}),
		"TrueType no program": dictOf(map[string]pdf.PDFValue{"Type": pdf.PDFName{Value: "Font"}, "Subtype": pdf.PDFName{Value: "TrueType"}}),
		"Type1 no program":    dictOf(map[string]pdf.PDFValue{"Type": pdf.PDFName{Value: "Font"}, "Subtype": pdf.PDFName{Value: "Type1"}}),
		"CIDType2 no program": dictOf(map[string]pdf.PDFValue{"Type": pdf.PDFName{Value: "Font"}, "Subtype": pdf.PDFName{Value: "CIDFontType2"}}),
		"CIDType0 no program": dictOf(map[string]pdf.PDFValue{"Type": pdf.PDFName{Value: "Font"}, "Subtype": pdf.PDFName{Value: "CIDFontType0"}}),
		"unknown subtype":     dictOf(map[string]pdf.PDFValue{"Type": pdf.PDFName{Value: "Font"}, "Subtype": pdf.PDFName{Value: "Weird"}}),
	} {
		if fixFontMetricsDict(d) {
			t.Errorf("%s: fixFontMetricsDict = true, want false", name)
		}
	}

	// A CIDFontType0 with a CID-keyed CFF routes to the CFF repair.
	d := cidFont("CIDFontType0", "FontFile3", streamOf(buildMinimalCIDCFF()), pdf.PDFArray{pdf.PDFInteger(1), ints(500, 500)})
	if !fixFontMetricsDict(d) {
		t.Error("CIDFontType0 with wrong widths: fixFontMetricsDict = false, want true")
	}
}

func TestFixType3Widths(t *testing.T) {
	proc := func(src string) pdf.PDFDict { return streamOf([]byte(src)) }
	font := func(widths pdf.PDFArray, procs map[string]pdf.PDFValue) pdf.PDFDict {
		return dictOf(map[string]pdf.PDFValue{
			"Type": pdf.PDFName{Value: "Font"}, "Subtype": pdf.PDFName{Value: "Type3"},
			"FirstChar": pdf.PDFInteger(65), "Widths": widths,
			"CharProcs": dictOf(procs),
			"Encoding": dictOf(map[string]pdf.PDFValue{"Differences": pdf.PDFArray{
				pdf.PDFInteger(65), pdf.PDFName{Value: "a"}, pdf.PDFName{Value: "b"},
				pdf.PDFName{Value: "c"}, pdf.PDFName{Value: "d"}, pdf.PDFName{Value: "e"},
			}}),
		})
	}
	procs := map[string]pdf.PDFValue{
		"a": proc("500 0 d0"),
		"b": proc("500 0 d0"),
		"c": proc("ignored"), // no d0/d1: width -1, never compared
		"d": undecodableStream(),
		// "e" has no procedure at all.
	}
	// a: integer width corrected; b: real width corrected to a real; c, d, e:
	// left alone; a name entry is skipped; a code with no glyph is skipped.
	d := font(pdf.PDFArray{
		pdf.PDFInteger(100), pdf.PDFReal(100.5), pdf.PDFInteger(1), pdf.PDFInteger(1),
		pdf.PDFInteger(1), pdf.PDFName{Value: "x"}, pdf.PDFInteger(1),
	}, procs)
	if !fixFontMetricsDict(d) {
		t.Fatal("fixFontMetricsDict = false, want true")
	}
	w := d.Entries.Get("Widths").(pdf.PDFArray)
	if w[0] != pdf.PDFInteger(500) {
		t.Errorf("integer width = %v, want 500", w[0])
	}
	if w[1] != pdf.PDFReal(500) {
		t.Errorf("real width = %v, want real 500", w[1])
	}
	for i := 2; i < 5; i++ {
		if w[i] != pdf.PDFInteger(1) {
			t.Errorf("Widths[%d] = %v, want 1 untouched", i, w[i])
		}
	}
	if fixFontMetricsDict(d) {
		t.Error("second pass changed the fixed font")
	}

	// A procedure that is a plain dict without a stream is skipped.
	d = font(ints(100), map[string]pdf.PDFValue{"a": dictOf(nil)})
	if fixFontMetricsDict(d) {
		t.Error("non-stream CharProc was used")
	}

	for name, mutate := range map[string]func(pdf.PDFDict){
		"no widths":      func(d pdf.PDFDict) { d.Entries.Del("Widths") },
		"no Differences": func(d pdf.PDFDict) { d.Entries.Set("Encoding", dictOf(nil)) },
	} {
		d := font(ints(100), procs)
		mutate(d)
		if fixFontMetricsDict(d) {
			t.Errorf("%s: fixFontMetricsDict = true, want false", name)
		}
	}
}

// TestTargetedFontFixersFallBackWithoutRefs verifies that targeted fixers hand
// back "not handled" when there is no pass or no issue to locate, leaving the
// caller to fall back to the full walk.
func TestTargetedFontFixersFallBackWithoutRefs(t *testing.T) {
	for name, f := range map[string]targetedFixer{
		"metrics":     fontMetricFixer{},
		"subset meta": fontSubsetMetaFixer{},
	} {
		changed, handled, err := f.fixTargeted(nil, nil)
		if changed || handled || err != nil {
			t.Errorf("%s: fixTargeted(nil, nil) = %v, %v, %v; want false, false, nil", name, changed, handled, err)
		}
	}
}

func subsetFont(subtype, baseFont string, desc pdf.PDFValue) pdf.PDFDict {
	d := dictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Font"}, "Subtype": pdf.PDFName{Value: subtype},
		"BaseFont": pdf.PDFName{Value: baseFont},
	})
	if desc != nil {
		d.Entries.Set("FontDescriptor", desc)
	}
	return d
}

func TestFixFontSubsetMetaDictIgnoresWhatItCannotRepair(t *testing.T) {
	prog := streamOf(type1Program([]string{"A"}, map[string]int{"A": 600}))
	withFile := func(key string, ff pdf.PDFDict) pdf.PDFDict { return dictOf(map[string]pdf.PDFValue{key: ff}) }
	cases := map[string]pdf.PDFDict{
		"not a font":                  dictOf(map[string]pdf.PDFValue{"Type": pdf.PDFName{Value: "Page"}}),
		"not a subset":                subsetFont("Type1", "Helvetica", withFile("FontFile", prog)),
		"no descriptor":               subsetFont("Type1", "ABCDEF+X", nil),
		"unrelated subtype":           subsetFont("TrueType", "ABCDEF+X", withFile("FontFile2", prog)),
		"CIDType0 no program":         subsetFont("CIDFontType0", "ABCDEF+X", dictOf(nil)),
		"CIDType2 no program":         subsetFont("CIDFontType2", "ABCDEF+X", dictOf(nil)),
		"Type1 no program":            subsetFont("Type1", "ABCDEF+X", dictOf(nil)),
		"Type1 undecodable":           subsetFont("Type1", "ABCDEF+X", withFile("FontFile", undecodableStream())),
		"Type1 empty program":         subsetFont("Type1", "ABCDEF+X", withFile("FontFile", streamOf(nil))),
		"Type1 no glyphs":             subsetFont("Type1", "ABCDEF+X", withFile("FontFile", streamOf(type1Program(nil, nil)))),
		"Type1C undecodable":          subsetFont("Type1", "ABCDEF+X", withFile("FontFile3", undecodableStream())),
		"Type1C not a CFF":            subsetFont("Type1", "ABCDEF+X", withFile("FontFile3", streamOf([]byte("garbage")))),
		"CIDType0 undecodable":        subsetFont("CIDFontType0", "ABCDEF+X", withFile("FontFile3", undecodableStream())),
		"CIDType0 not a CFF":          subsetFont("CIDFontType0", "ABCDEF+X", withFile("FontFile3", streamOf([]byte("garbage")))),
		"CIDType0 name-keyed":         subsetFont("CIDFontType0", "ABCDEF+X", withFile("FontFile3", streamOf(buildMinimalCFF()))),
		"CIDType2 undecodable":        subsetFont("CIDFontType2", "ABCDEF+X", withFile("FontFile2", undecodableStream())),
		"CIDType2 not sfnt":           subsetFont("CIDFontType2", "ABCDEF+X", withFile("FontFile2", streamOf([]byte("garbage")))),
		"CIDType0 predefined charset": subsetFont("CIDFontType0", "ABCDEF+X", withFile("FontFile3", streamOf(cidCFFWithCharsetOffset(0)))),
		"CIDType2 no maxp": subsetFont("CIDFontType2", "ABCDEF+X",
			withFile("FontFile2", streamOf(wrapInSfnt(0x00010000, map[string][]byte{"head": make([]byte, 54)})))),
	}
	for name, d := range cases {
		if fixFontSubsetMetaDict(d) {
			t.Errorf("%s: fixFontSubsetMetaDict = true, want false", name)
		}
	}
}

// cidCFFWithCharsetOffset is buildMinimalCIDCFF with its Top DICT charset
// operand rewritten to off (0..2 name a predefined charset, which carries no
// CIDs to read).
func cidCFFWithCharsetOffset(off int) []byte {
	cff := buildMinimalCIDCFF()
	td, _ := fontprog.ParseCFFTopDict(cff)
	i32 := func(v int) []byte { return []byte{29, byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v), 15} }
	return bytes.Replace(cff, i32(td.CharsetOffset), i32(off), 1)
}
