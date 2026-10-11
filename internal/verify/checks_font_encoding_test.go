package verify

import (
	"encoding/binary"
	"testing"

	"github.com/voidrab/gopdfrab/internal/fontprog"
	"github.com/voidrab/gopdfrab/internal/pdf"
)

// macOnlyTrueType builds a three-glyph TrueType program whose only cmap is a
// (1,0) Macintosh format-0 subtable: Mac Roman 0x41 ('A') -> GID 1, 0x8E
// ('eacute') -> GID 2. Advance widths are 0, 500, 600 at 1000 units/em.
func macOnlyTrueType() pdf.PDFDict {
	head := make([]byte, 54)
	binary.BigEndian.PutUint16(head[18:], 1000) // unitsPerEm
	maxp := []byte{0, 0, 0x50, 0, 0, 3}         // version 0.5, numGlyphs 3
	hhea := make([]byte, 36)
	binary.BigEndian.PutUint16(hhea[34:], 3) // numberOfHMetrics
	hmtx := make([]byte, 12)
	binary.BigEndian.PutUint16(hmtx[4:], 500)
	binary.BigEndian.PutUint16(hmtx[8:], 600)

	sub := make([]byte, 262)
	binary.BigEndian.PutUint16(sub[2:], 262) // format 0, length 262
	sub[6+0x41] = 1
	sub[6+0x8E] = 2
	cmap := []byte{0, 0, 0, 1, 0, 1, 0, 0, 0, 0, 0, 12} // one (1,0) record at offset 12
	cmap = append(cmap, sub...)

	return streamDict(string(fontprog.PackSfnt(map[string][]byte{
		"head": head, "maxp": maxp, "hhea": hhea, "hmtx": hmtx, "cmap": cmap,
	})))
}

func trueTypeFontWithEncoding(enc pdf.PDFValue) pdf.PDFDict {
	v := pdf.NewPDFDict()
	v.Entries.Set("Encoding", enc)
	return v
}

// TestSimpleTrueTypeMacCmapResolvesThroughGlyphNames covers ISO 32000-1
// 9.6.6.4 for a non-symbolic TrueType font without a (3,1) cmap: a code is
// mapped to its glyph name through /Encoding, the name to its Mac OS Roman
// code, and that code is looked up in the (1,0) cmap. The raw code is not.
func TestSimpleTrueTypeMacCmapResolvesThroughGlyphNames(t *testing.T) {
	ff := macOnlyTrueType()
	winAnsi := pdf.PDFName{Value: "WinAnsiEncoding"}

	// WinAnsi 0xE9 is eacute, Mac Roman 0x8E: present. Looking up the raw
	// code 0xE9 instead reported a glyph the font has.
	ctx := &ValidationContext{}
	ValidateSimpleTrueTypeSubset(trueTypeFontWithEncoding(winAnsi), ff, 0xE9, 0xE9, pdf.PDFArray{pdf.PDFInteger(600)}, ctx)
	if hasCheck(ctx, pdf.Checks.Font.SubsetGlyphCoverage) {
		t.Errorf("WinAnsi eacute reported missing: %v", issueKeys(ctx.Issues()))
	}

	// /Differences renames code 65 to /B, which the font lacks (Mac 0x42 is
	// unmapped). Looking up the raw code 65 found 'A' and hid the gap.
	enc := pdf.NewPDFDict()
	enc.Entries.Set("BaseEncoding", winAnsi)
	enc.Entries.Set("Differences", pdf.PDFArray{pdf.PDFInteger(65), pdf.PDFName{Value: "B"}})
	ctx = &ValidationContext{}
	ValidateSimpleTrueTypeSubset(trueTypeFontWithEncoding(enc), ff, 65, 65, pdf.PDFArray{pdf.PDFInteger(500)}, ctx)
	if !hasCheck(ctx, pdf.Checks.Font.SubsetGlyphCoverage) {
		t.Error("code 65 renamed to the absent /B was not reported")
	}

	// A name with no Mac OS Roman code (Lslash) has nothing to look up.
	enc = pdf.NewPDFDict()
	enc.Entries.Set("Differences", pdf.PDFArray{pdf.PDFInteger(65), pdf.PDFName{Value: "Lslash"}})
	ctx = &ValidationContext{}
	ValidateSimpleTrueTypeSubset(trueTypeFontWithEncoding(enc), ff, 65, 65, pdf.PDFArray{pdf.PDFInteger(500)}, ctx)
	if !hasCheck(ctx, pdf.Checks.Font.SubsetGlyphCoverage) {
		t.Error("code 65 renamed to /Lslash, which Mac Roman cannot encode, was not reported")
	}

	// Positive control: plain 'A' resolves.
	ctx = &ValidationContext{}
	ValidateSimpleTrueTypeSubset(trueTypeFontWithEncoding(winAnsi), ff, 65, 65, pdf.PDFArray{pdf.PDFInteger(500)}, ctx)
	if len(ctx.errs) != 0 {
		t.Errorf("code 65 'A' reported: %v", issueKeys(ctx.Issues()))
	}

	// Without /Encoding the font is read as symbolic and the raw code is
	// looked up in (1,0): 0x8E is GID 2.
	ctx = &ValidationContext{}
	ValidateSimpleTrueTypeSubset(pdf.NewPDFDict(), ff, 0x8E, 0x8E, pdf.PDFArray{pdf.PDFInteger(600)}, ctx)
	if len(ctx.errs) != 0 {
		t.Errorf("symbolic raw code 0x8E reported: %v", issueKeys(ctx.Issues()))
	}
}

// TestSimpleTrueTypeMetricsMacCmapResolvesThroughGlyphNames is the 6.3.6
// counterpart: the width compared is that of the glyph the name resolves to.
func TestSimpleTrueTypeMetricsMacCmapResolvesThroughGlyphNames(t *testing.T) {
	ff := macOnlyTrueType()
	v := trueTypeFontWithEncoding(pdf.PDFName{Value: "WinAnsiEncoding"})

	ctx := &ValidationContext{}
	validateSimpleTrueTypeMetrics(v, ff, 0xE9, pdf.PDFArray{pdf.PDFInteger(900)}, ctx)
	if !hasCheck(ctx, pdf.Checks.Font.AdvanceWidthMismatch) {
		t.Error("eacute width 900 vs font 600 not reported")
	}
	ctx = &ValidationContext{}
	validateSimpleTrueTypeMetrics(v, ff, 0xE9, pdf.PDFArray{pdf.PDFInteger(600)}, ctx)
	if len(ctx.errs) != 0 {
		t.Errorf("matching eacute width reported: %v", issueKeys(ctx.Issues()))
	}
}

// TestSimpleTrueTypeEncodedFontIgnoresSymbolicCmap: a font with /Encoding is
// non-symbolic, and ISO 32000-1 9.6.6.4 resolves its codes through (3,1) and
// (1,0) only. A glyph reachable solely through the (3,0) cmap by raw code is
// missing (6.3.5), as veraPDF also reports.
func TestSimpleTrueTypeEncodedFontIgnoresSymbolicCmap(t *testing.T) {
	head := make([]byte, 54)
	binary.BigEndian.PutUint16(head[18:], 1000)
	sub := make([]byte, 262)
	binary.BigEndian.PutUint16(sub[2:], 262)
	sub[6+0xB0] = 1                                     // raw code 0xB0 only
	cmap := []byte{0, 0, 0, 1, 0, 3, 0, 0, 0, 0, 0, 12} // one (3,0) record
	ff := streamDict(string(fontprog.PackSfnt(map[string][]byte{
		"head": head, "maxp": {0, 0, 0x50, 0, 0, 2}, "cmap": append(cmap, sub...),
	})))

	ctx := &ValidationContext{}
	ValidateSimpleTrueTypeSubset(trueTypeFontWithEncoding(pdf.PDFName{Value: "WinAnsiEncoding"}), ff, 0xB0, 0xB0, pdf.PDFArray{pdf.PDFInteger(400)}, ctx)
	if !hasCheck(ctx, pdf.Checks.Font.SubsetGlyphCoverage) {
		t.Error("WinAnsi degree resolvable only through (3,0) was not reported")
	}
	// Without /Encoding the font is symbolic and (3,0) applies.
	ctx = &ValidationContext{}
	ValidateSimpleTrueTypeSubset(pdf.NewPDFDict(), ff, 0xB0, 0xB0, pdf.PDFArray{pdf.PDFInteger(400)}, ctx)
	if len(ctx.errs) != 0 {
		t.Errorf("symbolic (3,0) lookup reported: %v", issueKeys(ctx.Issues()))
	}
}
