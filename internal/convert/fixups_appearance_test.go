package convert

import (
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// TestAppearanceFixerAppliesExclusively checks the Fixer/Applies contract
// (mirroring fixups_font_test.go's pattern): the fixer must claim exactly
// its five target checks and nothing else, since registerFixer panics on a
// Check claimed by more than one Fixer.
func TestAppearanceFixerAppliesExclusively(t *testing.T) {
	fixer := appearanceFixer{}
	want := map[pdf.Check]bool{
		pdf.Checks.Form.WidgetMissingAppearance:      true,
		pdf.Checks.Annotation.MissingAppearance:      true,
		pdf.Checks.Annotation.AppearanceMissingN:     true,
		pdf.Checks.Annotation.AppearanceExtraEntries: true,
		pdf.Checks.Annotation.AppearanceNNotStream:   true,
	}
	for _, c := range pdf.AllChecks() {
		if got := fixer.Applies(c); got != want[c] {
			t.Errorf("Applies(%s) = %v, want %v", c.Name(), got, want[c])
		}
	}
}

func minimalTrailer() pdf.PDFDict {
	trailer := pdf.NewPDFDict()
	trailer.Entries.Set("Root", pdf.NewPDFDict())
	return trailer
}

// contentBytesOf decodes a Form XObject stream's content, tolerating an
// unset /Filter (the fixer leaves new streams undecoded/plaintext until
// WriteDocument's MarkStreamDirty-driven re-encode).
func contentBytesOf(t *testing.T, xobj pdf.PDFDict) []byte {
	t.Helper()
	data, err := pdf.DecodeStream(xobj)
	if err != nil {
		t.Fatalf("decodeStream: %v", err)
	}
	return data
}

// TestAppearanceFixerSynthesizesTextFieldAppearance builds a text-field
// widget with a value and no /AP, runs the fixer, and checks that /AP/N
// becomes a Form XObject stream whose content draws the field's text using
// the bundled appearance font, then checks that a second pass is a no-op
// (idempotent, required for the bounded convert loop to terminate).
func TestAppearanceFixerSynthesizesTextFieldAppearance(t *testing.T) {
	widget := pdf.NewPDFDict()
	widget.Entries.Set("Type", pdf.PDFName{Value: "Annot"})
	widget.Entries.Set("Subtype", pdf.PDFName{Value: "Widget"})
	widget.Entries.Set("FT", pdf.PDFName{Value: "Tx"})
	widget.Entries.Set("Rect", pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFInteger(100), pdf.PDFInteger(20)})
	widget.Entries.Set("V", pdf.PDFString{Value: "John Doe"})
	widget.Entries.Set("DA", pdf.PDFString{Value: "/Helv 10 Tf 0 g"})

	trailer := minimalTrailer()
	trailer.Entries.Get("Root").(pdf.PDFDict).Entries.Set("AcroForm", pdf.NewPDFDict())
	trailer.Entries.Set("Widget", widget) // keep it reachable for walkDicts

	fixer := appearanceFixer{}
	changed, err := fixer.Fix(&trailer, nil)
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if !changed {
		t.Fatalf("changed = false, want true (AP was missing)")
	}

	ap, ok := widget.Entries.Get("AP").(pdf.PDFDict)
	if !ok {
		t.Fatalf("AP = %#v, want a pdf.PDFDict", widget.Entries.Get("AP"))
	}
	n, ok := ap.Entries.Get("N").(pdf.PDFDict)
	if !ok || !n.HasStream {
		t.Fatalf("AP/N = %#v, want a stream pdf.PDFDict", ap.Entries.Get("N"))
	}
	if ap.Entries.Len() != 1 {
		t.Errorf("AP has %d entries, want exactly 1 (N)", ap.Entries.Len())
	}

	var sawText bool
	pdf.NewContentScanner(contentBytesOf(t, n)).Scan(func(op string, operands []pdf.PDFValue) {
		if op == "Tj" && len(operands) == 1 {
			if s, ok := operands[0].(pdf.PDFString); ok && s.Value == "John Doe" {
				sawText = true
			}
		}
	})
	if !sawText {
		t.Errorf("AP/N content does not draw the field's text: %q", contentBytesOf(t, n))
	}

	res, ok := n.Entries.Get("Resources").(pdf.PDFDict)
	if !ok {
		t.Fatalf("AP/N has no Resources")
	}
	fonts, ok := res.Entries.Get("Font").(pdf.PDFDict)
	if !ok || fonts.Entries.Get("F0") == nil {
		t.Fatalf("AP/N Resources has no /Font /F0")
	}

	if annotationNeedsAppearanceFix(widget) {
		t.Fatalf("annotationNeedsAppearanceFix still true after Fix")
	}
	changed, err = fixer.Fix(&trailer, nil)
	if err != nil {
		t.Fatalf("Fix (second pass): %v", err)
	}
	if changed {
		t.Errorf("changed = true on second pass, want false (fixer must be idempotent)")
	}
}

// TestAppearanceFixerSynthesizesButtonStateSubdictionary builds a checkbox
// widget with no /AP and no /AS, and checks that /AP/N becomes a
// state-name-to-stream subdictionary (never a direct stream, per
// AppearanceNNotStream's Btn rule) with /AS set to the chosen state.
func TestAppearanceFixerSynthesizesButtonStateSubdictionary(t *testing.T) {
	widget := pdf.NewPDFDict()
	widget.Entries.Set("Type", pdf.PDFName{Value: "Annot"})
	widget.Entries.Set("Subtype", pdf.PDFName{Value: "Widget"})
	widget.Entries.Set("FT", pdf.PDFName{Value: "Btn"})
	widget.Entries.Set("Rect", pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFInteger(20), pdf.PDFInteger(20)})

	trailer := minimalTrailer()
	trailer.Entries.Set("Widget", widget)

	fixer := appearanceFixer{}
	if _, err := fixer.Fix(&trailer, nil); err != nil {
		t.Fatalf("Fix: %v", err)
	}

	as, ok := widget.Entries.Get("AS").(pdf.PDFName)
	if !ok || as.Value == "" {
		t.Fatalf("AS = %#v, want a non-empty pdf.PDFName", widget.Entries.Get("AS"))
	}

	ap := widget.Entries.Get("AP").(pdf.PDFDict)
	n := ap.Entries.Get("N").(pdf.PDFDict)
	if n.HasStream {
		t.Fatalf("Btn AP/N is a direct stream, want a subdictionary")
	}
	state, ok := n.Entries.Get(as.Value).(pdf.PDFDict)
	if !ok || !state.HasStream {
		t.Fatalf("AP/N[%q] = %#v, want a stream matching /AS", as.Value, n.Entries.Get(as.Value))
	}
}

// TestAppearanceFixerWrapsExistingBtnStreamIntoSubdictionary checks the
// AppearanceNNotStream-Btn case: an existing direct-stream /N is preserved,
// just wrapped under its /AS state name instead of replaced.
func TestAppearanceFixerWrapsExistingBtnStreamIntoSubdictionary(t *testing.T) {
	original := pdf.NewPDFDict()
	original.Entries.Set("Type", pdf.PDFName{Value: "XObject"})
	original.Entries.Set("Subtype", pdf.PDFName{Value: "Form"})
	original.HasStream = true
	original.RawStream = []byte("q Q")

	ap := pdf.NewPDFDict()
	ap.Entries.Set("N", original)

	widget := pdf.NewPDFDict()
	widget.Entries.Set("Type", pdf.PDFName{Value: "Annot"})
	widget.Entries.Set("Subtype", pdf.PDFName{Value: "Widget"})
	widget.Entries.Set("FT", pdf.PDFName{Value: "Btn"})
	widget.Entries.Set("AS", pdf.PDFName{Value: "On"})
	widget.Entries.Set("AP", ap)

	trailer := minimalTrailer()
	trailer.Entries.Set("Widget", widget)

	fixer := appearanceFixer{}
	changed, err := fixer.Fix(&trailer, nil)
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if !changed {
		t.Fatalf("changed = false, want true (N was a direct stream on a Btn)")
	}

	newAP := widget.Entries.Get("AP").(pdf.PDFDict)
	newN := newAP.Entries.Get("N").(pdf.PDFDict)
	wrapped, ok := newN.Entries.Get("On").(pdf.PDFDict)
	if !ok {
		t.Fatalf("AP/N[On] missing after wrapping")
	}
	if !pdf.EqualPDFValue(wrapped, original) {
		t.Errorf("wrapped stream != original stream; the existing appearance was replaced rather than preserved")
	}
}

// TestAppearanceFixerStripsExtraEntriesPreservingValidN checks the
// AppearanceExtraEntries case: an already-valid /N stream is kept exactly
// as-is, with only the offending /D sibling entry dropped.
func TestAppearanceFixerStripsExtraEntriesPreservingValidN(t *testing.T) {
	validN := pdf.NewPDFDict()
	validN.Entries.Set("Type", pdf.PDFName{Value: "XObject"})
	validN.Entries.Set("Subtype", pdf.PDFName{Value: "Form"})
	validN.HasStream = true
	validN.RawStream = []byte("q Q")

	ap := pdf.NewPDFDict()
	ap.Entries.Set("N", validN)
	ap.Entries.Set("D", pdf.NewPDFDict())

	annot := pdf.NewPDFDict()
	annot.Entries.Set("Type", pdf.PDFName{Value: "Annot"})
	annot.Entries.Set("Subtype", pdf.PDFName{Value: "Square"})
	annot.Entries.Set("AP", ap)

	trailer := minimalTrailer()
	trailer.Entries.Set("Annot", annot)

	fixer := appearanceFixer{}
	changed, err := fixer.Fix(&trailer, nil)
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if !changed {
		t.Fatalf("changed = false, want true (AP had an extra D entry)")
	}

	newAP := annot.Entries.Get("AP").(pdf.PDFDict)
	if newAP.Entries.Len() != 1 {
		t.Errorf("AP has %d entries, want exactly 1 (N)", newAP.Entries.Len())
	}
	if !pdf.EqualPDFValue(newAP.Entries.Get("N"), validN) {
		t.Errorf("N was replaced rather than preserved: %#v", newAP.Entries.Get("N"))
	}
}

// TestAppearanceFixerBuildsEmptyAppearanceForPlainAnnotation checks a
// non-form annotation (no /FT) with no /AP gets a structurally valid, empty
// Form XObject -- no font/text resources are needed for it.
func TestAppearanceFixerBuildsEmptyAppearanceForPlainAnnotation(t *testing.T) {
	annot := pdf.NewPDFDict()
	annot.Entries.Set("Type", pdf.PDFName{Value: "Annot"})
	annot.Entries.Set("Subtype", pdf.PDFName{Value: "Square"})
	annot.Entries.Set("Rect", pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFInteger(10), pdf.PDFInteger(10)})

	trailer := minimalTrailer()
	trailer.Entries.Set("Annot", annot)

	fixer := appearanceFixer{}
	if _, err := fixer.Fix(&trailer, nil); err != nil {
		t.Fatalf("Fix: %v", err)
	}

	ap := annot.Entries.Get("AP").(pdf.PDFDict)
	n := ap.Entries.Get("N").(pdf.PDFDict)
	if !n.HasStream {
		t.Fatalf("AP/N is not a stream")
	}
	if bbox, ok := n.Entries.Get("BBox").(pdf.PDFArray); !ok || len(bbox) != 4 {
		t.Errorf("AP/N BBox = %#v, want a 4-element array", n.Entries.Get("BBox"))
	}
}

// TestAppearanceFixerSkipsPopupAndLinkWithoutAP checks that Popup/Link
// annotations missing /AP are left untouched, mirroring validateAnnotation's
// exemption for those two subtypes.
func TestAppearanceFixerSkipsPopupAndLinkWithoutAP(t *testing.T) {
	for _, subtype := range []string{"Popup", "Link"} {
		annot := pdf.NewPDFDict()
		annot.Entries.Set("Type", pdf.PDFName{Value: "Annot"})
		annot.Entries.Set("Subtype", pdf.PDFName{Value: subtype})

		trailer := minimalTrailer()
		trailer.Entries.Set("Annot", annot)

		fixer := appearanceFixer{}
		changed, err := fixer.Fix(&trailer, nil)
		if err != nil {
			t.Fatalf("Fix: %v", err)
		}
		if changed {
			t.Errorf("subtype %s: changed = true, want false (exempt from requiring AP)", subtype)
		}
		if _, ok := annot.Entries.Lookup("AP"); ok {
			t.Errorf("subtype %s: AP was added, want none", subtype)
		}
	}
}

// TestAppearanceFixerInheritedBtnWrapsStream verifies that a child widget
// whose FT=Btn is inherited from its Parent (not set directly) gets its
// direct-stream AP/N wrapped in a state-name subdictionary.
func TestAppearanceFixerInheritedBtnWrapsStream(t *testing.T) {
	parent := pdf.NewPDFDict()
	parent.Entries.Set("FT", pdf.PDFName{Value: "Btn"})

	original := pdf.NewPDFDict()
	original.Entries.Set("Type", pdf.PDFName{Value: "XObject"})
	original.Entries.Set("Subtype", pdf.PDFName{Value: "Form"})
	original.HasStream = true
	original.RawStream = []byte("q Q")

	ap := pdf.NewPDFDict()
	ap.Entries.Set("N", original)

	widget := pdf.NewPDFDict()
	widget.Entries.Set("Type", pdf.PDFName{Value: "Annot"})
	widget.Entries.Set("Subtype", pdf.PDFName{Value: "Widget"})
	widget.Entries.Set("Parent", parent)
	widget.Entries.Set("AS", pdf.PDFName{Value: "Yes"})
	widget.Entries.Set("AP", ap)

	trailer := minimalTrailer()
	trailer.Entries.Set("Widget", widget)

	fixer := appearanceFixer{}
	changed, err := fixer.Fix(&trailer, nil)
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if !changed {
		t.Fatalf("changed = false, want true (inherited Btn with direct-stream N)")
	}

	newAP := widget.Entries.Get("AP").(pdf.PDFDict)
	newN := newAP.Entries.Get("N").(pdf.PDFDict)
	if newN.HasStream {
		t.Fatalf("AP/N is still a direct stream after fix, want subdictionary")
	}
	if _, ok := newN.Entries.Lookup("Yes"); !ok {
		t.Errorf("AP/N subdictionary missing 'Yes' state key; entries: %v", newN.Entries)
	}
}

// TestAppearanceFixerLeavesValidAppearanceUntouched checks that an
// annotation whose /AP already satisfies validateAnnotation is left
// byte-for-byte unchanged.
func TestAppearanceFixerLeavesValidAppearanceUntouched(t *testing.T) {
	validN := pdf.NewPDFDict()
	validN.HasStream = true
	validN.RawStream = []byte("q Q")

	ap := pdf.NewPDFDict()
	ap.Entries.Set("N", validN)

	annot := pdf.NewPDFDict()
	annot.Entries.Set("Type", pdf.PDFName{Value: "Annot"})
	annot.Entries.Set("Subtype", pdf.PDFName{Value: "Square"})
	annot.Entries.Set("AP", ap)

	trailer := minimalTrailer()
	trailer.Entries.Set("Annot", annot)

	fixer := appearanceFixer{}
	changed, err := fixer.Fix(&trailer, nil)
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if changed {
		t.Errorf("changed = true, want false (AP was already conformant)")
	}
	if !pdf.EqualPDFValue(annot.Entries.Get("AP"), ap) {
		t.Errorf("AP was modified despite already being conformant")
	}
}

func TestWinAnsiEncodeHelpers(t *testing.T) {
	if b, ok := winAnsiForUnicode('A'); !ok || b != 'A' {
		t.Errorf("winAnsiForUnicode(A) = %d, %v", b, ok)
	}
	if _, ok := winAnsiForUnicode(0x0530); ok { // Armenian, not in WinAnsi
		t.Error("winAnsiForUnicode of an out-of-range rune should be false")
	}
	if s := decodeUTF16BEToWinAnsi([]byte{0x00, 'H', 0x00, 'i'}); s != "Hi" {
		t.Errorf("decodeUTF16BEToWinAnsi = %q, want Hi", s)
	}
}

func TestClimbField(t *testing.T) {
	parent := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"V": pdf.PDFString{Value: "top"}})}
	child := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Parent": parent})}
	if v, ok := climbField(child, "V"); !ok || v != (pdf.PDFString{Value: "top"}) {
		t.Errorf("climbField inherited V = %v, %v", v, ok)
	}
	if _, ok := climbField(pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{})}, "V"); ok {
		t.Error("climbField on a field with no V/Parent should be false")
	}

	// Parent cycle must terminate.
	a := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{})}
	b := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Parent": a})}
	a.Entries.Set("Parent", b)
	if _, ok := climbField(a, "V"); ok {
		t.Error("climbField on a Parent cycle should terminate and return false")
	}
}

func TestFieldDisplayText(t *testing.T) {
	if got := fieldDisplayText(pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"V": pdf.PDFString{Value: "hi\tthere"}})}); got != "hi there" {
		t.Errorf("string V = %q, want \"hi there\"", got)
	}
	// UTF-16BE with BOM.
	if got := fieldDisplayText(pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"V": pdf.PDFHexString{Value: "FEFF00480049"}})}); got != "HI" {
		t.Errorf("UTF16 hex V = %q, want \"HI\"", got)
	}
	// Plain hex (no BOM).
	if got := fieldDisplayText(pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"V": pdf.PDFHexString{Value: "4869"}})}); got != "Hi" {
		t.Errorf("plain hex V = %q, want \"Hi\"", got)
	}
	if got := fieldDisplayText(pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{})}); got != "" {
		t.Errorf("no V = %q, want empty", got)
	}
	// Undecodable hex string.
	if got := fieldDisplayText(pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"V": pdf.PDFHexString{Value: "ZZ"}})}); got != "" {
		t.Errorf("undecodable hex V = %q, want empty", got)
	}
	// V present but neither PDFString nor PDFHexString.
	if got := fieldDisplayText(pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"V": pdf.PDFInteger(1)})}); got != "" {
		t.Errorf("non-string/hex V = %q, want empty", got)
	}
}

func TestAnnotBBox(t *testing.T) {
	if w, h := annotBBox(pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{})}); w != 0 || h != 0 {
		t.Errorf("annotBBox(no Rect) = (%v, %v), want (0, 0)", w, h)
	}
	wrongLen := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Rect": pdf.PDFArray{pdf.PDFInteger(1), pdf.PDFInteger(2)}})}
	if w, h := annotBBox(wrongLen); w != 0 || h != 0 {
		t.Errorf("annotBBox(2-element Rect) = (%v, %v), want (0, 0)", w, h)
	}
	nonNumeric := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Rect": pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFName{Value: "bad"}, pdf.PDFInteger(10)},
	})}
	if w, h := annotBBox(nonNumeric); w != 0 || h != 0 {
		t.Errorf("annotBBox(non-numeric Rect element) = (%v, %v), want (0, 0)", w, h)
	}
	valid := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Rect": pdf.PDFArray{pdf.PDFInteger(10), pdf.PDFInteger(20), pdf.PDFInteger(50), pdf.PDFInteger(60)},
	})}
	if w, h := annotBBox(valid); w != 40 || h != 40 {
		t.Errorf("annotBBox(valid Rect) = (%v, %v), want (40, 40)", w, h)
	}
}

func TestFormLevelDA(t *testing.T) {
	trailer := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Root": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"AcroForm": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"DA": pdf.PDFString{Value: "/Helv 12 Tf 0 g"}})},
		})},
	})}
	if got := formLevelDA(&trailer); got != "/Helv 12 Tf 0 g" {
		t.Errorf("formLevelDA = %q", got)
	}
	empty := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{})}
	if got := formLevelDA(&empty); got != "" {
		t.Errorf("formLevelDA(no Root) = %q, want empty", got)
	}
	noAcroForm := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Root": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{})}})}
	if got := formLevelDA(&noAcroForm); got != "" {
		t.Errorf("formLevelDA(no AcroForm) = %q, want empty", got)
	}
	wrongDAType := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Root": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"AcroForm": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"DA": pdf.PDFInteger(1)})},
		})},
	})}
	if got := formLevelDA(&wrongDAType); got != "" {
		t.Errorf("formLevelDA(non-string DA) = %q, want empty", got)
	}
}
