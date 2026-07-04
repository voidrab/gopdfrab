package verify

import (
	"encoding/binary"
	"os"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

func loadTTF(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("../convert/assets/fonts/LiberationSans-Regular.ttf")
	if err != nil {
		t.Skipf("font asset not available: %v", err)
	}
	return data
}

// TestTrueTypeTableParsers exercises the sfnt/cmap/glyf/hmtx accessors against a
// real TrueType font.
func TestTrueTypeTableParsers(t *testing.T) {
	tables, ok := ParseSfnt(loadTTF(t))
	if !ok {
		t.Fatal("ParseSfnt failed on a valid TTF")
	}

	if n := TTNumGlyphs(tables); n <= 0 {
		t.Fatalf("TTNumGlyphs = %d", n)
	}

	cmap := TTWindowsBMPCmap(tables)
	if cmap == nil {
		t.Fatal("TTWindowsBMPCmap returned nil")
	}
	gidMap := ParseCmapFormat4(cmap)
	gidA, ok := gidMap['A']
	if !ok {
		t.Fatal("no glyph for 'A'")
	}

	if !ttLocaHasGlyph(tables)(int(gidA)) {
		t.Error("ttLocaHasGlyph false for a real glyph")
	}
	if !TTGlyphInRange(tables)(int(gidA)) {
		t.Error("TTGlyphInRange false for a real glyph")
	}
	if !TTGlyphPresent(tables)(int(gidA)) {
		t.Error("TTGlyphPresent false for a real glyph")
	}
	if w := TTAdvanceWidth(tables, int(gidA)); w <= 0 {
		t.Errorf("TTAdvanceWidth('A') = %d", w)
	}
	if m := ParseCmapSubtable(cmap); m == nil {
		t.Error("ParseCmapSubtable(format4) returned nil")
	}
}

// TestCmapFormat0And6 covers the format-0 and format-6 cmap decoders with
// hand-built subtables, including the dispatch in ParseCmapSubtable.
func TestCmapFormat0And6(t *testing.T) {
	sub0 := make([]byte, 262)
	binary.BigEndian.PutUint16(sub0[0:], 0)
	sub0[6+65] = 5 // code 'A' -> gid 5
	if m := ParseCmapFormat0(sub0); m[65] != 5 {
		t.Errorf("ParseCmapFormat0[65] = %d, want 5", m[65])
	}
	if ParseCmapFormat0([]byte{0, 0}) != nil {
		t.Error("ParseCmapFormat0 on short input should be nil")
	}

	sub6 := make([]byte, 12)
	binary.BigEndian.PutUint16(sub6[0:], 6)
	binary.BigEndian.PutUint16(sub6[6:], 65) // firstCode
	binary.BigEndian.PutUint16(sub6[8:], 1)  // count
	binary.BigEndian.PutUint16(sub6[10:], 5) // gid
	if m := ParseCmapFormat6(sub6); m[65] != 5 {
		t.Errorf("ParseCmapFormat6[65] = %d, want 5", m[65])
	}
	if m := ParseCmapSubtable(sub6); m[65] != 5 {
		t.Error("ParseCmapSubtable(format6) failed")
	}
	if ParseCmapSubtable([]byte{0x00, 0x63}) != nil { // format 99
		t.Error("ParseCmapSubtable of unknown format should be nil")
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

// TestParseCIDWidths covers both the "c [w...]" and "cfirst clast w" forms.
func TestParseCIDWidths(t *testing.T) {
	w := pdf.PDFArray{
		pdf.PDFInteger(1), pdf.PDFArray{pdf.PDFInteger(500), pdf.PDFInteger(600)},
		pdf.PDFInteger(5), pdf.PDFInteger(7), pdf.PDFInteger(400),
	}
	got := ParseCIDWidths(w)
	m := map[int]int{}
	for _, pair := range got {
		m[pair[0]] = pair[1]
	}
	if m[1] != 500 || m[2] != 600 || m[5] != 400 || m[7] != 400 {
		t.Errorf("ParseCIDWidths = %v", got)
	}
}

// TestValidateEmbeddedTrueTypeFont runs the full font-dict validation over a
// font dict with an embedded (real) TrueType program.
func TestValidateEmbeddedTrueTypeFont(t *testing.T) {
	ttf := loadTTF(t)
	ff := pdf.NewPDFDict()
	ff.Entries["Length1"] = pdf.PDFInteger(len(ttf))
	ff.HasStream = true
	ff.RawStream = ttf

	desc := pdf.NewPDFDict()
	desc.Entries["Type"] = pdf.PDFName{Value: "FontDescriptor"}
	desc.Entries["FontName"] = pdf.PDFName{Value: "LiberationSans"}
	desc.Entries["Flags"] = pdf.PDFInteger(32)
	desc.Entries["FontFile2"] = ff

	font := pdf.NewPDFDict()
	font.Entries["Type"] = pdf.PDFName{Value: "Font"}
	font.Entries["Subtype"] = pdf.PDFName{Value: "TrueType"}
	font.Entries["BaseFont"] = pdf.PDFName{Value: "LiberationSans"}
	font.Entries["Encoding"] = pdf.PDFName{Value: "WinAnsiEncoding"}
	font.Entries["FirstChar"] = pdf.PDFInteger(65)
	font.Entries["LastChar"] = pdf.PDFInteger(66)
	font.Entries["Widths"] = pdf.PDFArray{pdf.PDFInteger(667), pdf.PDFInteger(667)}
	font.Entries["FontDescriptor"] = desc

	// Just needs to run through the embedded-program validation without panicking.
	ValidateFontDict(font, &ValidationContext{})
}

// buildMinimalCFF assembles a tiny name-keyed CFF font with two glyphs
// (.notdef and "A"), a format-0 charset, and an empty Private DICT. All Top
// DICT operands use the 5-byte int32 encoding (operator 29) so section offsets
// are fixed regardless of their magnitude.
func buildMinimalCFF() []byte {
	i32 := func(v int) []byte {
		var b [5]byte
		b[0] = 29
		binary.BigEndian.PutUint32(b[1:], uint32(v))
		return b[:]
	}
	// Fixed layout offsets (see the section comments below).
	const (
		charsetOff     = 45
		charStringsOff = 48
		privateOff     = 56
	)

	var cff []byte
	// Header: major=1 minor=0 hdrSize=4 offSize=1.
	cff = append(cff, 0x01, 0x00, 0x04, 0x01)
	// Name INDEX: 1 entry "Font".
	cff = append(cff, 0x00, 0x01, 0x01, 0x01, 0x05, 'F', 'o', 'n', 't')
	// Top DICT INDEX: 1 entry (23-byte dict).
	var top []byte
	top = append(top, i32(charStringsOff)...)
	top = append(top, 17) // CharStrings
	top = append(top, i32(charsetOff)...)
	top = append(top, 15)                 // charset
	top = append(top, i32(4)...)          // Private size (4-byte dict below)
	top = append(top, i32(privateOff)...) // Private offset
	top = append(top, 18)                 // Private
	cff = append(cff, 0x00, 0x01, 0x01, 0x01, byte(len(top)+1))
	cff = append(cff, top...)
	// String INDEX: empty.
	cff = append(cff, 0x00, 0x00)
	// Global Subr INDEX: empty.
	cff = append(cff, 0x00, 0x00)
	// charset (offset 45): format 0, one SID (34 = "A") for glyph 1.
	cff = append(cff, 0x00, 0x00, 0x22)
	// CharStrings INDEX (offset 48): two glyphs, each a lone endchar (0x0e).
	cff = append(cff, 0x00, 0x02, 0x01, 0x01, 0x02, 0x03, 0x0e, 0x0e)
	// Private DICT (offset 56): defaultWidthX 0 (op 20), nominalWidthX 0 (op 21).
	cff = append(cff, 0x8b, 20, 0x8b, 21)
	return cff
}

func TestParseMinimalCFF(t *testing.T) {
	cff := buildMinimalCFF()

	td, ok := ParseCFFTopDict(cff)
	if !ok {
		t.Fatal("ParseCFFTopDict failed on a hand-built CFF")
	}
	if td.CSOffset != 48 || td.CharsetOffset != 45 {
		t.Errorf("Top DICT offsets = CS %d, charset %d; want 48, 45", td.CSOffset, td.CharsetOffset)
	}

	names := CFFGlyphNames(cff)
	if len(names) != 2 {
		t.Fatalf("CFFGlyphNames = %v, want 2 glyph names", names)
	}
	if names[1] != "A" {
		t.Errorf("glyph 1 name = %q, want \"A\"", names[1])
	}

	// ParseCFFIndex over the Name INDEX yields the single "Font" entry.
	entries, _ := ParseCFFIndex(cff, int(cff[2]))
	if len(entries) != 1 || string(entries[0]) != "Font" {
		t.Errorf("Name INDEX = %q, want [\"Font\"]", entries)
	}
}

func TestCFFSIDName(t *testing.T) {
	if got := cffSIDName(0, nil); got != ".notdef" {
		t.Errorf("SID 0 = %q, want .notdef", got)
	}
	custom := [][]byte{[]byte("MyGlyph")}
	if got := cffSIDName(len(cffStandardStrings), custom); got != "MyGlyph" {
		t.Errorf("first custom SID = %q, want MyGlyph", got)
	}
	if got := cffSIDName(999999, nil); got != "" {
		t.Errorf("out-of-range SID = %q, want empty", got)
	}
}
