package convert

import (
	"bytes"
	"encoding/binary"
	"os"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/writer"

	"github.com/voidrab/gopdfrab/internal/verify"
)

// buildMinimalCIDCFF assembles a tiny CID-keyed CFF font with 3 glyphs
// (.notdef, CID 1 width 600, CID 2 width 700), a format-0 CID charset, a
// single FDArray Font DICT (format-0 FDSelect), and a Private DICT with zero
// width defaults. Ported from verify.buildMinimalCIDCFF (internal/verify's
// test-only helper isn't importable across packages).
func buildMinimalCIDCFF() []byte {
	i32 := func(v int) []byte {
		var b [5]byte
		b[0] = 29
		binary.BigEndian.PutUint32(b[1:], uint32(v))
		return b[:]
	}

	header := []byte{0x01, 0x00, 0x04, 0x01}
	nameIndex := []byte{0x00, 0x01, 0x01, 0x01, 0x05, 'F', 'o', 'n', 't'}

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

	fdArrayOff := csEnd
	const fdDictLen = 11
	fdArrayIndexLen := 2 + 1 + 2 + fdDictLen
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

// fixtureTrailer opens path and returns its fully-resolved object graph,
// skipping the test if the corpus isn't present. The caller must keep doc
// alive (via the returned closer) for as long as it uses the trailer, since
// stream bytes may be read lazily.
func fixtureTrailer(t *testing.T, path string) (trailer pdf.PDFDict, closeDoc func()) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Skip("corpus fixture not present")
	}
	doc, err := pdf.Open(path)
	if err != nil {
		t.Fatalf("pdf.Open(%s): %v", path, err)
	}
	graph, err := doc.ResolveGraph()
	if err != nil {
		doc.Close()
		t.Fatalf("ResolveGraph: %v", err)
	}
	trailer, ok := graph.(pdf.PDFDict)
	if !ok {
		doc.Close()
		t.Fatalf("resolved graph is not a dictionary")
	}
	return trailer, func() { doc.Close() }
}

// assertCheckClearedByWrite serializes trailer through WriteDocument and
// re-verifies the result, asserting check c is no longer reported -- the
// same round-trip TestLZWStreamFixerRoundTripsThroughWriter uses to confirm
// a Fixer's edits actually clear the violation, not just look right in memory.
func assertCheckClearedByWrite(t *testing.T, trailer pdf.PDFDict, c pdf.Check) {
	t.Helper()
	var buf bytes.Buffer
	if err := writer.WriteDocument(&buf, trailer); err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}
	doc, err := pdf.Open(writeTempPDF(t, "font_program_fixed.pdf", buf.Bytes()))
	if err != nil {
		t.Fatalf("pdf.Open(written output): %v", err)
	}
	defer doc.Close()
	res, err := verify.Verify(doc, pdf.PDFA1B)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	for _, iss := range res.Issues {
		if iss.Check() == c {
			t.Errorf("check %s (%s/%d) still present after fix+rewrite: %v", c.Name(), c.Clause(), c.Subclause(), iss)
		}
	}
}

// findFontBySubtype returns the first Font dict in trailer with the given
// Subtype, failing the test if none is found.
func findFontBySubtype(t *testing.T, trailer pdf.PDFDict, subtype string) pdf.PDFDict {
	t.Helper()
	var found pdf.PDFDict
	ok := false
	walkDicts(trailer, map[uintptr]bool{}, func(d pdf.PDFDict) {
		if ok || (d.Entries["Type"] != pdf.PDFName{Value: "Font"}) {
			return
		}
		st, _ := d.Entries["Subtype"].(pdf.PDFName)
		if st.Value == subtype {
			found, ok = d, true
		}
	})
	if !ok {
		t.Fatalf("no Font dict with Subtype %s found in trailer", subtype)
	}
	return found
}

// TestPromoteEmptyGlyphsInFontsIdempotent covers promoteEmptyGlyphsInFonts'
// guard cascade (CIDFontType2 dispatch, FontDescriptor/FontFile2 presence,
// decode success) via a real embedded TrueType program -- Liberation Sans has
// blank glyphs (e.g. space) that trigger promotion -- and checks the second
// pass over the already-promoted program is a no-op.
func TestPromoteEmptyGlyphsInFontsIdempotent(t *testing.T) {
	ttf := loadLiberationSansForTest(t)
	ff := pdf.PDFDict{Entries: map[string]pdf.PDFValue{}, HasStream: true, RawStream: ttf}
	desc := pdf.PDFDict{Entries: map[string]pdf.PDFValue{"FontFile2": ff}}
	font := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Subtype":        pdf.PDFName{Value: "CIDFontType2"},
		"FontDescriptor": desc,
	}}
	trailer := pdf.PDFDict{Entries: map[string]pdf.PDFValue{"Font": font}}

	if err := promoteEmptyGlyphsInFonts(&trailer, nil); err != nil {
		t.Fatalf("promoteEmptyGlyphsInFonts: %v", err)
	}
	desc = trailer.Entries["Font"].(pdf.PDFDict).Entries["FontDescriptor"].(pdf.PDFDict)
	ff1, ok := desc.Entries["FontFile2"].(pdf.PDFDict)
	if !ok {
		t.Fatalf("FontFile2 missing after first pass")
	}
	repaired1, err := pdf.DecodeStream(ff1)
	if err != nil {
		t.Fatalf("DecodeStream (first pass): %v", err)
	}
	if string(repaired1) == string(ttf) {
		t.Fatal("sanity: first pass did not change the font program")
	}

	if err := promoteEmptyGlyphsInFonts(&trailer, nil); err != nil {
		t.Fatalf("promoteEmptyGlyphsInFonts (second pass): %v", err)
	}
	desc = trailer.Entries["Font"].(pdf.PDFDict).Entries["FontDescriptor"].(pdf.PDFDict)
	ff2 := desc.Entries["FontFile2"].(pdf.PDFDict)
	repaired2, err := pdf.DecodeStream(ff2)
	if err != nil {
		t.Fatalf("DecodeStream (second pass): %v", err)
	}
	if string(repaired2) != string(repaired1) {
		t.Error("second pass over an already-promoted program changed it further, want a no-op")
	}
}

// TestFontMetricFixerAppliesOnlyToAdvanceWidthMismatch mirrors
// TestFontDictFixerAppliesOnlyToCIDToGIDMapMissing: a Fixer must claim
// exactly its Check(s), since registerFixer panics on overlap.
func TestFontMetricFixerAppliesOnlyToAdvanceWidthMismatch(t *testing.T) {
	fixer := fontMetricFixer{}
	for _, c := range pdf.AllChecks() {
		want := c == pdf.Checks.Font.AdvanceWidthMismatch
		if got := fixer.Applies(c); got != want {
			t.Errorf("Applies(%s/%d) = %v, want %v", c.Clause(), c.Subclause(), got, want)
		}
	}
}

func TestFontSubsetMetaFixerAppliesOnlyToCharSetAndCIDSet(t *testing.T) {
	fixer := fontSubsetMetaFixer{}
	for _, c := range pdf.AllChecks() {
		want := c == pdf.Checks.Font.Type1SubsetCharSet || c == pdf.Checks.Font.CIDSubsetCIDSet
		if got := fixer.Applies(c); got != want {
			t.Errorf("Applies(%s/%d) = %v, want %v", c.Clause(), c.Subclause(), got, want)
		}
	}
}

// runFixerAndCheckIdempotent runs fixer twice over trailer, asserting the
// first pass changes something and the second pass is a no-op.
func runFixerAndCheckIdempotent(t *testing.T, fixer Fixer, trailer *pdf.PDFDict) {
	t.Helper()
	changed, err := fixer.Fix(trailer, nil)
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if !changed {
		t.Fatalf("changed = false, want true")
	}
	changed, err = fixer.Fix(trailer, nil)
	if err != nil {
		t.Fatalf("Fix (second pass): %v", err)
	}
	if changed {
		t.Errorf("changed = true on second pass, want false (fixer must be idempotent)")
	}
}

func TestFontMetricFixerCorrectsSimpleTrueTypeWidths(t *testing.T) {
	path := "../../tests/Isartor/PDFA-1b/6.3 Fonts/6.3.6 Font metrics/isartor-6-3-6-t01-fail-b.pdf"
	trailer, closeDoc := fixtureTrailer(t, path)
	defer closeDoc()

	runFixerAndCheckIdempotent(t, fontMetricFixer{}, &trailer)
	assertCheckClearedByWrite(t, trailer, pdf.Checks.Font.AdvanceWidthMismatch)
}

func TestFontMetricFixerCorrectsType1Widths(t *testing.T) {
	path := "../../tests/Isartor/PDFA-1b/6.3 Fonts/6.3.6 Font metrics/isartor-6-3-6-t01-fail-a.pdf"
	trailer, closeDoc := fixtureTrailer(t, path)
	defer closeDoc()

	runFixerAndCheckIdempotent(t, fontMetricFixer{}, &trailer)
	assertCheckClearedByWrite(t, trailer, pdf.Checks.Font.AdvanceWidthMismatch)
}

func TestFontMetricFixerCorrectsCIDTrueTypeWidths(t *testing.T) {
	path := "../../tests/Isartor/PDFA-1b/6.3 Fonts/6.3.6 Font metrics/isartor-6-3-6-t01-fail-c.pdf"
	trailer, closeDoc := fixtureTrailer(t, path)
	defer closeDoc()

	runFixerAndCheckIdempotent(t, fontMetricFixer{}, &trailer)
	assertCheckClearedByWrite(t, trailer, pdf.Checks.Font.AdvanceWidthMismatch)
}

func TestFontMetricFixerCorrectsType3Widths(t *testing.T) {
	path := "../../tests/veraPDF/PDF_A-1b/6.3 Fonts/6.3.6 Font metrics/veraPDF test suite 6-3-6-t01-fail-a.pdf"
	trailer, closeDoc := fixtureTrailer(t, path)
	defer closeDoc()

	runFixerAndCheckIdempotent(t, fontMetricFixer{}, &trailer)
	assertCheckClearedByWrite(t, trailer, pdf.Checks.Font.AdvanceWidthMismatch)
}

// TestFixCIDCFFWidthsCorrectsMismatch drives fixCIDCFFWidths directly: no
// corpus fixture exercises a CIDFontType0/CFF width mismatch, so the font
// program is hand-built (buildMinimalCIDCFF: CID 1 width 600, CID 2 width 700).
func TestFixCIDCFFWidthsCorrectsMismatch(t *testing.T) {
	ff := pdf.PDFDict{HasStream: true, RawStream: buildMinimalCIDCFF()}
	v := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"W": pdf.PDFArray{pdf.PDFInteger(1), pdf.PDFArray{pdf.PDFInteger(500), pdf.PDFInteger(500)}},
	}}

	if !fixCIDCFFWidths(v, ff) {
		t.Fatalf("fixCIDCFFWidths = false, want true (500/500 mismatches the embedded 600/700)")
	}
	want := map[int]int{1: 600, 2: 700}
	for _, pair := range verify.ParseCIDWidths(v.Entries["W"].(pdf.PDFArray)) {
		if pair[1] != want[pair[0]] {
			t.Errorf("CID %d width = %d, want %d", pair[0], pair[1], want[pair[0]])
		}
	}

	// Idempotent: the now-correct widths should no longer trigger a change.
	if fixCIDCFFWidths(v, ff) {
		t.Error("fixCIDCFFWidths on already-corrected widths = true, want false")
	}
}

// TestFixCIDCFFWidthsNoOpWithoutW covers the missing-/W short-circuit.
func TestFixCIDCFFWidthsNoOpWithoutW(t *testing.T) {
	ff := pdf.PDFDict{HasStream: true, RawStream: buildMinimalCIDCFF()}
	if fixCIDCFFWidths(pdf.PDFDict{Entries: map[string]pdf.PDFValue{}}, ff) {
		t.Error("fixCIDCFFWidths without /W = true, want false")
	}
}

// TestFixTrueTypeCIDSetSkipsNonIdentityCIDToGIDMap covers the CID!=GID guard:
// a stream (non-/Identity) CIDToGIDMap means CIDs don't correspond to GIDs
// directly, so fixTrueTypeCIDSet must bail out without touching desc.
func TestFixTrueTypeCIDSetSkipsNonIdentityCIDToGIDMap(t *testing.T) {
	ttf := loadLiberationSansForTest(t)
	d := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"CIDToGIDMap": pdf.PDFDict{HasStream: true, RawStream: []byte{0, 1, 0, 2}},
	}}
	desc := pdf.PDFDict{Entries: map[string]pdf.PDFValue{}}
	ff := pdf.PDFDict{HasStream: true, RawStream: ttf}

	if fixTrueTypeCIDSet(d, desc, ff) {
		t.Error("fixTrueTypeCIDSet with a stream CIDToGIDMap = true, want false")
	}
	if desc.Entries["CIDSet"] != nil {
		t.Error("desc/CIDSet was populated despite the non-Identity CIDToGIDMap guard")
	}
}

// TestFixTrueTypeCIDSetNoOpWhenAlreadyComplete covers the already-complete
// /CIDSet no-op branch via a real embedded TrueType program.
func TestFixTrueTypeCIDSetNoOpWhenAlreadyComplete(t *testing.T) {
	ttf := loadLiberationSansForTest(t)
	d := pdf.PDFDict{Entries: map[string]pdf.PDFValue{}}
	desc := pdf.PDFDict{Entries: map[string]pdf.PDFValue{}}
	ff := pdf.PDFDict{HasStream: true, RawStream: ttf}

	if !fixTrueTypeCIDSet(d, desc, ff) {
		t.Fatal("sanity: first pass should populate CIDSet")
	}
	if fixTrueTypeCIDSet(d, desc, ff) {
		t.Error("fixTrueTypeCIDSet on an already-complete CIDSet = true, want false")
	}
}

// TestFontSubsetMetaFixerSynthesizesType1CharSet covers a raw Type1 program
// (FontFile) whose descriptor lacks /CharSet entirely.
func TestFontSubsetMetaFixerSynthesizesType1CharSet(t *testing.T) {
	path := "../../tests/Isartor/PDFA-1b/6.3 Fonts/6.3.5 Font subsets/isartor-6-3-5-t02-fail-a.pdf"
	trailer, closeDoc := fixtureTrailer(t, path)
	defer closeDoc()

	font := findFontBySubtype(t, trailer, "Type1")
	desc := font.Entries["FontDescriptor"].(pdf.PDFDict)
	if desc.Entries["CharSet"] != nil {
		t.Fatalf("fixture precondition failed: CharSet already present")
	}

	runFixerAndCheckIdempotent(t, fontSubsetMetaFixer{}, &trailer)

	cs, ok := desc.Entries["CharSet"].(pdf.PDFString)
	if !ok || cs.Value == "" {
		t.Fatalf("CharSet = %v, want a non-empty pdf.PDFString", desc.Entries["CharSet"])
	}
	assertCheckClearedByWrite(t, trailer, pdf.Checks.Font.Type1SubsetCharSet)
}

// TestFontSubsetMetaFixerSynthesizesCFFCharSet covers a Type1 font embedded
// as a name-keyed CFF program (FontFile3, "Type1C"): one fixture with no
// /CharSet at all, one with an empty /CharSet string.
func TestFontSubsetMetaFixerSynthesizesCFFCharSet(t *testing.T) {
	for _, path := range []string{
		"../../tests/veraPDF/PDF_A-1b/6.3 Fonts/6.3.5 Font subsets/6-3-5-t02-fail-a.pdf",
		"../../tests/veraPDF/PDF_A-1b/6.3 Fonts/6.3.5 Font subsets/6-3-5-t02-fail-b.pdf",
	} {
		t.Run(path, func(t *testing.T) {
			trailer, closeDoc := fixtureTrailer(t, path)
			defer closeDoc()

			font := findFontBySubtype(t, trailer, "Type1")
			desc := font.Entries["FontDescriptor"].(pdf.PDFDict)

			runFixerAndCheckIdempotent(t, fontSubsetMetaFixer{}, &trailer)

			cs, ok := desc.Entries["CharSet"].(pdf.PDFString)
			if !ok || cs.Value == "" {
				t.Fatalf("CharSet = %v, want a non-empty pdf.PDFString", desc.Entries["CharSet"])
			}
			assertCheckClearedByWrite(t, trailer, pdf.Checks.Font.Type1SubsetCharSet)
		})
	}
}

// TestFontSubsetMetaFixerSynthesizesCFFCIDSet covers a CIDFontType0
// (CID-keyed CFF) descriptor with no /CIDSet and one with an incomplete one.
func TestFontSubsetMetaFixerSynthesizesCFFCIDSet(t *testing.T) {
	for _, path := range []string{
		"../../tests/veraPDF/PDF_A-1b/6.3 Fonts/6.3.5 Font subsets/6-3-5-t03-fail-a.pdf",
		"../../tests/veraPDF/PDF_A-1b/6.3 Fonts/6.3.5 Font subsets/6-3-5-t03-fail-b.pdf",
		"../../tests/veraPDF/PDF_A-1b/6.3 Fonts/6.3.5 Font subsets/6-3-5-t03-fail-c.pdf",
	} {
		t.Run(path, func(t *testing.T) {
			trailer, closeDoc := fixtureTrailer(t, path)
			defer closeDoc()

			font := findFontBySubtype(t, trailer, "CIDFontType0")
			desc := font.Entries["FontDescriptor"].(pdf.PDFDict)

			runFixerAndCheckIdempotent(t, fontSubsetMetaFixer{}, &trailer)

			cidSet, ok := desc.Entries["CIDSet"].(pdf.PDFDict)
			if !ok || !cidSet.HasStream || len(cidSet.RawStream) == 0 {
				t.Fatalf("CIDSet = %v, want a non-empty stream dict", desc.Entries["CIDSet"])
			}
			if (cidSet.Entries["Filter"] != pdf.PDFName{Value: "FlateDecode"}) {
				t.Errorf("CIDSet Filter = %v, want /FlateDecode", cidSet.Entries["Filter"])
			}
			assertCheckClearedByWrite(t, trailer, pdf.Checks.Font.CIDSubsetCIDSet)
		})
	}
}

// TestFontSubsetMetaFixerRegeneratesIncompleteCharSet covers a Type1C subset
// whose CharSet omits glyph names the embedded program defines: the verifier
// must report Type1SubsetCharSet (a metadata defect), never SubsetGlyphCoverage
// (the glyphs exist), and the meta fixer must regenerate CharSet in place.
func TestFontSubsetMetaFixerRegeneratesIncompleteCharSet(t *testing.T) {
	path := "../../tests/veraPDF/PDF_A-1b/6.3 Fonts/6.3.5 Font subsets/6-3-5-t02-fail-c.pdf"
	trailer, closeDoc := fixtureTrailer(t, path)
	defer closeDoc()

	res, err := verify.VerifyFile(path, pdf.PDFA1B)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	sawCharSet := false
	for _, iss := range res.Issues {
		switch iss.Check() {
		case pdf.Checks.Font.Type1SubsetCharSet:
			sawCharSet = true
		case pdf.Checks.Font.SubsetGlyphCoverage:
			t.Errorf("SubsetGlyphCoverage reported for glyphs the program defines: %v", iss)
		}
	}
	if !sawCharSet {
		t.Fatalf("Type1SubsetCharSet not reported for incomplete CharSet")
	}

	runFixerAndCheckIdempotent(t, fontSubsetMetaFixer{}, &trailer)
	assertCheckClearedByWrite(t, trailer, pdf.Checks.Font.Type1SubsetCharSet)
}
