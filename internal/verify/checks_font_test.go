package verify

import (
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// buildUnembeddedTrueTypeFont returns a minimal Type/TrueType font dict with
// no FontFile2 in its descriptor (simulating a non-embedded font).
func buildUnembeddedTrueTypeFont(name string) (pdf.PDFDict, uintptr) {
	desc := pdf.NewPDFDict()
	desc.Entries["Type"] = pdf.PDFName{Value: "FontDescriptor"}
	desc.Entries["FontName"] = pdf.PDFName{Value: name}
	desc.Entries["Flags"] = pdf.PDFInteger(32)

	font := pdf.NewPDFDict()
	font.Entries["Type"] = pdf.PDFName{Value: "Font"}
	font.Entries["Subtype"] = pdf.PDFName{Value: "TrueType"}
	font.Entries["BaseFont"] = pdf.PDFName{Value: name}
	font.Entries["Encoding"] = pdf.PDFName{Value: "WinAnsiEncoding"}
	font.Entries["FirstChar"] = pdf.PDFInteger(32)
	font.Entries["LastChar"] = pdf.PDFInteger(32)
	font.Entries["Widths"] = pdf.PDFArray{pdf.PDFInteger(278)}
	font.Entries["FontDescriptor"] = desc

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
	desc.Entries["FontFile2"] = pdf.NewPDFDict()
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
	charProcs.Entries["g65"] = proc

	enc := pdf.NewPDFDict()
	enc.Entries["Differences"] = pdf.PDFArray{pdf.PDFInteger(65), pdf.PDFName{Value: "g65"}}

	v := pdf.NewPDFDict()
	v.Entries["FirstChar"] = pdf.PDFInteger(65)
	v.Entries["LastChar"] = pdf.PDFInteger(65)
	v.Entries["Widths"] = pdf.PDFArray{pdf.PDFInteger(500)}
	v.Entries["CharProcs"] = charProcs
	v.Entries["Encoding"] = enc

	ctx := &ValidationContext{}
	validateType3Metrics(v, ctx)
	if hasCheck(ctx, pdf.Checks.Font.AdvanceWidthMismatch) {
		t.Error("unexpected AdvanceWidthMismatch for matching Type3 width")
	}

	v.Entries["Widths"] = pdf.PDFArray{pdf.PDFInteger(520)}
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
	v.Entries["CIDToGIDMap"] = pdf.PDFName{Value: "Identity"}
	if !IdentityCIDToGIDMap(v) {
		t.Error("/Identity CIDToGIDMap should be true")
	}
	v.Entries["CIDToGIDMap"] = pdf.NewPDFDict() // an embedded stream map
	if IdentityCIDToGIDMap(v) {
		t.Error("a stream CIDToGIDMap should not count as Identity")
	}
}

func TestDescendantCIDFont(t *testing.T) {
	if d := DescendantCIDFont(pdf.NewPDFDict()); d.Entries != nil {
		t.Error("DescendantCIDFont with no DescendantFonts should be empty")
	}
	cid := pdf.NewPDFDict()
	cid.Entries["Subtype"] = pdf.PDFName{Value: "CIDFontType2"}
	v := pdf.NewPDFDict()
	v.Entries["DescendantFonts"] = pdf.PDFArray{cid}
	got := DescendantCIDFont(v)
	if got.Entries["Subtype"] != (pdf.PDFName{Value: "CIDFontType2"}) {
		t.Error("DescendantCIDFont should return the first descendant dict")
	}
}

func TestSameCIDSystemInfo(t *testing.T) {
	a := pdf.NewPDFDict()
	a.Entries["Registry"] = pdf.PDFString{Value: "Adobe"}
	a.Entries["Ordering"] = pdf.PDFString{Value: "Identity"}
	b := pdf.NewPDFDict()
	b.Entries["Registry"] = pdf.PDFString{Value: "Adobe"}
	b.Entries["Ordering"] = pdf.PDFString{Value: "Identity"}
	if !SameCIDSystemInfo(a, b) {
		t.Error("matching Registry/Ordering should be equal")
	}
	b.Entries["Ordering"] = pdf.PDFString{Value: "Japan1"}
	if SameCIDSystemInfo(a, b) {
		t.Error("differing Ordering should not be equal")
	}
}

func TestValidateType0Font(t *testing.T) {
	cidCSI := pdf.NewPDFDict()
	cidCSI.Entries["Registry"] = pdf.PDFString{Value: "Adobe"}
	cidCSI.Entries["Ordering"] = pdf.PDFString{Value: "Identity"}
	cid := pdf.NewPDFDict()
	cid.Entries["CIDSystemInfo"] = cidCSI

	// A named, non-predefined CMap is flagged as neither predefined nor embedded.
	v := pdf.NewPDFDict()
	v.Entries["Encoding"] = pdf.PDFName{Value: "Custom-Encoding"}
	v.Entries["DescendantFonts"] = pdf.PDFArray{cid}
	ctx := &ValidationContext{}
	validateType0Font(v, ctx)
	if !hasCheck(ctx, pdf.Checks.Font.CMapNotEmbedded) {
		t.Error("expected CMapNotEmbedded for a non-predefined named CMap")
	}

	// Identity-H is predefined: no CMapNotEmbedded.
	v.Entries["Encoding"] = pdf.PDFName{Value: "Identity-H"}
	ctx2 := &ValidationContext{}
	validateType0Font(v, ctx2)
	if hasCheck(ctx2, pdf.Checks.Font.CMapNotEmbedded) {
		t.Error("unexpected CMapNotEmbedded for Identity-H")
	}

	// An embedded CMap dict with mismatched CIDSystemInfo.
	cmapCSI := pdf.NewPDFDict()
	cmapCSI.Entries["Registry"] = pdf.PDFString{Value: "Adobe"}
	cmapCSI.Entries["Ordering"] = pdf.PDFString{Value: "Japan1"}
	cmap := pdf.NewPDFDict()
	cmap.Entries["CIDSystemInfo"] = cmapCSI
	v.Entries["Encoding"] = cmap
	ctx3 := &ValidationContext{}
	validateType0Font(v, ctx3)
	if !hasCheck(ctx3, pdf.Checks.Font.CIDSystemInfoMismatch) {
		t.Error("expected CIDSystemInfoMismatch for incompatible CIDSystemInfo")
	}
}

func TestValidateCMapStreamAndCIDLimits(t *testing.T) {
	cmap := pdf.NewPDFDict()
	cmap.Entries["Type"] = pdf.PDFName{Value: "CMap"}
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

func TestEmbeddedProgramMatchesSubtype(t *testing.T) {
	descFF := pdf.NewPDFDict()
	descFF.Entries["FontFile"] = pdf.NewPDFDict()
	if !EmbeddedProgramMatchesSubtype("Type1", descFF) {
		t.Error("Type1 with FontFile should match")
	}
	if EmbeddedProgramMatchesSubtype("TrueType", descFF) {
		t.Error("TrueType with only FontFile (Type1) should not match")
	}

	ff3 := pdf.NewPDFDict()
	ff3.Entries["Subtype"] = pdf.PDFName{Value: "Type1C"}
	descFF3 := pdf.NewPDFDict()
	descFF3.Entries["FontFile3"] = ff3
	if !EmbeddedProgramMatchesSubtype("Type1", descFF3) {
		t.Error("Type1 with a Type1C FontFile3 should match")
	}

	ff3cid := pdf.NewPDFDict()
	ff3cid.Entries["Subtype"] = pdf.PDFName{Value: "CIDFontType0C"}
	descCID := pdf.NewPDFDict()
	descCID.Entries["FontFile3"] = ff3cid
	if !EmbeddedProgramMatchesSubtype("CIDFontType0", descCID) {
		t.Error("CIDFontType0 with a CIDFontType0C FontFile3 should match")
	}
	if EmbeddedProgramMatchesSubtype("CIDFontType0", descFF3) {
		t.Error("CIDFontType0 with a Type1C FontFile3 should not match")
	}

	descFF2 := pdf.NewPDFDict()
	descFF2.Entries["FontFile2"] = pdf.NewPDFDict()
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
	desc.Entries["FontFile"] = ff
	desc.Entries["CharSet"] = pdf.PDFString{Value: "/A"}

	v := pdf.NewPDFDict()
	v.Entries["Type"] = pdf.PDFName{Value: "Font"}
	v.Entries["Subtype"] = pdf.PDFName{Value: "Type1"}
	v.Entries["BaseFont"] = pdf.PDFName{Value: baseFont}
	v.Entries["Encoding"] = pdf.PDFName{Value: "WinAnsiEncoding"}
	v.Entries["FirstChar"] = pdf.PDFInteger(65)
	v.Entries["LastChar"] = pdf.PDFInteger(65)
	v.Entries["Widths"] = pdf.PDFArray{pdf.PDFInteger(500)}
	v.Entries["FontDescriptor"] = desc
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
	desc := v.Entries["FontDescriptor"].(pdf.PDFDict)
	delete(desc.Entries, "CharSet")
	v.Entries["FontDescriptor"] = desc
	ctx := &ValidationContext{}
	ValidateFontDict(v, ctx)
	if !hasCheck(ctx, pdf.Checks.Font.Type1SubsetCharSet) {
		t.Error("expected Type1SubsetCharSet for a subset font descriptor lacking CharSet")
	}
}

func TestValidateFontDictTrueTypeSubset(t *testing.T) {
	ttf := loadTTF(t)
	tables, _ := ParseSfnt(ttf)
	gidA, _ := ParseCmapFormat4(TTWindowsBMPCmap(tables))['A']
	widthA := TTAdvanceWidth(tables, int(gidA))

	ff := pdf.NewPDFDict()
	ff.HasStream = true
	ff.RawStream = ttf
	desc := pdf.NewPDFDict()
	desc.Entries["FontFile2"] = ff

	v := pdf.NewPDFDict()
	v.Entries["Type"] = pdf.PDFName{Value: "Font"}
	v.Entries["Subtype"] = pdf.PDFName{Value: "TrueType"}
	v.Entries["BaseFont"] = pdf.PDFName{Value: "ABCDEF+LiberationSans"}
	v.Entries["Encoding"] = pdf.PDFName{Value: "WinAnsiEncoding"}
	v.Entries["FirstChar"] = pdf.PDFInteger(65)
	v.Entries["LastChar"] = pdf.PDFInteger(65)
	v.Entries["Widths"] = pdf.PDFArray{pdf.PDFInteger(widthA)}
	v.Entries["FontDescriptor"] = desc

	ctx := &ValidationContext{}
	ValidateFontDict(v, ctx)
	if hasCheck(ctx, pdf.Checks.Font.SubsetGlyphCoverage) || hasCheck(ctx, pdf.Checks.Font.AdvanceWidthMismatch) {
		t.Errorf("unexpected violation for a correctly embedded TrueType subset: %v", ctx.errs)
	}
}

func TestValidateFontDictCIDFontType2(t *testing.T) {
	desc, ff, gidA, widthA := buildCIDTrueTypeFont(t)
	desc.Entries["CIDSet"] = func() pdf.PDFDict {
		ttf := loadTTF(t)
		tables, _ := ParseSfnt(ttf)
		n := TTNumGlyphs(tables)
		bm := make([]byte, (n+7)/8)
		for i := range bm {
			bm[i] = 0xFF
		}
		d := pdf.NewPDFDict()
		d.HasStream = true
		d.RawStream = bm
		return d
	}()
	desc.Entries["FontFile2"] = ff

	v := pdf.NewPDFDict()
	v.Entries["Type"] = pdf.PDFName{Value: "Font"}
	v.Entries["Subtype"] = pdf.PDFName{Value: "Type0"}
	v.Entries["BaseFont"] = pdf.PDFName{Value: "ABCDEF+LiberationSans"}
	v.Entries["Encoding"] = pdf.PDFName{Value: "Identity-H"}
	v.Entries["DescendantFonts"] = pdf.PDFArray{func() pdf.PDFDict {
		cid := pdf.NewPDFDict()
		cid.Entries["Type"] = pdf.PDFName{Value: "Font"}
		cid.Entries["Subtype"] = pdf.PDFName{Value: "CIDFontType2"}
		cid.Entries["FontDescriptor"] = desc
		cid.Entries["CIDToGIDMap"] = pdf.PDFName{Value: "Identity"}
		cid.Entries["W"] = pdf.PDFArray{pdf.PDFInteger(gidA), pdf.PDFArray{pdf.PDFInteger(widthA)}}
		return cid
	}()}

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
	desc.Entries["FontFile3"] = ff
	cidSet := pdf.NewPDFDict()
	cidSet.HasStream = true
	cidSet.RawStream = []byte{0xE0} // CIDs 0, 1, 2 all marked
	desc.Entries["CIDSet"] = cidSet

	cid := pdf.NewPDFDict()
	cid.Entries["Type"] = pdf.PDFName{Value: "Font"}
	cid.Entries["Subtype"] = pdf.PDFName{Value: "CIDFontType0"}
	cid.Entries["FontDescriptor"] = desc
	cid.Entries["W"] = pdf.PDFArray{pdf.PDFInteger(1), pdf.PDFArray{pdf.PDFInteger(600), pdf.PDFInteger(700)}}

	v := pdf.NewPDFDict()
	v.Entries["Type"] = pdf.PDFName{Value: "Font"}
	v.Entries["Subtype"] = pdf.PDFName{Value: "Type0"}
	v.Entries["BaseFont"] = pdf.PDFName{Value: "ABCDEF+CIDTest"}
	v.Entries["Encoding"] = pdf.PDFName{Value: "Identity-H"}
	v.Entries["DescendantFonts"] = pdf.PDFArray{cid}

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
	charProcs.Entries["g65"] = proc
	enc := pdf.NewPDFDict()
	enc.Entries["Differences"] = pdf.PDFArray{pdf.PDFInteger(65), pdf.PDFName{Value: "g65"}}

	v := pdf.NewPDFDict()
	v.Entries["Type"] = pdf.PDFName{Value: "Font"}
	v.Entries["Subtype"] = pdf.PDFName{Value: "Type3"}
	v.Entries["FirstChar"] = pdf.PDFInteger(65)
	v.Entries["LastChar"] = pdf.PDFInteger(65)
	v.Entries["Widths"] = pdf.PDFArray{pdf.PDFInteger(500)}
	v.Entries["CharProcs"] = charProcs
	v.Entries["Encoding"] = enc

	ctx := &ValidationContext{}
	ValidateFontDict(v, ctx)
	if hasCheck(ctx, pdf.Checks.Font.AdvanceWidthMismatch) {
		t.Error("unexpected AdvanceWidthMismatch for a matching Type3 glyph")
	}

	// A missing/invalid Subtype is reported and skips further checks.
	vBad := pdf.NewPDFDict()
	vBad.Entries["Type"] = pdf.PDFName{Value: "Font"}
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
	desc.Entries["Flags"] = pdf.PDFInteger(4) // symbolic
	desc.Entries["FontFile2"] = ff

	v := pdf.NewPDFDict()
	v.Entries["Type"] = pdf.PDFName{Value: "Font"}
	v.Entries["Subtype"] = pdf.PDFName{Value: "TrueType"}
	v.Entries["BaseFont"] = pdf.PDFName{Value: "SymbolFont"}
	v.Entries["Encoding"] = pdf.PDFName{Value: "WinAnsiEncoding"} // shall not be set for symbolic
	v.Entries["FontDescriptor"] = desc

	ctx := &ValidationContext{}
	ValidateFontDict(v, ctx)
	if !hasCheck(ctx, pdf.Checks.Font.SymbolicTrueTypeEncoding) {
		t.Error("expected SymbolicTrueTypeEncoding when a symbolic font declares Encoding")
	}
}

func TestValidateFontDictCIDFontType0NotEmbedded(t *testing.T) {
	// The verifier walks and validates the descendant CIDFont dict directly
	// (it is itself a /Type /Font dict); CIDNotEmbedded fires there, not on
	// the wrapping Type0 dict (which is exempted from the embedding check).
	cid := pdf.NewPDFDict()
	cid.Entries["Type"] = pdf.PDFName{Value: "Font"}
	cid.Entries["Subtype"] = pdf.PDFName{Value: "CIDFontType0"}
	cid.Entries["BaseFont"] = pdf.PDFName{Value: "NotEmbedded"}
	cid.Entries["FontDescriptor"] = pdf.NewPDFDict() // no embedded program

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
	v.Entries["Widths"] = pdf.PDFArray{pdf.PDFInteger(999)} // deliberately wrong
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
