package verify

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"strings"
	"testing"

	"github.com/voidrab/gopdfrab/internal/fontprog"
	"github.com/voidrab/gopdfrab/internal/pdf"
)

func loadTTF(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("../fontsubst/fonts/LiberationSans-Regular.ttf")
	if err != nil {
		t.Skipf("font asset not available: %v", err)
	}
	return data
}

// TestTrueTypeTableParsers exercises the sfnt/cmap/glyf/hmtx accessors against a
// real TrueType font.
func TestTrueTypeTableParsers(t *testing.T) {
	tables, ok := fontprog.ParseSfnt(loadTTF(t))
	if !ok {
		t.Fatal("ParseSfnt failed on a valid TTF")
	}

	if n := fontprog.TTNumGlyphs(tables); n <= 0 {
		t.Fatalf("TTNumGlyphs = %d", n)
	}

	cmap := fontprog.TTWindowsBMPCmap(tables)
	if cmap == nil {
		t.Fatal("TTWindowsBMPCmap returned nil")
	}
	gidMap := fontprog.ParseCmapFormat4(cmap)
	gidA, ok := gidMap['A']
	if !ok {
		t.Fatal("no glyph for 'A'")
	}

	if !fontprog.TTLocaHasGlyph(tables)(int(gidA)) {
		t.Error("TTLocaHasGlyph false for a real glyph")
	}
	if !fontprog.TTGlyphInRange(tables)(int(gidA)) {
		t.Error("TTGlyphInRange false for a real glyph")
	}
	if !fontprog.TTGlyphPresent(tables)(int(gidA)) {
		t.Error("TTGlyphPresent false for a real glyph")
	}
	if w := fontprog.TTAdvanceWidth(tables, int(gidA)); w <= 0 {
		t.Errorf("TTAdvanceWidth('A') = %v", w)
	}
	if m := fontprog.ParseCmapSubtable(cmap); m == nil {
		t.Error("ParseCmapSubtable(format4) returned nil")
	}
}

// TestValidateEmbeddedTrueTypeFont runs the full font-dict validation over a
// font dict with an embedded (real) TrueType program.
func TestValidateEmbeddedTrueTypeFont(t *testing.T) {
	ttf := loadTTF(t)
	ff := pdf.NewPDFDict()
	ff.Entries.Set("Length1", pdf.PDFInteger(len(ttf)))
	ff.SetStream(ttf)

	desc := pdf.NewPDFDict()
	desc.Entries.Set("Type", pdf.PDFName{Value: "FontDescriptor"})
	desc.Entries.Set("FontName", pdf.PDFName{Value: "LiberationSans"})
	desc.Entries.Set("Flags", pdf.PDFInteger(32))
	desc.Entries.Set("FontFile2", ff)

	font := pdf.NewPDFDict()
	font.Entries.Set("Type", pdf.PDFName{Value: "Font"})
	font.Entries.Set("Subtype", pdf.PDFName{Value: "TrueType"})
	font.Entries.Set("BaseFont", pdf.PDFName{Value: "LiberationSans"})
	font.Entries.Set("Encoding", pdf.PDFName{Value: "WinAnsiEncoding"})
	font.Entries.Set("FirstChar", pdf.PDFInteger(65))
	font.Entries.Set("LastChar", pdf.PDFInteger(66))
	font.Entries.Set("Widths", pdf.PDFArray{pdf.PDFInteger(667), pdf.PDFInteger(667)})
	font.Entries.Set("FontDescriptor", desc)

	// Just needs to run through the embedded-program validation without panicking.
	ValidateFontDict(font, &ValidationContext{Spec: a1bSpec})
}

// buildMinimalCFF assembles a tiny name-keyed CFF font with two glyphs
// (.notdef and "A"), a format-0 charset, and an empty Private DICT. All Top
// DICT operands use the 5-byte int32 encoding (operator 29) so section offsets
// are fixed regardless of their magnitude.
func buildMinimalCFF() []byte {
	return buildMinimalCFFWith(nil, []byte{0x8b, 20, 0x8b, 21})
}

// buildMinimalCFFWith is buildMinimalCFF with extra operators prepended to the
// Top DICT and its own Private DICT, for the FontMatrix and width cases.
func buildMinimalCFFWith(extraTop, private []byte) []byte {
	i32 := func(v int) []byte {
		var b [5]byte
		b[0] = 29
		binary.BigEndian.PutUint32(b[1:], uint32(v))
		return b[:]
	}
	// Fixed layout offsets (see the section comments below).
	charsetOff := 45 + len(extraTop)
	charStringsOff := charsetOff + 3
	privateOff := charStringsOff + 8

	var cff []byte
	// Header: major=1 minor=0 hdrSize=4 offSize=1.
	cff = append(cff, 0x01, 0x00, 0x04, 0x01)
	// Name INDEX: 1 entry "Font".
	cff = append(cff, 0x00, 0x01, 0x01, 0x01, 0x05, 'F', 'o', 'n', 't')
	// Top DICT INDEX: 1 entry (23-byte dict).
	top := append([]byte(nil), extraTop...)
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
	// Private DICT: defaultWidthX (op 20) and nominalWidthX (op 21).
	cff = append(cff, private...)
	return cff
}

// cffReal nibble-encodes a decimal string as a CFF DICT real operand.
func cffReal(s string) []byte {
	nibbles := []byte{}
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c >= '0' && c <= '9':
			nibbles = append(nibbles, c-'0')
		case c == '.':
			nibbles = append(nibbles, 0xA)
		case c == '-':
			nibbles = append(nibbles, 0xE)
		}
	}
	nibbles = append(nibbles, 0xF)
	if len(nibbles)%2 == 1 {
		nibbles = append(nibbles, 0xF)
	}
	out := []byte{30}
	for i := 0; i < len(nibbles); i += 2 {
		out = append(out, nibbles[i]<<4|nibbles[i+1])
	}
	return out
}

func TestParseMinimalCFF(t *testing.T) {
	cff := buildMinimalCFF()

	td, ok := fontprog.ParseCFFTopDict(cff)
	if !ok {
		t.Fatal("ParseCFFTopDict failed on a hand-built CFF")
	}
	if td.CSOffset != 48 || td.CharsetOffset != 45 {
		t.Errorf("Top DICT offsets = CS %d, charset %d; want 48, 45", td.CSOffset, td.CharsetOffset)
	}

	names := fontprog.CFFGlyphNames(cff)
	if len(names) != 2 {
		t.Fatalf("CFFGlyphNames = %v, want 2 glyph names", names)
	}
	if names[1] != "A" {
		t.Errorf("glyph 1 name = %q, want \"A\"", names[1])
	}

	// ParseCFFIndex over the Name INDEX yields the single "Font" entry.
	entries, _ := fontprog.ParseCFFIndex(cff, int(cff[2]))
	if len(entries) != 1 || string(entries[0]) != "Font" {
		t.Errorf("Name INDEX = %q, want [\"Font\"]", entries)
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
	ff.SetStream(cff)

	widths := fontprog.CFFCIDAdvanceWidths(cff)
	if widths[1] != 600 || widths[2] != 700 {
		t.Fatalf("CFFCIDAdvanceWidths = %v, want {1:600, 2:700, ...}", widths)
	}

	// Coverage: both referenced CIDs are defined -> no report.
	w := pdf.PDFArray{pdf.PDFInteger(1), pdf.PDFArray{pdf.PDFInteger(600), pdf.PDFInteger(700)}}
	ctx := &ValidationContext{Spec: a1bSpec}
	ValidateCIDCFFSubset(pdf.PDFDict{}, ff, w, ctx)
	if hasCheck(ctx, pdf.Checks.Font.SubsetGlyphCoverage) {
		t.Error("unexpected SubsetGlyphCoverage for CIDs present in charset")
	}

	// Coverage: CID 5 is absent from the charset -> reported.
	wMissing := pdf.PDFArray{pdf.PDFInteger(5), pdf.PDFArray{pdf.PDFInteger(600)}}
	ctx2 := &ValidationContext{Spec: a1bSpec}
	ValidateCIDCFFSubset(pdf.PDFDict{}, ff, wMissing, ctx2)
	if !hasCheck(ctx2, pdf.Checks.Font.SubsetGlyphCoverage) {
		t.Error("expected SubsetGlyphCoverage for CID absent from charset")
	}

	// Metrics: matching widths -> no report.
	ctx3 := &ValidationContext{Spec: a1bSpec}
	validateCIDCFFMetrics(pdf.PDFDict{}, pdf.PDFDict{}, ff, w, ctx3)
	if hasCheck(ctx3, pdf.Checks.Font.AdvanceWidthMismatch) {
		t.Error("unexpected AdvanceWidthMismatch for matching CFF widths")
	}

	// Metrics: mismatched width -> reported.
	wBad := pdf.PDFArray{pdf.PDFInteger(1), pdf.PDFArray{pdf.PDFInteger(650)}}
	ctx4 := &ValidationContext{Spec: a1bSpec}
	validateCIDCFFMetrics(pdf.PDFDict{}, pdf.PDFDict{}, ff, wBad, ctx4)
	if !hasCheck(ctx4, pdf.Checks.Font.AdvanceWidthMismatch) {
		t.Error("expected AdvanceWidthMismatch for mismatched CFF width")
	}

	// CIDSet bitmap: both CIDs marked -> no report.
	desc := pdf.NewPDFDict()
	cidSet := pdf.NewPDFDict()
	cidSet.SetStream([]byte{0xE0})
	// bits for CID 0 (.notdef, 0x80), 1 (0x40), 2 (0x20)
	desc.Entries.Set("CIDSet", cidSet)
	ctx5 := &ValidationContext{Spec: a1bSpec}
	validateCIDSetBitmap(pdf.PDFDict{}, desc, ff, ctx5)
	if hasCheck(ctx5, pdf.Checks.Font.CIDSubsetCIDSet) {
		t.Error("unexpected CIDSubsetCIDSet when bitmap covers all CIDs")
	}

	// CIDSet bitmap: CID 2 not marked -> reported.
	desc2 := pdf.NewPDFDict()
	cidSet2 := pdf.NewPDFDict()
	cidSet2.SetStream([]byte{0xC0})
	// CID 0 and CID 1 marked, CID 2 missing
	desc2.Entries.Set("CIDSet", cidSet2)
	ctx6 := &ValidationContext{Spec: a1bSpec}
	validateCIDSetBitmap(pdf.PDFDict{}, desc2, ff, ctx6)
	if !hasCheck(ctx6, pdf.Checks.Font.CIDSubsetCIDSet) {
		t.Error("expected CIDSubsetCIDSet when a CID's bit is missing")
	}
}

func TestCidsToCheckAndDefaultWidth(t *testing.T) {
	w := pdf.PDFArray{pdf.PDFInteger(1), pdf.PDFArray{pdf.PDFInteger(500), pdf.PDFInteger(600)}}
	desc := pdf.NewPDFDict()

	// No usage info: falls back to the W array's CIDs.
	cids, known := cidsToCheck(desc, w, &ValidationContext{Spec: a1bSpec})
	if known {
		t.Error("cidsToCheck: known should be false with no UsedCIDs")
	}
	if len(cids) != 2 || cids[0] != 1 || cids[1] != 2 {
		t.Errorf("cidsToCheck fallback = %v, want [1 2]", cids)
	}

	// Usage info present: reflects UsedCIDs instead of W.
	ptr := pdf.ValuePointer(desc.Entries)
	ctx := &ValidationContext{Spec: a1bSpec, UsedCIDs: map[uintptr]map[int]bool{ptr: {7: true, 3: true}}}
	cids, known = cidsToCheck(desc, w, ctx)
	if !known || len(cids) != 2 || cids[0] != 3 || cids[1] != 7 {
		t.Errorf("cidsToCheck with usage = %v, known=%v, want [3 7] true", cids, known)
	}

	if cidDefaultWidth(desc) != 1000 {
		t.Error("cidDefaultWidth should default to 1000 when DW is absent")
	}
	desc.Entries.Set("DW", pdf.PDFInteger(2000))
	if cidDefaultWidth(desc) != 2000 {
		t.Error("cidDefaultWidth should reflect the DW entry")
	}
}

// buildCIDTrueTypeFont returns a CIDFontType2 descendant dict embedding the
// real LiberationSans TTF, plus the GID for 'A' and its hmtx advance width.
func buildCIDTrueTypeFont(t *testing.T) (desc pdf.PDFDict, ff pdf.PDFDict, gidA int, widthA int) {
	t.Helper()
	ttf := loadTTF(t)
	tables, ok := fontprog.ParseSfnt(ttf)
	if !ok {
		t.Fatal("ParseSfnt failed")
	}
	gid, ok := fontprog.ParseCmapFormat4(fontprog.TTWindowsBMPCmap(tables))['A']
	if !ok {
		t.Fatal("no glyph for 'A'")
	}
	ff = pdf.NewPDFDict()
	ff.SetStream(ttf)

	desc = pdf.NewPDFDict()
	return desc, ff, int(gid), int(math.Round(fontprog.TTAdvanceWidth(tables, int(gid))))
}

func TestCIDTrueTypeSubsetAndMetrics(t *testing.T) {
	desc, ff, gidA, widthA := buildCIDTrueTypeFont(t)

	w := pdf.PDFArray{pdf.PDFInteger(gidA), pdf.PDFArray{pdf.PDFInteger(widthA)}}
	ctx := &ValidationContext{Spec: a1bSpec}
	ValidateCIDTrueTypeSubset(desc, ff, w, ctx)
	if hasCheck(ctx, pdf.Checks.Font.SubsetGlyphCoverage) {
		t.Error("unexpected SubsetGlyphCoverage for a real glyph")
	}

	wMissing := pdf.PDFArray{pdf.PDFInteger(999999), pdf.PDFArray{pdf.PDFInteger(500)}}
	ctx2 := &ValidationContext{Spec: a1bSpec}
	ValidateCIDTrueTypeSubset(desc, ff, wMissing, ctx2)
	if !hasCheck(ctx2, pdf.Checks.Font.SubsetGlyphCoverage) {
		t.Error("expected SubsetGlyphCoverage for an out-of-range CID")
	}

	ctx3 := &ValidationContext{Spec: a1bSpec}
	validateCIDTrueTypeMetrics(desc, ff, w, ctx3)
	if hasCheck(ctx3, pdf.Checks.Font.AdvanceWidthMismatch) {
		t.Error("unexpected AdvanceWidthMismatch for matching hmtx width")
	}

	wBad := pdf.PDFArray{pdf.PDFInteger(gidA), pdf.PDFArray{pdf.PDFInteger(widthA + 500)}}
	ctx4 := &ValidationContext{Spec: a1bSpec}
	validateCIDTrueTypeMetrics(desc, ff, wBad, ctx4)
	if !hasCheck(ctx4, pdf.Checks.Font.AdvanceWidthMismatch) {
		t.Error("expected AdvanceWidthMismatch for mismatched hmtx width")
	}
	// Rendered CID absent from W falls back to DW; make DW clash with hmtx.
	desc.Entries.Set("DW", pdf.PDFInteger(widthA+500))
	ptr := pdf.ValuePointer(desc.Entries)
	ctx5 := &ValidationContext{Spec: a1bSpec, UsedCIDs: map[uintptr]map[int]bool{ptr: {gidA: true}}}
	validateCIDTrueTypeMetrics(desc, ff, pdf.PDFArray{}, ctx5)
	if !hasCheck(ctx5, pdf.Checks.Font.AdvanceWidthMismatch) {
		t.Error("expected AdvanceWidthMismatch via DW fallback for a rendered CID absent from W")
	}
}

func TestValidateCIDSetTrueType(t *testing.T) {
	_, ff, _, _ := buildCIDTrueTypeFont(t)
	ttf := loadTTF(t)
	tables, _ := fontprog.ParseSfnt(ttf)
	numGlyphs := fontprog.TTNumGlyphs(tables)

	// A CID the document renders, which the font has a glyph for.
	rendered := 0
	hasGlyph := fontprog.TTLocaHasGlyph(tables)
	for cid := 1; cid < numGlyphs; cid++ {
		if hasGlyph(cid) {
			rendered = cid
			break
		}
	}
	if rendered == 0 {
		t.Fatal("fixture font has no glyph with outline data")
	}

	// The check is scoped to rendered CIDs, so the font dict identity carrying
	// the usage is what drives it.
	font := pdf.NewPDFDict()
	usage := map[uintptr]map[int]bool{
		pdf.ValuePointer(font.Entries): {rendered: true},
	}

	desc := pdf.NewPDFDict()
	cidSet := pdf.NewPDFDict()
	cidSet.SetStream(make([]byte, (numGlyphs+7)/8))
	// all-zero: the rendered CID is unmarked
	desc.Entries.Set("CIDSet", cidSet)
	ctx := &ValidationContext{Spec: a1bSpec, UsedCIDs: usage}
	validateCIDSetTrueType(font, desc, ff, ctx)
	if !hasCheck(ctx, pdf.Checks.Font.CIDSubsetCIDSet) {
		t.Error("expected CIDSubsetCIDSet with an empty CIDSet bitmap")
	}

	fullBitmap := make([]byte, (numGlyphs+7)/8)
	for i := range fullBitmap {
		fullBitmap[i] = 0xFF
	}
	cidSet2 := pdf.NewPDFDict()
	cidSet2.SetStream(fullBitmap)
	desc2 := pdf.NewPDFDict()
	desc2.Entries.Set("CIDSet", cidSet2)
	ctx2 := &ValidationContext{Spec: a1bSpec, UsedCIDs: usage}
	validateCIDSetTrueType(font, desc2, ff, ctx2)
	if hasCheck(ctx2, pdf.Checks.Font.CIDSubsetCIDSet) {
		t.Error("unexpected CIDSubsetCIDSet with a fully-set CIDSet bitmap")
	}

	// A glyph the font carries but the document never renders is not the
	// CIDSet's business: a TrueType subsetter routinely leaves outlines behind.
	desc3 := pdf.NewPDFDict()
	cidSet3 := pdf.NewPDFDict()
	cidSet3.SetStream(make([]byte, (numGlyphs+7)/8))
	desc3.Entries.Set("CIDSet", cidSet3)
	ctx3 := &ValidationContext{Spec: a1bSpec, UsedCIDs: map[uintptr]map[int]bool{pdf.ValuePointer(font.Entries): {}}}
	validateCIDSetTrueType(font, desc3, ff, ctx3)
	if hasCheck(ctx3, pdf.Checks.Font.CIDSubsetCIDSet) {
		t.Error("unexpected CIDSubsetCIDSet for glyphs the document never renders")
	}

	// Usage unknown (an undecodable content stream): the check must not run at
	// all rather than fall back to the glyph table.
	ctx4 := &ValidationContext{Spec: a1bSpec}
	validateCIDSetTrueType(font, desc, ff, ctx4)
	if hasCheck(ctx4, pdf.Checks.Font.CIDSubsetCIDSet) {
		t.Error("unexpected CIDSubsetCIDSet when usage is unknown")
	}
}

// encryptType1 is the inverse of DecryptType1Block: it prepends the
// conventional 4 zero lenIV bytes and encrypts plain so that
// DecryptType1Block(result, seedKey) reproduces plain exactly.
func encryptType1(plain []byte, seedKey uint16) []byte {
	return encryptType1Pad(plain, seedKey, 4)
}

// encryptType1Pad is encryptType1 with the lenIV padding length spelled out.
func encryptType1Pad(plain []byte, seedKey uint16, lenIV int) []byte {
	padded := append(make([]byte, lenIV), plain...)
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
	return buildType1FontHeader("")
}

// buildType1FontHeader is buildType1Font with an extra clear-text header line,
// for the /FontMatrix cases.
func buildType1FontHeader(extra string) []byte {
	return buildType1FontHeaderRaw("%!PS-AdobeFont-1.0: Test\n/Encoding StandardEncoding def\n" + extra)
}

// buildType1FontHeaderRaw is buildType1Font with the whole clear-text header
// replaced, for the /Encoding cases.
func buildType1FontHeaderRaw(header string) []byte {
	csPlain := []byte{139, 248, 136, 13} // sbx=0 wx=500, hsbw
	csCipher := encryptType1(csPlain, 4330)

	var binPlain []byte
	binPlain = append(binPlain, []byte("dup /CharStrings 2 dict dup begin\n/A 8 RD ")...)
	binPlain = append(binPlain, csCipher...)
	binPlain = append(binPlain, []byte(" ND\nend\n")...)
	eexecCipher := encryptType1(binPlain, 55665)

	var font []byte
	font = append(font, []byte(header+"currentfile eexec\n")...)
	font = append(font, eexecCipher...)
	return font
}

// TestType1LenIVZero verifies that a program declaring /lenIV 0 pads its
// charstrings with nothing and names the charstring operator "-|" rather than
// "RD". Reading either the way the default prescribes left the program with no
// glyphs and no widths, so 6.3.5 and 6.3.6 both went quiet on it.
func TestType1LenIVZero(t *testing.T) {
	csCipher := encryptType1Pad([]byte{139, 248, 136, 13}, 4330, 0) // sbx=0 wx=500, hsbw

	var binPlain []byte
	binPlain = append(binPlain, []byte("dup /Private 2 dict dup begin\n/lenIV 0 def\n")...)
	binPlain = append(binPlain, []byte("dup /CharStrings 1 dict dup begin\n/A 4 -| ")...)
	binPlain = append(binPlain, csCipher...)
	binPlain = append(binPlain, []byte(" |-\nend\n")...)

	var font []byte
	font = append(font, []byte("%!PS-AdobeFont-1.0: Test\n/Encoding StandardEncoding def\ncurrentfile eexec\n")...)
	font = append(font, encryptType1(binPlain, 55665)...)

	if lenIV := fontprog.Type1LenIV(fontprog.DecryptType1Block(font[fontprog.Type1EexecBinStart(font):], 55665)); lenIV != 0 {
		t.Errorf("lenIV = %d, want 0", lenIV)
	}
	if names := fontprog.Type1GlyphNames(font); len(names) != 1 || names[0] != "A" {
		t.Errorf("Type1GlyphNames = %v, want [A]", names)
	}
	if got := fontprog.Type1GlyphWidths(font)["A"]; got != 500 {
		t.Errorf("width of /A = %v, want 500", got)
	}
}

// TestType1MetricsSkipsUnusedCodes verifies that a /Widths entry for a code the
// document never draws is not compared. It says nothing about what gets
// rendered, and the base encoding can still put a real glyph name on it.
// Comparing it reported a mismatch on a font whose drawn codes are all
// consistent.
func TestType1MetricsSkipsUnusedCodes(t *testing.T) {
	ff := pdf.NewStreamDict(pdf.NewPDFDict().Entries, buildType1Font())
	v := pdf.NewPDFDict()
	v.Entries.Set("Encoding", pdf.PDFName{Value: "StandardEncoding"})
	widths := pdf.PDFArray{pdf.PDFInteger(400)} // /A is 500 in the program

	ctx := &ValidationContext{Spec: a1bSpec, UsedCharCodes: map[uintptr]map[int]bool{
		pdf.ValuePointer(v.Entries): {66: true},
	}}
	validateType1Metrics(v, pdf.PDFDict{}, ff, 65, widths, v.Entries.Get("Encoding"), ctx)
	if hasCheck(ctx, pdf.Checks.Font.AdvanceWidthMismatch) {
		t.Error("width of a code that is never shown should not be compared")
	}

	shown := &ValidationContext{Spec: a1bSpec, UsedCharCodes: map[uintptr]map[int]bool{
		pdf.ValuePointer(v.Entries): {65: true},
	}}
	validateType1Metrics(v, pdf.PDFDict{}, ff, 65, widths, v.Entries.Get("Encoding"), shown)
	if !hasCheck(shown, pdf.Checks.Font.AdvanceWidthMismatch) {
		t.Error("width of a shown code should still be compared")
	}
}

// TestType1MetricsMissingWidth verifies that a shown code below FirstChar takes
// its width from the descriptor's /MissingWidth, so it is compared there
// instead of going unchecked.
func TestType1MetricsMissingWidth(t *testing.T) {
	ff := pdf.NewStreamDict(pdf.NewPDFDict().Entries, buildType1Font())
	desc := pdf.NewPDFDict()
	desc.Entries.Set("MissingWidth", pdf.PDFInteger(78))
	v := pdf.NewPDFDict()
	v.Entries.Set("Encoding", pdf.PDFName{Value: "StandardEncoding"})

	ctx := &ValidationContext{Spec: a1bSpec, UsedCharCodes: map[uintptr]map[int]bool{
		pdf.ValuePointer(v.Entries): {65: true}, // /A, 500 in the program
	}}
	validateType1Metrics(v, desc, ff, 70, pdf.PDFArray{pdf.PDFInteger(500)}, v.Entries.Get("Encoding"), ctx)
	if !hasCheck(ctx, pdf.Checks.Font.AdvanceWidthMismatch) {
		t.Error("a shown code outside /Widths should be compared against /MissingWidth")
	}
}

// TestType1SubroutinizedWidth verifies that the hsbw width is found in a
// subroutinized program, which pushes the hsbw operands and keeps hsbw itself
// in a subr. A scan that gives up at callsubr finds no width for the glyph,
// and 6.3.6 never compares it.
func TestType1SubroutinizedWidth(t *testing.T) {
	subr := encryptType1([]byte{13, 11}, 4330) // hsbw, return
	glyph := encryptType1([]byte{139, 248, 136, 255, 0, 0, 0, 7, 10, 14}, 4330)

	var binPlain []byte
	binPlain = append(binPlain, []byte("dup /Private 2 dict dup begin\n/Subrs 8 array\ndup 7 6 RD ")...)
	binPlain = append(binPlain, subr...)
	binPlain = append(binPlain, []byte(" NP\nND\n")...)
	binPlain = append(binPlain, []byte("dup /CharStrings 1 dict dup begin\n/A 14 RD ")...)
	binPlain = append(binPlain, glyph...)
	binPlain = append(binPlain, []byte(" ND\nend\n")...)

	var font []byte
	font = append(font, []byte("%!PS-AdobeFont-1.0: Test\n/Encoding StandardEncoding def\ncurrentfile eexec\n")...)
	font = append(font, encryptType1(binPlain, 55665)...)

	if got := fontprog.Type1GlyphWidths(font)["A"]; got != 500 {
		t.Errorf("width of a subroutinized /A = %v, want 500", got)
	}
}

// TestType1BuiltinEncodingArray covers the biggest of the width bails: a
// program that builds its own /Encoding array instead of naming a standard
// encoding. Subset TeX fonts nearly all do, and four real-world files reached
// veraPDF's 6.3.6 through this hole.
func TestType1BuiltinEncodingArray(t *testing.T) {
	custom := "/Encoding 256 array\n" +
		"0 1 255 {1 index exch /.notdef put} for\n" +
		"dup 62 /braceex put\n" +
		"dup 122 /bracehtipdownleft put\n" +
		"readonly def\n"
	font := buildType1FontHeaderRaw("%!PS-AdobeFont-1.0: Test\n" + custom)

	enc, ok := fontprog.Type1EncodingTable(font, "")
	if !ok {
		t.Fatal("Type1EncodingTable should resolve a program's own /Encoding array")
	}
	if enc[62] != "braceex" || enc[122] != "bracehtipdownleft" {
		t.Errorf("enc[62]=%q enc[122]=%q, want braceex and bracehtipdownleft", enc[62], enc[122])
	}
	if enc[63] != "" {
		t.Errorf("enc[63] = %q, want a code the array never sets to be empty", enc[63])
	}

	// A program naming a standard encoding still resolves to that, not an array.
	if enc, ok := fontprog.Type1EncodingTable(buildType1Font(), ""); !ok || enc[65] != "A" {
		t.Errorf("named StandardEncoding regressed: ok=%v enc[65]=%q", ok, enc[65])
	}
}

func TestType1WidthScale(t *testing.T) {
	cases := []struct {
		name  string
		extra string
		scale float64
		ok    bool
	}{
		{"none declared", "", 1, true},
		{"default", "/FontMatrix [0.001 0 0 0.001 0 0] readonly def\n", 1, true},
		{"half em", "/FontMatrix [0.0005 0 0 0.0005 0 0] readonly def\n", 0.5, true},
		{"shear leaves the advance alone", "/FontMatrix [0.001 0 0.0002 0.001 0 0] def\n", 1, true},
		{"rotated", "/FontMatrix [0 0.001 -0.001 0 0 0] def\n", 0, false},
		{"degenerate", "/FontMatrix [0 0 0 0 0 0] def\n", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scale, ok := fontprog.Type1WidthScale(buildType1FontHeader(tc.extra))
			if scale != tc.scale || ok != tc.ok {
				t.Errorf("Type1WidthScale = %v, %v; want %v, %v", scale, ok, tc.scale, tc.ok)
			}
		})
	}
}

// TestType1MetricsFontMatrix covers a 1/2000 em program, whose charstring
// widths are twice what /Widths says.
func TestType1MetricsFontMatrix(t *testing.T) {
	font := buildType1FontHeader("/FontMatrix [0.0005 0 0 0.0005 0 0] readonly def\n")
	if w := fontprog.Type1GlyphWidths(font)["A"]; w != 250 {
		t.Fatalf("Type1GlyphWidths[A] = %v, want 250 (500 glyph units at 1/2000 em)", w)
	}

	ff := pdf.NewPDFDict()
	ff.SetStream(font)

	ctx := &ValidationContext{Spec: a1bSpec}
	validateType1Metrics(pdf.PDFDict{}, pdf.PDFDict{}, ff, 65, pdf.PDFArray{pdf.PDFInteger(250)}, nil, ctx)
	if hasCheck(ctx, pdf.Checks.Font.AdvanceWidthMismatch) {
		t.Error("unexpected AdvanceWidthMismatch for a width matching the scaled advance")
	}

	ctx2 := &ValidationContext{Spec: a1bSpec}
	validateType1Metrics(pdf.PDFDict{}, pdf.PDFDict{}, ff, 65, pdf.PDFArray{pdf.PDFInteger(500)}, nil, ctx2)
	if !hasCheck(ctx2, pdf.Checks.Font.AdvanceWidthMismatch) {
		t.Error("expected AdvanceWidthMismatch: 500 is the unscaled charstring width")
	}
}

// TestType1MetricsUnusableFontMatrix keeps the bail measurable: a matrix that is
// not a plain horizontal scale still gives up, and yields no widths for the
// convert-side fixer to write.
func TestType1MetricsUnusableFontMatrix(t *testing.T) {
	font := buildType1FontHeader("/FontMatrix [0 0.001 -0.001 0 0 0] def\n")
	if w := fontprog.Type1GlyphWidths(font); len(w) != 0 {
		t.Errorf("Type1GlyphWidths = %v, want none", w)
	}
	if names := fontprog.Type1GlyphNames(font); len(names) != 1 {
		t.Errorf("Type1GlyphNames = %v, want the glyph to still be named", names)
	}
	_, stats := fontprog.Type1WidthTable(font, nil, nil)
	if stats.Skip != fontprog.WidthSkipFontMatrix {
		t.Errorf("skip = %q, want %q", stats.Skip, fontprog.WidthSkipFontMatrix)
	}
}

func TestType1EexecRoundTrip(t *testing.T) {
	font := buildType1Font()

	binStart := fontprog.Type1EexecBinStart(font)
	if binStart <= 0 {
		t.Fatalf("Type1EexecBinStart = %d, want > 0", binStart)
	}

	plain := fontprog.DecryptType1Block(font[binStart:], 55665)
	if !bytes.Contains(plain, []byte("/CharStrings")) {
		t.Fatalf("decrypted eexec section has no /CharStrings: %q", plain)
	}
	if lenIV := fontprog.Type1LenIV(plain); lenIV != 4 {
		t.Errorf("lenIV = %d, want the default 4", lenIV)
	}

	names := fontprog.Type1GlyphNames(font)
	if len(names) != 1 || names[0] != "A" {
		t.Fatalf("Type1GlyphNames = %v, want [A]", names)
	}

	widths := fontprog.Type1GlyphWidths(font)
	if widths["A"] != 500 {
		t.Fatalf("Type1GlyphWidths[A] = %v, want 500", widths["A"])
	}

	enc, ok := fontprog.Type1EncodingTable(font, "")
	if !ok || enc[65] != "A" {
		t.Fatalf("Type1EncodingTable fallback: enc[65] = %q, ok=%v", enc[65], ok)
	}
	if enc2, ok2 := fontprog.Type1EncodingTable(font, "WinAnsiEncoding"); !ok2 || enc2[65] != "A" {
		t.Error("Type1EncodingTable(WinAnsiEncoding) should resolve to WinAnsiGlyphName")
	}
	if _, ok3 := fontprog.Type1EncodingTable(font, "UnknownEncoding"); ok3 {
		t.Error("Type1EncodingTable(unknown) should be ok=false")
	}
	if fontprog.Type1EexecBinStart([]byte("no marker here")) != -1 {
		t.Error("Type1EexecBinStart should be -1 without an eexec marker")
	}
}

func TestValidateType1Metrics(t *testing.T) {
	font := buildType1Font()
	ff := pdf.NewPDFDict()
	ff.SetStream(font)

	widths := pdf.PDFArray{pdf.PDFInteger(500)}
	ctx := &ValidationContext{Spec: a1bSpec}
	validateType1Metrics(pdf.PDFDict{}, pdf.PDFDict{}, ff, 65, widths, nil, ctx)
	if hasCheck(ctx, pdf.Checks.Font.AdvanceWidthMismatch) {
		t.Error("unexpected AdvanceWidthMismatch for matching Type1 width")
	}

	widthsBad := pdf.PDFArray{pdf.PDFInteger(520)}
	ctx2 := &ValidationContext{Spec: a1bSpec}
	validateType1Metrics(pdf.PDFDict{}, pdf.PDFDict{}, ff, 65, widthsBad, nil, ctx2)
	if !hasCheck(ctx2, pdf.Checks.Font.AdvanceWidthMismatch) {
		t.Error("expected AdvanceWidthMismatch for mismatched Type1 width")
	}
}

func TestValidateType1SubsetCoverage(t *testing.T) {
	font := buildType1Font()
	ff := pdf.NewPDFDict()
	ff.SetStream(font)

	desc := pdf.NewPDFDict()
	desc.Entries.Set("FontFile", ff)
	desc.Entries.Set("CharSet", pdf.PDFString{Value: "/A"})

	v := pdf.NewPDFDict()
	v.Entries.Set("Encoding", pdf.PDFName{Value: "WinAnsiEncoding"})

	widths := pdf.PDFArray{pdf.PDFInteger(500)} // code 65 = "A", present in program and CharSet
	ctx := &ValidationContext{Spec: a1bSpec}
	ValidateType1SubsetCoverage(v, v, desc, 65, 65, widths, ctx)
	if hasCheck(ctx, pdf.Checks.Font.SubsetGlyphCoverage) || hasCheck(ctx, pdf.Checks.Font.Type1SubsetCharSet) {
		t.Error("unexpected report for a glyph covered by both the program and CharSet")
	}

	// Code 66 ("B") has a non-zero width but no glyph in the program.
	widthsMissing := pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(500)}
	ctx2 := &ValidationContext{Spec: a1bSpec}
	ValidateType1SubsetCoverage(v, v, desc, 65, 66, widthsMissing, ctx2)
	if !hasCheck(ctx2, pdf.Checks.Font.SubsetGlyphCoverage) {
		t.Error("expected SubsetGlyphCoverage for a glyph absent from the embedded program")
	}

	// CharSet omits "A" though the program defines it.
	desc2 := pdf.NewPDFDict()
	desc2.Entries.Set("FontFile", ff)
	desc2.Entries.Set("CharSet", pdf.PDFString{Value: "/Zzz"})
	ctx3 := &ValidationContext{Spec: a1bSpec}
	ValidateType1SubsetCoverage(v, v, desc2, 65, 65, widths, ctx3)
	if !hasCheck(ctx3, pdf.Checks.Font.Type1SubsetCharSet) {
		t.Error("expected Type1SubsetCharSet when CharSet omits a glyph the program defines")
	}

	// Empty CharSet is flagged directly.
	desc3 := pdf.NewPDFDict()
	desc3.Entries.Set("CharSet", pdf.PDFString{Value: ""})
	ctx4 := &ValidationContext{Spec: a1bSpec}
	ValidateType1SubsetCoverage(v, v, desc3, 65, 65, widths, ctx4)
	if !hasCheck(ctx4, pdf.Checks.Font.Type1SubsetCharSet) {
		t.Error("expected Type1SubsetCharSet for an empty CharSet")
	}
}

func TestValidateType1CMetrics(t *testing.T) {
	cff := buildMinimalCFF() // glyph "A" from checks_font_program_test.go's builder
	ff := pdf.NewPDFDict()
	ff.SetStream(cff)

	v := pdf.NewPDFDict()
	v.Entries.Set("Encoding", pdf.PDFName{Value: "WinAnsiEncoding"})

	widths := fontprog.CFFAdvanceWidths(cff)
	aWidth := widths["A"]

	ctx := &ValidationContext{Spec: a1bSpec}
	validateType1CMetrics(pdf.PDFDict{}, v, ff, 65, pdf.PDFArray{pdf.PDFInteger(aWidth)}, ctx)
	if hasCheck(ctx, pdf.Checks.Font.AdvanceWidthMismatch) {
		t.Error("unexpected AdvanceWidthMismatch for matching CFF width")
	}

	ctx2 := &ValidationContext{Spec: a1bSpec}
	validateType1CMetrics(pdf.PDFDict{}, v, ff, 65, pdf.PDFArray{pdf.PDFInteger(aWidth + 50)}, ctx2)
	if !hasCheck(ctx2, pdf.Checks.Font.AdvanceWidthMismatch) {
		t.Error("expected AdvanceWidthMismatch for mismatched CFF width")
	}
}

func TestEmbeddedType1GlyphNames(t *testing.T) {
	descType1 := pdf.NewPDFDict()
	ff := pdf.NewPDFDict()
	ff.SetStream(buildType1Font())
	descType1.Entries.Set("FontFile", ff)
	if names := embeddedType1GlyphNames(descType1, &ValidationContext{Spec: a1bSpec}); len(names) != 1 || names[0] != "A" {
		t.Errorf("embeddedType1GlyphNames(FontFile) = %v, want [A]", names)
	}

	descCFF := pdf.NewPDFDict()
	ff3 := pdf.NewPDFDict()
	ff3.SetStream(buildMinimalCFF())
	descCFF.Entries.Set("FontFile3", ff3)
	if names := embeddedType1GlyphNames(descCFF, &ValidationContext{Spec: a1bSpec}); len(names) == 0 {
		t.Error("embeddedType1GlyphNames(FontFile3) returned nothing for a valid CFF")
	}

	if names := embeddedType1GlyphNames(pdf.NewPDFDict(), &ValidationContext{Spec: a1bSpec}); names != nil {
		t.Error("embeddedType1GlyphNames with no program should be nil")
	}
}

func TestTrueTypeCmapSubtablesAndSymbolicCmap(t *testing.T) {
	ttf := loadTTF(t)
	desc := pdf.NewPDFDict()
	ff := pdf.NewPDFDict()
	ff.SetStream(ttf)
	desc.Entries.Set("FontFile2", ff)

	cm, ok := trueTypeCmaps(&ValidationContext{Spec: a1bSpec}, desc)
	n := cm.count
	if !ok || n <= 0 {
		t.Errorf("trueTypeCmaps = %d subtables, %v", n, ok)
	}
	if _, ok := trueTypeCmaps(&ValidationContext{Spec: a1bSpec}, pdf.NewPDFDict()); ok {
		t.Error("trueTypeCmaps should be ok=false with no FontFile2")
	}

	// A (1,0) Mac subtable is returned when no (3,0) Windows symbol subtable exists.
	tables, _ := fontprog.ParseSfnt(ttf)
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
		if fontprog.TTCmapSubtable(tables, 1, 0) == nil {
			t.Error("TTCmapSubtable should find the (1,0) subtable")
		}
	}
}

func TestValidateSimpleTrueTypeMetricsAndSubset(t *testing.T) {
	ttf := loadTTF(t)
	ff := pdf.NewPDFDict()
	ff.SetStream(ttf)

	tables, _ := fontprog.ParseSfnt(ttf)
	gidA, _ := fontprog.ParseCmapFormat4(fontprog.TTWindowsBMPCmap(tables))['A']
	widthA := fontprog.TTAdvanceWidth(tables, int(gidA))

	v := pdf.NewPDFDict()
	v.Entries.Set("Encoding", pdf.PDFName{Value: "WinAnsiEncoding"})

	widths := pdf.PDFArray{pdf.PDFInteger(widthA)} // FirstChar 65 ('A')
	ctx := &ValidationContext{Spec: a1bSpec}
	validateSimpleTrueTypeMetrics(v, ff, 65, widths, ctx)
	if hasCheck(ctx, pdf.Checks.Font.AdvanceWidthMismatch) {
		t.Error("unexpected AdvanceWidthMismatch for matching hmtx width")
	}

	ctx2 := &ValidationContext{Spec: a1bSpec}
	validateSimpleTrueTypeMetrics(v, ff, 65, pdf.PDFArray{pdf.PDFInteger(widthA + 500)}, ctx2)
	if !hasCheck(ctx2, pdf.Checks.Font.AdvanceWidthMismatch) {
		t.Error("expected AdvanceWidthMismatch for mismatched hmtx width")
	}

	// Subset coverage: 'A' is present; a made-up private-use code with no cmap
	// entry, forced non-symbolic, resolves to .notdef and is flagged.
	desc := pdf.NewPDFDict()
	desc.Entries.Set("Flags", pdf.PDFInteger(0))
	// non-symbolic
	v.Entries.Set("FontDescriptor", desc)
	widthsSubset := pdf.PDFArray{pdf.PDFInteger(widthA)}
	ctx3 := &ValidationContext{Spec: a1bSpec}
	ValidateSimpleTrueTypeSubset(v, ff, 65, 65, widthsSubset, ctx3)
	if hasCheck(ctx3, pdf.Checks.Font.SubsetGlyphCoverage) {
		t.Error("unexpected SubsetGlyphCoverage for a real glyph")
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
		if _, ok := fontprog.ParseCFFTopDict(buildCFFWithTopDict(bad)); ok {
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
	if _, ok := fontprog.ParseCFFTopDict(buildCFFWithTopDict(withReal)); !ok {
		t.Error("ParseCFFTopDict should tolerate a real-number operand")
	}

	// FontMatrix (escape 12 7) and Private with too few operands.
	fm := []byte{12, 7, 139, 18} // FontMatrix marker, then Private with only 1 operand (ignored)
	td, ok := fontprog.ParseCFFTopDict(buildCFFWithTopDict(fm))
	if !ok || !td.HasFontMatrix {
		t.Error("ParseCFFTopDict should record HasFontMatrix from escape operator 7")
	}
}

func TestFontProgramValidBranches(t *testing.T) {
	ctx := &ValidationContext{Spec: a1bSpec}

	ttf := loadTTF(t)
	good2 := pdf.NewPDFDict()
	good2.SetStream(ttf)
	if !fontProgramValid(ctx, good2, "FontFile2") {
		t.Error("a valid TrueType FontFile2 should be valid")
	}

	bad2 := pdf.NewPDFDict()
	bad2.SetStream([]byte("not a font"))
	if fontProgramValid(ctx, bad2, "FontFile2") {
		t.Error("garbage FontFile2 data should be invalid")
	}

	cff := buildMinimalCFF()
	good3 := pdf.NewPDFDict()
	good3.SetStream(cff)
	if !fontProgramValid(ctx, good3, "FontFile3") {
		t.Error("a valid bare CFF FontFile3 should be valid")
	}

	// OpenType-wrapped CFF (sfnt with 'OTTO' tag).
	otf := pdf.NewPDFDict()
	otf.SetStream(append([]byte{0x4F, 0x54, 0x54, 0x4F}, ttf[4:]...))
	if !fontProgramValid(ctx, otf, "FontFile3") {
		t.Error("an OpenType-wrapped FontFile3 should be valid")
	}

	badFF3 := pdf.NewPDFDict()
	badFF3.SetStream([]byte("garbage"))
	if fontProgramValid(ctx, badFF3, "FontFile3") {
		t.Error("garbage FontFile3 data should be invalid")
	}

	goodT1 := pdf.NewPDFDict()
	goodT1.SetStream([]byte("%!PS-AdobeFont-1.0\n"))
	if !fontProgramValid(ctx, goodT1, "FontFile") {
		t.Error("a Type1 program starting with %! should be valid")
	}
	badT1 := pdf.NewPDFDict()
	badT1.SetStream([]byte("garbage"))
	if fontProgramValid(ctx, badT1, "FontFile") {
		t.Error("a Type1 program without a %! marker should be invalid")
	}

	empty := pdf.NewPDFDict()
	if fontProgramValid(ctx, empty, "FontFile2") {
		t.Error("a non-stream dict should be invalid")
	}

	unknown := pdf.NewPDFDict()
	unknown.SetStream([]byte("x"))
	if !fontProgramValid(ctx, unknown, "SomeOtherKey") {
		t.Error("an unrecognized key should default to valid")
	}
}

// TestFontProgramCheckersDecodeFailureGuards exercises the shared "no stream /
// undecodable / unparseable program" early-return guard present in most of
// this file's checkers -- each should be a silent no-op rather than panic.
func TestFontProgramCheckersDecodeFailureGuards(t *testing.T) {
	noStream := pdf.NewPDFDict() // IsStream()=false
	garbage := pdf.NewPDFDict()
	garbage.SetStream([]byte("not a font program"))

	ctx := &ValidationContext{Spec: a1bSpec}
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
	validateType1Metrics(v, pdf.PDFDict{}, noStream, 0, nil, nil, ctx)
	validateCMapWMode(v, pdf.NewPDFDict(), ctx) // no stream
	if _, ok := trueTypeCmaps(ctx, desc); ok {
		t.Error("trueTypeCmaps should be ok=false without a FontFile2")
	}
	ValidateFontProgram(v, desc, "Test", ctx) // no FontFile* entries at all

	if len(ctx.errs) != 0 {
		t.Errorf("expected no violations from decode-failure/no-program guards, got %v", ctx.errs)
	}

	// ValidateType1SubsetCoverage / embeddedType1GlyphNames with no CharSet/program.
	ctx2 := &ValidationContext{Spec: a1bSpec}
	ValidateType1SubsetCoverage(v, v, desc, 0, 0, nil, ctx2) // no CharSet: early return
	if len(ctx2.errs) != 0 {
		t.Error("unexpected violation with no CharSet entry")
	}
	if names := embeddedType1GlyphNames(desc, ctx2); names != nil {
		t.Error("embeddedType1GlyphNames should be nil with neither FontFile nor FontFile3")
	}
	descBadFF := pdf.NewPDFDict()
	descBadFF.Entries.Set("FontFile", garbage)
	if names := embeddedType1GlyphNames(descBadFF, ctx2); names != nil {
		t.Error("embeddedType1GlyphNames should be nil for an unparseable FontFile")
	}
}

func TestValidateSimpleTrueTypeUsedCodesKnown(t *testing.T) {
	ttf := loadTTF(t)
	ff := pdf.NewPDFDict()
	ff.SetStream(ttf)

	tables, _ := fontprog.ParseSfnt(ttf)
	gidA, _ := fontprog.ParseCmapFormat4(fontprog.TTWindowsBMPCmap(tables))['A']
	widthA := fontprog.TTAdvanceWidth(tables, int(gidA))

	v := pdf.NewPDFDict()
	v.Entries.Set("Encoding", pdf.PDFName{Value: "WinAnsiEncoding"})
	ptr := pdf.ValuePointer(v.Entries)

	// Usage info known: drives the usedCodes branch instead of the Widths fallback.
	ctx := &ValidationContext{Spec: a1bSpec, UsedCharCodes: map[uintptr]map[int]bool{ptr: {65: true}}}
	ValidateSimpleTrueTypeSubset(v, ff, 65, 65, pdf.PDFArray{pdf.PDFInteger(widthA)}, ctx)
	if hasCheck(ctx, pdf.Checks.Font.SubsetGlyphCoverage) {
		t.Error("unexpected SubsetGlyphCoverage for a used, present glyph")
	}

	// validateSimpleTrueTypeMetrics doesn't consult usage info at all, but
	// exercise it here too against the same fixture for completeness.
	ctx2 := &ValidationContext{Spec: a1bSpec}
	validateSimpleTrueTypeMetrics(v, ff, 65, pdf.PDFArray{pdf.PDFInteger(widthA)}, ctx2)
	if hasCheck(ctx2, pdf.Checks.Font.AdvanceWidthMismatch) {
		t.Error("unexpected AdvanceWidthMismatch for a matching width")
	}
}

func TestValidateCMapWMode(t *testing.T) {
	cmap := pdf.NewPDFDict()
	cmap.SetStream(nil)
	cmap.Entries.Set("WMode", pdf.PDFInteger(1))
	cmap.SetStream([]byte("/WMode 0 def\n"))

	ctx := &ValidationContext{Spec: a1bSpec}
	validateCMapWMode(pdf.PDFDict{}, cmap, ctx)
	if !hasCheck(ctx, pdf.Checks.Font.CMapWModeInconsistent) {
		t.Error("expected CMapWModeInconsistent when dict/stream WMode disagree")
	}

	cmapOK := pdf.NewPDFDict()
	cmapOK.SetStream(nil)
	cmapOK.Entries.Set("WMode", pdf.PDFInteger(1))
	cmapOK.SetStream([]byte("/WMode 1 def\n"))
	ctx2 := &ValidationContext{Spec: a1bSpec}
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
	ff.SetStream(nil)
	ff.Entries.Set("Filter", pdf.PDFName{Value: "FlateDecode"})
	ff.SetStream([]byte("not a zlib stream"))

	ctx := &ValidationContext{Spec: a1bSpec}
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
	ff.SetStream(nil)
	ff.Entries.Set("Filter", pdf.PDFName{Value: "FlateDecode"})
	ff.SetStream([]byte("not a zlib stream"))

	desc := pdf.NewPDFDict()
	desc.Entries.Set("FontFile2", ff)

	ctx := &ValidationContext{Spec: a1bSpec}
	ValidateFontProgram(pdf.NewPDFDict(), desc, "Test", ctx)

	if !hasCheck(ctx, pdf.Checks.Font.InvalidProgram) {
		t.Error("expected Font.InvalidProgram for a damaged font program")
	}
	if !hasCheck(ctx, pdf.Checks.Structure.StreamUndecodable) {
		t.Error("expected Structure.StreamUndecodable for a stream that will not decode")
	}
}

// TestFontFile3BrokenCFFIsInvalid covers the FontFile3 arm of fontProgramValid:
// a plausible CFF header is not a valid font. A good header with an
// unparseable Top DICT must not pass 6.3.2, since the CID coverage and metrics
// checks need that same DICT and would otherwise skip the program silently.
func TestFontFile3BrokenCFFIsInvalid(t *testing.T) {
	// major=1, minor=0, hdrSize=4, offSize=1, then garbage where the Name
	// INDEX should be.
	broken := pdf.NewPDFDict()
	broken.SetStream([]byte{1, 0, 4, 1, 0xFF, 0xFF, 0xFF, 0xFF})

	ctx := &ValidationContext{Spec: a1bSpec}
	if fontProgramValid(ctx, broken, "FontFile3") {
		t.Error("a CFF with a valid header but an unparseable Top DICT was accepted as a valid font program")
	}

	desc := pdf.NewPDFDict()
	desc.Entries.Set("FontFile3", broken)
	reportCtx := &ValidationContext{Spec: a1bSpec}
	ValidateFontProgram(pdf.NewPDFDict(), desc, "Test", reportCtx)
	if !hasCheck(reportCtx, pdf.Checks.Font.InvalidProgram) {
		t.Error("expected Font.InvalidProgram (6.3.2) for a broken CFF")
	}
}

// TestFontFileSubtype covers 6.3.2: an embedded font file may declare no
// Subtype at all, or Type1C or CIDFontType0C -- anything else (notably the
// post-PDF-1.4 OpenType) is not a format PDF/A-1 allows.
func TestFontFileSubtype(t *testing.T) {
	// A CFF that parses, so only the Subtype is ever in question.
	program := buildMinimalCFF()

	for _, tc := range []struct {
		name    string
		subtype string // "" means no Subtype entry
		want    bool
	}{
		{"no subtype", "", false},
		{"Type1C", "Type1C", false},
		{"CIDFontType0C", "CIDFontType0C", false},
		{"OpenType", "OpenType", true},
		{"nonsense", "TrueType", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ff := pdf.NewPDFDict()
			ff.SetStream(program)
			if tc.subtype != "" {
				ff.Entries.Set("Subtype", pdf.PDFName{Value: tc.subtype})
			}
			desc := pdf.NewPDFDict()
			desc.Entries.Set("FontFile3", ff)

			ctx := &ValidationContext{Spec: a1bSpec}
			ValidateFontProgram(pdf.NewPDFDict(), desc, "Test", ctx)

			if got := hasCheck(ctx, pdf.Checks.Font.FontFileSubtype); got != tc.want {
				t.Errorf("FontFileSubtype reported = %v, want %v", got, tc.want)
			}
			if hasCheck(ctx, pdf.Checks.Font.InvalidProgram) {
				t.Error("the font program itself is valid; InvalidProgram should not be reported")
			}
		})
	}
}

// TestFontFileSubtypeNonName covers a Subtype that is not a name at all: the
// check reads a name and stays silent otherwise, leaving the wrong-type
// report to the object-model checks.
func TestFontFileSubtypeNonName(t *testing.T) {
	ff := pdf.NewPDFDict()
	ff.SetStream(buildMinimalCFF())
	ff.Entries.Set("Subtype", pdf.PDFString{Value: "Type1C"})

	desc := pdf.NewPDFDict()
	desc.Entries.Set("FontFile3", ff)

	ctx := &ValidationContext{Spec: a1bSpec}
	ValidateFontProgram(pdf.NewPDFDict(), desc, "Test", ctx)
	if hasCheck(ctx, pdf.Checks.Font.FontFileSubtype) {
		t.Error("a non-name Subtype should not be reported as an unsupported font file format")
	}
}

// buildType1FontTwoGlyphs is buildType1Font with a second glyph /B of a
// different advance width (700 vs 500), so a /Differences remapping of a code
// from one to the other is observable in the 6.3.6 width comparison.
func buildType1FontTwoGlyphs() []byte {
	csA := encryptType1([]byte{139, 248, 136, 13}, 4330) // wx=500, hsbw
	csB := encryptType1([]byte{139, 249, 80, 13}, 4330)  // wx=700, hsbw

	var binPlain []byte
	binPlain = append(binPlain, []byte("dup /CharStrings 2 dict dup begin\n/A 8 RD ")...)
	binPlain = append(binPlain, csA...)
	binPlain = append(binPlain, []byte(" ND\n/B 8 RD ")...)
	binPlain = append(binPlain, csB...)
	binPlain = append(binPlain, []byte(" ND\nend\n")...)

	var font []byte
	font = append(font, []byte("%!PS-AdobeFont-1.0: Test\n/Encoding StandardEncoding def\ncurrentfile eexec\n")...)
	font = append(font, encryptType1(binPlain, 55665)...)
	return font
}

// TestValidateType1MetricsHonoursDifferences pins the 6.3.6 width comparison
// against a Type1 font whose /Encoding is a dictionary carrying /Differences.
// The program's built-in encoding is StandardEncoding, where code 65 is /A
// (width 500); /Differences remaps 65 to /B (width 700) and /Widths agrees
// with the remapping, so the font is conformant.
//
// The call site must read /Encoding as a dictionary. Reading it as absent
// would fall back to the built-in encoding, compare the width against /A, and
// report AdvanceWidthMismatch on this correct file.
func TestValidateType1MetricsHonoursDifferences(t *testing.T) {
	font := buildType1FontTwoGlyphs()
	if w := fontprog.Type1GlyphWidths(font); w["A"] != 500 || w["B"] != 700 {
		t.Fatalf("fixture widths = %v, want A=500 B=700", w)
	}

	encoding := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type":        pdf.PDFName{Value: "Encoding"},
		"Differences": pdf.PDFArray{pdf.PDFInteger(65), pdf.PDFName{Value: "B"}},
	})}
	ff := pdf.NewPDFDict()
	ff.SetStream(font)

	ctx := &ValidationContext{Spec: a1bSpec}
	validateType1Metrics(pdf.PDFDict{}, pdf.PDFDict{}, ff, 65, pdf.PDFArray{pdf.PDFInteger(700)}, encoding, ctx)
	if hasCheck(ctx, pdf.Checks.Font.AdvanceWidthMismatch) {
		t.Error("AdvanceWidthMismatch reported for a font whose /Widths match its /Differences remapping")
	}

	// The remapping must still catch a genuine mismatch: 500 is /A's width,
	// which is exactly the wrong answer the old code accepted.
	ctx2 := &ValidationContext{Spec: a1bSpec}
	validateType1Metrics(pdf.PDFDict{}, pdf.PDFDict{}, ff, 65, pdf.PDFArray{pdf.PDFInteger(500)}, encoding, ctx2)
	if !hasCheck(ctx2, pdf.Checks.Font.AdvanceWidthMismatch) {
		t.Error("expected AdvanceWidthMismatch when /Widths ignores the /Differences remapping")
	}
}

// type1CoverageFixture returns a descriptor embedding buildType1Font's program
// (one glyph, /A, 500 wide) with the given CharSet, and a font dictionary with
// the given /Encoding.
func type1CoverageFixture(charSet pdf.PDFValue, encoding pdf.PDFValue) (v, desc pdf.PDFDict) {
	desc = pdf.NewPDFDict()
	desc.Entries.Set("FontFile", streamDict(string(buildType1Font())))
	desc.Entries.Set("CharSet", charSet)
	v = pdf.NewPDFDict()
	if encoding != nil {
		v.Entries.Set("Encoding", encoding)
	}
	return v, desc
}

// TestType1SubsetCharSetHexString covers 6.3.5: CharSet is a string, and a
// hexadecimal string is as valid as a literal one. A CharSet written in hex
// that omits a glyph the program defines must be reported just like the
// literal form.
func TestType1SubsetCharSetHexString(t *testing.T) {
	winAnsi := pdf.PDFName{Value: "WinAnsiEncoding"}
	widths := pdf.PDFArray{pdf.PDFInteger(500)} // code 65, /A

	// "/Zzz" in hex: lists glyphs, but not /A.
	v, desc := type1CoverageFixture(pdf.PDFHexString{Value: "2F5A7A7A"}, winAnsi)
	ctx := &ValidationContext{Spec: a1bSpec}
	ValidateType1SubsetCoverage(v, v, desc, 65, 65, widths, ctx)
	if !hasCheck(ctx, pdf.Checks.Font.Type1SubsetCharSet) {
		t.Error("a hex-string CharSet omitting /A was not reported")
	}

	// "/A" in hex lists the glyph: consistent.
	v, desc = type1CoverageFixture(pdf.PDFHexString{Value: "2F41"}, winAnsi)
	ctx = &ValidationContext{Spec: a1bSpec}
	ValidateType1SubsetCoverage(v, v, desc, 65, 65, widths, ctx)
	if len(ctx.errs) != 0 {
		t.Errorf("a consistent hex-string CharSet was reported: %v", issueKeys(ctx.Issues()))
	}
}

// TestType1SubsetCoverageSkipsWhatItCannotJudge covers the inputs the 6.3.5
// Type 1 check leaves alone: a CharSet that is not a string, and an /Encoding
// that does not resolve to glyph names.
func TestType1SubsetCoverageSkipsWhatItCannotJudge(t *testing.T) {
	widthsMissingGlyph := pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(500)} // code 66 /B: not in the program
	for _, tc := range []struct {
		name     string
		charSet  pdf.PDFValue
		encoding pdf.PDFValue
	}{
		{"CharSet is a name", pdf.PDFName{Value: "A"}, pdf.PDFName{Value: "WinAnsiEncoding"}},
		{"Encoding is not modelled", pdf.PDFString{Value: "/A"}, pdf.PDFName{Value: "MacExpertEncoding"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, desc := type1CoverageFixture(tc.charSet, tc.encoding)
			ctx := &ValidationContext{Spec: a1bSpec}
			ValidateType1SubsetCoverage(v, v, desc, 65, 66, widthsMissingGlyph, ctx)
			if len(ctx.errs) != 0 {
				t.Errorf("reported %v", issueKeys(ctx.Issues()))
			}
		})
	}
}

// TestType1SubsetCoverageCodeFilters covers which character codes the Widths
// fallback examines: codes outside 0..255 and codes with no glyph name are
// passed over, real-valued widths count, and the scan goes on to later codes.
func TestType1SubsetCoverageCodeFilters(t *testing.T) {
	enc := pdf.NewPDFDict()
	enc.Entries.Set("BaseEncoding", pdf.PDFName{Value: "WinAnsiEncoding"})
	enc.Entries.Set("Differences", pdf.PDFArray{pdf.PDFInteger(70), pdf.PDFName{Value: ".notdef"}})

	run := func(first int, widths pdf.PDFArray) *ValidationContext {
		v, desc := type1CoverageFixture(pdf.PDFString{Value: "/A"}, enc)
		ctx := &ValidationContext{Spec: a1bSpec}
		ValidateType1SubsetCoverage(v, v, desc, first, first+len(widths)-1, widths, ctx)
		return ctx
	}
	nonZero := pdf.PDFInteger(500)

	// Code 256 lies outside the byte range and is passed over.
	if ctx := run(255, pdf.PDFArray{pdf.PDFInteger(0), nonZero}); len(ctx.errs) != 0 {
		t.Errorf("code 256 reported: %v", issueKeys(ctx.Issues()))
	}
	// Code 1 has no glyph name in WinAnsiEncoding; code 70 is explicitly .notdef.
	if ctx := run(1, pdf.PDFArray{nonZero}); len(ctx.errs) != 0 {
		t.Errorf("code without a glyph name reported: %v", issueKeys(ctx.Issues()))
	}
	if ctx := run(70, pdf.PDFArray{nonZero}); len(ctx.errs) != 0 {
		t.Errorf("code named .notdef reported: %v", issueKeys(ctx.Issues()))
	}
	// /A is fine, code 70 is .notdef, then /G (71) is absent from the program:
	// the scan reaches it, and does so for a real-valued width as well.
	ctx := run(65, pdf.PDFArray{nonZero, pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFInteger(0), nonZero, pdf.PDFReal(500.5)})
	if got := extMessages(ctx.Issues(), pdf.Checks.Font.SubsetGlyphCoverage); !strings.Contains(got, "code 71") {
		t.Errorf("missing glyph at code 71 not reported: %v", issueKeys(ctx.Issues()))
	}
	ctx = run(65, pdf.PDFArray{nonZero, pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFReal(500.5)})
	if got := extMessages(ctx.Issues(), pdf.Checks.Font.SubsetGlyphCoverage); !strings.Contains(got, "code 71") {
		t.Errorf("real-valued width at code 71 not examined: %v", issueKeys(ctx.Issues()))
	}
}

// TestType1CMetricsSkipsCodesItCannotCompare covers the Widths entries the
// Type1C width check passes over, one per reason, each followed by a genuine
// mismatch on /A that must still be found.
func TestType1CMetricsSkipsCodesItCannotCompare(t *testing.T) {
	cff := buildMinimalCFF()
	ff := streamDict(string(cff))
	aWidth := int(fontprog.CFFAdvanceWidths(cff)["A"])

	// Codes -1..65: -1 is out of range, 0..64 are either unnamed in
	// WinAnsiEncoding or named glyphs the program lacks; only code 65 can be
	// compared, and it is wrong.
	widths := make(pdf.PDFArray, 67)
	for i := range widths {
		widths[i] = pdf.PDFInteger(aWidth + 300)
	}
	v := pdf.NewPDFDict()
	v.Entries.Set("Encoding", pdf.PDFName{Value: "WinAnsiEncoding"})
	ctx := &ValidationContext{Spec: a1bSpec}
	validateType1CMetrics(v, v, ff, -1, widths, ctx)
	got := extMessages(ctx.Issues(), pdf.Checks.Font.AdvanceWidthMismatch)
	if !strings.Contains(got, "character code 65 (/A)") {
		t.Errorf("messages %q: want the mismatch reported at code 65", got)
	}

	// Past the byte range nothing can be compared.
	ctx = &ValidationContext{Spec: a1bSpec}
	validateType1CMetrics(v, v, ff, 255, pdf.PDFArray{pdf.PDFInteger(1), pdf.PDFInteger(1)}, ctx)
	if len(ctx.errs) != 0 {
		t.Errorf("code 256 reported: %v", issueKeys(ctx.Issues()))
	}

	// An encoding that names no glyphs leaves nothing to compare against.
	bare := pdf.NewPDFDict()
	ctx = &ValidationContext{Spec: a1bSpec}
	validateType1CMetrics(bare, bare, ff, 65, pdf.PDFArray{pdf.PDFInteger(aWidth + 300)}, ctx)
	if len(ctx.errs) != 0 {
		t.Errorf("font without Encoding reported: %v", issueKeys(ctx.Issues()))
	}
}

// TestCIDCFFMetricsSkipsUnshownCIDs checks that, once the CIDs a document shows
// are known, a /W width for a CID it never shows is not compared.
func TestCIDCFFMetricsSkipsUnshownCIDs(t *testing.T) {
	ff := streamDict(string(buildMinimalCIDCFF())) // CID 1 is 600 wide, CID 2 is 700
	font := pdf.NewPDFDict()
	w := pdf.PDFArray{pdf.PDFInteger(1), pdf.PDFArray{pdf.PDFInteger(650), pdf.PDFInteger(700)}} // CID 1 is wrong

	shown := func(cids map[int]bool) *ValidationContext {
		return &ValidationContext{Spec: a1bSpec, UsedCIDs: map[uintptr]map[int]bool{pdf.ValuePointer(font.Entries): cids}}
	}
	ctx := shown(map[int]bool{2: true})
	validateCIDCFFMetrics(font, font, ff, w, ctx)
	if len(ctx.errs) != 0 {
		t.Errorf("width of an unshown CID compared: %v", issueKeys(ctx.Issues()))
	}
	ctx = shown(map[int]bool{1: true})
	validateCIDCFFMetrics(font, font, ff, w, ctx)
	if !hasCheck(ctx, pdf.Checks.Font.AdvanceWidthMismatch) {
		t.Error("width of a shown CID not compared")
	}
}

// TestEmbeddedType1GlyphNamesUndecodable checks that a Type 1 program whose
// stream will not decode yields no glyph names, and does not fall through to
// the FontFile3 lookup.
func TestEmbeddedType1GlyphNamesUndecodable(t *testing.T) {
	desc := pdf.NewPDFDict()
	desc.Entries.Set("FontFile", brokenStreamDict())
	desc.Entries.Set("FontFile3", streamDict(string(buildMinimalCFF())))
	ctx := &ValidationContext{Spec: a1bSpec}
	if names := embeddedType1GlyphNames(desc, ctx); names != nil {
		t.Errorf("names = %v, want nil", names)
	}

	// A FontFile3 that will not decode likewise yields none.
	desc = pdf.NewPDFDict()
	desc.Entries.Set("FontFile3", brokenStreamDict())
	if names := embeddedType1GlyphNames(desc, ctx); names != nil {
		t.Errorf("names = %v, want nil", names)
	}
}

// corruptCFFCharset returns a copy of a CFF program with the format byte of
// its charset set to a value no format uses, so the charset cannot be parsed.
func corruptCFFCharset(t *testing.T, cff []byte) []byte {
	t.Helper()
	td, ok := fontprog.ParseCFFTopDict(cff)
	if !ok || td.CharsetOffset <= 2 {
		t.Fatalf("fixture has no explicit charset: %+v", td)
	}
	out := append([]byte(nil), cff...)
	out[td.CharsetOffset] = 9
	return out
}

// TestCIDCFFSubsetUsageAndKinds covers the 6.3.5 CFF coverage check for the
// two kinds of CFF program a CIDFontType0 may embed.
func TestCIDCFFSubsetUsageAndKinds(t *testing.T) {
	font := pdf.NewPDFDict()
	withUsage := func(cids ...int) *ValidationContext {
		used := map[int]bool{}
		for _, c := range cids {
			used[c] = true
		}
		return &ValidationContext{Spec: a1bSpec, UsedCIDs: map[uintptr]map[int]bool{pdf.ValuePointer(font.Entries): used}}
	}
	widthFor := func(cid int) pdf.PDFArray {
		return pdf.PDFArray{pdf.PDFInteger(cid), pdf.PDFArray{pdf.PDFInteger(600)}}
	}

	t.Run("CID-keyed program", func(t *testing.T) {
		ff := streamDict(string(buildMinimalCIDCFF())) // CIDs 0, 1 and 2 have glyphs
		// CID 5 has no glyph, but a document that never shows it is unaffected.
		ctx := withUsage(1)
		ValidateCIDCFFSubset(font, ff, widthFor(5), ctx)
		if len(ctx.errs) != 0 {
			t.Errorf("unshown CID 5 reported: %v", issueKeys(ctx.Issues()))
		}
		ctx = withUsage(5)
		ValidateCIDCFFSubset(font, ff, widthFor(5), ctx)
		if !hasCheck(ctx, pdf.Checks.Font.SubsetGlyphCoverage) {
			t.Error("shown CID 5 not reported")
		}
	})

	t.Run("CID-keyed program with an unreadable charset", func(t *testing.T) {
		ff := streamDict(string(corruptCFFCharset(t, buildMinimalCIDCFF())))
		ctx := &ValidationContext{Spec: a1bSpec}
		ValidateCIDCFFSubset(font, ff, widthFor(5), ctx)
		if len(ctx.errs) != 0 {
			t.Errorf("reported %v; an unparseable charset is not a coverage verdict", issueKeys(ctx.Issues()))
		}
	})

	t.Run("name-keyed program", func(t *testing.T) {
		// Without CID keying a CID is a glyph index; the program has 2 CharStrings.
		ff := streamDict(string(buildMinimalCFF()))
		ctx := &ValidationContext{Spec: a1bSpec}
		ValidateCIDCFFSubset(font, ff, widthFor(1), ctx)
		if len(ctx.errs) != 0 {
			t.Errorf("CID 1 reported: %v", issueKeys(ctx.Issues()))
		}
		ctx = &ValidationContext{Spec: a1bSpec}
		ValidateCIDCFFSubset(font, ff, widthFor(2), ctx)
		if got := extMessages(ctx.Issues(), pdf.Checks.Font.SubsetGlyphCoverage); !strings.Contains(got, "CID 2") || !strings.Contains(got, "count=2") {
			t.Errorf("messages %q: want CID 2 reported against a CharStrings count of 2", got)
		}
		ctx = withUsage(1)
		ValidateCIDCFFSubset(font, ff, widthFor(2), ctx)
		if len(ctx.errs) != 0 {
			t.Errorf("unshown CID 2 reported: %v", issueKeys(ctx.Issues()))
		}
	})
}

// TestCIDSetBitmapGuards checks the inputs on which the CIDSet bitmap check
// stays silent, each against a control that proves the same fixture does
// report once the input is repaired.
func TestCIDSetBitmapGuards(t *testing.T) {
	cidKeyed := buildMinimalCIDCFF()
	emptyBitmap := func() pdf.PDFDict {
		desc := pdf.NewPDFDict()
		desc.Entries.Set("CIDSet", streamDict("\x00"))
		return desc
	}
	run := func(desc, ff pdf.PDFDict) *ValidationContext {
		ctx := &ValidationContext{Spec: a1bSpec}
		validateCIDSetBitmap(pdf.NewPDFDict(), desc, ff, ctx)
		return ctx
	}

	if !hasCheck(run(emptyBitmap(), streamDict(string(cidKeyed))), pdf.Checks.Font.CIDSubsetCIDSet) {
		t.Fatal("control: an empty CIDSet over a CID-keyed program was not reported")
	}
	for _, tc := range []struct {
		name     string
		desc, ff pdf.PDFDict
	}{
		{"CIDSet that will not decode", func() pdf.PDFDict {
			d := pdf.NewPDFDict()
			d.Entries.Set("CIDSet", brokenStreamDict())
			return d
		}(), streamDict(string(cidKeyed))},
		{"program that will not decode", emptyBitmap(), brokenStreamDict()},
		{"program without CID keying", emptyBitmap(), streamDict(string(buildMinimalCFF()))},
		{"program with an unreadable charset", emptyBitmap(), streamDict(string(corruptCFFCharset(t, cidKeyed)))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if ctx := run(tc.desc, tc.ff); hasCheck(ctx, pdf.Checks.Font.CIDSubsetCIDSet) {
				t.Errorf("reported %v", issueKeys(ctx.Issues()))
			}
		})
	}
}

// renameSfntTable returns a copy of an sfnt program with the table tagged old
// renamed to new in the table directory, which makes the table invisible to
// the lookups without disturbing the rest of the file.
func renameSfntTable(t *testing.T, sfnt []byte, old, new string) []byte {
	t.Helper()
	n := int(binary.BigEndian.Uint16(sfnt[4:6]))
	out := append([]byte(nil), sfnt...)
	for i := range n {
		rec := out[12+16*i:]
		if string(rec[:4]) == old {
			copy(rec[:4], new)
			return out
		}
	}
	t.Fatalf("sfnt has no %q table", old)
	return nil
}

// TestCIDSetTrueTypeGuards checks the inputs on which the TrueType CIDSet
// check stays silent, against a control that does report.
func TestCIDSetTrueTypeGuards(t *testing.T) {
	ttf := loadTTF(t)
	tables, _ := fontprog.ParseSfnt(ttf)
	rendered := 0
	hasGlyph := fontprog.TTLocaHasGlyph(tables)
	for cid := 1; cid < fontprog.TTNumGlyphs(tables); cid++ {
		if hasGlyph(cid) {
			rendered = cid
			break
		}
	}
	font := pdf.NewPDFDict()
	usage := map[uintptr]map[int]bool{pdf.ValuePointer(font.Entries): {rendered: true}}
	desc := func(bitmap pdf.PDFDict) pdf.PDFDict {
		d := pdf.NewPDFDict()
		d.Entries.Set("CIDSet", bitmap)
		return d
	}
	run := func(d, ff pdf.PDFDict) *ValidationContext {
		ctx := &ValidationContext{Spec: a1bSpec, UsedCIDs: usage}
		validateCIDSetTrueType(font, d, ff, ctx)
		return ctx
	}

	good := streamDict(string(ttf))
	if !hasCheck(run(desc(streamDict("\x00")), good), pdf.Checks.Font.CIDSubsetCIDSet) {
		t.Fatal("control: an empty CIDSet for a rendered glyph was not reported")
	}
	if ctx := run(desc(brokenStreamDict()), good); len(ctx.errs) != 1 || !hasCheck(ctx, pdf.Checks.Structure.StreamUndecodable) {
		t.Errorf("undecodable CIDSet: %v, want only StreamUndecodable", issueKeys(ctx.Issues()))
	}
	if ctx := run(desc(streamDict("\x00")), brokenStreamDict()); len(ctx.errs) != 1 || !hasCheck(ctx, pdf.Checks.Structure.StreamUndecodable) {
		t.Errorf("undecodable program: %v, want only StreamUndecodable", issueKeys(ctx.Issues()))
	}
}

// TestCIDDefaultWidthReal covers a real-valued /DW.
func TestCIDDefaultWidthReal(t *testing.T) {
	d := pdf.NewPDFDict()
	d.Entries.Set("DW", pdf.PDFReal(750.9))
	if got := cidDefaultWidth(d); got != 750 {
		t.Errorf("cidDefaultWidth = %d, want 750", got)
	}
}

// TestTrueTypeCmapSubtablesUnavailable covers every way the cmap subtable count
// cannot be determined.
func TestTrueTypeCmapSubtablesUnavailable(t *testing.T) {
	ttf := loadTTF(t)
	for _, tc := range []struct {
		name string
		ff   pdf.PDFDict
	}{
		{"stream will not decode", brokenStreamDict()},
		{"not a font", streamDict("not a font")},
		{"no cmap table", streamDict(string(renameSfntTable(t, ttf, "cmap", "cmax")))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			desc := pdf.NewPDFDict()
			desc.Entries.Set("FontFile2", tc.ff)
			if cm, ok := trueTypeCmaps(&ValidationContext{Spec: a1bSpec}, desc); ok {
				t.Errorf("trueTypeCmaps = %d subtables, true; want unavailable", cm.count)
			}
		})
	}
}

// TestSimpleTrueTypeSubsetWidthsFallback covers the Widths-driven scan used when
// no usage information exists: only codes with a positive (integer or real)
// width are examined, the scan stops at the first code without a glyph, and a
// font with no positive width has nothing to examine. Liberation Sans has no
// glyph for the control codes 1..7 (nor for 127) under WinAnsiEncoding.
func TestSimpleTrueTypeSubsetWidthsFallback(t *testing.T) {
	ff := streamDict(string(loadTTF(t)))
	v := pdf.NewPDFDict()
	v.Entries.Set("Encoding", pdf.PDFName{Value: "WinAnsiEncoding"})

	run := func(first int, widths pdf.PDFArray) *ValidationContext {
		ctx := &ValidationContext{Spec: a1bSpec}
		ValidateSimpleTrueTypeSubset(v, ff, first, first+len(widths)-1, widths, ctx)
		return ctx
	}
	reportedAt := func(ctx *ValidationContext) string {
		return extMessages(ctx.Issues(), pdf.Checks.Font.SubsetGlyphCoverage)
	}

	// Two absent codes: only the first is reported.
	ctx := run(1, pdf.PDFArray{pdf.PDFInteger(500), pdf.PDFInteger(500)})
	if n := len(ctx.errs); n != 1 || !strings.Contains(reportedAt(ctx), "code 1 ") {
		t.Errorf("integer widths: %v, want exactly one finding at code 1", issueKeys(ctx.Issues()))
	}
	// A real width counts as used too.
	ctx = run(1, pdf.PDFArray{pdf.PDFReal(500.5)})
	if !strings.Contains(reportedAt(ctx), "code 1 ") {
		t.Errorf("real-valued width at code 1 not examined: %v", issueKeys(ctx.Issues()))
	}
	// A zero or negative width means the code is not used.
	ctx = run(1, pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFReal(0), pdf.PDFInteger(-5)})
	if len(ctx.errs) != 0 {
		t.Errorf("unused codes reported: %v", issueKeys(ctx.Issues()))
	}
	// Unused codes before a used absent one are skipped, and the scan goes on.
	ctx = run(0, pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFInteger(500)})
	if !strings.Contains(reportedAt(ctx), "code 2 ") {
		t.Errorf("used code 2 not reached: %v", issueKeys(ctx.Issues()))
	}
}

// TestSimpleTrueTypeMetricsSkipsWhatItCannotCompare covers the Widths entries
// the hmtx comparison passes over -- non-numeric, zero, unmapped by any cmap,
// or in a font with no usable horizontal metrics -- each followed by a genuine
// mismatch on 'A' that must still be found.
func TestSimpleTrueTypeMetricsSkipsWhatItCannotCompare(t *testing.T) {
	ttf := loadTTF(t)
	tables, _ := fontprog.ParseSfnt(ttf)
	gidA := fontprog.ParseCmapFormat4(fontprog.TTWindowsBMPCmap(tables))['A']
	wrongA := pdf.PDFInteger(int(fontprog.TTAdvanceWidth(tables, int(gidA))) + 500)

	v := pdf.NewPDFDict()
	v.Entries.Set("Encoding", pdf.PDFName{Value: "WinAnsiEncoding"})
	check := func(ff pdf.PDFDict, first int, widths pdf.PDFArray) *ValidationContext {
		ctx := &ValidationContext{Spec: a1bSpec}
		validateSimpleTrueTypeMetrics(v, ff, first, widths, ctx)
		return ctx
	}
	good := streamDict(string(ttf))

	// Code 1 has no glyph in any cmap, code 2 holds a name instead of a
	// number, codes 3..64 have zero width; only 'A' (65) can be compared.
	widths := make(pdf.PDFArray, 65)
	for i := range widths {
		widths[i] = pdf.PDFInteger(0)
	}
	widths[0] = pdf.PDFReal(5000.5)
	widths[1] = pdf.PDFName{Value: "x"}
	widths[64] = wrongA
	ctx := check(good, 1, widths)
	if got := extMessages(ctx.Issues(), pdf.Checks.Font.AdvanceWidthMismatch); !strings.Contains(got, "character code 65") {
		t.Errorf("messages %q: want the mismatch at code 65 after the skipped entries", got)
	}
	widths[64] = pdf.PDFInteger(0)
	if ctx := check(good, 1, widths); len(ctx.errs) != 0 {
		t.Errorf("uncomparable entries reported: %v", issueKeys(ctx.Issues()))
	}

	// A character the font's (3,1) cmap does not cover (U+4E2D, named by
	// Differences) cannot be located in hmtx, whatever width the PDF claims.
	cjk := pdf.NewPDFDict()
	cjk.Entries.Set("BaseEncoding", pdf.PDFName{Value: "WinAnsiEncoding"})
	cjk.Entries.Set("Differences", pdf.PDFArray{pdf.PDFInteger(65), pdf.PDFName{Value: "uni4E2D"}})
	vCJK := pdf.NewPDFDict()
	vCJK.Entries.Set("Encoding", cjk)
	ctx = &ValidationContext{Spec: a1bSpec}
	validateSimpleTrueTypeMetrics(vCJK, good, 65, pdf.PDFArray{wrongA}, ctx)
	if len(ctx.errs) != 0 {
		t.Errorf("character absent from the cmap reported: %v", issueKeys(ctx.Issues()))
	}

	// A font whose horizontal metrics are unusable has no advance to compare.
	noMetrics := streamDict(string(renameSfntTable(t, ttf, "hmtx", "hmtz")))
	ctx = check(noMetrics, 65, pdf.PDFArray{wrongA})
	if len(ctx.errs) != 0 {
		t.Errorf("font without hmtx reported: %v", issueKeys(ctx.Issues()))
	}
}

// TestType1MetricsSkipsWhatItCannotCompare covers the Widths entries the Type 1
// width comparison passes over -- a code out of range, one without a glyph name,
// a non-numeric or zero width -- each ahead of a real-valued mismatch on /A
// (code 65) that must still be found.
func TestType1MetricsSkipsWhatItCannotCompare(t *testing.T) {
	ff := streamDict(string(buildType1Font())) // /A is 500 wide
	v := pdf.NewPDFDict()
	enc := pdf.PDFName{Value: "StandardEncoding"}
	v.Entries.Set("Encoding", enc)
	run := func(first int, widths pdf.PDFArray) *ValidationContext {
		ctx := &ValidationContext{Spec: a1bSpec}
		validateType1Metrics(v, pdf.NewPDFDict(), ff, first, widths, enc, ctx)
		return ctx
	}

	// Codes -1..65. Code -1 is out of range; code 0 names no glyph in
	// StandardEncoding; code 1 holds a name rather than a number; codes
	// 2..64 are zero.
	widths := make(pdf.PDFArray, 67)
	for i := range widths {
		widths[i] = pdf.PDFInteger(0)
	}
	widths[0] = pdf.PDFInteger(900)
	widths[1] = pdf.PDFInteger(900)
	widths[2] = pdf.PDFName{Value: "x"}
	widths[66] = pdf.PDFReal(999.5)
	ctx := run(-1, widths)
	if got := extMessages(ctx.Issues(), pdf.Checks.Font.AdvanceWidthMismatch); !strings.Contains(got, "character code 65 (/A): PDF width 999") {
		t.Errorf("messages %q: want the real-valued mismatch at code 65", got)
	}
	widths[66] = pdf.PDFReal(500.5)
	if ctx := run(-1, widths); len(ctx.errs) != 0 {
		t.Errorf("uncomparable entries reported: %v", issueKeys(ctx.Issues()))
	}

	// Beyond the byte range there is nothing to compare.
	if ctx := run(255, pdf.PDFArray{pdf.PDFInteger(900), pdf.PDFInteger(900)}); len(ctx.errs) != 0 {
		t.Errorf("code 256 reported: %v", issueKeys(ctx.Issues()))
	}
}

// TestType1MetricsShownCodesOutsideWidths covers shown codes that fall outside
// /Widths and so take /MissingWidth: a code without a glyph name, one whose
// glyph the program lacks, and one within the 1-unit tolerance are all fine;
// one that really differs is reported.
func TestType1MetricsShownCodesOutsideWidths(t *testing.T) {
	ff := streamDict(string(buildType1Font())) // /A is 500 wide
	v := pdf.NewPDFDict()
	enc := pdf.PDFName{Value: "StandardEncoding"}
	v.Entries.Set("Encoding", enc)

	run := func(missingWidth int, shown ...int) *ValidationContext {
		desc := pdf.NewPDFDict()
		desc.Entries.Set("MissingWidth", pdf.PDFInteger(missingWidth))
		used := map[int]bool{}
		for _, cc := range shown {
			used[cc] = true
		}
		ctx := &ValidationContext{Spec: a1bSpec, UsedCharCodes: map[uintptr]map[int]bool{pdf.ValuePointer(v.Entries): used}}
		// Widths cover only code 70, so every shown code is outside them.
		validateType1Metrics(v, desc, ff, 70, pdf.PDFArray{pdf.PDFInteger(0)}, enc, ctx)
		return ctx
	}
	// Code 1 names no glyph; code 66 (/B) is not in the program; both precede /A.
	ctx := run(999, 1, 66, 65)
	if got := extMessages(ctx.Issues(), pdf.Checks.Font.AdvanceWidthMismatch); !strings.Contains(got, "character code 65 (/A): PDF width 999") {
		t.Errorf("messages %q: want the MissingWidth mismatch at code 65", got)
	}
	if ctx := run(500, 1, 66, 65); len(ctx.errs) != 0 {
		t.Errorf("a MissingWidth equal to the program's width reported: %v", issueKeys(ctx.Issues()))
	}
	if ctx := run(501, 65); len(ctx.errs) != 0 {
		t.Errorf("a width within the 1-unit tolerance reported: %v", issueKeys(ctx.Issues()))
	}
}

// TestCMapWModeSilentCases covers the embedded CMaps for which no WMode
// comparison is possible, against a control that does report.
func TestCMapWModeSilentCases(t *testing.T) {
	mk := func(wmode pdf.PDFValue, stream pdf.PDFDict) pdf.PDFDict {
		if wmode != nil {
			stream.Entries.Set("WMode", wmode)
		}
		return stream
	}
	run := func(cmap pdf.PDFDict) *ValidationContext {
		ctx := &ValidationContext{Spec: a1bSpec}
		validateCMapWMode(pdf.NewPDFDict(), cmap, ctx)
		return ctx
	}
	if !hasCheck(run(mk(pdf.PDFInteger(1), streamDict("/WMode 0 def"))), pdf.Checks.Font.CMapWModeInconsistent) {
		t.Fatal("control: disagreeing WModes were not reported")
	}
	broken := brokenStreamDict()
	for _, tc := range []struct {
		name string
		cmap pdf.PDFDict
	}{
		{"dictionary WMode is not an integer", mk(pdf.PDFName{Value: "1"}, streamDict("/WMode 0 def"))},
		{"stream will not decode", mk(pdf.PDFInteger(1), broken)},
		{"stream declares no WMode", mk(pdf.PDFInteger(1), streamDict("/CMapName /X def"))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if ctx := run(tc.cmap); hasCheck(ctx, pdf.Checks.Font.CMapWModeInconsistent) {
				t.Errorf("reported %v", issueKeys(ctx.Issues()))
			}
		})
	}
}

// TestType1SubsetCoverageUsesBuiltinEncoding covers ISO 32000-1 9.6.6.1: an
// embedded Type 1 font without /Encoding, or whose /Encoding dictionary has no
// /BaseEncoding, uses the program's built-in encoding. buildType1Font's is
// StandardEncoding, where code 66 is /B, which the program does not define.
func TestType1SubsetCoverageUsesBuiltinEncoding(t *testing.T) {
	widths := pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(500)} // code 66 /B

	v, desc := type1CoverageFixture(pdf.PDFString{Value: "/A"}, nil)
	ctx := &ValidationContext{Spec: a1bSpec}
	ValidateType1SubsetCoverage(v, v, desc, 65, 66, widths, ctx)
	if !hasCheck(ctx, pdf.Checks.Font.SubsetGlyphCoverage) {
		t.Error("font without /Encoding: code 66 /B not reported")
	}

	enc := pdf.NewPDFDict()
	enc.Entries.Set("Differences", pdf.PDFArray{pdf.PDFInteger(70), pdf.PDFName{Value: "A"}})
	v, desc = type1CoverageFixture(pdf.PDFString{Value: "/A"}, enc)
	ctx = &ValidationContext{Spec: a1bSpec}
	ValidateType1SubsetCoverage(v, v, desc, 65, 66, widths, ctx)
	if !hasCheck(ctx, pdf.Checks.Font.SubsetGlyphCoverage) {
		t.Error("/Encoding without /BaseEncoding: code 66 /B not reported")
	}

	// Positive control: code 65 is /A in the built-in encoding.
	v, desc = type1CoverageFixture(pdf.PDFString{Value: "/A"}, nil)
	ctx = &ValidationContext{Spec: a1bSpec}
	ValidateType1SubsetCoverage(v, v, desc, 65, 65, pdf.PDFArray{pdf.PDFInteger(500)}, ctx)
	if len(ctx.errs) != 0 {
		t.Errorf("code 65 /A reported: %v", issueKeys(ctx.Issues()))
	}
}

// TestType1SubsetCoverageMacRomanEncoding verifies that /MacRomanEncoding
// resolves to its Annex D glyph names instead of being skipped. Mac Roman 0x80
// is /Adieresis.
func TestType1SubsetCoverageMacRomanEncoding(t *testing.T) {
	v, desc := type1CoverageFixture(pdf.PDFString{Value: "/A"}, pdf.PDFName{Value: "MacRomanEncoding"})
	ctx := &ValidationContext{Spec: a1bSpec}
	ValidateType1SubsetCoverage(v, v, desc, 0x80, 0x80, pdf.PDFArray{pdf.PDFInteger(500)}, ctx)
	if !hasCheck(ctx, pdf.Checks.Font.SubsetGlyphCoverage) {
		t.Error("MacRomanEncoding code 0x80 /Adieresis not reported")
	}
}

// type1CFontDict returns a Type1 font dictionary embedding buildMinimalCFF as
// FontFile3, with Widths covering codes 65..65+len(widths)-1.
func type1CFontDict(baseFont string, widths ...int) pdf.PDFDict {
	desc := pdf.NewPDFDict()
	desc.Entries.Set("FontFile3", streamDict(string(buildMinimalCFF())))
	desc.Entries.Set("CharSet", pdf.PDFString{Value: "/A"})

	w := make(pdf.PDFArray, len(widths))
	for i, x := range widths {
		w[i] = pdf.PDFInteger(x)
	}
	v := pdf.NewPDFDict()
	v.Entries.Set("Type", pdf.PDFName{Value: "Font"})
	v.Entries.Set("Subtype", pdf.PDFName{Value: "Type1"})
	v.Entries.Set("BaseFont", pdf.PDFName{Value: baseFont})
	v.Entries.Set("Encoding", pdf.PDFName{Value: "WinAnsiEncoding"})
	v.Entries.Set("FirstChar", pdf.PDFInteger(65))
	v.Entries.Set("LastChar", pdf.PDFInteger(65+len(widths)-1))
	v.Entries.Set("Widths", w)
	v.Entries.Set("FontDescriptor", desc)
	return v
}

// TestValidateFontDictType1WithType1CProgram pins that a Type1 font embedding
// its program as FontFile3 is held to the CFF advance widths (6.3.6).
func TestValidateFontDictType1WithType1CProgram(t *testing.T) {
	aWidth := int(fontprog.CFFAdvanceWidths(buildMinimalCFF())["A"])

	ctx := &ValidationContext{Spec: a1bSpec}
	ValidateFontDict(type1CFontDict("Test", aWidth+50), ctx)
	if !hasCheck(ctx, pdf.Checks.Font.AdvanceWidthMismatch) {
		t.Errorf("a wrong width against a FontFile3 program was not reported: %v", issueKeys(ctx.Issues()))
	}

	ctx = &ValidationContext{Spec: a1bSpec}
	ValidateFontDict(type1CFontDict("Test", aWidth), ctx)
	if hasCheck(ctx, pdf.Checks.Font.AdvanceWidthMismatch) {
		t.Errorf("a matching width was reported: %v", issueKeys(ctx.Issues()))
	}
}

// TestValidateFontDictType1SubsetWithType1CProgram pins that the subset glyph
// coverage check (6.3.5) works for a FontFile3 program, where the glyph names
// come from the font dictionary's own encoding rather than a Type 1 program.
func TestValidateFontDictType1SubsetWithType1CProgram(t *testing.T) {
	aWidth := int(fontprog.CFFAdvanceWidths(buildMinimalCFF())["A"])

	ctx := &ValidationContext{Spec: a1bSpec}
	ValidateFontDict(type1CFontDict("ABCDEF+Test", aWidth, 500), ctx) // code 66 /B is not in the program
	if !hasCheck(ctx, pdf.Checks.Font.SubsetGlyphCoverage) {
		t.Errorf("a used glyph missing from the FontFile3 subset was not reported: %v", issueKeys(ctx.Issues()))
	}

	ctx = &ValidationContext{Spec: a1bSpec}
	ValidateFontDict(type1CFontDict("ABCDEF+Test", aWidth), ctx)
	if hasCheck(ctx, pdf.Checks.Font.SubsetGlyphCoverage) || hasCheck(ctx, pdf.Checks.Font.Type1SubsetCharSet) {
		t.Errorf("a complete subset was reported: %v", issueKeys(ctx.Issues()))
	}
}

// TestType1SubsetCoverageUsedCodesStopAtFirstViolation pins that, when usage
// is known, only shown codes are examined, in code order, and the first
// offending one ends the scan.
func TestType1SubsetCoverageUsedCodesStopAtFirstViolation(t *testing.T) {
	v, desc := type1CoverageFixture(pdf.PDFString{Value: "/A"}, pdf.PDFName{Value: "WinAnsiEncoding"})
	widths := pdf.PDFArray{pdf.PDFInteger(500), pdf.PDFInteger(500), pdf.PDFInteger(500)} // /A /B /C

	ctx := &ValidationContext{Spec: a1bSpec, UsedCharCodes: map[uintptr]map[int]bool{pdf.ValuePointer(v.Entries): {67: true, 66: true}}}
	ValidateType1SubsetCoverage(v, v, desc, 65, 67, widths, ctx)
	if len(ctx.errs) != 1 || !strings.Contains(extMessages(ctx.Issues(), pdf.Checks.Font.SubsetGlyphCoverage), "code 66") {
		t.Errorf("want exactly one finding, for code 66: %v", issueKeys(ctx.Issues()))
	}

	ctx = &ValidationContext{Spec: a1bSpec, UsedCharCodes: map[uintptr]map[int]bool{pdf.ValuePointer(v.Entries): {65: true}}}
	ValidateType1SubsetCoverage(v, v, desc, 65, 67, widths, ctx)
	if len(ctx.errs) != 0 {
		t.Errorf("unshown codes 66/67 were examined: %v", issueKeys(ctx.Issues()))
	}
}

// TestType1CMetricsSkipsUnusedCodes pins that a Type1C width is compared only
// for codes the document shows, when usage is known.
func TestType1CMetricsSkipsUnusedCodes(t *testing.T) {
	cff := buildMinimalCFF()
	ff := streamDict(string(cff))
	aWidth := int(fontprog.CFFAdvanceWidths(cff)["A"])
	v := pdf.NewPDFDict()
	v.Entries.Set("Encoding", pdf.PDFName{Value: "WinAnsiEncoding"})
	widths := pdf.PDFArray{pdf.PDFInteger(aWidth + 50)}

	for _, tc := range []struct {
		name string
		used int
		want bool
	}{
		{"code never shown", 66, false},
		{"code shown", 65, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := &ValidationContext{Spec: a1bSpec, UsedCharCodes: map[uintptr]map[int]bool{pdf.ValuePointer(v.Entries): {tc.used: true}}}
			validateType1CMetrics(v, v, ff, 65, widths, ctx)
			if got := hasCheck(ctx, pdf.Checks.Font.AdvanceWidthMismatch); got != tc.want {
				t.Errorf("AdvanceWidthMismatch reported = %v, want %v", got, tc.want)
			}
		})
	}
}
