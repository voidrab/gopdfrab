package pdfrab

import "testing"

// TestAppearanceFixerAppliesExclusively checks the Fixer/Applies contract
// (mirroring fixups_font_test.go's pattern): the fixer must claim exactly
// its five target checks and nothing else, since registerFixer panics on a
// Check claimed by more than one Fixer.
func TestAppearanceFixerAppliesExclusively(t *testing.T) {
	fixer := appearanceFixer{}
	want := map[Check]bool{
		Checks.Form.WidgetMissingAppearance:      true,
		Checks.Annotation.MissingAppearance:      true,
		Checks.Annotation.AppearanceMissingN:     true,
		Checks.Annotation.AppearanceExtraEntries: true,
		Checks.Annotation.AppearanceNNotStream:   true,
	}
	for _, c := range AllChecks() {
		if got := fixer.Applies(c); got != want[c] {
			t.Errorf("Applies(%s) = %v, want %v", c.Name(), got, want[c])
		}
	}
}

func minimalTrailer() PDFDict {
	trailer := NewPDFDict()
	trailer.Entries["Root"] = NewPDFDict()
	return trailer
}

// contentBytesOf decodes a Form XObject stream's content, tolerating an
// unset /Filter (the fixer leaves new streams undecoded/plaintext until
// WriteDocument's MarkStreamDirty-driven re-encode).
func contentBytesOf(t *testing.T, xobj PDFDict) []byte {
	t.Helper()
	data, err := decodeStream(xobj)
	if err != nil {
		t.Fatalf("decodeStream: %v", err)
	}
	return data
}

// TestAppearanceFixerSynthesizesTextFieldAppearance builds a text-field
// widget with a value and no /AP, runs the fixer, and checks that /AP/N
// becomes a Form XObject stream whose content draws the field's text using
// the bundled appearanceFont(), then checks that a second pass is a no-op
// (idempotent, required for the bounded convert loop to terminate).
func TestAppearanceFixerSynthesizesTextFieldAppearance(t *testing.T) {
	widget := NewPDFDict()
	widget.Entries["Type"] = PDFName{Value: "Annot"}
	widget.Entries["Subtype"] = PDFName{Value: "Widget"}
	widget.Entries["FT"] = PDFName{Value: "Tx"}
	widget.Entries["Rect"] = PDFArray{PDFInteger(0), PDFInteger(0), PDFInteger(100), PDFInteger(20)}
	widget.Entries["V"] = PDFString{Value: "John Doe"}
	widget.Entries["DA"] = PDFString{Value: "/Helv 10 Tf 0 g"}

	trailer := minimalTrailer()
	trailer.Entries["Root"].(PDFDict).Entries["AcroForm"] = NewPDFDict()
	trailer.Entries["Widget"] = widget // keep it reachable for walkDicts

	fixer := appearanceFixer{}
	changed, err := fixer.Fix(&trailer, nil)
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if !changed {
		t.Fatalf("changed = false, want true (AP was missing)")
	}

	ap, ok := widget.Entries["AP"].(PDFDict)
	if !ok {
		t.Fatalf("AP = %#v, want a PDFDict", widget.Entries["AP"])
	}
	n, ok := ap.Entries["N"].(PDFDict)
	if !ok || !n.HasStream {
		t.Fatalf("AP/N = %#v, want a stream PDFDict", ap.Entries["N"])
	}
	if len(ap.Entries) != 1 {
		t.Errorf("AP has %d entries, want exactly 1 (N)", len(ap.Entries))
	}

	var sawText bool
	newContentScanner(contentBytesOf(t, n)).scan(func(op string, operands []PDFValue) {
		if op == "Tj" && len(operands) == 1 {
			if s, ok := operands[0].(PDFString); ok && s.Value == "John Doe" {
				sawText = true
			}
		}
	})
	if !sawText {
		t.Errorf("AP/N content does not draw the field's text: %q", contentBytesOf(t, n))
	}

	res, ok := n.Entries["Resources"].(PDFDict)
	if !ok {
		t.Fatalf("AP/N has no Resources")
	}
	fonts, ok := res.Entries["Font"].(PDFDict)
	if !ok || fonts.Entries["F0"] == nil {
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
	widget := NewPDFDict()
	widget.Entries["Type"] = PDFName{Value: "Annot"}
	widget.Entries["Subtype"] = PDFName{Value: "Widget"}
	widget.Entries["FT"] = PDFName{Value: "Btn"}
	widget.Entries["Rect"] = PDFArray{PDFInteger(0), PDFInteger(0), PDFInteger(20), PDFInteger(20)}

	trailer := minimalTrailer()
	trailer.Entries["Widget"] = widget

	fixer := appearanceFixer{}
	if _, err := fixer.Fix(&trailer, nil); err != nil {
		t.Fatalf("Fix: %v", err)
	}

	as, ok := widget.Entries["AS"].(PDFName)
	if !ok || as.Value == "" {
		t.Fatalf("AS = %#v, want a non-empty PDFName", widget.Entries["AS"])
	}

	ap := widget.Entries["AP"].(PDFDict)
	n := ap.Entries["N"].(PDFDict)
	if n.HasStream {
		t.Fatalf("Btn AP/N is a direct stream, want a subdictionary")
	}
	state, ok := n.Entries[as.Value].(PDFDict)
	if !ok || !state.HasStream {
		t.Fatalf("AP/N[%q] = %#v, want a stream matching /AS", as.Value, n.Entries[as.Value])
	}
}

// TestAppearanceFixerWrapsExistingBtnStreamIntoSubdictionary checks the
// AppearanceNNotStream-Btn case: an existing direct-stream /N is preserved,
// just wrapped under its /AS state name instead of replaced.
func TestAppearanceFixerWrapsExistingBtnStreamIntoSubdictionary(t *testing.T) {
	original := NewPDFDict()
	original.Entries["Type"] = PDFName{Value: "XObject"}
	original.Entries["Subtype"] = PDFName{Value: "Form"}
	original.HasStream = true
	original.RawStream = []byte("q Q")

	ap := NewPDFDict()
	ap.Entries["N"] = original

	widget := NewPDFDict()
	widget.Entries["Type"] = PDFName{Value: "Annot"}
	widget.Entries["Subtype"] = PDFName{Value: "Widget"}
	widget.Entries["FT"] = PDFName{Value: "Btn"}
	widget.Entries["AS"] = PDFName{Value: "On"}
	widget.Entries["AP"] = ap

	trailer := minimalTrailer()
	trailer.Entries["Widget"] = widget

	fixer := appearanceFixer{}
	changed, err := fixer.Fix(&trailer, nil)
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if !changed {
		t.Fatalf("changed = false, want true (N was a direct stream on a Btn)")
	}

	newAP := widget.Entries["AP"].(PDFDict)
	newN := newAP.Entries["N"].(PDFDict)
	wrapped, ok := newN.Entries["On"].(PDFDict)
	if !ok {
		t.Fatalf("AP/N[On] missing after wrapping")
	}
	if !EqualPDFValue(wrapped, original) {
		t.Errorf("wrapped stream != original stream; the existing appearance was replaced rather than preserved")
	}
}

// TestAppearanceFixerStripsExtraEntriesPreservingValidN checks the
// AppearanceExtraEntries case: an already-valid /N stream is kept exactly
// as-is, with only the offending /D sibling entry dropped.
func TestAppearanceFixerStripsExtraEntriesPreservingValidN(t *testing.T) {
	validN := NewPDFDict()
	validN.Entries["Type"] = PDFName{Value: "XObject"}
	validN.Entries["Subtype"] = PDFName{Value: "Form"}
	validN.HasStream = true
	validN.RawStream = []byte("q Q")

	ap := NewPDFDict()
	ap.Entries["N"] = validN
	ap.Entries["D"] = NewPDFDict()

	annot := NewPDFDict()
	annot.Entries["Type"] = PDFName{Value: "Annot"}
	annot.Entries["Subtype"] = PDFName{Value: "Square"}
	annot.Entries["AP"] = ap

	trailer := minimalTrailer()
	trailer.Entries["Annot"] = annot

	fixer := appearanceFixer{}
	changed, err := fixer.Fix(&trailer, nil)
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if !changed {
		t.Fatalf("changed = false, want true (AP had an extra D entry)")
	}

	newAP := annot.Entries["AP"].(PDFDict)
	if len(newAP.Entries) != 1 {
		t.Errorf("AP has %d entries, want exactly 1 (N)", len(newAP.Entries))
	}
	if !EqualPDFValue(newAP.Entries["N"], validN) {
		t.Errorf("N was replaced rather than preserved: %#v", newAP.Entries["N"])
	}
}

// TestAppearanceFixerBuildsEmptyAppearanceForPlainAnnotation checks a
// non-form annotation (no /FT) with no /AP gets a structurally valid, empty
// Form XObject -- no font/text resources are needed for it.
func TestAppearanceFixerBuildsEmptyAppearanceForPlainAnnotation(t *testing.T) {
	annot := NewPDFDict()
	annot.Entries["Type"] = PDFName{Value: "Annot"}
	annot.Entries["Subtype"] = PDFName{Value: "Square"}
	annot.Entries["Rect"] = PDFArray{PDFInteger(0), PDFInteger(0), PDFInteger(10), PDFInteger(10)}

	trailer := minimalTrailer()
	trailer.Entries["Annot"] = annot

	fixer := appearanceFixer{}
	if _, err := fixer.Fix(&trailer, nil); err != nil {
		t.Fatalf("Fix: %v", err)
	}

	ap := annot.Entries["AP"].(PDFDict)
	n := ap.Entries["N"].(PDFDict)
	if !n.HasStream {
		t.Fatalf("AP/N is not a stream")
	}
	if bbox, ok := n.Entries["BBox"].(PDFArray); !ok || len(bbox) != 4 {
		t.Errorf("AP/N BBox = %#v, want a 4-element array", n.Entries["BBox"])
	}
}

// TestAppearanceFixerSkipsPopupAndLinkWithoutAP checks that Popup/Link
// annotations missing /AP are left untouched, mirroring validateAnnotation's
// exemption for those two subtypes.
func TestAppearanceFixerSkipsPopupAndLinkWithoutAP(t *testing.T) {
	for _, subtype := range []string{"Popup", "Link"} {
		annot := NewPDFDict()
		annot.Entries["Type"] = PDFName{Value: "Annot"}
		annot.Entries["Subtype"] = PDFName{Value: subtype}

		trailer := minimalTrailer()
		trailer.Entries["Annot"] = annot

		fixer := appearanceFixer{}
		changed, err := fixer.Fix(&trailer, nil)
		if err != nil {
			t.Fatalf("Fix: %v", err)
		}
		if changed {
			t.Errorf("subtype %s: changed = true, want false (exempt from requiring AP)", subtype)
		}
		if _, ok := annot.Entries["AP"]; ok {
			t.Errorf("subtype %s: AP was added, want none", subtype)
		}
	}
}

// TestAppearanceFixerLeavesValidAppearanceUntouched checks that an
// annotation whose /AP already satisfies validateAnnotation is left
// byte-for-byte unchanged.
func TestAppearanceFixerLeavesValidAppearanceUntouched(t *testing.T) {
	validN := NewPDFDict()
	validN.HasStream = true
	validN.RawStream = []byte("q Q")

	ap := NewPDFDict()
	ap.Entries["N"] = validN

	annot := NewPDFDict()
	annot.Entries["Type"] = PDFName{Value: "Annot"}
	annot.Entries["Subtype"] = PDFName{Value: "Square"}
	annot.Entries["AP"] = ap

	trailer := minimalTrailer()
	trailer.Entries["Annot"] = annot

	fixer := appearanceFixer{}
	changed, err := fixer.Fix(&trailer, nil)
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if changed {
		t.Errorf("changed = true, want false (AP was already conformant)")
	}
	if !EqualPDFValue(annot.Entries["AP"], ap) {
		t.Errorf("AP was modified despite already being conformant")
	}
}
