package fontprog

import (
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// fontDict builds a font dictionary with the given /Encoding value (nil for
// none), /BaseFont name and descriptor /Flags (0 for no descriptor).
func fontDict(enc pdf.PDFValue, baseFont string, flags int) pdf.PDFDict {
	d := pdf.NewPDFDict()
	if enc != nil {
		d.Entries.Set("Encoding", enc)
	}
	if baseFont != "" {
		d.Entries.Set("BaseFont", pdf.PDFName{Value: baseFont})
	}
	if flags != 0 {
		desc := pdf.NewPDFDict()
		desc.Entries.Set("Flags", pdf.PDFInteger(flags))
		d.Entries.Set("FontDescriptor", desc)
	}
	return d
}

func encDict(base string, diffs ...pdf.PDFValue) pdf.PDFDict {
	d := pdf.NewPDFDict()
	if base != "" {
		d.Entries.Set("BaseEncoding", pdf.PDFName{Value: base})
	}
	if diffs != nil {
		d.Entries.Set("Differences", pdf.PDFArray(diffs))
	}
	return d
}

func TestSimpleFontCodeToUnicodeBases(t *testing.T) {
	if got := SimpleFontCodeToUnicode(pdf.PDFName{Value: "MacRomanEncoding"}); got != MacRomanToUnicode {
		t.Error("MacRomanEncoding should select the Mac table")
	}
	// StandardEncoding and an unmodelled name both take the Standard table.
	for _, name := range []string{"StandardEncoding", "Whatever"} {
		if got := SimpleFontCodeToUnicode(pdf.PDFName{Value: name}); got != StandardToUnicode {
			t.Errorf("%q should select the Standard table", name)
		}
	}
	// No usable /Encoding at all is read as WinAnsi.
	if got := SimpleFontCodeToUnicode(nil); got != WinAnsiToUnicode {
		t.Error("an absent encoding should read as WinAnsi")
	}
	// Differences: out-of-range codes are skipped, later names advance the code.
	got := SimpleFontCodeToUnicode(encDict("", pdf.PDFInteger(300), pdf.PDFName{Value: "A"}, pdf.PDFInteger(65), pdf.PDFName{Value: "B"}, pdf.PDFName{Value: "C"}))
	if got[65] != 'B' || got[66] != 'C' {
		t.Errorf("Differences 65 B C -> %04X %04X, want B C", got[65], got[66])
	}
}

func TestNamedEncodingTable(t *testing.T) {
	if got, ok := namedEncodingTable("StandardEncoding"); !ok || got != StandardEncoding {
		t.Error("StandardEncoding")
	}
	if got, ok := namedEncodingTable("WinAnsiEncoding"); !ok || got != WinAnsiGlyphName {
		t.Error("WinAnsiEncoding")
	}
	if got, ok := namedEncodingTable("MacRomanEncoding"); !ok || got != MacRomanGlyphName {
		t.Error("MacRomanEncoding")
	}
	if _, ok := namedEncodingTable("MacExpertEncoding"); ok {
		t.Error("MacExpertEncoding is not modelled")
	}
}

func TestApplyDifferencesSkipsOutOfRange(t *testing.T) {
	var names [256]string
	applyDifferences(&names, encDict("", pdf.PDFInteger(-1), pdf.PDFName{Value: "neg"}, pdf.PDFInteger(255), pdf.PDFName{Value: "last"}, pdf.PDFName{Value: "past"}))
	if names[255] != "last" {
		t.Errorf("code 255 = %q, want last", names[255])
	}
	for code, n := range names {
		if n != "" && code != 255 {
			t.Errorf("code %d = %q, want it untouched", code, n)
		}
	}
	// No Differences at all is a no-op.
	applyDifferences(&names, pdf.NewPDFDict())
}

func TestSimpleFontGlyphNameTableForms(t *testing.T) {
	if names, ok := SimpleFontGlyphNameTable(fontDict(pdf.PDFName{Value: "StandardEncoding"}, "", 0)); !ok || names != StandardEncoding {
		t.Error("named StandardEncoding")
	}
	if _, ok := SimpleFontGlyphNameTable(fontDict(nil, "", 0)); ok {
		t.Error("no /Encoding is not modelled")
	}
}

func TestSymbolGlyphNameToUnicode(t *testing.T) {
	if u, ok := symbolGlyphNameToUnicode("A"); !ok || u != 'A' {
		t.Errorf("AGL name: %04X %v", u, ok)
	}
	var symOnly, zapfOnly string
	for n := range SymbolGlyphNameUnicode {
		if _, ok := GlyphNameToUnicode(n); !ok {
			symOnly = n
			break
		}
	}
	for n := range ZapfDingbatsGlyphNameUnicode {
		if _, ok := GlyphNameToUnicode(n); !ok {
			zapfOnly = n
			break
		}
	}
	if symOnly == "" || zapfOnly == "" {
		t.Fatal("expected glyph names that only the Symbol and Zapf lists know")
	}
	if u, ok := symbolGlyphNameToUnicode(symOnly); !ok || u != SymbolGlyphNameUnicode[symOnly] {
		t.Errorf("Symbol name %q: %04X %v", symOnly, u, ok)
	}
	if u, ok := symbolGlyphNameToUnicode(zapfOnly); !ok || u != ZapfDingbatsGlyphNameUnicode[zapfOnly] {
		t.Errorf("Zapf name %q: %04X %v", zapfOnly, u, ok)
	}
	if _, ok := symbolGlyphNameToUnicode("no-such-glyph"); ok {
		t.Error("unknown name should not resolve")
	}
}

func TestOriginalSimpleFontCodeToUnicode(t *testing.T) {
	// Named encodings.
	for _, c := range []struct {
		name  string
		table [256]uint16
		known bool
	}{
		{"WinAnsiEncoding", WinAnsiToUnicode, true},
		{"MacRomanEncoding", MacRomanToUnicode, true},
		{"StandardEncoding", StandardToUnicode, true},
		{"MacExpertEncoding", [256]uint16{}, false},
	} {
		table, known := OriginalSimpleFontCodeToUnicode(fontDict(pdf.PDFName{Value: c.name}, "", 0))
		if table != c.table || known != c.known {
			t.Errorf("%s: known=%v, want %v (or wrong table)", c.name, known, c.known)
		}
	}

	// Dictionary with a BaseEncoding.
	table, known := OriginalSimpleFontCodeToUnicode(fontDict(encDict("WinAnsiEncoding"), "", 0))
	if !known || table != WinAnsiToUnicode {
		t.Error("dict with BaseEncoding WinAnsi")
	}
	// Dictionary without one, on the standard Symbol and Zapf fonts.
	table, known = OriginalSimpleFontCodeToUnicode(fontDict(encDict(""), "ABCDEF+Symbol", 0))
	if !known || table != SymbolToUnicode {
		t.Error("Symbol's built-in encoding")
	}
	table, known = OriginalSimpleFontCodeToUnicode(fontDict(encDict(""), "ZapfDingbats", 0))
	if !known || table != ZapfDingbatsToUnicode {
		t.Error("ZapfDingbats' built-in encoding")
	}
	// A non-symbolic font falls back to Standard; a symbolic one stays unknown.
	table, known = OriginalSimpleFontCodeToUnicode(fontDict(encDict(""), "Foo", 32))
	if !known || table != StandardToUnicode {
		t.Error("non-symbolic font without BaseEncoding reads as Standard")
	}
	if _, known = OriginalSimpleFontCodeToUnicode(fontDict(encDict(""), "Foo", 4)); known {
		t.Error("a symbolic font without BaseEncoding is unknowable")
	}

	// No /Encoding: the standard symbol fonts, WinAnsi for non-symbolic, unknown for symbolic.
	table, known = OriginalSimpleFontCodeToUnicode(fontDict(nil, "Symbol", 0))
	if !known || table != SymbolToUnicode {
		t.Error("no encoding on Symbol")
	}
	table, known = OriginalSimpleFontCodeToUnicode(fontDict(nil, "Foo", 32))
	if !known || table != WinAnsiToUnicode {
		t.Error("no encoding on a non-symbolic font reads as WinAnsi")
	}
	if _, known = OriginalSimpleFontCodeToUnicode(fontDict(nil, "Foo", 4)); known {
		t.Error("no encoding on a symbolic font is unknowable")
	}

	// Differences resolve through the Symbol and Zapf lists too, skipping out-of-range codes.
	var symName string
	for n := range SymbolGlyphNameUnicode {
		if _, ok := GlyphNameToUnicode(n); !ok {
			symName = n
			break
		}
	}
	table, _ = OriginalSimpleFontCodeToUnicode(fontDict(encDict("WinAnsiEncoding",
		pdf.PDFInteger(-3), pdf.PDFName{Value: "A"},
		pdf.PDFInteger(65), pdf.PDFName{Value: symName}, pdf.PDFName{Value: "nonsense"}), "", 0))
	if table[65] != SymbolGlyphNameUnicode[symName] || table[66] != 0 {
		t.Errorf("Differences: %04X %04X, want %04X 0000", table[65], table[66], SymbolGlyphNameUnicode[symName])
	}

	// A /ToUnicode CMap fills only the codes still unresolved.
	toUni := pdf.NewPDFDict()
	toUni.HasStream = true
	toUni.RawStream = []byte("1 beginbfchar\n<41> <0058>\n<01> <0059>\nendbfchar\n1 beginbfrange\n<0100> <0101> <005A>\nendbfrange\n")
	d := fontDict(pdf.PDFName{Value: "WinAnsiEncoding"}, "", 0)
	d.Entries.Set("ToUnicode", toUni)
	table, _ = OriginalSimpleFontCodeToUnicode(d)
	if table[0x41] != 'A' {
		t.Errorf("ToUnicode must not override a known code: %04X", table[0x41])
	}
	if table[0x01] != 'Y' {
		t.Errorf("ToUnicode should fill an unresolved code: %04X", table[0x01])
	}
}

func TestFontFlagsSymbolic(t *testing.T) {
	if fontFlagsSymbolic(fontDict(nil, "", 0)) {
		t.Error("no descriptor is not symbolic")
	}
	if !fontFlagsSymbolic(fontDict(nil, "", 4)) {
		t.Error("flag 4 is symbolic")
	}
	if fontFlagsSymbolic(fontDict(nil, "", 32)) {
		t.Error("flag 32 alone is not symbolic")
	}
}
