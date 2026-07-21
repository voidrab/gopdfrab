package verify

import (
	"bytes"
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

// buildMinimalCIDCFF assembles a tiny CID-keyed CFF font with 3 glyphs
// (.notdef, CID 1 width 600, CID 2 width 700), a format-0 CID charset, a
// single FDArray Font DICT (format-0 FDSelect), and a Private DICT with zero
// width defaults. All Top DICT / FD DICT operands use the 5-byte int32
// encoding so section offsets can be computed and patched independent of
// their magnitude.
func buildMinimalCIDCFF() []byte {
	i32 := func(v int) []byte {
		var b [5]byte
		b[0] = 29
		binary.BigEndian.PutUint32(b[1:], uint32(v))
		return b[:]
	}

	header := []byte{0x01, 0x00, 0x04, 0x01}
	nameIndex := []byte{0x00, 0x01, 0x01, 0x01, 0x05, 'F', 'o', 'n', 't'}

	// Top DICT content is fixed-length (every operand is i32-encoded), so its
	// byte layout — and hence every later section's offset — can be computed
	// before the referenced offsets are known.
	topDictLen := 6 + 6 + 2 + 7 + 7 // CharStrings, charset, ROS, FDArray, FDSelect
	topDictIndex := []byte{0x00, 0x01, 0x01, byte(1), byte(topDictLen + 1)}

	nameEnd := len(header) + len(nameIndex)
	topDictEnd := nameEnd + len(topDictIndex) + topDictLen
	stringIndex := []byte{0x00, 0x00}
	globalSubrIndex := []byte{0x00, 0x00}
	stringEnd := topDictEnd + len(stringIndex)
	subrEnd := stringEnd + len(globalSubrIndex)

	charsetOff := subrEnd
	charset := []byte{0x00, 0x00, 0x01, 0x00, 0x02} // format 0: gid1->CID1, gid2->CID2
	charsetEnd := charsetOff + len(charset)

	csOff := charsetEnd
	cs0 := []byte{0x0e}           // .notdef: endchar, no width
	cs1 := []byte{248, 236, 0x0e} // CID1: width 600, endchar
	cs2 := []byte{249, 80, 0x0e}  // CID2: width 700, endchar
	csIndex := []byte{0x00, 0x03, 0x01, 1, 2, 5, 8}
	csIndex = append(csIndex, cs0...)
	csIndex = append(csIndex, cs1...)
	csIndex = append(csIndex, cs2...)
	csEnd := csOff + len(csIndex)

	// fdDict's own byte length is fixed (i32+i32+op18 = 11 bytes) regardless of
	// the Private offset value, so the FDArray/FDSelect layout — and hence the
	// Private DICT's offset — can be computed before fdDict is filled in.
	fdArrayOff := csEnd
	const fdDictLen = 11
	fdArrayIndexLen := 2 + 1 + 2 + fdDictLen // count + offSize + 2 offsets + data
	fdArrayEnd := fdArrayOff + fdArrayIndexLen

	fdSelectOff := fdArrayEnd
	fdSelect := []byte{0x00, 0x00, 0x00, 0x00} // format 0, FD 0 for all 3 glyphs
	fdSelectEnd := fdSelectOff + len(fdSelect)

	privOff := fdSelectEnd
	fdDict := append(i32(4), i32(privOff)...)
	fdDict = append(fdDict, 18) // Private: [size offset]
	if len(fdDict) != fdDictLen {
		panic("buildMinimalCIDCFF: fd dict length mismatch")
	}
	fdArrayIndex := []byte{0x00, 0x01, 0x01, 1, byte(fdDictLen + 1)}
	fdArrayIndex = append(fdArrayIndex, fdDict...)

	privateDict := []byte{0x8b, 20, 0x8b, 21} // defaultWidthX=0, nominalWidthX=0

	topDict := append(i32(csOff), 17)
	topDict = append(topDict, i32(charsetOff)...)
	topDict = append(topDict, 15)
	topDict = append(topDict, 12, 30) // ROS
	topDict = append(topDict, i32(fdArrayOff)...)
	topDict = append(topDict, 12, 36)
	topDict = append(topDict, i32(fdSelectOff)...)
	topDict = append(topDict, 12, 37)
	if len(topDict) != topDictLen {
		panic("buildMinimalCIDCFF: top dict length mismatch")
	}

	var cff []byte
	cff = append(cff, header...)
	cff = append(cff, nameIndex...)
	cff = append(cff, topDictIndex...)
	cff = append(cff, topDict...)
	cff = append(cff, stringIndex...)
	cff = append(cff, globalSubrIndex...)
	cff = append(cff, charset...)
	cff = append(cff, csIndex...)
	cff = append(cff, fdArrayIndex...)
	cff = append(cff, fdSelect...)
	cff = append(cff, privateDict...)
	return cff
}

func TestCIDCFFSubsetAndMetrics(t *testing.T) {
	cff := buildMinimalCIDCFF()
	ff := pdf.NewPDFDict()
	ff.HasStream = true
	ff.RawStream = cff

	widths := CFFCIDAdvanceWidths(cff)
	if widths[1] != 600 || widths[2] != 700 {
		t.Fatalf("CFFCIDAdvanceWidths = %v, want {1:600, 2:700, ...}", widths)
	}

	// Coverage: both referenced CIDs are defined -> no report.
	w := pdf.PDFArray{pdf.PDFInteger(1), pdf.PDFArray{pdf.PDFInteger(600), pdf.PDFInteger(700)}}
	ctx := &ValidationContext{}
	ValidateCIDCFFSubset(pdf.PDFDict{}, ff, w, ctx)
	if hasCheck(ctx, pdf.Checks.Font.SubsetGlyphCoverage) {
		t.Error("unexpected SubsetGlyphCoverage for CIDs present in charset")
	}

	// Coverage: CID 5 is absent from the charset -> reported.
	wMissing := pdf.PDFArray{pdf.PDFInteger(5), pdf.PDFArray{pdf.PDFInteger(600)}}
	ctx2 := &ValidationContext{}
	ValidateCIDCFFSubset(pdf.PDFDict{}, ff, wMissing, ctx2)
	if !hasCheck(ctx2, pdf.Checks.Font.SubsetGlyphCoverage) {
		t.Error("expected SubsetGlyphCoverage for CID absent from charset")
	}

	// Metrics: matching widths -> no report.
	ctx3 := &ValidationContext{}
	validateCIDCFFMetrics(pdf.PDFDict{}, pdf.PDFDict{}, ff, w, ctx3)
	if hasCheck(ctx3, pdf.Checks.Font.AdvanceWidthMismatch) {
		t.Error("unexpected AdvanceWidthMismatch for matching CFF widths")
	}

	// Metrics: mismatched width -> reported.
	wBad := pdf.PDFArray{pdf.PDFInteger(1), pdf.PDFArray{pdf.PDFInteger(650)}}
	ctx4 := &ValidationContext{}
	validateCIDCFFMetrics(pdf.PDFDict{}, pdf.PDFDict{}, ff, wBad, ctx4)
	if !hasCheck(ctx4, pdf.Checks.Font.AdvanceWidthMismatch) {
		t.Error("expected AdvanceWidthMismatch for mismatched CFF width")
	}

	// CIDSet bitmap: both CIDs marked -> no report.
	desc := pdf.NewPDFDict()
	cidSet := pdf.NewPDFDict()
	cidSet.HasStream = true
	cidSet.RawStream = []byte{0xE0} // bits for CID 0 (.notdef, 0x80), 1 (0x40), 2 (0x20)
	desc.Entries["CIDSet"] = cidSet
	ctx5 := &ValidationContext{}
	validateCIDSetBitmap(pdf.PDFDict{}, desc, ff, ctx5)
	if hasCheck(ctx5, pdf.Checks.Font.CIDSubsetCIDSet) {
		t.Error("unexpected CIDSubsetCIDSet when bitmap covers all CIDs")
	}

	// CIDSet bitmap: CID 2 not marked -> reported.
	desc2 := pdf.NewPDFDict()
	cidSet2 := pdf.NewPDFDict()
	cidSet2.HasStream = true
	cidSet2.RawStream = []byte{0xC0} // CID 0 and CID 1 marked, CID 2 missing
	desc2.Entries["CIDSet"] = cidSet2
	ctx6 := &ValidationContext{}
	validateCIDSetBitmap(pdf.PDFDict{}, desc2, ff, ctx6)
	if !hasCheck(ctx6, pdf.Checks.Font.CIDSubsetCIDSet) {
		t.Error("expected CIDSubsetCIDSet when a CID's bit is missing")
	}
}

func TestCidsToCheckAndDefaultWidth(t *testing.T) {
	w := pdf.PDFArray{pdf.PDFInteger(1), pdf.PDFArray{pdf.PDFInteger(500), pdf.PDFInteger(600)}}
	desc := pdf.NewPDFDict()

	// No usage info: falls back to the W array's CIDs.
	cids, known := cidsToCheck(desc, w, &ValidationContext{})
	if known {
		t.Error("cidsToCheck: known should be false with no UsedCIDs")
	}
	if len(cids) != 2 || cids[0] != 1 || cids[1] != 2 {
		t.Errorf("cidsToCheck fallback = %v, want [1 2]", cids)
	}

	// Usage info present: reflects UsedCIDs instead of W.
	ptr := pdf.ValuePointer(desc.Entries)
	ctx := &ValidationContext{UsedCIDs: map[uintptr]map[int]bool{ptr: {7: true, 3: true}}}
	cids, known = cidsToCheck(desc, w, ctx)
	if !known || len(cids) != 2 || cids[0] != 3 || cids[1] != 7 {
		t.Errorf("cidsToCheck with usage = %v, known=%v, want [3 7] true", cids, known)
	}

	if cidDefaultWidth(desc) != 1000 {
		t.Error("cidDefaultWidth should default to 1000 when DW is absent")
	}
	desc.Entries["DW"] = pdf.PDFInteger(2000)
	if cidDefaultWidth(desc) != 2000 {
		t.Error("cidDefaultWidth should reflect the DW entry")
	}
}

// buildCIDTrueTypeFont returns a CIDFontType2 descendant dict embedding the
// real LiberationSans TTF, plus the GID for 'A' and its hmtx advance width.
func buildCIDTrueTypeFont(t *testing.T) (desc pdf.PDFDict, ff pdf.PDFDict, gidA int, widthA int) {
	t.Helper()
	ttf := loadTTF(t)
	tables, ok := ParseSfnt(ttf)
	if !ok {
		t.Fatal("ParseSfnt failed")
	}
	gid, ok := ParseCmapFormat4(TTWindowsBMPCmap(tables))['A']
	if !ok {
		t.Fatal("no glyph for 'A'")
	}
	ff = pdf.NewPDFDict()
	ff.HasStream = true
	ff.RawStream = ttf

	desc = pdf.NewPDFDict()
	return desc, ff, int(gid), TTAdvanceWidth(tables, int(gid))
}

func TestCIDTrueTypeSubsetAndMetrics(t *testing.T) {
	desc, ff, gidA, widthA := buildCIDTrueTypeFont(t)

	w := pdf.PDFArray{pdf.PDFInteger(gidA), pdf.PDFArray{pdf.PDFInteger(widthA)}}
	ctx := &ValidationContext{}
	ValidateCIDTrueTypeSubset(desc, ff, w, ctx)
	if hasCheck(ctx, pdf.Checks.Font.SubsetGlyphCoverage) {
		t.Error("unexpected SubsetGlyphCoverage for a real glyph")
	}

	wMissing := pdf.PDFArray{pdf.PDFInteger(999999), pdf.PDFArray{pdf.PDFInteger(500)}}
	ctx2 := &ValidationContext{}
	ValidateCIDTrueTypeSubset(desc, ff, wMissing, ctx2)
	if !hasCheck(ctx2, pdf.Checks.Font.SubsetGlyphCoverage) {
		t.Error("expected SubsetGlyphCoverage for an out-of-range CID")
	}

	ctx3 := &ValidationContext{}
	validateCIDTrueTypeMetrics(desc, ff, w, ctx3)
	if hasCheck(ctx3, pdf.Checks.Font.AdvanceWidthMismatch) {
		t.Error("unexpected AdvanceWidthMismatch for matching hmtx width")
	}

	wBad := pdf.PDFArray{pdf.PDFInteger(gidA), pdf.PDFArray{pdf.PDFInteger(widthA + 500)}}
	ctx4 := &ValidationContext{}
	validateCIDTrueTypeMetrics(desc, ff, wBad, ctx4)
	if !hasCheck(ctx4, pdf.Checks.Font.AdvanceWidthMismatch) {
		t.Error("expected AdvanceWidthMismatch for mismatched hmtx width")
	}

	// Rendered CID absent from W falls back to DW; make DW clash with hmtx.
	desc.Entries["DW"] = pdf.PDFInteger(widthA + 500)
	ptr := pdf.ValuePointer(desc.Entries)
	ctx5 := &ValidationContext{UsedCIDs: map[uintptr]map[int]bool{ptr: {gidA: true}}}
	validateCIDTrueTypeMetrics(desc, ff, pdf.PDFArray{}, ctx5)
	if !hasCheck(ctx5, pdf.Checks.Font.AdvanceWidthMismatch) {
		t.Error("expected AdvanceWidthMismatch via DW fallback for a rendered CID absent from W")
	}
}

func TestValidateCIDSetTrueType(t *testing.T) {
	_, ff, _, _ := buildCIDTrueTypeFont(t)
	ttf := loadTTF(t)
	tables, _ := ParseSfnt(ttf)
	numGlyphs := TTNumGlyphs(tables)

	desc := pdf.NewPDFDict()
	cidSet := pdf.NewPDFDict()
	cidSet.HasStream = true
	cidSet.RawStream = make([]byte, (numGlyphs+7)/8) // all-zero: first populated glyph is unmarked
	desc.Entries["CIDSet"] = cidSet
	ctx := &ValidationContext{}
	validateCIDSetTrueType(pdf.PDFDict{}, desc, ff, ctx)
	if !hasCheck(ctx, pdf.Checks.Font.CIDSubsetCIDSet) {
		t.Error("expected CIDSubsetCIDSet with an empty CIDSet bitmap")
	}

	fullBitmap := make([]byte, (numGlyphs+7)/8)
	for i := range fullBitmap {
		fullBitmap[i] = 0xFF
	}
	cidSet2 := pdf.NewPDFDict()
	cidSet2.HasStream = true
	cidSet2.RawStream = fullBitmap
	desc2 := pdf.NewPDFDict()
	desc2.Entries["CIDSet"] = cidSet2
	ctx2 := &ValidationContext{}
	validateCIDSetTrueType(pdf.PDFDict{}, desc2, ff, ctx2)
	if hasCheck(ctx2, pdf.Checks.Font.CIDSubsetCIDSet) {
		t.Error("unexpected CIDSubsetCIDSet with a fully-set CIDSet bitmap")
	}
}

// encryptType1 is the inverse of DecryptType1Block: it prepends the
// conventional 4 zero lenIV bytes and encrypts plain so that
// DecryptType1Block(result, seedKey) reproduces plain exactly.
func encryptType1(plain []byte, seedKey uint16) []byte {
	padded := append([]byte{0, 0, 0, 0}, plain...)
	r := seedKey
	out := make([]byte, len(padded))
	for i, p := range padded {
		c := p ^ byte(r>>8)
		out[i] = c
		r = (uint16(c)+r)*52845 + 22719
	}
	return out
}

// buildType1Font assembles a minimal clear-text + eexec-encrypted Type1
// font program with a single glyph "A" (StandardEncoding, hsbw width 500).
func buildType1Font() []byte {
	csPlain := []byte{139, 248, 136, 13} // sbx=0 wx=500, hsbw
	csCipher := encryptType1(csPlain, 4330)

	var binPlain []byte
	binPlain = append(binPlain, []byte("dup /CharStrings 2 dict dup begin\n/A 8 RD ")...)
	binPlain = append(binPlain, csCipher...)
	binPlain = append(binPlain, []byte(" ND\nend\n")...)
	eexecCipher := encryptType1(binPlain, 55665)

	var font []byte
	font = append(font, []byte("%!PS-AdobeFont-1.0: Test\n/Encoding StandardEncoding def\ncurrentfile eexec\n")...)
	font = append(font, eexecCipher...)
	return font
}

func TestType1EexecRoundTrip(t *testing.T) {
	font := buildType1Font()

	binStart := Type1EexecBinStart(font)
	if binStart <= 0 {
		t.Fatalf("Type1EexecBinStart = %d, want > 0", binStart)
	}

	cs := Type1CharStringsSection(font, binStart)
	if cs == nil || !bytes.HasPrefix(cs, []byte("/CharStrings")) {
		t.Fatalf("Type1CharStringsSection = %q", cs)
	}

	names := Type1GlyphNames(font)
	if len(names) != 1 || names[0] != "A" {
		t.Fatalf("Type1GlyphNames = %v, want [A]", names)
	}

	widths := Type1GlyphWidths(font)
	if widths["A"] != 500 {
		t.Fatalf("Type1GlyphWidths[A] = %d, want 500", widths["A"])
	}

	enc, ok := Type1EncodingTable(font, "")
	if !ok || enc[65] != "A" {
		t.Fatalf("Type1EncodingTable fallback: enc[65] = %q, ok=%v", enc[65], ok)
	}
	if enc2, ok2 := Type1EncodingTable(font, "WinAnsiEncoding"); !ok2 || enc2[65] != "A" {
		t.Error("Type1EncodingTable(WinAnsiEncoding) should resolve to WinAnsiGlyphName")
	}
	if _, ok3 := Type1EncodingTable(font, "UnknownEncoding"); ok3 {
		t.Error("Type1EncodingTable(unknown) should be ok=false")
	}
	if Type1EexecBinStart([]byte("no marker here")) != -1 {
		t.Error("Type1EexecBinStart should be -1 without an eexec marker")
	}
}

func TestParseType1AdvanceWidth(t *testing.T) {
	// sbw: sbx sby wx wy -> advance is the 3rd operand.
	sbw := []byte{139, 139, byte(139 + 10), 139, 12, 8} // 0,0,10,0, escape 12 8 (sbw)
	if w, ok := parseType1AdvanceWidth(sbw); !ok || w != 10 {
		t.Errorf("parseType1AdvanceWidth(sbw) = %d, %v, want 10, true", w, ok)
	}
	if _, ok := parseType1AdvanceWidth([]byte{14}); ok {
		t.Error("parseType1AdvanceWidth(endchar only) should be ok=false")
	}
	if _, ok := parseType1AdvanceWidth(nil); ok {
		t.Error("parseType1AdvanceWidth(empty) should be ok=false")
	}
}

func TestValidateType1Metrics(t *testing.T) {
	font := buildType1Font()
	ff := pdf.NewPDFDict()
	ff.HasStream = true
	ff.RawStream = font

	widths := pdf.PDFArray{pdf.PDFInteger(500)}
	ctx := &ValidationContext{}
	validateType1Metrics(pdf.PDFDict{}, ff, 65, widths, "", ctx)
	if hasCheck(ctx, pdf.Checks.Font.AdvanceWidthMismatch) {
		t.Error("unexpected AdvanceWidthMismatch for matching Type1 width")
	}

	widthsBad := pdf.PDFArray{pdf.PDFInteger(520)}
	ctx2 := &ValidationContext{}
	validateType1Metrics(pdf.PDFDict{}, ff, 65, widthsBad, "", ctx2)
	if !hasCheck(ctx2, pdf.Checks.Font.AdvanceWidthMismatch) {
		t.Error("expected AdvanceWidthMismatch for mismatched Type1 width")
	}
}

func TestValidateType1SubsetCoverage(t *testing.T) {
	font := buildType1Font()
	ff := pdf.NewPDFDict()
	ff.HasStream = true
	ff.RawStream = font

	desc := pdf.NewPDFDict()
	desc.Entries["FontFile"] = ff
	desc.Entries["CharSet"] = pdf.PDFString{Value: "/A"}

	v := pdf.NewPDFDict()
	v.Entries["Encoding"] = pdf.PDFName{Value: "WinAnsiEncoding"}

	widths := pdf.PDFArray{pdf.PDFInteger(500)} // code 65 = "A", present in program and CharSet
	ctx := &ValidationContext{}
	ValidateType1SubsetCoverage(v, v, desc, 65, 65, widths, ctx)
	if hasCheck(ctx, pdf.Checks.Font.SubsetGlyphCoverage) || hasCheck(ctx, pdf.Checks.Font.Type1SubsetCharSet) {
		t.Error("unexpected report for a glyph covered by both the program and CharSet")
	}

	// Code 66 ("B") has a non-zero width but no glyph in the program.
	widthsMissing := pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(500)}
	ctx2 := &ValidationContext{}
	ValidateType1SubsetCoverage(v, v, desc, 65, 66, widthsMissing, ctx2)
	if !hasCheck(ctx2, pdf.Checks.Font.SubsetGlyphCoverage) {
		t.Error("expected SubsetGlyphCoverage for a glyph absent from the embedded program")
	}

	// CharSet omits "A" though the program defines it.
	desc2 := pdf.NewPDFDict()
	desc2.Entries["FontFile"] = ff
	desc2.Entries["CharSet"] = pdf.PDFString{Value: "/Zzz"}
	ctx3 := &ValidationContext{}
	ValidateType1SubsetCoverage(v, v, desc2, 65, 65, widths, ctx3)
	if !hasCheck(ctx3, pdf.Checks.Font.Type1SubsetCharSet) {
		t.Error("expected Type1SubsetCharSet when CharSet omits a glyph the program defines")
	}

	// Empty CharSet is flagged directly.
	desc3 := pdf.NewPDFDict()
	desc3.Entries["CharSet"] = pdf.PDFString{Value: ""}
	ctx4 := &ValidationContext{}
	ValidateType1SubsetCoverage(v, v, desc3, 65, 65, widths, ctx4)
	if !hasCheck(ctx4, pdf.Checks.Font.Type1SubsetCharSet) {
		t.Error("expected Type1SubsetCharSet for an empty CharSet")
	}
}

func TestSplitCharSetNames(t *testing.T) {
	got := SplitCharSetNames("/a /b/c  ")
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("SplitCharSetNames = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("SplitCharSetNames[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSimpleFontGlyphNameTableCustomEncoding(t *testing.T) {
	v := pdf.NewPDFDict()
	encDict := pdf.NewPDFDict()
	encDict.Entries["BaseEncoding"] = pdf.PDFName{Value: "WinAnsiEncoding"}
	encDict.Entries["Differences"] = pdf.PDFArray{pdf.PDFInteger(65), pdf.PDFName{Value: "Agrave"}}
	v.Entries["Encoding"] = encDict

	names, ok := SimpleFontGlyphNameTable(v)
	if !ok || names[65] != "Agrave" {
		t.Errorf("SimpleFontGlyphNameTable custom Differences: names[65] = %q, ok=%v", names[65], ok)
	}

	v2 := pdf.NewPDFDict()
	v2.Entries["Encoding"] = pdf.PDFName{Value: "MacRomanEncoding"}
	if _, ok := SimpleFontGlyphNameTable(v2); ok {
		t.Error("SimpleFontGlyphNameTable(MacRomanEncoding) should be ok=false (unmodeled)")
	}
}

func TestValidateType1CMetrics(t *testing.T) {
	cff := buildMinimalCFF() // glyph "A" from checks_font_program_test.go's builder
	ff := pdf.NewPDFDict()
	ff.HasStream = true
	ff.RawStream = cff

	v := pdf.NewPDFDict()
	v.Entries["Encoding"] = pdf.PDFName{Value: "WinAnsiEncoding"}

	widths := CFFAdvanceWidths(cff)
	aWidth := widths["A"]

	ctx := &ValidationContext{}
	validateType1CMetrics(pdf.PDFDict{}, v, ff, 65, pdf.PDFArray{pdf.PDFInteger(aWidth)}, ctx)
	if hasCheck(ctx, pdf.Checks.Font.AdvanceWidthMismatch) {
		t.Error("unexpected AdvanceWidthMismatch for matching CFF width")
	}

	ctx2 := &ValidationContext{}
	validateType1CMetrics(pdf.PDFDict{}, v, ff, 65, pdf.PDFArray{pdf.PDFInteger(aWidth + 50)}, ctx2)
	if !hasCheck(ctx2, pdf.Checks.Font.AdvanceWidthMismatch) {
		t.Error("expected AdvanceWidthMismatch for mismatched CFF width")
	}
}

func TestEmbeddedType1GlyphNames(t *testing.T) {
	descType1 := pdf.NewPDFDict()
	ff := pdf.NewPDFDict()
	ff.HasStream = true
	ff.RawStream = buildType1Font()
	descType1.Entries["FontFile"] = ff
	if names := embeddedType1GlyphNames(descType1, &ValidationContext{}); len(names) != 1 || names[0] != "A" {
		t.Errorf("embeddedType1GlyphNames(FontFile) = %v, want [A]", names)
	}

	descCFF := pdf.NewPDFDict()
	ff3 := pdf.NewPDFDict()
	ff3.HasStream = true
	ff3.RawStream = buildMinimalCFF()
	descCFF.Entries["FontFile3"] = ff3
	if names := embeddedType1GlyphNames(descCFF, &ValidationContext{}); len(names) == 0 {
		t.Error("embeddedType1GlyphNames(FontFile3) returned nothing for a valid CFF")
	}

	if names := embeddedType1GlyphNames(pdf.NewPDFDict(), &ValidationContext{}); names != nil {
		t.Error("embeddedType1GlyphNames with no program should be nil")
	}
}

func TestTrueTypeCmapSubtablesAndSymbolicCmap(t *testing.T) {
	ttf := loadTTF(t)
	desc := pdf.NewPDFDict()
	ff := pdf.NewPDFDict()
	ff.HasStream = true
	ff.RawStream = ttf
	desc.Entries["FontFile2"] = ff

	n, ok := trueTypeCmapSubtables(&ValidationContext{}, desc)
	if !ok || n <= 0 {
		t.Errorf("trueTypeCmapSubtables = %d, %v", n, ok)
	}
	if _, ok := trueTypeCmapSubtables(&ValidationContext{}, pdf.NewPDFDict()); ok {
		t.Error("trueTypeCmapSubtables should be ok=false with no FontFile2")
	}

	// A (1,0) Mac subtable is returned when no (3,0) Windows symbol subtable exists.
	tables, _ := ParseSfnt(ttf)
	cmap := tables["cmap"]
	numSub := int(binary.BigEndian.Uint16(cmap[2:4]))
	found31, found10 := false, false
	for i := range numSub {
		rec := cmap[4+i*8:]
		platform := binary.BigEndian.Uint16(rec[0:2])
		encoding := binary.BigEndian.Uint16(rec[2:4])
		if platform == 3 && encoding == 0 {
			found31 = true
		}
		if platform == 1 && encoding == 0 {
			found10 = true
		}
	}
	if !found31 && found10 {
		if TTSymbolicCmap(tables) == nil {
			t.Error("TTSymbolicCmap should fall back to the (1,0) subtable")
		}
	}
}

func TestValidateSimpleTrueTypeMetricsAndSubset(t *testing.T) {
	ttf := loadTTF(t)
	ff := pdf.NewPDFDict()
	ff.HasStream = true
	ff.RawStream = ttf

	tables, _ := ParseSfnt(ttf)
	gidA, _ := ParseCmapFormat4(TTWindowsBMPCmap(tables))['A']
	widthA := TTAdvanceWidth(tables, int(gidA))

	v := pdf.NewPDFDict()
	v.Entries["Encoding"] = pdf.PDFName{Value: "WinAnsiEncoding"}

	widths := pdf.PDFArray{pdf.PDFInteger(widthA)} // FirstChar 65 ('A')
	ctx := &ValidationContext{}
	validateSimpleTrueTypeMetrics(v, ff, 65, widths, ctx)
	if hasCheck(ctx, pdf.Checks.Font.AdvanceWidthMismatch) {
		t.Error("unexpected AdvanceWidthMismatch for matching hmtx width")
	}

	ctx2 := &ValidationContext{}
	validateSimpleTrueTypeMetrics(v, ff, 65, pdf.PDFArray{pdf.PDFInteger(widthA + 500)}, ctx2)
	if !hasCheck(ctx2, pdf.Checks.Font.AdvanceWidthMismatch) {
		t.Error("expected AdvanceWidthMismatch for mismatched hmtx width")
	}

	// Subset coverage: 'A' is present; a made-up private-use code with no cmap
	// entry, forced non-symbolic, resolves to .notdef and is flagged.
	desc := pdf.NewPDFDict()
	desc.Entries["Flags"] = pdf.PDFInteger(0) // non-symbolic
	v.Entries["FontDescriptor"] = desc
	widthsSubset := pdf.PDFArray{pdf.PDFInteger(widthA)}
	ctx3 := &ValidationContext{}
	ValidateSimpleTrueTypeSubset(v, ff, 65, 65, widthsSubset, ctx3)
	if hasCheck(ctx3, pdf.Checks.Font.SubsetGlyphCoverage) {
		t.Error("unexpected SubsetGlyphCoverage for a real glyph")
	}
}

// TestTrueTypeTableGuards exercises the short-buffer / out-of-range defensive
// branches of the low-level sfnt table accessors using synthetic tables (the
// real LiberationSans TTF only ever hits their "happy path").
func TestTrueTypeTableGuards(t *testing.T) {
	if TTNumGlyphs(map[string][]byte{}) != 0 {
		t.Error("TTNumGlyphs should be 0 with no maxp table")
	}
	if TTNumGlyphs(map[string][]byte{"maxp": {0, 0}}) != 0 {
		t.Error("TTNumGlyphs should be 0 with a truncated maxp table")
	}

	if ttLocaHasGlyph(map[string][]byte{})(0) {
		t.Error("ttLocaHasGlyph should be false with no loca/head tables")
	}
	// A format-0 (short) loca table: 2-byte entries.
	head0 := make([]byte, 52) // locFormat (offset 50) = 0
	shortLoca := map[string][]byte{"head": head0, "loca": {0, 0, 0, 2}}
	hasGlyph := ttLocaHasGlyph(shortLoca)
	if !hasGlyph(0) {
		t.Error("ttLocaHasGlyph(format0) should detect a non-empty glyph 0")
	}
	if hasGlyph(5) {
		t.Error("ttLocaHasGlyph(format0) should be false past the end of the table")
	}

	if TTGlyphInRange(map[string][]byte{})(0) {
		t.Error("TTGlyphInRange should be false with no maxp table")
	}

	if TTAdvanceWidth(map[string][]byte{}, 0) != -1 {
		t.Error("TTAdvanceWidth should be -1 with no hmtx/hhea/head tables")
	}
	head := make([]byte, 20)
	head[18], head[19] = 0x03, 0xE8 // unitsPerEm = 1000
	hhea := make([]byte, 36)
	hhea[34], hhea[35] = 0x00, 0x01                                                     // numberOfHMetrics = 1
	tables := map[string][]byte{"head": head, "hhea": hhea, "hmtx": {0x01, 0xF4, 0, 0}} // aw=500
	if w := TTAdvanceWidth(tables, 0); w != 500 {
		t.Errorf("TTAdvanceWidth = %d, want 500", w)
	}
	// gid beyond numberOfHMetrics falls back to the last entry.
	if w := TTAdvanceWidth(tables, 5); w != 500 {
		t.Errorf("TTAdvanceWidth(beyond nHM) = %d, want 500 (fallback)", w)
	}
	headZeroUPM := make([]byte, 20) // unitsPerEm = 0
	if TTAdvanceWidth(map[string][]byte{"head": headZeroUPM, "hhea": hhea, "hmtx": {0, 0}}, 0) != -1 {
		t.Error("TTAdvanceWidth should be -1 when unitsPerEm is 0")
	}

	if TTWindowsBMPCmap(map[string][]byte{}) != nil {
		t.Error("TTWindowsBMPCmap should be nil with no cmap table")
	}
	if TTWindowsBMPCmap(map[string][]byte{"cmap": {0, 0, 0, 1}}) != nil {
		t.Error("TTWindowsBMPCmap should be nil when no (3,1) subtable exists")
	}
	if TTSymbolicCmap(map[string][]byte{}) != nil {
		t.Error("TTSymbolicCmap should be nil with no cmap table")
	}

	if ParseCmapFormat4([]byte{0, 4}) != nil {
		t.Error("ParseCmapFormat4 should be nil for a too-short subtable")
	}
	if ParseCmapFormat6([]byte{0, 6}) != nil {
		t.Error("ParseCmapFormat6 should be nil for a too-short subtable")
	}
}

func TestParseCFFCharsetCIDsFormats(t *testing.T) {
	// CharsetOffset must be > 2 (offsets 0-2 mean a predefined charset), so
	// pad with 3 leading bytes and place the table at offset 3.
	pad := []byte{0, 0, 0}

	// Format 1: (first, nLeft) ranges, nLeft is a 1-byte count.
	f1 := append(append([]byte{}, pad...), 0x01, 0x00, 0x05, 0x02) // first CID 5, nLeft 2 -> gids 1,2,3 get CIDs 5,6,7
	if got := ParseCFFCharsetCIDs(f1, 3, 4); got == nil || got[1] != 5 || got[2] != 6 || got[3] != 7 {
		t.Errorf("ParseCFFCharsetCIDs(format1) = %v", got)
	}

	// Format 2: like format 1 but nLeft is a 2-byte count.
	f2 := append(append([]byte{}, pad...), 0x02, 0x00, 0x0A, 0x00, 0x01) // first CID 10, nLeft 1 -> gids 1,2 get CIDs 10,11
	if got := ParseCFFCharsetCIDs(f2, 3, 3); got == nil || got[1] != 10 || got[2] != 11 {
		t.Errorf("ParseCFFCharsetCIDs(format2) = %v", got)
	}

	if ParseCFFCharsetCIDs(f1, 3, 0) != nil {
		t.Error("ParseCFFCharsetCIDs should be nil for numGlyphs <= 0")
	}
	if ParseCFFCharsetCIDs(f1, 2, 4) != nil {
		t.Error("ParseCFFCharsetCIDs should be nil for a predefined-charset offset (<=2)")
	}
	if ParseCFFCharsetCIDs(append(append([]byte{}, pad...), 0x09), 3, 4) != nil {
		t.Error("ParseCFFCharsetCIDs should be nil for an unknown format")
	}
	if ParseCFFCharsetCIDs(f1, 3, 100) != nil {
		t.Error("ParseCFFCharsetCIDs should be nil when the table runs past the data")
	}
}

// buildCFFWithTopDict assembles a minimal CFF (header + Name INDEX) whose Top
// DICT INDEX contains exactly topDict as its single entry's raw bytes, for
// probing ParseCFFTopDict's operand-decoding error branches directly.
func buildCFFWithTopDict(topDict []byte) []byte {
	var cff []byte
	cff = append(cff, 0x01, 0x00, 0x04, 0x01) // header, hdrSize=4
	cff = append(cff, 0x00, 0x01, 0x01, 0x01, 0x05, 'F', 'o', 'n', 't')
	cff = append(cff, 0x00, 0x01, 0x01, 1, byte(len(topDict)+1))
	cff = append(cff, topDict...)
	return cff
}

func TestParseCFFTopDictOperandTruncation(t *testing.T) {
	for name, bad := range map[string][]byte{
		"247-250 truncated": {247},
		"251-254 truncated": {251},
		"op28 truncated":    {28, 0},
		"op29 truncated":    {29, 0, 0},
		"escape truncated":  {12},
	} {
		if _, ok := ParseCFFTopDict(buildCFFWithTopDict(bad)); ok {
			t.Errorf("ParseCFFTopDict(%s) should fail", name)
		}
	}

	// A real-number operand (op 30, valid in a Top DICT though unused by any
	// operator this package reads) must be skipped without upsetting the walk.
	nibbles := []byte{0x1, 0xA, 0x5, 0xF} // "1.5"
	var real []byte
	for i := 0; i < len(nibbles); i += 2 {
		real = append(real, nibbles[i]<<4|nibbles[i+1])
	}
	withReal := append(append([]byte{30}, real...), 17) // real number, then CharStrings op (no operand)
	if _, ok := ParseCFFTopDict(buildCFFWithTopDict(withReal)); !ok {
		t.Error("ParseCFFTopDict should tolerate a real-number operand")
	}

	// FontMatrix (escape 12 7) and Private with too few operands.
	fm := []byte{12, 7, 139, 18} // FontMatrix marker, then Private with only 1 operand (ignored)
	td, ok := ParseCFFTopDict(buildCFFWithTopDict(fm))
	if !ok || !td.HasFontMatrix {
		t.Error("ParseCFFTopDict should record HasFontMatrix from escape operator 7")
	}
}

func TestParseCFFTopDictMalformed(t *testing.T) {
	if _, ok := ParseCFFTopDict(nil); ok {
		t.Error("ParseCFFTopDict should fail on empty input")
	}
	if _, ok := ParseCFFTopDict([]byte{1, 0, 4, 1}); ok {
		t.Error("ParseCFFTopDict should fail with no Name INDEX data")
	}
	// A Name INDEX with a truncated Top DICT INDEX count.
	truncated := []byte{1, 0, 4, 1, 0x00, 0x01, 0x01, 0x01, 0x05, 'F', 'o', 'n', 't', 0x00}
	if _, ok := ParseCFFTopDict(truncated); ok {
		t.Error("ParseCFFTopDict should fail with a truncated Top DICT INDEX")
	}
}

func TestParseType1AdvanceWidthMoreOperators(t *testing.T) {
	// Negative number via the 251-254 range, then hsbw.
	cs := []byte{byte(251), 0, 139, 13} // push -108, push 0, hsbw -> wx = stack[1] = 0
	if w, ok := parseType1AdvanceWidth(cs); !ok || w != 0 {
		t.Errorf("parseType1AdvanceWidth(251-254 range) = %d, %v", w, ok)
	}

	// 2-byte integer (op 28) then hsbw.
	cs28 := []byte{28, 0x01, 0x2C, 139, 13} // push 300, push 0, hsbw -> wx=0
	if w, ok := parseType1AdvanceWidth(cs28); !ok || w != 0 {
		t.Errorf("parseType1AdvanceWidth(op28) = %d, %v", w, ok)
	}

	// 4-byte integer (op 29) then hsbw, using the pushed value as wx.
	cs29 := []byte{139, 29, 0x00, 0x00, 0x01, 0x2C, 13} // push 0 (sbx), push 300 (wx), hsbw
	if w, ok := parseType1AdvanceWidth(cs29); !ok || w != 300 {
		t.Errorf("parseType1AdvanceWidth(op29) = %d, %v", w, ok)
	}

	// A non-hsbw/sbw/endchar operator clears the stack and parsing continues.
	csClear := []byte{139, 9, 139, byte(139 + 5), 13} // push 0, closepath(9,clears), push 0, push 5, hsbw
	if w, ok := parseType1AdvanceWidth(csClear); !ok || w != 5 {
		t.Errorf("parseType1AdvanceWidth(stack-clear op) = %d, %v", w, ok)
	}

	// Truncated operand encodings should fail cleanly rather than panic.
	for _, bad := range [][]byte{
		{247},      // 247-250 missing 2nd byte
		{251},      // 251-254 missing 2nd byte
		{28, 0},    // op28 missing 3rd byte
		{29, 0, 0}, // op29 missing bytes
		{12},       // escape missing 2nd byte
	} {
		if _, ok := parseType1AdvanceWidth(bad); ok {
			t.Errorf("parseType1AdvanceWidth(%v) should fail on truncated input", bad)
		}
	}
}

func TestFontProgramValidBranches(t *testing.T) {
	ctx := &ValidationContext{}

	ttf := loadTTF(t)
	good2 := pdf.NewPDFDict()
	good2.HasStream = true
	good2.RawStream = ttf
	if !fontProgramValid(ctx, good2, "FontFile2") {
		t.Error("a valid TrueType FontFile2 should be valid")
	}

	bad2 := pdf.NewPDFDict()
	bad2.HasStream = true
	bad2.RawStream = []byte("not a font")
	if fontProgramValid(ctx, bad2, "FontFile2") {
		t.Error("garbage FontFile2 data should be invalid")
	}

	cff := buildMinimalCFF()
	good3 := pdf.NewPDFDict()
	good3.HasStream = true
	good3.RawStream = cff
	if !fontProgramValid(ctx, good3, "FontFile3") {
		t.Error("a valid bare CFF FontFile3 should be valid")
	}

	// OpenType-wrapped CFF (sfnt with 'OTTO' tag).
	otf := pdf.NewPDFDict()
	otf.HasStream = true
	otf.RawStream = append([]byte{0x4F, 0x54, 0x54, 0x4F}, ttf[4:]...)
	if !fontProgramValid(ctx, otf, "FontFile3") {
		t.Error("an OpenType-wrapped FontFile3 should be valid")
	}

	badFF3 := pdf.NewPDFDict()
	badFF3.HasStream = true
	badFF3.RawStream = []byte("garbage")
	if fontProgramValid(ctx, badFF3, "FontFile3") {
		t.Error("garbage FontFile3 data should be invalid")
	}

	goodT1 := pdf.NewPDFDict()
	goodT1.HasStream = true
	goodT1.RawStream = []byte("%!PS-AdobeFont-1.0\n")
	if !fontProgramValid(ctx, goodT1, "FontFile") {
		t.Error("a Type1 program starting with %! should be valid")
	}
	badT1 := pdf.NewPDFDict()
	badT1.HasStream = true
	badT1.RawStream = []byte("garbage")
	if fontProgramValid(ctx, badT1, "FontFile") {
		t.Error("a Type1 program without a %! marker should be invalid")
	}

	empty := pdf.NewPDFDict()
	if fontProgramValid(ctx, empty, "FontFile2") {
		t.Error("a non-stream dict should be invalid")
	}

	unknown := pdf.NewPDFDict()
	unknown.HasStream = true
	unknown.RawStream = []byte("x")
	if !fontProgramValid(ctx, unknown, "SomeOtherKey") {
		t.Error("an unrecognized key should default to valid")
	}
}

// TestFontProgramCheckersDecodeFailureGuards exercises the shared "no stream /
// undecodable / unparseable program" early-return guard present in most of
// this file's checkers -- each should be a silent no-op rather than panic.
func TestFontProgramCheckersDecodeFailureGuards(t *testing.T) {
	noStream := pdf.NewPDFDict() // HasStream=false
	garbage := pdf.NewPDFDict()
	garbage.HasStream = true
	garbage.RawStream = []byte("not a font program")

	ctx := &ValidationContext{}
	v := pdf.NewPDFDict()
	desc := pdf.NewPDFDict()

	validateType1CMetrics(v, v, noStream, 0, nil, ctx)
	validateType1CMetrics(v, v, garbage, 0, nil, ctx)
	validateCIDCFFMetrics(v, v, noStream, nil, ctx)
	validateCIDCFFMetrics(v, v, garbage, nil, ctx)
	ValidateCIDCFFSubset(v, noStream, nil, ctx)
	ValidateCIDCFFSubset(v, garbage, nil, ctx)
	validateCIDSetBitmap(v, desc, noStream, ctx) // no CIDSet on desc: early return
	validateCIDSetTrueType(v, desc, noStream, ctx)
	ValidateCIDTrueTypeSubset(v, noStream, nil, ctx)
	ValidateCIDTrueTypeSubset(v, garbage, nil, ctx)
	validateCIDTrueTypeMetrics(v, noStream, nil, ctx)
	validateCIDTrueTypeMetrics(v, garbage, nil, ctx)
	ValidateSimpleTrueTypeSubset(v, noStream, 0, 0, nil, ctx)
	ValidateSimpleTrueTypeSubset(v, garbage, 0, 0, nil, ctx)
	validateSimpleTrueTypeMetrics(v, noStream, 0, nil, ctx)
	validateSimpleTrueTypeMetrics(v, garbage, 0, nil, ctx)
	validateType1Metrics(v, noStream, 0, nil, "", ctx)
	validateCMapWMode(v, pdf.NewPDFDict(), ctx) // no stream
	if _, ok := trueTypeCmapSubtables(ctx, desc); ok {
		t.Error("trueTypeCmapSubtables should be ok=false without a FontFile2")
	}
	ValidateFontProgram(v, desc, "Test", ctx) // no FontFile* entries at all

	if len(ctx.errs) != 0 {
		t.Errorf("expected no violations from decode-failure/no-program guards, got %v", ctx.errs)
	}

	// ValidateType1SubsetCoverage / embeddedType1GlyphNames with no CharSet/program.
	ctx2 := &ValidationContext{}
	ValidateType1SubsetCoverage(v, v, desc, 0, 0, nil, ctx2) // no CharSet: early return
	if len(ctx2.errs) != 0 {
		t.Error("unexpected violation with no CharSet entry")
	}
	if names := embeddedType1GlyphNames(desc, ctx2); names != nil {
		t.Error("embeddedType1GlyphNames should be nil with neither FontFile nor FontFile3")
	}
	descBadFF := pdf.NewPDFDict()
	descBadFF.Entries["FontFile"] = garbage
	if names := embeddedType1GlyphNames(descBadFF, ctx2); names != nil {
		t.Error("embeddedType1GlyphNames should be nil for an unparseable FontFile")
	}
}

func TestParseCIDWidthsMalformedEntries(t *testing.T) {
	// A non-numeric leading entry is skipped rather than misinterpreted.
	w := pdf.PDFArray{pdf.PDFName{Value: "oops"}, pdf.PDFInteger(1), pdf.PDFArray{pdf.PDFInteger(500)}}
	got := ParseCIDWidths(w)
	if len(got) != 1 || got[0][0] != 1 || got[0][1] != 500 {
		t.Errorf("ParseCIDWidths with a leading non-numeric entry = %v", got)
	}

	// Truncated range form (missing width) yields nothing extra.
	w2 := pdf.PDFArray{pdf.PDFInteger(1), pdf.PDFInteger(5)}
	if got := ParseCIDWidths(w2); got != nil {
		t.Errorf("ParseCIDWidths(truncated range) = %v, want nil", got)
	}

	// Non-numeric second/third elements in range form are skipped.
	w3 := pdf.PDFArray{pdf.PDFInteger(1), pdf.PDFName{Value: "x"}}
	if got := ParseCIDWidths(w3); got != nil {
		t.Errorf("ParseCIDWidths(non-numeric c2) = %v, want nil", got)
	}
	w4 := pdf.PDFArray{pdf.PDFInteger(1), pdf.PDFInteger(2), pdf.PDFName{Value: "x"}}
	if got := ParseCIDWidths(w4); got != nil {
		t.Errorf("ParseCIDWidths(non-numeric width) = %v, want nil", got)
	}
}

func TestParseSfntAndCFFIndexMalformed(t *testing.T) {
	if _, ok := ParseSfnt(nil); ok {
		t.Error("ParseSfnt should fail on empty input")
	}
	if _, ok := ParseSfnt([]byte("BAD!garbagegarbagegarbage")); ok {
		t.Error("ParseSfnt should fail on an unrecognized version tag")
	}
	// Valid tag but zero tables.
	zeroTables := []byte{0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0, 0, 0, 0, 0, 0}
	if _, ok := ParseSfnt(zeroTables); ok {
		t.Error("ParseSfnt should fail with numTables=0")
	}

	if entries, end := ParseCFFIndex(nil, 0); entries != nil || end != 0 {
		t.Errorf("ParseCFFIndex(empty) = %v, %d, want nil, 0", entries, end)
	}
	if entries, _ := ParseCFFIndex([]byte{0, 0}, 0); entries != nil {
		t.Error("ParseCFFIndex with count=0 should return nil entries")
	}

	if cffStringIndexEntries(nil) != nil {
		t.Error("cffStringIndexEntries should be nil for too-short input")
	}
}

func TestValidateSimpleTrueTypeUsedCodesKnown(t *testing.T) {
	ttf := loadTTF(t)
	ff := pdf.NewPDFDict()
	ff.HasStream = true
	ff.RawStream = ttf

	tables, _ := ParseSfnt(ttf)
	gidA, _ := ParseCmapFormat4(TTWindowsBMPCmap(tables))['A']
	widthA := TTAdvanceWidth(tables, int(gidA))

	v := pdf.NewPDFDict()
	v.Entries["Encoding"] = pdf.PDFName{Value: "WinAnsiEncoding"}
	ptr := pdf.ValuePointer(v.Entries)

	// Usage info known: drives the usedCodes branch instead of the Widths fallback.
	ctx := &ValidationContext{UsedCharCodes: map[uintptr]map[int]bool{ptr: {65: true}}}
	ValidateSimpleTrueTypeSubset(v, ff, 65, 65, pdf.PDFArray{pdf.PDFInteger(widthA)}, ctx)
	if hasCheck(ctx, pdf.Checks.Font.SubsetGlyphCoverage) {
		t.Error("unexpected SubsetGlyphCoverage for a used, present glyph")
	}

	// validateSimpleTrueTypeMetrics doesn't consult usage info at all, but
	// exercise it here too against the same fixture for completeness.
	ctx2 := &ValidationContext{}
	validateSimpleTrueTypeMetrics(v, ff, 65, pdf.PDFArray{pdf.PDFInteger(widthA)}, ctx2)
	if hasCheck(ctx2, pdf.Checks.Font.AdvanceWidthMismatch) {
		t.Error("unexpected AdvanceWidthMismatch for a matching width")
	}
}

func TestValidateCMapWMode(t *testing.T) {
	cmap := pdf.NewPDFDict()
	cmap.HasStream = true
	cmap.Entries["WMode"] = pdf.PDFInteger(1)
	cmap.RawStream = []byte("/WMode 0 def\n")

	ctx := &ValidationContext{}
	validateCMapWMode(pdf.PDFDict{}, cmap, ctx)
	if !hasCheck(ctx, pdf.Checks.Font.CMapWModeInconsistent) {
		t.Error("expected CMapWModeInconsistent when dict/stream WMode disagree")
	}

	cmapOK := pdf.NewPDFDict()
	cmapOK.HasStream = true
	cmapOK.Entries["WMode"] = pdf.PDFInteger(1)
	cmapOK.RawStream = []byte("/WMode 1 def\n")
	ctx2 := &ValidationContext{}
	validateCMapWMode(pdf.PDFDict{}, cmapOK, ctx2)
	if hasCheck(ctx2, pdf.Checks.Font.CMapWModeInconsistent) {
		t.Error("unexpected CMapWModeInconsistent when dict/stream WMode agree")
	}
}

// TestUndecodableStreamReportedOnce covers the StreamKey dedup: one broken
// font program read by several checkers must yield a single issue, not one
// per reader.
func TestUndecodableStreamReportedOnce(t *testing.T) {
	ff := pdf.NewPDFDict()
	ff.HasStream = true
	ff.Entries["Filter"] = pdf.PDFName{Value: "FlateDecode"}
	ff.RawStream = []byte("not a zlib stream")

	ctx := &ValidationContext{}
	v := pdf.NewPDFDict()
	ValidateCIDCFFSubset(v, ff, nil, ctx)
	validateCIDTrueTypeMetrics(v, ff, nil, ctx)
	ValidateSimpleTrueTypeSubset(v, ff, 0, 0, nil, ctx)

	n := 0
	for _, e := range ctx.errs {
		if e.Check() == pdf.Checks.Structure.StreamUndecodable {
			n++
		}
	}
	if n != 1 {
		t.Errorf("StreamUndecodable reported %d times for one stream, want 1", n)
	}
}

// TestUndecodableFontProgramReportsBothChecks covers the deliberate overlap: a
// broken FontFile stream is both a structural defect (6.1.7) and a damaged
// font program (6.3.2), and the two say different things to the user.
func TestUndecodableFontProgramReportsBothChecks(t *testing.T) {
	ff := pdf.NewPDFDict()
	ff.HasStream = true
	ff.Entries["Filter"] = pdf.PDFName{Value: "FlateDecode"}
	ff.RawStream = []byte("not a zlib stream")

	desc := pdf.NewPDFDict()
	desc.Entries["FontFile2"] = ff

	ctx := &ValidationContext{}
	ValidateFontProgram(pdf.NewPDFDict(), desc, "Test", ctx)

	if !hasCheck(ctx, pdf.Checks.Font.InvalidProgram) {
		t.Error("expected Font.InvalidProgram for a damaged font program")
	}
	if !hasCheck(ctx, pdf.Checks.Structure.StreamUndecodable) {
		t.Error("expected Structure.StreamUndecodable for a stream that will not decode")
	}
}
