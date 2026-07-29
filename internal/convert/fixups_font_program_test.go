package convert

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"sort"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfgen"
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
	if err := writer.WriteDocument(&buf, trailer, 0); err != nil {
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
		if ok || (d.Entries.Get("Type") != pdf.PDFName{Value: "Font"}) {
			return
		}
		st, _ := d.Entries.Get("Subtype").(pdf.PDFName)
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
	ff := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{}), HasStream: true, RawStream: ttf}
	desc := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"FontFile2": ff})}
	font := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Subtype":        pdf.PDFName{Value: "CIDFontType2"},
		"FontDescriptor": desc,
	})}
	trailer := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Font": font})}

	if err := promoteEmptyGlyphsInFonts(&trailer, nil); err != nil {
		t.Fatalf("promoteEmptyGlyphsInFonts: %v", err)
	}
	desc = trailer.Entries.Get("Font").(pdf.PDFDict).Entries.Get("FontDescriptor").(pdf.PDFDict)
	ff1, ok := desc.Entries.Get("FontFile2").(pdf.PDFDict)
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
	desc = trailer.Entries.Get("Font").(pdf.PDFDict).Entries.Get("FontDescriptor").(pdf.PDFDict)
	ff2 := desc.Entries.Get("FontFile2").(pdf.PDFDict)
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
	v := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"W": pdf.PDFArray{pdf.PDFInteger(1), pdf.PDFArray{pdf.PDFInteger(500), pdf.PDFInteger(500)}},
	})}

	if !fixCIDCFFWidths(v, ff) {
		t.Fatalf("fixCIDCFFWidths = false, want true (500/500 mismatches the embedded 600/700)")
	}
	want := map[int]int{1: 600, 2: 700}
	for _, pair := range verify.ParseCIDWidths(v.Entries.Get("W").(pdf.PDFArray)) {
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
	if fixCIDCFFWidths(pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{})}, ff) {
		t.Error("fixCIDCFFWidths without /W = true, want false")
	}
}

// TestFixTrueTypeCIDSetSkipsNonIdentityCIDToGIDMap covers the CID!=GID guard:
// a stream (non-/Identity) CIDToGIDMap means CIDs don't correspond to GIDs
// directly, so fixTrueTypeCIDSet must bail out without touching desc.
func TestFixTrueTypeCIDSetSkipsNonIdentityCIDToGIDMap(t *testing.T) {
	ttf := loadLiberationSansForTest(t)
	d := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"CIDToGIDMap": pdf.PDFDict{HasStream: true, RawStream: []byte{0, 1, 0, 2}},
	})}
	desc := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{})}
	ff := pdf.PDFDict{HasStream: true, RawStream: ttf}

	if fixTrueTypeCIDSet(d, desc, ff) {
		t.Error("fixTrueTypeCIDSet with a stream CIDToGIDMap = true, want false")
	}
	if desc.Entries.Get("CIDSet") != nil {
		t.Error("desc/CIDSet was populated despite the non-Identity CIDToGIDMap guard")
	}
}

// TestFixTrueTypeCIDSetNoOpWhenAlreadyComplete covers the already-complete
// /CIDSet no-op branch via a real embedded TrueType program.
func TestFixTrueTypeCIDSetNoOpWhenAlreadyComplete(t *testing.T) {
	ttf := loadLiberationSansForTest(t)
	d := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{})}
	desc := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{})}
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
	desc := font.Entries.Get("FontDescriptor").(pdf.PDFDict)
	if desc.Entries.Get("CharSet") != nil {
		t.Fatalf("fixture precondition failed: CharSet already present")
	}

	runFixerAndCheckIdempotent(t, fontSubsetMetaFixer{}, &trailer)

	cs, ok := desc.Entries.Get("CharSet").(pdf.PDFString)
	if !ok || cs.Value == "" {
		t.Fatalf("CharSet = %v, want a non-empty pdf.PDFString", desc.Entries.Get("CharSet"))
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
			desc := font.Entries.Get("FontDescriptor").(pdf.PDFDict)

			runFixerAndCheckIdempotent(t, fontSubsetMetaFixer{}, &trailer)

			cs, ok := desc.Entries.Get("CharSet").(pdf.PDFString)
			if !ok || cs.Value == "" {
				t.Fatalf("CharSet = %v, want a non-empty pdf.PDFString", desc.Entries.Get("CharSet"))
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
			desc := font.Entries.Get("FontDescriptor").(pdf.PDFDict)

			runFixerAndCheckIdempotent(t, fontSubsetMetaFixer{}, &trailer)

			cidSet, ok := desc.Entries.Get("CIDSet").(pdf.PDFDict)
			if !ok || !cidSet.HasStream || len(cidSet.RawStream) == 0 {
				t.Fatalf("CIDSet = %v, want a non-empty stream dict", desc.Entries.Get("CIDSet"))
			}
			if (cidSet.Entries.Get("Filter") != pdf.PDFName{Value: "FlateDecode"}) {
				t.Errorf("CIDSet Filter = %v, want /FlateDecode", cidSet.Entries.Get("Filter"))
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

	res, err := verify.VerifyFile(path, pdf.PDFA1B, nil)
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

func TestFontFileSubtypeFixerAppliesOnlyToFontFileSubtype(t *testing.T) {
	fixer := fontFileSubtypeFixer{}
	for _, c := range pdf.AllChecks() {
		want := c == pdf.Checks.Font.FontFileSubtype
		if got := fixer.Applies(c); got != want {
			t.Errorf("Applies(%s/%d) = %v, want %v", c.Clause(), c.Subclause(), got, want)
		}
	}
}

// buildMinimalCFF assembles a tiny name-keyed CFF font with two glyphs
// (.notdef and "A"), a format-0 charset, and an empty Private DICT. Ported
// from verify.buildMinimalCFF, which is test-only and so not importable.
func buildMinimalCFF() []byte {
	i32 := func(v int) []byte {
		var b [5]byte
		b[0] = 29
		binary.BigEndian.PutUint32(b[1:], uint32(v))
		return b[:]
	}
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
	// Top DICT INDEX: 1 entry.
	var top []byte
	top = append(top, i32(charStringsOff)...)
	top = append(top, 17) // CharStrings
	top = append(top, i32(charsetOff)...)
	top = append(top, 15)                 // charset
	top = append(top, i32(4)...)          // Private size
	top = append(top, i32(privateOff)...) // Private offset
	top = append(top, 18)                 // Private
	cff = append(cff, 0x00, 0x01, 0x01, 0x01, byte(len(top)+1))
	cff = append(cff, top...)
	// String INDEX and Global Subr INDEX: both empty.
	cff = append(cff, 0x00, 0x00, 0x00, 0x00)
	// charset: format 0, one SID (34 = "A") for glyph 1.
	cff = append(cff, 0x00, 0x00, 0x22)
	// CharStrings INDEX: two glyphs, each a lone endchar.
	cff = append(cff, 0x00, 0x02, 0x01, 0x01, 0x02, 0x03, 0x0e, 0x0e)
	// Private DICT: defaultWidthX 0, nominalWidthX 0.
	cff = append(cff, 0x8b, 20, 0x8b, 21)
	return cff
}

// wrapInSfnt packs tables into an sfnt container with the given version tag,
// so tests can build the OpenType wrappers the fixer has to unwrap.
func wrapInSfnt(tag uint32, tables map[string][]byte) []byte {
	names := make([]string, 0, len(tables))
	for name := range tables {
		names = append(names, name)
	}
	sort.Strings(names)

	head := make([]byte, 12+16*len(names))
	binary.BigEndian.PutUint32(head[0:4], tag)
	binary.BigEndian.PutUint16(head[4:6], uint16(len(names)))
	body := []byte{}
	off := len(head)
	for i, name := range names {
		rec := head[12+16*i:]
		copy(rec[:4], name)
		binary.BigEndian.PutUint32(rec[8:12], uint32(off+len(body)))
		binary.BigEndian.PutUint32(rec[12:16], uint32(len(tables[name])))
		body = append(body, tables[name]...)
	}
	return append(head, body...)
}

// fontWithFontFile3 builds a trailer holding one font of the given subtype
// whose descriptor embeds program under FontFile3 with the given /Subtype.
func fontWithFontFile3(fontSubtype, fileSubtype string, program []byte) pdf.PDFDict {
	ff := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{}), HasStream: true, RawStream: program}
	if fileSubtype != "" {
		ff.Entries.Set("Subtype", pdf.PDFName{Value: fileSubtype})
	}
	desc := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"FontFile3": ff})}
	font := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type":           pdf.PDFName{Value: "Font"},
		"Subtype":        pdf.PDFName{Value: fontSubtype},
		"FontDescriptor": desc,
	})}
	return pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Font": font})}
}

// descriptorOf returns the descriptor of the single font in a trailer built by
// fontWithFontFile3.
func descriptorOf(t *testing.T, trailer pdf.PDFDict) pdf.PDFDict {
	t.Helper()
	font, ok := trailer.Entries.Get("Font").(pdf.PDFDict)
	if !ok {
		t.Fatalf("trailer has no font")
	}
	desc, ok := font.Entries.Get("FontDescriptor").(pdf.PDFDict)
	if !ok {
		t.Fatalf("font has no descriptor")
	}
	return desc
}

// TestFontFileSubtypeFixerUnwrapsOpenTypeCFF covers the common case: the CFF
// table inside an OpenType wrapper becomes the font file itself, keeping the
// document's own glyphs rather than substituting a bundled face.
func TestFontFileSubtypeFixerUnwrapsOpenTypeCFF(t *testing.T) {
	cff := buildMinimalCIDCFF()
	otf := wrapInSfnt(0x4F54544F, map[string][]byte{"CFF ": cff, "head": make([]byte, 54)})

	for _, tc := range []struct {
		fontSubtype string
		wantSubtype string
	}{
		{"CIDFontType0", "CIDFontType0C"},
		{"Type1", "CIDFontType0C"}, // the CFF itself is CID-keyed
	} {
		t.Run(tc.fontSubtype, func(t *testing.T) {
			trailer := fontWithFontFile3(tc.fontSubtype, "OpenType", otf)
			runFixerAndCheckIdempotent(t, fontFileSubtypeFixer{}, &trailer)

			ff, ok := descriptorOf(t, trailer).Entries.Get("FontFile3").(pdf.PDFDict)
			if !ok {
				t.Fatalf("FontFile3 missing after fix")
			}
			if (ff.Entries.Get("Subtype") != pdf.PDFName{Value: tc.wantSubtype}) {
				t.Errorf("Subtype = %v, want /%s", ff.Entries.Get("Subtype"), tc.wantSubtype)
			}
			got, err := pdf.DecodeStream(ff)
			if err != nil {
				t.Fatalf("DecodeStream: %v", err)
			}
			if !bytes.Equal(got, cff) {
				t.Error("the unwrapped stream is not the CFF table the wrapper carried")
			}
		})
	}
}

// TestFontFileSubtypeFixerRelabelsBareCFF covers a bare CFF that only misnames
// itself: the stream is already what PDF 1.4 wants, so only the name changes.
func TestFontFileSubtypeFixerRelabelsBareCFF(t *testing.T) {
	cff := buildMinimalCFF()
	trailer := fontWithFontFile3("Type1", "OpenType", cff)
	runFixerAndCheckIdempotent(t, fontFileSubtypeFixer{}, &trailer)

	ff := descriptorOf(t, trailer).Entries.Get("FontFile3").(pdf.PDFDict)
	if (ff.Entries.Get("Subtype") != pdf.PDFName{Value: "Type1C"}) {
		t.Errorf("Subtype = %v, want /Type1C", ff.Entries.Get("Subtype"))
	}
	if !bytes.Equal(ff.RawStream, cff) {
		t.Error("a bare CFF was re-encoded, want it left untouched")
	}
}

// TestFontFileSubtypeFixerMovesTrueTypeToFontFile2 covers a glyf-flavoured
// wrapper under a TrueType font: the program belongs under FontFile2, which
// has no Subtype key for the check to object to.
func TestFontFileSubtypeFixerMovesTrueTypeToFontFile2(t *testing.T) {
	ttf := loadLiberationSansForTest(t)
	trailer := fontWithFontFile3("CIDFontType2", "OpenType", ttf)
	runFixerAndCheckIdempotent(t, fontFileSubtypeFixer{}, &trailer)

	desc := descriptorOf(t, trailer)
	if desc.Entries.Get("FontFile3") != nil {
		t.Error("FontFile3 still present after the program moved to FontFile2")
	}
	ff, ok := desc.Entries.Get("FontFile2").(pdf.PDFDict)
	if !ok {
		t.Fatalf("FontFile2 missing after fix")
	}
	if ff.Entries.Get("Subtype") != nil {
		t.Error("FontFile2 carries a Subtype, which PDF 1.4 does not define for it")
	}
	if ff.Entries.Get("Length1") != pdf.PDFInteger(len(ttf)) {
		t.Errorf("Length1 = %v, want %d", ff.Entries.Get("Length1"), len(ttf))
	}
	got, err := pdf.DecodeStream(ff)
	if err != nil {
		t.Fatalf("DecodeStream: %v", err)
	}
	if !bytes.Equal(got, ttf) {
		t.Error("the moved program is not the one the wrapper carried")
	}
}

// TestFontFileSubtypeFixerLeavesUnusableProgram covers what the fixer must not
// do: a TrueType wrapper under a font that cannot read TrueType glyphs, and a
// stream that is no font at all, are both left for the substitution fixer.
func TestFontFileSubtypeFixerLeavesUnusableProgram(t *testing.T) {
	ttf := loadLiberationSansForTest(t)
	for _, tc := range []struct {
		name        string
		fontSubtype string
		program     []byte
	}{
		{"truetype under a Type1 font", "Type1", ttf},
		{"not a font at all", "Type1", []byte("neither CFF nor sfnt")},
		{"empty stream", "Type1", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			trailer := fontWithFontFile3(tc.fontSubtype, "OpenType", tc.program)
			changed, err := fontFileSubtypeFixer{}.Fix(&trailer, nil)
			if err != nil {
				t.Fatalf("Fix: %v", err)
			}
			if changed {
				t.Error("changed = true, want false: this program cannot be unwrapped")
			}
		})
	}
}

// TestFontFileSubtypeFixerDropsSpuriousSubtype covers FontFile and FontFile2,
// which have no Subtype key at all, so one there just goes away.
func TestFontFileSubtypeFixerDropsSpuriousSubtype(t *testing.T) {
	for _, key := range []string{"FontFile", "FontFile2"} {
		t.Run(key, func(t *testing.T) {
			ff := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
				"Subtype": pdf.PDFName{Value: "OpenType"},
			}), HasStream: true, RawStream: []byte("program")}
			desc := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{key: ff})}
			font := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
				"Type":           pdf.PDFName{Value: "Font"},
				"Subtype":        pdf.PDFName{Value: "TrueType"},
				"FontDescriptor": desc,
			})}
			trailer := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Font": font})}

			runFixerAndCheckIdempotent(t, fontFileSubtypeFixer{}, &trailer)
			got := descriptorOf(t, trailer).Entries.Get(key).(pdf.PDFDict)
			if got.Entries.Get("Subtype") != nil {
				t.Errorf("%s still carries a Subtype", key)
			}
		})
	}
}

// TestFontFileSubtypeFixerSkipsConformingFonts covers the no-op cases: a font
// file with a legal Subtype or none at all is never touched.
func TestFontFileSubtypeFixerSkipsConformingFonts(t *testing.T) {
	for _, subtype := range []string{"", "Type1C", "CIDFontType0C"} {
		trailer := fontWithFontFile3("Type1", subtype, buildMinimalCFF())
		changed, err := fontFileSubtypeFixer{}.Fix(&trailer, nil)
		if err != nil {
			t.Fatalf("Fix: %v", err)
		}
		if changed {
			t.Errorf("Subtype %q: changed = true, want false", subtype)
		}
	}
}

// TestFontFileSubtypeFixerSkipsNonFonts covers the guards: a dict that is not
// a font, and a font with no descriptor, are both left alone.
func TestFontFileSubtypeFixerSkipsNonFonts(t *testing.T) {
	notAFont := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Subtype": pdf.PDFName{Value: "Type1"},
	})}
	if fixFontFileSubtypeDict(notAFont) {
		t.Error("a dict without /Type /Font was treated as a font")
	}
	noDescriptor := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type":    pdf.PDFName{Value: "Font"},
		"Subtype": pdf.PDFName{Value: "Type1"},
	})}
	if fixFontFileSubtypeDict(noDescriptor) {
		t.Error("a font without a descriptor reported a change")
	}
}

// TestConvertClearsOpenTypeFontFile is the end-to-end proof: a document whose
// only defect class includes an OpenType-wrapped font file converts to
// conformance with the CFF unwrapped in place, not substituted away.
func TestConvertClearsOpenTypeFontFile(t *testing.T) {
	cff := buildMinimalCFF()
	otf := wrapInSfnt(0x4F54544F, map[string][]byte{"CFF ": cff, "head": make([]byte, 54)})

	b := pdfgen.NewBuilder("%PDF-1.4\n")
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.Obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] "+
		"/Resources << /Font << /F1 4 0 R >> >> /Contents 7 0 R >>")
	b.Obj(4, "<< /Type /Font /Subtype /Type1 /BaseFont /Font /FirstChar 65 /LastChar 65 "+
		"/Widths [0] /FontDescriptor 5 0 R >>")
	b.Obj(5, "<< /Type /FontDescriptor /FontName /Font /Flags 4 /ItalicAngle 0 "+
		"/Ascent 700 /Descent -200 /CapHeight 700 /StemV 80 /FontBBox [0 0 100 100] "+
		"/FontFile3 6 0 R >>")
	b.StreamObj(6, fmt.Sprintf("<< /Subtype /OpenType /Length %d >>", len(otf)), otf)
	content := []byte("BT /F1 12 Tf 10 100 Td (A) Tj ET")
	b.StreamObj(7, fmt.Sprintf("<< /Length %d >>", len(content)), content)
	data := b.FinishClassic("<< /Size 8 /Root 1 0 R >>")

	res, err := verify.VerifyBytes(data, pdf.PDFA1B, nil)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	found := false
	for _, iss := range res.Issues {
		if iss.Check() == pdf.Checks.Font.FontFileSubtype {
			found = true
		}
	}
	if !found {
		t.Fatal("sanity: FontFileSubtype not reported for an OpenType font file")
	}

	cr, err := ConvertBytes(data, pdf.PDFA1B, Options{})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	defer cr.Close()
	for _, iss := range cr.Result.Issues {
		if iss.Check() == pdf.Checks.Font.FontFileSubtype {
			t.Errorf("FontFileSubtype survived conversion: %v", iss)
		}
	}
	if len(cr.RasterizedPages) != 0 {
		t.Errorf("page %v was rasterized; the font file should have been unwrapped instead", cr.RasterizedPages)
	}
}
