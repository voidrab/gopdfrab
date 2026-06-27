package convert

import (
	"bytes"
	"os"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/writer"

	"github.com/voidrab/gopdfrab/internal/verify"
)

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
	res, err := verify.Verify(doc, pdf.PDFA_1B)
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
	path := "../../test documents/Isartor testsuite/PDFA-1b/6.3 Fonts/6.3.6 Font metrics/isartor-6-3-6-t01-fail-b.pdf"
	trailer, closeDoc := fixtureTrailer(t, path)
	defer closeDoc()

	runFixerAndCheckIdempotent(t, fontMetricFixer{}, &trailer)
	assertCheckClearedByWrite(t, trailer, pdf.Checks.Font.AdvanceWidthMismatch)
}

func TestFontMetricFixerCorrectsType1Widths(t *testing.T) {
	path := "../../test documents/Isartor testsuite/PDFA-1b/6.3 Fonts/6.3.6 Font metrics/isartor-6-3-6-t01-fail-a.pdf"
	trailer, closeDoc := fixtureTrailer(t, path)
	defer closeDoc()

	runFixerAndCheckIdempotent(t, fontMetricFixer{}, &trailer)
	assertCheckClearedByWrite(t, trailer, pdf.Checks.Font.AdvanceWidthMismatch)
}

func TestFontMetricFixerCorrectsCIDTrueTypeWidths(t *testing.T) {
	path := "../../test documents/Isartor testsuite/PDFA-1b/6.3 Fonts/6.3.6 Font metrics/isartor-6-3-6-t01-fail-c.pdf"
	trailer, closeDoc := fixtureTrailer(t, path)
	defer closeDoc()

	runFixerAndCheckIdempotent(t, fontMetricFixer{}, &trailer)
	assertCheckClearedByWrite(t, trailer, pdf.Checks.Font.AdvanceWidthMismatch)
}

func TestFontMetricFixerCorrectsType3Widths(t *testing.T) {
	path := "../../test documents/veraPDF/PDF_A-1b/6.3 Fonts/6.3.6 Font metrics/veraPDF test suite 6-3-6-t01-fail-a.pdf"
	trailer, closeDoc := fixtureTrailer(t, path)
	defer closeDoc()

	runFixerAndCheckIdempotent(t, fontMetricFixer{}, &trailer)
	assertCheckClearedByWrite(t, trailer, pdf.Checks.Font.AdvanceWidthMismatch)
}

// TestFontSubsetMetaFixerSynthesizesType1CharSet covers a raw Type1 program
// (FontFile) whose descriptor lacks /CharSet entirely.
func TestFontSubsetMetaFixerSynthesizesType1CharSet(t *testing.T) {
	path := "../../test documents/Isartor testsuite/PDFA-1b/6.3 Fonts/6.3.5 Font subsets/isartor-6-3-5-t02-fail-a.pdf"
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
		"../../test documents/veraPDF/PDF_A-1b/6.3 Fonts/6.3.5 Font subsets/6-3-5-t02-fail-a.pdf",
		"../../test documents/veraPDF/PDF_A-1b/6.3 Fonts/6.3.5 Font subsets/6-3-5-t02-fail-b.pdf",
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
		"../../test documents/veraPDF/PDF_A-1b/6.3 Fonts/6.3.5 Font subsets/6-3-5-t03-fail-a.pdf",
		"../../test documents/veraPDF/PDF_A-1b/6.3 Fonts/6.3.5 Font subsets/6-3-5-t03-fail-b.pdf",
		"../../test documents/veraPDF/PDF_A-1b/6.3 Fonts/6.3.5 Font subsets/6-3-5-t03-fail-c.pdf",
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
