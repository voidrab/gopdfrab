package convert

import (
	"bytes"
	"strings"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/writer"

	"github.com/voidrab/gopdfrab/internal/verify"
)

// targetedFixture opens path, numbers its graph, and verifies it in-heap the
// way the convert loop does, returning the pass and the issues for check c.
func targetedFixture(t *testing.T, path string, c pdf.Check) (*fixPass, []pdf.PDFError, func()) {
	t.Helper()
	trailerHolder := new(pdf.PDFDict)
	trailer, closeDoc := fixtureTrailer(t, path)
	*trailerHolder = trailer

	doc, err := pdf.Open(path)
	if err != nil {
		closeDoc()
		t.Fatalf("pdf.Open(%s): %v", path, err)
	}
	objs := writer.NumberObjects(*trailerHolder)
	doc.SeedResolvedGraph(*trailerHolder, objs)
	res, err := verify.Verify(doc, pdf.PDFA_1B)
	if err != nil {
		doc.Close()
		closeDoc()
		t.Fatalf("Verify: %v", err)
	}
	issues := res.IssuesForCheck(c)
	if len(issues) == 0 {
		doc.Close()
		closeDoc()
		t.Fatalf("fixture reports no %s issues", c.Name())
	}
	pass := &fixPass{trailer: trailerHolder, objs: objs}
	return pass, issues, func() { doc.Close(); closeDoc() }
}

// runTargetedAndCheckIdempotent asserts fixTargeted handles the batch, changes
// the graph on the first call, and is a no-op on the second.
func runTargetedAndCheckIdempotent(t *testing.T, tf targetedFixer, pass *fixPass, issues []pdf.PDFError) {
	t.Helper()
	changed, handled, err := tf.fixTargeted(pass, issues)
	if err != nil {
		t.Fatalf("fixTargeted: %v", err)
	}
	if !handled {
		t.Fatalf("handled = false, want targeted handling (all issues carry refs)")
	}
	if !changed {
		t.Fatalf("changed = false, want true")
	}
	changed, handled, err = tf.fixTargeted(pass, issues)
	if err != nil {
		t.Fatalf("fixTargeted (second pass): %v", err)
	}
	if !handled {
		t.Fatalf("handled = false on second pass, want true")
	}
	if changed {
		t.Errorf("changed = true on second pass, want false (targeted fix must be idempotent)")
	}
}

func TestFontMetricFixerTargetsIssueRefs(t *testing.T) {
	path := "../../tests/Isartor/PDFA-1b/6.3 Fonts/6.3.6 Font metrics/isartor-6-3-6-t01-fail-b.pdf"
	pass, issues, done := targetedFixture(t, path, pdf.Checks.Font.AdvanceWidthMismatch)
	defer done()

	runTargetedAndCheckIdempotent(t, fontMetricFixer{}, pass, issues)
	assertCheckClearedByWrite(t, *pass.trailer, pdf.Checks.Font.AdvanceWidthMismatch)
}

func TestFontSubsetMetaFixerTargetsIssueRefs(t *testing.T) {
	path := "../../tests/Isartor/PDFA-1b/6.3 Fonts/6.3.5 Font subsets/isartor-6-3-5-t02-fail-a.pdf"
	pass, issues, done := targetedFixture(t, path, pdf.Checks.Font.Type1SubsetCharSet)
	defer done()

	runTargetedAndCheckIdempotent(t, fontSubsetMetaFixer{}, pass, issues)
	assertCheckClearedByWrite(t, *pass.trailer, pdf.Checks.Font.Type1SubsetCharSet)
}

// appearanceTargetWidget builds a minimal widget annotation with no /AP.
func appearanceTargetWidget() pdf.PDFDict {
	w := pdf.NewPDFDict()
	w.Entries["Type"] = pdf.PDFName{Value: "Annot"}
	w.Entries["Subtype"] = pdf.PDFName{Value: "Widget"}
	w.Entries["Rect"] = pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFInteger(100), pdf.PDFInteger(20)}
	w.Entries["_ref"] = pdf.PDFRef{ObjNum: 90}
	return w
}

// TestAppearanceFixerTargetsOnlyFlaggedAnnots documents the targeted
// contract: the verifier reports every violating annotation per pass, so
// fixTargeted may leave an unflagged (but equally violating) one untouched.
func TestAppearanceFixerTargetsOnlyFlaggedAnnots(t *testing.T) {
	flagged, other := appearanceTargetWidget(), appearanceTargetWidget()
	root := pdf.NewPDFDict()
	root.Entries["_ref"] = pdf.PDFRef{ObjNum: 91}
	root.Entries["Annots"] = pdf.PDFArray{flagged, other}
	trailer := pdf.NewPDFDict()
	trailer.Entries["Root"] = root

	objs := writer.NumberObjects(trailer)
	pass := &fixPass{trailer: &trailer, objs: objs}
	ref := flagged.Entries["_ref"].(pdf.PDFRef)
	issue := pdf.NewError(pdf.Checks.Annotation.MissingAppearance, nil, 1, &ref)

	changed, handled, err := appearanceFixer{}.fixTargeted(pass, []pdf.PDFError{issue})
	if err != nil {
		t.Fatalf("fixTargeted: %v", err)
	}
	if !handled || !changed {
		t.Fatalf("handled=%v changed=%v, want true/true", handled, changed)
	}
	if _, ok := flagged.Entries["AP"].(pdf.PDFDict); !ok {
		t.Error("flagged widget got no /AP")
	}
	if _, ok := other.Entries["AP"]; ok {
		t.Error("unflagged widget was touched by the targeted fix")
	}

	// A ref-less issue in the batch must force the full-walk fallback, which
	// then fixes the remaining widget too.
	noRef := pdf.NewError(pdf.Checks.Annotation.MissingAppearance, nil, 1, nil)
	_, handled, err = appearanceFixer{}.fixTargeted(pass, []pdf.PDFError{noRef})
	if err != nil {
		t.Fatalf("fixTargeted: %v", err)
	}
	if handled {
		t.Fatal("handled = true with a ref-less issue, want fallback")
	}
	if _, err := (appearanceFixer{}).Fix(&trailer, nil); err != nil {
		t.Fatalf("Fix fallback: %v", err)
	}
	if _, ok := other.Entries["AP"].(pdf.PDFDict); !ok {
		t.Error("full-walk fallback did not fix the remaining widget")
	}
}

// cmapStreamDict builds a CMap stream whose cidrange CID exceeds the 65535
// limit, undeflated so DecodeStream returns it as-is.
func cmapStreamDict(objNum int) pdf.PDFDict {
	content := "1 begincidrange\n<0000> <00ff> 70000\nendcidrange\n"
	return pdf.PDFDict{
		Entries: map[string]pdf.PDFValue{
			"Type": pdf.PDFName{Value: "CMap"},
			"_ref": pdf.PDFRef{ObjNum: objNum},
		},
		HasStream: true,
		RawStream: []byte(content),
	}
}

// TestCmapCIDClampFixerTargetsIssueRefs shares the flagged CMap between two
// graph slots: the targeted rewrite must reach both (stream fields do not
// propagate through the shared Entries map) and leave an unflagged, equally
// violating CMap untouched.
func TestCmapCIDClampFixerTargetsIssueRefs(t *testing.T) {
	flagged, other := cmapStreamDict(20), cmapStreamDict(21)
	root := pdf.NewPDFDict()
	root.Entries["_ref"] = pdf.PDFRef{ObjNum: 1}
	root.Entries["Enc"] = flagged
	root.Entries["List"] = pdf.PDFArray{flagged, other}
	trailer := pdf.NewPDFDict()
	trailer.Entries["Root"] = root

	objs := writer.NumberObjects(trailer)
	pass := &fixPass{trailer: &trailer, objs: objs}
	ref := flagged.Entries["_ref"].(pdf.PDFRef)
	issue := pdf.NewError(pdf.Checks.Structure.CMapCIDOutOfRange, nil, 0, &ref)

	changed, handled, err := cmapCIDClampFixer{}.fixTargeted(pass, []pdf.PDFError{issue})
	if err != nil {
		t.Fatalf("fixTargeted: %v", err)
	}
	if !handled || !changed {
		t.Fatalf("handled=%v changed=%v, want true/true", handled, changed)
	}

	for slot, d := range map[string]pdf.PDFDict{
		"Enc":     root.Entries["Enc"].(pdf.PDFDict),
		"List[0]": root.Entries["List"].(pdf.PDFArray)[0].(pdf.PDFDict),
	} {
		data, err := pdf.DecodeStream(d)
		if err != nil {
			t.Fatalf("DecodeStream(%s): %v", slot, err)
		}
		if !bytes.Contains(data, []byte("65535")) || bytes.Contains(data, []byte("70000")) {
			t.Errorf("%s not clamped: %q", slot, data)
		}
	}
	if got := root.Entries["List"].(pdf.PDFArray)[1].(pdf.PDFDict); !bytes.Contains(got.RawStream, []byte("70000")) {
		t.Errorf("unflagged CMap was touched by the targeted fix: %q", got.RawStream)
	}
}

func TestContentLimitsFixerTargetsIssueRefs(t *testing.T) {
	path := "../../tests/Isartor/PDFA-1b/6.2 Graphics/6.2.10 Content Streams/isartor-6-2-10-t01-fail-a.pdf"
	pass, issues, done := targetedFixture(t, path, pdf.Checks.Colour.UndefinedOperator)
	defer done()

	runTargetedAndCheckIdempotent(t, contentLimitsFixer{}, pass, issues)
	assertCheckClearedByWrite(t, *pass.trailer, pdf.Checks.Colour.UndefinedOperator)
}

// TestContentLimitsFixerTargetsOwnedScalars covers the graph-scalar flavour:
// the verifier reports a scalar violation against its owning dict, and the
// targeted fix clamps exactly that dict's owned scalars (entries and nested
// arrays, but not child dicts, which are their own targets).
func TestContentLimitsFixerTargetsOwnedScalars(t *testing.T) {
	plain := pdf.NewPDFDict()
	plain.Entries["_ref"] = pdf.PDFRef{ObjNum: 30}
	plain.Entries["Big"] = pdf.PDFInteger(3_000_000_000)
	plain.Entries["List"] = pdf.PDFArray{pdf.PDFReal(40000)}
	child := pdf.NewPDFDict()
	child.Entries["Big"] = pdf.PDFInteger(3_000_000_000)
	plain.Entries["Child"] = child
	root := pdf.NewPDFDict()
	root.Entries["_ref"] = pdf.PDFRef{ObjNum: 31}
	root.Entries["Thing"] = plain
	trailer := pdf.NewPDFDict()
	trailer.Entries["Root"] = root

	objs := writer.NumberObjects(trailer)
	pass := &fixPass{trailer: &trailer, objs: objs}
	ref := plain.Entries["_ref"].(pdf.PDFRef)
	issue := pdf.NewError(pdf.Checks.Structure.IntegerOutOfRange, nil, 0, &ref)

	changed, handled, err := contentLimitsFixer{}.fixTargeted(pass, []pdf.PDFError{issue})
	if err != nil {
		t.Fatalf("fixTargeted: %v", err)
	}
	if !handled || !changed {
		t.Fatalf("handled=%v changed=%v, want true/true", handled, changed)
	}
	if got := plain.Entries["Big"]; got != pdf.PDFInteger(2147483647) {
		t.Errorf("owned integer = %v, want clamped to 2147483647", got)
	}
	if got := plain.Entries["List"].(pdf.PDFArray)[0]; got != pdf.PDFReal(32767) {
		t.Errorf("owned array real = %v, want clamped to 32767", got)
	}
	if got := child.Entries["Big"]; got != pdf.PDFInteger(3_000_000_000) {
		t.Errorf("child dict scalar = %v, want untouched (child is its own target)", got)
	}
}

// TestContentLimitsFixerTargetedNeverRewritesNonContentStreams guards the
// corruption hazard: a stream flagged for an out-of-range entry value (e.g.
// an image XObject) must keep its bytes; only walkContentStreams' dispatch
// set gets the content rewrite.
func TestContentLimitsFixerTargetedNeverRewritesNonContentStreams(t *testing.T) {
	raw := []byte{0x00, 0x01, 0xfe, 0xff, ' ', 'q', ' ', 0x03}
	img := pdf.PDFDict{
		Entries: map[string]pdf.PDFValue{
			"Subtype": pdf.PDFName{Value: "Image"},
			"Bad":     pdf.PDFInteger(3_000_000_000),
			"_ref":    pdf.PDFRef{ObjNum: 50},
		},
		HasStream: true,
		RawStream: raw,
	}
	root := pdf.NewPDFDict()
	root.Entries["_ref"] = pdf.PDFRef{ObjNum: 51}
	root.Entries["Img"] = img
	trailer := pdf.NewPDFDict()
	trailer.Entries["Root"] = root

	objs := writer.NumberObjects(trailer)
	pass := &fixPass{trailer: &trailer, objs: objs}
	ref := img.Entries["_ref"].(pdf.PDFRef)
	issue := pdf.NewError(pdf.Checks.Structure.IntegerOutOfRange, nil, 0, &ref)

	changed, handled, err := contentLimitsFixer{}.fixTargeted(pass, []pdf.PDFError{issue})
	if err != nil {
		t.Fatalf("fixTargeted: %v", err)
	}
	if !handled || !changed {
		t.Fatalf("handled=%v changed=%v, want true/true", handled, changed)
	}
	if got := root.Entries["Img"].(pdf.PDFDict); !bytes.Equal(got.RawStream, raw) {
		t.Errorf("image stream bytes were rewritten: %v", got.RawStream)
	}
	if got := img.Entries["Bad"]; got != pdf.PDFInteger(2147483647) {
		t.Errorf("entry scalar = %v, want clamped", got)
	}
}

// TestNameTooLongFixerTargetsValueFlavour covers overlong name values owned
// by the flagged dict, directly and inside arrays.
func TestNameTooLongFixerTargetsValueFlavour(t *testing.T) {
	long := strings.Repeat("N", maxNameLength+5)
	d := pdf.NewPDFDict()
	d.Entries["_ref"] = pdf.PDFRef{ObjNum: 60}
	d.Entries["Name"] = pdf.PDFName{Value: long}
	d.Entries["Arr"] = pdf.PDFArray{pdf.PDFName{Value: long}}
	root := pdf.NewPDFDict()
	root.Entries["_ref"] = pdf.PDFRef{ObjNum: 61}
	root.Entries["D"] = d
	trailer := pdf.NewPDFDict()
	trailer.Entries["Root"] = root

	objs := writer.NumberObjects(trailer)
	pass := &fixPass{trailer: &trailer, objs: objs}
	ref := d.Entries["_ref"].(pdf.PDFRef)
	issue := pdf.NewError(pdf.Checks.Structure.NameTooLong, nil, 0, &ref)

	changed, handled, err := nameTooLongFixer{}.fixTargeted(pass, []pdf.PDFError{issue})
	if err != nil {
		t.Fatalf("fixTargeted: %v", err)
	}
	if !handled || !changed {
		t.Fatalf("handled=%v changed=%v, want true/true", handled, changed)
	}
	want := long[:maxNameLength]
	if got := d.Entries["Name"].(pdf.PDFName).Value; got != want {
		t.Errorf("entry name len = %d, want truncated to %d", len(got), maxNameLength)
	}
	if got := d.Entries["Arr"].(pdf.PDFArray)[0].(pdf.PDFName).Value; got != want {
		t.Errorf("array name len = %d, want truncated to %d", len(got), maxNameLength)
	}
}

func TestNameTooLongFixerTargetsKeyFlavourRefs(t *testing.T) {
	longKey := strings.Repeat("A", maxNameLength+10)
	makeDict := func(objNum int) pdf.PDFDict {
		d := pdf.NewPDFDict()
		d.Entries["_ref"] = pdf.PDFRef{ObjNum: objNum}
		d.Entries[longKey] = pdf.PDFInteger(1)
		return d
	}
	flagged, other := makeDict(40), makeDict(41)
	root := pdf.NewPDFDict()
	root.Entries["_ref"] = pdf.PDFRef{ObjNum: 42}
	root.Entries["A"] = flagged
	root.Entries["B"] = other
	trailer := pdf.NewPDFDict()
	trailer.Entries["Root"] = root

	objs := writer.NumberObjects(trailer)
	pass := &fixPass{trailer: &trailer, objs: objs}
	ref := flagged.Entries["_ref"].(pdf.PDFRef)
	issue := pdf.NewError(pdf.Checks.Structure.NameTooLong, nil, 0, &ref)

	changed, handled, err := nameTooLongFixer{}.fixTargeted(pass, []pdf.PDFError{issue})
	if err != nil {
		t.Fatalf("fixTargeted: %v", err)
	}
	if !handled || !changed {
		t.Fatalf("handled=%v changed=%v, want true/true", handled, changed)
	}
	if _, exists := flagged.Entries[longKey]; exists {
		t.Error("flagged dict still has the overlong key")
	}
	if _, exists := flagged.Entries[longKey[:maxNameLength-8]]; !exists {
		t.Error("flagged dict has no shortened replacement key")
	}
	if _, exists := other.Entries[longKey]; !exists {
		t.Error("unflagged dict was touched by the targeted fix")
	}
}

func TestFontMetricFixerTargetedFallsBackWithoutRefs(t *testing.T) {
	path := "../../tests/Isartor/PDFA-1b/6.3 Fonts/6.3.6 Font metrics/isartor-6-3-6-t01-fail-b.pdf"
	pass, issues, done := targetedFixture(t, path, pdf.Checks.Font.AdvanceWidthMismatch)
	defer done()

	noRef := pdf.NewError(pdf.Checks.Font.AdvanceWidthMismatch, nil, 0, nil)
	_, handled, err := fontMetricFixer{}.fixTargeted(pass, append(issues, noRef))
	if err != nil {
		t.Fatalf("fixTargeted: %v", err)
	}
	if handled {
		t.Fatal("handled = true with a ref-less issue in the batch, want full-walk fallback")
	}
}
