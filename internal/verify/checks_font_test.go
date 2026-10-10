package verify

import (
	"testing"

	"github.com/voidrab/gopdfrab/internal/fontprog"
	"github.com/voidrab/gopdfrab/internal/pdf"
)

// buildUnembeddedTrueTypeFont returns a minimal Type/TrueType font dict with
// no FontFile2 in its descriptor (simulating a non-embedded font).
func buildUnembeddedTrueTypeFont(name string) (pdf.PDFDict, uintptr) {
	desc := pdf.NewPDFDict()
	desc.Entries.Set("Type", pdf.PDFName{Value: "FontDescriptor"})
	desc.Entries.Set("FontName", pdf.PDFName{Value: name})
	desc.Entries.Set("Flags", pdf.PDFInteger(32))

	font := pdf.NewPDFDict()
	font.Entries.Set("Type", pdf.PDFName{Value: "Font"})
	font.Entries.Set("Subtype", pdf.PDFName{Value: "TrueType"})
	font.Entries.Set("BaseFont", pdf.PDFName{Value: name})
	font.Entries.Set("Encoding", pdf.PDFName{Value: "WinAnsiEncoding"})
	font.Entries.Set("FirstChar", pdf.PDFInteger(32))
	font.Entries.Set("LastChar", pdf.PDFInteger(32))
	font.Entries.Set("Widths", pdf.PDFArray{pdf.PDFInteger(278)})
	font.Entries.Set("FontDescriptor", desc)

	ptr := pdf.ValuePointer(font.Entries)
	return font, ptr
}

// TestSimpleNotEmbedded_DrawnFontFlagged verifies that a non-embedded simple
// font present in UsedCharCodes (drawn in content) is flagged even when
// SkipUnusedSimpleFonts is true.
func TestSimpleNotEmbedded_DrawnFontFlagged(t *testing.T) {
	font, ptr := buildUnembeddedTrueTypeFont("ArialMT")

	ctx := &ValidationContext{
		SkipUnusedSimpleFonts: true,
		UsedCharCodes:         map[uintptr]map[int]bool{ptr: {32: true}},
	}
	ValidateFontDict(font, ctx)

	var got []pdf.PDFError
	for _, e := range ctx.errs {
		if e.Check() == pdf.Checks.Font.SimpleNotEmbedded {
			got = append(got, e)
		}
	}
	if len(got) == 0 {
		t.Error("expected SimpleNotEmbedded for drawn non-embedded font, got none")
	}
}

// TestSimpleNotEmbedded_UndrawnFontSkipped verifies that a non-embedded
// simple font absent from UsedCharCodes is not flagged when
// SkipUnusedSimpleFonts is true (veraPDF / PDFA1B behaviour).
func TestSimpleNotEmbedded_UndrawnFontSkipped(t *testing.T) {
	font, _ := buildUnembeddedTrueTypeFont("ArialMT")

	ctx := &ValidationContext{
		SkipUnusedSimpleFonts: true,
		UsedCharCodes:         map[uintptr]map[int]bool{},
	}
	ValidateFontDict(font, ctx)

	for _, e := range ctx.errs {
		if e.Check() == pdf.Checks.Font.SimpleNotEmbedded {
			t.Errorf("unexpected SimpleNotEmbedded for undrawn font: %v", e)
		}
	}
}

func TestHasEmbeddedProgram(t *testing.T) {
	desc := pdf.NewPDFDict()
	if HasEmbeddedProgram(desc, "FontFile", "FontFile2") {
		t.Error("HasEmbeddedProgram should be false with no FontFile* entries")
	}
	desc.Entries.Set("FontFile2", pdf.NewPDFDict())
	if !HasEmbeddedProgram(desc, "FontFile", "FontFile2") {
		t.Error("HasEmbeddedProgram should be true when FontFile2 is present")
	}
	if HasEmbeddedProgram(pdf.PDFDict{}, "FontFile") {
		t.Error("HasEmbeddedProgram should be false for a dict with a nil Entries map")
	}
}

func TestType3GlyphWidthAndMetrics(t *testing.T) {
	if w := Type3GlyphWidth([]byte("500 0 d0\n")); w != 500 {
		t.Errorf("Type3GlyphWidth(d0) = %g, want 500", w)
	}
	if w := Type3GlyphWidth([]byte("1 0 0 0 1 1 d1\n")); w != 1 {
		t.Errorf("Type3GlyphWidth(d1) = %g, want 1", w)
	}
	if w := Type3GlyphWidth([]byte("q Q\n")); w != -1 {
		t.Errorf("Type3GlyphWidth(no d0/d1) = %g, want -1", w)
	}
}

func TestValidateType3Metrics(t *testing.T) {
	proc := pdf.NewPDFDict()
	proc.HasStream = true
	proc.RawStream = []byte("500 0 d0\n")

	charProcs := pdf.NewPDFDict()
	charProcs.Entries.Set("g65", proc)

	enc := pdf.NewPDFDict()
	enc.Entries.Set("Differences", pdf.PDFArray{pdf.PDFInteger(65), pdf.PDFName{Value: "g65"}})

	v := pdf.NewPDFDict()
	v.Entries.Set("FirstChar", pdf.PDFInteger(65))
	v.Entries.Set("LastChar", pdf.PDFInteger(65))
	v.Entries.Set("Widths", pdf.PDFArray{pdf.PDFInteger(500)})
	v.Entries.Set("CharProcs", charProcs)
	v.Entries.Set("Encoding", enc)

	ctx := &ValidationContext{}
	validateType3Metrics(v, ctx)
	if hasCheck(ctx, pdf.Checks.Font.AdvanceWidthMismatch) {
		t.Error("unexpected AdvanceWidthMismatch for matching Type3 width")
	}
	v.Entries.Set("Widths", pdf.PDFArray{pdf.PDFInteger(520)})
	ctx2 := &ValidationContext{}
	validateType3Metrics(v, ctx2)
	if !hasCheck(ctx2, pdf.Checks.Font.AdvanceWidthMismatch) {
		t.Error("expected AdvanceWidthMismatch for mismatched Type3 width")
	}
}

func TestIdentityCIDToGIDMap(t *testing.T) {
	v := pdf.NewPDFDict()
	if !IdentityCIDToGIDMap(v) {
		t.Error("absent CIDToGIDMap should count as Identity")
	}
	v.Entries.Set("CIDToGIDMap", pdf.PDFName{Value: "Identity"})
	if !IdentityCIDToGIDMap(v) {
		t.Error("/Identity CIDToGIDMap should be true")
	}
	v.Entries.Set("CIDToGIDMap", pdf.NewPDFDict()) // an embedded stream map
	if IdentityCIDToGIDMap(v) {
		t.Error("a stream CIDToGIDMap should not count as Identity")
	}
}

func TestDescendantCIDFont(t *testing.T) {
	if d := DescendantCIDFont(pdf.NewPDFDict()); d.Entries != nil {
		t.Error("DescendantCIDFont with no DescendantFonts should be empty")
	}
	cid := pdf.NewPDFDict()
	cid.Entries.Set("Subtype", pdf.PDFName{Value: "CIDFontType2"})
	v := pdf.NewPDFDict()
	v.Entries.Set("DescendantFonts", pdf.PDFArray{cid})
	got := DescendantCIDFont(v)
	if got.Entries.Get("Subtype") != (pdf.PDFName{Value: "CIDFontType2"}) {
		t.Error("DescendantCIDFont should return the first descendant dict")
	}
}

func TestSameCIDSystemInfo(t *testing.T) {
	a := pdf.NewPDFDict()
	a.Entries.Set("Registry", pdf.PDFString{Value: "Adobe"})
	a.Entries.Set("Ordering", pdf.PDFString{Value: "Identity"})
	b := pdf.NewPDFDict()
	b.Entries.Set("Registry", pdf.PDFString{Value: "Adobe"})
	b.Entries.Set("Ordering", pdf.PDFString{Value: "Identity"})
	if !SameCIDSystemInfo(a, b) {
		t.Error("matching Registry/Ordering should be equal")
	}
	b.Entries.Set("Ordering", pdf.PDFString{Value: "Japan1"})
	if SameCIDSystemInfo(a, b) {
		t.Error("differing Ordering should not be equal")
	}
}

func TestValidateType0Font(t *testing.T) {
	cidCSI := pdf.NewPDFDict()
	cidCSI.Entries.Set("Registry", pdf.PDFString{Value: "Adobe"})
	cidCSI.Entries.Set("Ordering", pdf.PDFString{Value: "Identity"})
	cid := pdf.NewPDFDict()
	cid.Entries.Set("CIDSystemInfo", cidCSI)

	// A named, non-predefined CMap is flagged as neither predefined nor embedded.
	v := pdf.NewPDFDict()
	v.Entries.Set("Encoding", pdf.PDFName{Value: "Custom-Encoding"})
	v.Entries.Set("DescendantFonts", pdf.PDFArray{cid})
	ctx := &ValidationContext{}
	validateType0Font(v, ctx)
	if !hasCheck(ctx, pdf.Checks.Font.CMapNotEmbedded) {
		t.Error("expected CMapNotEmbedded for a non-predefined named CMap")
	}
	// Identity-H is predefined: no CMapNotEmbedded.
	v.Entries.Set("Encoding", pdf.PDFName{Value: "Identity-H"})
	ctx2 := &ValidationContext{}
	validateType0Font(v, ctx2)
	if hasCheck(ctx2, pdf.Checks.Font.CMapNotEmbedded) {
		t.Error("unexpected CMapNotEmbedded for Identity-H")
	}

	// An embedded CMap dict with mismatched CIDSystemInfo.
	cmapCSI := pdf.NewPDFDict()
	cmapCSI.Entries.Set("Registry", pdf.PDFString{Value: "Adobe"})
	cmapCSI.Entries.Set("Ordering", pdf.PDFString{Value: "Japan1"})
	cmap := pdf.NewPDFDict()
	cmap.Entries.Set("CIDSystemInfo", cmapCSI)
	v.Entries.Set("Encoding", cmap)
	ctx3 := &ValidationContext{}
	validateType0Font(v, ctx3)
	if !hasCheck(ctx3, pdf.Checks.Font.CIDSystemInfoMismatch) {
		t.Error("expected CIDSystemInfoMismatch for incompatible CIDSystemInfo")
	}
}

func TestValidateCMapStreamAndCIDLimits(t *testing.T) {
	cmap := pdf.NewPDFDict()
	cmap.Entries.Set("Type", pdf.PDFName{Value: "CMap"})
	cmap.HasStream = true
	cmap.RawStream = []byte("1 begincidrange\n<0000> <FFFF> 70000\nendcidrange\n")

	ctx := &ValidationContext{}
	validateCMapStream(cmap, ctx)
	var got bool
	for _, e := range ctx.errs {
		if e.Check() == pdf.Checks.Structure.CMapCIDOutOfRange {
			got = true
		}
	}
	if !got {
		t.Error("expected CMapCIDOutOfRange for a CID beyond 65535")
	}

	// Not a CMap stream: no-op.
	nonCMap := pdf.NewPDFDict()
	ctx2 := &ValidationContext{}
	validateCMapStream(nonCMap, ctx2)
	if len(ctx2.errs) != 0 {
		t.Error("validateCMapStream should be a no-op for a non-CMap dict")
	}
}

func TestEmbeddedProgramMatchesSubtype(t *testing.T) {
	descFF := pdf.NewPDFDict()
	descFF.Entries.Set("FontFile", pdf.NewPDFDict())
	if !EmbeddedProgramMatchesSubtype("Type1", descFF) {
		t.Error("Type1 with FontFile should match")
	}
	if EmbeddedProgramMatchesSubtype("TrueType", descFF) {
		t.Error("TrueType with only FontFile (Type1) should not match")
	}

	ff3 := pdf.NewPDFDict()
	ff3.Entries.Set("Subtype", pdf.PDFName{Value: "Type1C"})
	descFF3 := pdf.NewPDFDict()
	descFF3.Entries.Set("FontFile3", ff3)
	if !EmbeddedProgramMatchesSubtype("Type1", descFF3) {
		t.Error("Type1 with a Type1C FontFile3 should match")
	}

	ff3cid := pdf.NewPDFDict()
	ff3cid.Entries.Set("Subtype", pdf.PDFName{Value: "CIDFontType0C"})
	descCID := pdf.NewPDFDict()
	descCID.Entries.Set("FontFile3", ff3cid)
	if !EmbeddedProgramMatchesSubtype("CIDFontType0", descCID) {
		t.Error("CIDFontType0 with a CIDFontType0C FontFile3 should match")
	}
	if EmbeddedProgramMatchesSubtype("CIDFontType0", descFF3) {
		t.Error("CIDFontType0 with a Type1C FontFile3 should not match")
	}

	descFF2 := pdf.NewPDFDict()
	descFF2.Entries.Set("FontFile2", pdf.NewPDFDict())
	if !EmbeddedProgramMatchesSubtype("TrueType", descFF2) {
		t.Error("TrueType with FontFile2 should match")
	}
	if !EmbeddedProgramMatchesSubtype("CIDFontType2", descFF2) {
		t.Error("CIDFontType2 with FontFile2 should match")
	}

	if EmbeddedProgramMatchesSubtype("Type1", pdf.NewPDFDict()) {
		t.Error("Type1 with no embedded program should not match")
	}
	if EmbeddedProgramMatchesSubtype("Type1", pdf.PDFDict{}) {
		t.Error("a nil-Entries descriptor should not match")
	}
}

// buildType1FontDict returns a complete Type1 font dictionary embedding
// buildType1Font's program, for exercising ValidateFontDict's Type1 path.
func buildType1FontDict(baseFont string) pdf.PDFDict {
	ff := pdf.NewPDFDict()
	ff.HasStream = true
	ff.RawStream = buildType1Font()

	desc := pdf.NewPDFDict()
	desc.Entries.Set("FontFile", ff)
	desc.Entries.Set("CharSet", pdf.PDFString{Value: "/A"})

	v := pdf.NewPDFDict()
	v.Entries.Set("Type", pdf.PDFName{Value: "Font"})
	v.Entries.Set("Subtype", pdf.PDFName{Value: "Type1"})
	v.Entries.Set("BaseFont", pdf.PDFName{Value: baseFont})
	v.Entries.Set("Encoding", pdf.PDFName{Value: "WinAnsiEncoding"})
	v.Entries.Set("FirstChar", pdf.PDFInteger(65))
	v.Entries.Set("LastChar", pdf.PDFInteger(65))
	v.Entries.Set("Widths", pdf.PDFArray{pdf.PDFInteger(500)})
	v.Entries.Set("FontDescriptor", desc)
	return v
}

func TestValidateFontDictType1Subset(t *testing.T) {
	v := buildType1FontDict("ABCDEF+Test")
	ctx := &ValidationContext{}
	ValidateFontDict(v, ctx)
	if hasCheck(ctx, pdf.Checks.Font.SimpleNotEmbedded) {
		t.Error("unexpected SimpleNotEmbedded for an embedded Type1 font")
	}
	if hasCheck(ctx, pdf.Checks.Font.Type1SubsetCharSet) {
		t.Error("unexpected Type1SubsetCharSet: CharSet lists the only used glyph")
	}
}

func TestValidateFontDictType1SubsetMissingCharSet(t *testing.T) {
	v := buildType1FontDict("ABCDEF+Test")
	desc := v.Entries.Get("FontDescriptor").(pdf.PDFDict)
	desc.Entries.Del("CharSet")
	v.Entries.Set("FontDescriptor", desc)
	ctx := &ValidationContext{}
	ValidateFontDict(v, ctx)
	if !hasCheck(ctx, pdf.Checks.Font.Type1SubsetCharSet) {
		t.Error("expected Type1SubsetCharSet for a subset font descriptor lacking CharSet")
	}
}

func TestValidateFontDictTrueTypeSubset(t *testing.T) {
	ttf := loadTTF(t)
	tables, _ := fontprog.ParseSfnt(ttf)
	gidA, _ := fontprog.ParseCmapFormat4(fontprog.TTWindowsBMPCmap(tables))['A']
	widthA := fontprog.TTAdvanceWidth(tables, int(gidA))

	ff := pdf.NewPDFDict()
	ff.HasStream = true
	ff.RawStream = ttf
	desc := pdf.NewPDFDict()
	desc.Entries.Set("FontFile2", ff)

	v := pdf.NewPDFDict()
	v.Entries.Set("Type", pdf.PDFName{Value: "Font"})
	v.Entries.Set("Subtype", pdf.PDFName{Value: "TrueType"})
	v.Entries.Set("BaseFont", pdf.PDFName{Value: "ABCDEF+LiberationSans"})
	v.Entries.Set("Encoding", pdf.PDFName{Value: "WinAnsiEncoding"})
	v.Entries.Set("FirstChar", pdf.PDFInteger(65))
	v.Entries.Set("LastChar", pdf.PDFInteger(65))
	v.Entries.Set("Widths", pdf.PDFArray{pdf.PDFInteger(widthA)})
	v.Entries.Set("FontDescriptor", desc)

	ctx := &ValidationContext{}
	ValidateFontDict(v, ctx)
	if hasCheck(ctx, pdf.Checks.Font.SubsetGlyphCoverage) || hasCheck(ctx, pdf.Checks.Font.AdvanceWidthMismatch) {
		t.Errorf("unexpected violation for a correctly embedded TrueType subset: %v", ctx.errs)
	}
}

func TestValidateFontDictCIDFontType2(t *testing.T) {
	desc, ff, gidA, widthA := buildCIDTrueTypeFont(t)
	desc.Entries.Set("CIDSet", func() pdf.PDFDict {
		ttf := loadTTF(t)
		tables, _ := fontprog.ParseSfnt(ttf)
		n := fontprog.TTNumGlyphs(tables)
		bm := make([]byte, (n+7)/8)
		for i := range bm {
			bm[i] = 0xFF
		}
		d := pdf.NewPDFDict()
		d.HasStream = true
		d.RawStream = bm
		return d
	}())
	desc.Entries.Set("FontFile2", ff)

	v := pdf.NewPDFDict()
	v.Entries.Set("Type", pdf.PDFName{Value: "Font"})
	v.Entries.Set("Subtype", pdf.PDFName{Value: "Type0"})
	v.Entries.Set("BaseFont", pdf.PDFName{Value: "ABCDEF+LiberationSans"})
	v.Entries.Set("Encoding", pdf.PDFName{Value: "Identity-H"})
	v.Entries.Set("DescendantFonts", pdf.PDFArray{func() pdf.PDFDict {
		cid := pdf.NewPDFDict()
		cid.Entries.Set("Type", pdf.PDFName{Value: "Font"})
		cid.Entries.Set("Subtype", pdf.PDFName{Value: "CIDFontType2"})
		cid.Entries.Set("FontDescriptor", desc)
		cid.Entries.Set("CIDToGIDMap", pdf.PDFName{Value: "Identity"})
		cid.Entries.Set("W", pdf.PDFArray{pdf.PDFInteger(gidA), pdf.PDFArray{pdf.PDFInteger(widthA)}})
		return cid
	}()})

	ctx := &ValidationContext{}
	ValidateFontDict(v, ctx)
	if hasCheck(ctx, pdf.Checks.Font.CIDToGIDMapMissing) {
		t.Error("unexpected CIDToGIDMapMissing: CIDToGIDMap is present")
	}
	if hasCheck(ctx, pdf.Checks.Font.SubsetGlyphCoverage) || hasCheck(ctx, pdf.Checks.Font.AdvanceWidthMismatch) {
		t.Errorf("unexpected violation for a correctly embedded CIDFontType2 subset: %v", ctx.errs)
	}
}

func TestValidateFontDictCIDFontType0(t *testing.T) {
	cff := buildMinimalCIDCFF()
	ff := pdf.NewPDFDict()
	ff.HasStream = true
	ff.RawStream = cff

	desc := pdf.NewPDFDict()
	desc.Entries.Set("FontFile3", ff)
	cidSet := pdf.NewPDFDict()
	cidSet.HasStream = true
	cidSet.RawStream = []byte{0xE0}
	// CIDs 0, 1, 2 all marked
	desc.Entries.Set("CIDSet", cidSet)

	cid := pdf.NewPDFDict()
	cid.Entries.Set("Type", pdf.PDFName{Value: "Font"})
	cid.Entries.Set("Subtype", pdf.PDFName{Value: "CIDFontType0"})
	cid.Entries.Set("FontDescriptor", desc)
	cid.Entries.Set("W", pdf.PDFArray{pdf.PDFInteger(1), pdf.PDFArray{pdf.PDFInteger(600), pdf.PDFInteger(700)}})

	v := pdf.NewPDFDict()
	v.Entries.Set("Type", pdf.PDFName{Value: "Font"})
	v.Entries.Set("Subtype", pdf.PDFName{Value: "Type0"})
	v.Entries.Set("BaseFont", pdf.PDFName{Value: "ABCDEF+CIDTest"})
	v.Entries.Set("Encoding", pdf.PDFName{Value: "Identity-H"})
	v.Entries.Set("DescendantFonts", pdf.PDFArray{cid})

	ctx := &ValidationContext{}
	ValidateFontDict(v, ctx)
	if hasCheck(ctx, pdf.Checks.Font.SubsetGlyphCoverage) || hasCheck(ctx, pdf.Checks.Font.AdvanceWidthMismatch) || hasCheck(ctx, pdf.Checks.Font.CIDSubsetCIDSet) {
		t.Errorf("unexpected violation for a correctly embedded CIDFontType0 subset: %v", ctx.errs)
	}
}

func TestValidateFontDictType3(t *testing.T) {
	proc := pdf.NewPDFDict()
	proc.HasStream = true
	proc.RawStream = []byte("500 0 d0\n")
	charProcs := pdf.NewPDFDict()
	charProcs.Entries.Set("g65", proc)
	enc := pdf.NewPDFDict()
	enc.Entries.Set("Differences", pdf.PDFArray{pdf.PDFInteger(65), pdf.PDFName{Value: "g65"}})

	v := pdf.NewPDFDict()
	v.Entries.Set("Type", pdf.PDFName{Value: "Font"})
	v.Entries.Set("Subtype", pdf.PDFName{Value: "Type3"})
	v.Entries.Set("FirstChar", pdf.PDFInteger(65))
	v.Entries.Set("LastChar", pdf.PDFInteger(65))
	v.Entries.Set("Widths", pdf.PDFArray{pdf.PDFInteger(500)})
	v.Entries.Set("CharProcs", charProcs)
	v.Entries.Set("Encoding", enc)

	ctx := &ValidationContext{}
	ValidateFontDict(v, ctx)
	if hasCheck(ctx, pdf.Checks.Font.AdvanceWidthMismatch) {
		t.Error("unexpected AdvanceWidthMismatch for a matching Type3 glyph")
	}

	// A missing/invalid Subtype is reported and skips further checks.
	vBad := pdf.NewPDFDict()
	vBad.Entries.Set("Type", pdf.PDFName{Value: "Font"})
	ctx2 := &ValidationContext{}
	ValidateFontDict(vBad, ctx2)
	if !hasCheck(ctx2, pdf.Checks.Font.InvalidSubtype) {
		t.Error("expected InvalidSubtype for a font dict with no Subtype")
	}
}

func TestValidateFontDictSymbolicTrueType(t *testing.T) {
	ttf := loadTTF(t)
	ff := pdf.NewPDFDict()
	ff.HasStream = true
	ff.RawStream = ttf
	desc := pdf.NewPDFDict()
	desc.Entries.Set("Flags", pdf.PDFInteger(4))
	// symbolic
	desc.Entries.Set("FontFile2", ff)

	v := pdf.NewPDFDict()
	v.Entries.Set("Type", pdf.PDFName{Value: "Font"})
	v.Entries.Set("Subtype", pdf.PDFName{Value: "TrueType"})
	v.Entries.Set("BaseFont", pdf.PDFName{Value: "SymbolFont"})
	v.Entries.Set("Encoding", pdf.PDFName{Value: "WinAnsiEncoding"})
	// shall not be set for symbolic
	v.Entries.Set("FontDescriptor", desc)

	ctx := &ValidationContext{}
	ValidateFontDict(v, ctx)
	if !hasCheck(ctx, pdf.Checks.Font.SymbolicTrueTypeEncoding) {
		t.Error("expected SymbolicTrueTypeEncoding when a symbolic font declares Encoding")
	}
}

// TestValidateTrueTypeEncodingUnusedFont covers the clause's own wording: the
// non-symbolic encoding rule governs fonts "used for rendering", so a font a
// resource dictionary names and no content stream selects is out of its scope
// under the veraPDF-matching profile -- and still reported under the literal
// one, which is what the Isartor suite expects.
func TestValidateTrueTypeEncodingUnusedFont(t *testing.T) {
	enc := pdf.NewPDFDict()
	enc.Entries.Set("Differences", pdf.PDFArray{pdf.PDFInteger(128), pdf.PDFName{Value: "gamma"}})
	desc := pdf.NewPDFDict()
	desc.Entries.Set("Flags", pdf.PDFInteger(32)) // non-symbolic

	v := pdf.NewPDFDict()
	v.Entries.Set("Type", pdf.PDFName{Value: "Font"})
	v.Entries.Set("Subtype", pdf.PDFName{Value: "TrueType"})
	v.Entries.Set("Encoding", enc)

	unused := &ValidationContext{
		SkipUnusedSimpleFonts: true,
		UsedCharCodes:         map[uintptr]map[int]bool{},
	}
	validateTrueTypeEncoding(v, desc, unused)
	if hasCheck(unused, pdf.Checks.Font.TrueTypeEncoding) {
		t.Error("reported 6.3.7 for a font no content stream selects")
	}

	shown := &ValidationContext{
		SkipUnusedSimpleFonts: true,
		UsedCharCodes:         map[uintptr]map[int]bool{pdf.ValuePointer(v.Entries): {128: true}},
	}
	validateTrueTypeEncoding(v, desc, shown)
	if !hasCheck(shown, pdf.Checks.Font.TrueTypeEncoding) {
		t.Error("no 6.3.7 for a shown font whose Encoding is a dictionary")
	}

	literal := &ValidationContext{} // Legacy1B: no usage gating
	validateTrueTypeEncoding(v, desc, literal)
	if !hasCheck(literal, pdf.Checks.Font.TrueTypeEncoding) {
		t.Error("the spec-literal profile must report an unused font too")
	}
}

func TestValidateFontDictCIDFontType0NotEmbedded(t *testing.T) {
	// The verifier walks and validates the descendant CIDFont dict directly
	// (it is itself a /Type /Font dict); CIDNotEmbedded fires there, not on
	// the wrapping Type0 dict (which is exempted from the embedding check).
	cid := pdf.NewPDFDict()
	cid.Entries.Set("Type", pdf.PDFName{Value: "Font"})
	cid.Entries.Set("Subtype", pdf.PDFName{Value: "CIDFontType0"})
	cid.Entries.Set("BaseFont", pdf.PDFName{Value: "NotEmbedded"})
	cid.Entries.Set("FontDescriptor", pdf.NewPDFDict()) // no embedded program

	ctx := &ValidationContext{}
	ValidateFontDict(cid, ctx)
	if !hasCheck(ctx, pdf.Checks.Font.CIDNotEmbedded) {
		t.Error("expected CIDNotEmbedded for a CIDFontType0 with no embedded program")
	}
}

func TestValidateFontDictInvisibleFontSkipsChecks(t *testing.T) {
	// A Type1 subset font with a mismatched width would normally be flagged,
	// but a font shown only under invisible rendering modes is exempt.
	v := buildType1FontDict("ABCDEF+Test")
	v.Entries.Set("Widths", pdf.PDFArray{pdf.PDFInteger(999)}) // deliberately wrong
	ptr := pdf.ValuePointer(v.Entries)

	ctx := &ValidationContext{InvisibleOnlyFontPtrs: map[uintptr]bool{ptr: true}}
	ValidateFontDict(v, ctx)
	if hasCheck(ctx, pdf.Checks.Font.AdvanceWidthMismatch) || hasCheck(ctx, pdf.Checks.Font.SimpleNotEmbedded) {
		t.Errorf("an invisible-only font should skip embedding/metrics checks, got %v", ctx.errs)
	}
}

func TestValidateFontDictType1NonSubset(t *testing.T) {
	// A non-subset BaseFont (no "ABCDEF+" prefix) skips subset-coverage checks
	// even though CharSet is absent.
	v := buildType1FontDict("Test")
	ctx := &ValidationContext{}
	ValidateFontDict(v, ctx)
	if hasCheck(ctx, pdf.Checks.Font.Type1SubsetCharSet) {
		t.Error("a non-subset font should not require CharSet")
	}
}

func TestCheckCMapCIDLimitsCIDChar(t *testing.T) {
	data := []byte("1 begincidchar\n<0041> 70000\nendcidchar\n")
	ctx := &ValidationContext{}
	checkCMapCIDLimits(pdf.PDFDict{}, data, ctx)
	var got bool
	for _, e := range ctx.errs {
		if e.Check() == pdf.Checks.Structure.CMapCIDOutOfRange {
			got = true
		}
	}
	if !got {
		t.Error("expected CMapCIDOutOfRange for a cidchar CID beyond 65535")
	}

	// Within range: no violation.
	ctx2 := &ValidationContext{}
	checkCMapCIDLimits(pdf.PDFDict{}, []byte("1 begincidchar\n<0041> 100\nendcidchar\n"), ctx2)
	if len(ctx2.errs) != 0 {
		t.Error("unexpected violation for an in-range cidchar CID")
	}
}

// TestSimpleNotEmbedded_LegacyStrictness verifies that with
// SkipUnusedSimpleFonts=false (Legacy1B) a non-embedded font is always flagged.
func TestSimpleNotEmbedded_LegacyStrictness(t *testing.T) {
	font, _ := buildUnembeddedTrueTypeFont("ArialMT")

	ctx := &ValidationContext{
		SkipUnusedSimpleFonts: false,
		UsedCharCodes:         map[uintptr]map[int]bool{},
	}
	ValidateFontDict(font, ctx)

	var got []pdf.PDFError
	for _, e := range ctx.errs {
		if e.Check() == pdf.Checks.Font.SimpleNotEmbedded {
			got = append(got, e)
		}
	}
	if len(got) == 0 {
		t.Error("expected SimpleNotEmbedded in strict mode for non-embedded font, got none")
	}
}

// type3Font builds a Type3 font dictionary with one glyph /g65 at code 65
// whose charproc is the given stream (or non-stream value when proc has no
// stream).
func type3Font(proc pdf.PDFDict, widths pdf.PDFArray) pdf.PDFDict {
	charProcs := pdf.NewPDFDict()
	charProcs.Entries.Set("g65", proc)
	enc := pdf.NewPDFDict()
	enc.Entries.Set("Differences", pdf.PDFArray{pdf.PDFInteger(65), pdf.PDFName{Value: "g65"}})

	v := pdf.NewPDFDict()
	v.Entries.Set("FirstChar", pdf.PDFInteger(65))
	v.Entries.Set("LastChar", pdf.PDFInteger(65))
	v.Entries.Set("Widths", widths)
	v.Entries.Set("CharProcs", charProcs)
	v.Entries.Set("Encoding", enc)
	return v
}

func type3Proc(content string) pdf.PDFDict {
	proc := pdf.NewPDFDict()
	proc.HasStream = true
	proc.RawStream = []byte(content)
	return proc
}

// TestType3GlyphWidthReal covers a real-valued wx operand.
func TestType3GlyphWidthReal(t *testing.T) {
	if w := Type3GlyphWidth([]byte("250.5 0 d0\n")); w != 250.5 {
		t.Errorf("Type3GlyphWidth(real wx) = %g, want 250.5", w)
	}
}

// TestValidateType3MetricsRealWidths checks that a real-valued Widths entry is
// compared against the glyph's d0 width like an integer one.
func TestValidateType3MetricsRealWidths(t *testing.T) {
	proc := type3Proc("500 0 d0\n")
	ctx := &ValidationContext{}
	validateType3Metrics(type3Font(proc, pdf.PDFArray{pdf.PDFReal(500.4)}), ctx)
	if hasCheck(ctx, pdf.Checks.Font.AdvanceWidthMismatch) {
		t.Error("a real width within tolerance was reported")
	}
	ctx = &ValidationContext{}
	validateType3Metrics(type3Font(proc, pdf.PDFArray{pdf.PDFReal(520.5)}), ctx)
	if !hasCheck(ctx, pdf.Checks.Font.AdvanceWidthMismatch) {
		t.Error("a real width off by 20.5 was not reported")
	}
}

// TestValidateType3MetricsSkipsUncheckableGlyphs checks that every way a
// glyph's width can be unverifiable skips the glyph without a report.
func TestValidateType3MetricsSkipsUncheckableGlyphs(t *testing.T) {
	broken := type3Proc("not a zlib stream")
	broken.Entries.Set("Filter", pdf.PDFName{Value: "FlateDecode"})

	for _, tc := range []struct {
		name string
		mod  func(v pdf.PDFDict) pdf.PDFDict
	}{
		{"missing FirstChar", func(v pdf.PDFDict) pdf.PDFDict { v.Entries.Del("FirstChar"); return v }},
		{"no Differences", func(v pdf.PDFDict) pdf.PDFDict {
			v.Entries.Set("Encoding", pdf.NewPDFDict())
			return v
		}},
		{"Widths longer than LastChar", func(v pdf.PDFDict) pdf.PDFDict {
			// Code 66 has a glyph whose d0 disagrees with its width, but lies
			// past LastChar and so is not part of the font.
			v.Entries.Set("Widths", pdf.PDFArray{pdf.PDFInteger(500), pdf.PDFInteger(999)})
			cp := v.Entries.Get("CharProcs").(pdf.PDFDict)
			cp.Entries.Set("g66", type3Proc("500 0 d0\n"))
			enc := v.Entries.Get("Encoding").(pdf.PDFDict)
			enc.Entries.Set("Differences", pdf.PDFArray{pdf.PDFInteger(65), pdf.PDFName{Value: "g65"}, pdf.PDFName{Value: "g66"}})
			return v
		}},
		{"code without a glyph name", func(v pdf.PDFDict) pdf.PDFDict {
			v.Entries.Set("FirstChar", pdf.PDFInteger(64))
			v.Entries.Set("LastChar", pdf.PDFInteger(64))
			return v
		}},
		{"charproc is not a stream", func(v pdf.PDFDict) pdf.PDFDict {
			cp := v.Entries.Get("CharProcs").(pdf.PDFDict)
			cp.Entries.Set("g65", pdf.PDFInteger(1))
			return v
		}},
		{"charproc does not decode", func(v pdf.PDFDict) pdf.PDFDict {
			cp := v.Entries.Get("CharProcs").(pdf.PDFDict)
			cp.Entries.Set("g65", broken)
			return v
		}},
		{"charproc has no d0 or d1", func(v pdf.PDFDict) pdf.PDFDict {
			cp := v.Entries.Get("CharProcs").(pdf.PDFDict)
			cp.Entries.Set("g65", type3Proc("q Q\n"))
			return v
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := tc.mod(type3Font(type3Proc("500 0 d0\n"), pdf.PDFArray{pdf.PDFInteger(900)}))
			ctx := &ValidationContext{}
			validateType3Metrics(v, ctx)
			if hasCheck(ctx, pdf.Checks.Font.AdvanceWidthMismatch) {
				t.Error("AdvanceWidthMismatch reported for a glyph whose width cannot be checked")
			}
		})
	}
}

// TestEmbeddedProgramMatchesSubtypeOtherSubtypes covers subtypes with no
// specific program kind (Type3, Type0, missing): any embedded program counts.
func TestEmbeddedProgramMatchesSubtypeOtherSubtypes(t *testing.T) {
	embedded := pdf.NewPDFDict()
	embedded.Entries.Set("FontFile2", pdf.NewPDFDict())
	for _, subtype := range []string{"Type3", "Type0", ""} {
		if !EmbeddedProgramMatchesSubtype(subtype, embedded) {
			t.Errorf("subtype %q with FontFile2: want embedded", subtype)
		}
		if EmbeddedProgramMatchesSubtype(subtype, pdf.NewPDFDict()) {
			t.Errorf("subtype %q with no program: want not embedded", subtype)
		}
	}
}

// TestValidateFontDictMissingBaseFont covers 6.3.5's requirement that a
// non-Type3 font names itself.
func TestValidateFontDictMissingBaseFont(t *testing.T) {
	v := buildType1FontDict("Test")
	v.Entries.Del("BaseFont")
	ctx := &ValidationContext{}
	ValidateFontDict(v, ctx)
	if !hasCheck(ctx, pdf.Checks.Font.FontBaseFont) {
		t.Error("expected FontBaseFont for a Type1 font without BaseFont")
	}
}

// TestSameCIDSystemInfoNonStringFields checks that a Registry or Ordering that
// is not a string reads as empty, so it differs from a real value.
func TestSameCIDSystemInfoNonStringFields(t *testing.T) {
	a := pdf.NewPDFDict()
	a.Entries.Set("Registry", pdf.PDFString{Value: "Adobe"})
	a.Entries.Set("Ordering", pdf.PDFString{Value: "Identity"})
	b := pdf.NewPDFDict()
	b.Entries.Set("Registry", pdf.PDFInteger(1))
	b.Entries.Set("Ordering", pdf.PDFString{Value: "Identity"})
	if SameCIDSystemInfo(a, b) {
		t.Error("a non-string Registry must not equal a string Registry")
	}
}

// TestValidateCMapStreamUndecodable checks that a CMap whose filter chain is
// broken is reported as undecodable and not scanned.
func TestValidateCMapStreamUndecodable(t *testing.T) {
	cmap := pdf.NewPDFDict()
	cmap.Entries.Set("Type", pdf.PDFName{Value: "CMap"})
	cmap.Entries.Set("Filter", pdf.PDFName{Value: "FlateDecode"})
	cmap.HasStream = true
	cmap.RawStream = []byte("not a zlib stream")
	ctx := &ValidationContext{}
	validateCMapStream(cmap, ctx)
	if !hasCheck(ctx, pdf.Checks.Structure.StreamUndecodable) {
		t.Error("expected StreamUndecodable for a broken CMap stream")
	}
	if hasCheck(ctx, pdf.Checks.Structure.CMapCIDOutOfRange) {
		t.Error("a broken CMap stream must not be scanned")
	}
}
