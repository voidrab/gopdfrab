package verify

import (
	"fmt"
	"os"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

func TestAsFloat(t *testing.T) {
	if f, ok := AsFloat(pdf.PDFInteger(5)); !ok || f != 5 {
		t.Errorf("AsFloat(PDFInteger) = %g, %v", f, ok)
	}
	if f, ok := AsFloat(pdf.PDFReal(2.5)); !ok || f != 2.5 {
		t.Errorf("AsFloat(PDFReal) = %g, %v", f, ok)
	}
	if _, ok := AsFloat(pdf.PDFName{Value: "x"}); ok {
		t.Error("AsFloat should be false for a non-numeric value")
	}
}

func TestValidateActions(t *testing.T) {
	forbidden := pdf.NewPDFDict()
	forbidden.Entries["S"] = pdf.PDFName{Value: "JavaScript"}
	ctx := &ValidationContext{}
	validateActions(forbidden, ctx)
	if !hasCheck(ctx, pdf.Checks.Action.ForbiddenActionType) {
		t.Error("expected ForbiddenActionType for a JavaScript action")
	}

	namedBad := pdf.NewPDFDict()
	namedBad.Entries["S"] = pdf.PDFName{Value: "Named"}
	namedBad.Entries["N"] = pdf.PDFName{Value: "GoBackToStart"}
	ctx2 := &ValidationContext{}
	validateActions(namedBad, ctx2)
	if !hasCheck(ctx2, pdf.Checks.Action.DisallowedNamedAction) {
		t.Error("expected DisallowedNamedAction for a non-standard Named action")
	}

	namedOK := pdf.NewPDFDict()
	namedOK.Entries["S"] = pdf.PDFName{Value: "Named"}
	namedOK.Entries["N"] = pdf.PDFName{Value: "NextPage"}
	ctx3 := &ValidationContext{}
	validateActions(namedOK, ctx3)
	if len(ctx3.errs) != 0 {
		t.Error("unexpected violation for a standard Named action")
	}

	notAction := pdf.NewPDFDict()
	ctx4 := &ValidationContext{}
	validateActions(notAction, ctx4)
	if len(ctx4.errs) != 0 {
		t.Error("a dict with no recognized action type S should be a no-op")
	}
}

func TestValidateAdditionalActions(t *testing.T) {
	v := pdf.NewPDFDict()
	v.Entries["AA"] = pdf.NewPDFDict()
	ctx := &ValidationContext{}
	validateAdditionalActions(v, ctx)
	if !hasCheck(ctx, pdf.Checks.Action.AdditionalActions) {
		t.Error("expected AdditionalActions when AA is present")
	}

	ctx2 := &ValidationContext{}
	validateAdditionalActions(pdf.NewPDFDict(), ctx2)
	if len(ctx2.errs) != 0 {
		t.Error("unexpected violation when AA is absent")
	}
}

func TestValidateExtGState(t *testing.T) {
	v := pdf.NewPDFDict()
	v.Entries["Type"] = pdf.PDFName{Value: "ExtGState"}
	v.Entries["TR"] = pdf.PDFName{Value: "Identity"}
	v.Entries["TR2"] = pdf.PDFName{Value: "Custom"}
	v.Entries["RI"] = pdf.PDFName{Value: "BadIntent"}
	v.Entries["SMask"] = pdf.PDFName{Value: "Luminosity"}
	v.Entries["BM"] = pdf.PDFName{Value: "Multiply"}
	v.Entries["CA"] = pdf.PDFReal(0.5)
	v.Entries["ca"] = pdf.PDFReal(0.5)
	ctx := &ValidationContext{}
	validateExtGState(v, ctx)
	for _, chk := range []pdf.Check{
		pdf.Checks.Transparency.TransferFunction,
		pdf.Checks.Transparency.DefaultTransferFunction,
		pdf.Checks.Transparency.ExtGStateRenderingIntent,
		pdf.Checks.Transparency.SoftMaskExtGState,
		pdf.Checks.Transparency.BlendMode,
		pdf.Checks.Transparency.StrokingAlpha,
		pdf.Checks.Transparency.NonStrokingAlpha,
	} {
		if !hasCheck(ctx, chk) {
			t.Errorf("expected check %v on a fully non-conformant ExtGState", chk)
		}
	}

	// A conformant ExtGState triggers nothing.
	good := pdf.NewPDFDict()
	good.Entries["Type"] = pdf.PDFName{Value: "ExtGState"}
	good.Entries["BM"] = pdf.PDFName{Value: "Normal"}
	good.Entries["CA"] = pdf.PDFReal(1.0)
	ctx2 := &ValidationContext{}
	validateExtGState(good, ctx2)
	if len(ctx2.errs) != 0 {
		t.Errorf("unexpected violation for a conformant ExtGState: %v", ctx2.errs)
	}

	// Wrong Type or no relevant keys: no-op.
	ctx3 := &ValidationContext{}
	validateExtGState(pdf.NewPDFDict(), ctx3)
	if len(ctx3.errs) != 0 {
		t.Error("unexpected violation for a dict with no transparency-related keys")
	}
	other := pdf.NewPDFDict()
	other.Entries["Type"] = pdf.PDFName{Value: "Annot"}
	other.Entries["TR"] = pdf.PDFName{Value: "Identity"}
	ctx4 := &ValidationContext{}
	validateExtGState(other, ctx4)
	if len(ctx4.errs) != 0 {
		t.Error("unexpected violation for a dict whose Type is not ExtGState")
	}
}

func TestValidateTransparencyGroup(t *testing.T) {
	group := pdf.NewPDFDict()
	group.Entries["S"] = pdf.PDFName{Value: "Transparency"}
	v := pdf.NewPDFDict()
	v.Entries["Group"] = group
	ctx := &ValidationContext{}
	validateTransparencyGroup(v, ctx)
	if !hasCheck(ctx, pdf.Checks.Transparency.TransparencyGroup) {
		t.Error("expected TransparencyGroup for /S /Transparency")
	}

	ctx2 := &ValidationContext{}
	validateTransparencyGroup(pdf.NewPDFDict(), ctx2)
	if len(ctx2.errs) != 0 {
		t.Error("unexpected violation with no Group entry")
	}
}

func TestValidateXObjectDict(t *testing.T) {
	img := pdf.NewPDFDict()
	img.Entries["Subtype"] = pdf.PDFName{Value: "Image"}
	img.Entries["Interpolate"] = pdf.PDFBoolean(true)
	img.Entries["Alternates"] = pdf.PDFArray{}
	img.Entries["OPI"] = pdf.NewPDFDict()
	img.Entries["Intent"] = pdf.PDFName{Value: "BadIntent"}
	img.Entries["BitsPerComponent"] = pdf.PDFInteger(3)
	img.Entries["SMask"] = pdf.PDFName{Value: "Luminosity"}
	ctx := &ValidationContext{}
	validateXObjectDict(img, ctx)
	for _, chk := range []pdf.Check{
		pdf.Checks.Image.ImageInterpolate,
		pdf.Checks.Image.ImageAlternates,
		pdf.Checks.Image.ImageOPI,
		pdf.Checks.Image.ImageRenderingIntent,
		pdf.Checks.Image.ImageBitsPerComponent,
		pdf.Checks.Transparency.ImageWithSoftMask,
	} {
		if !hasCheck(ctx, chk) {
			t.Errorf("expected check %v on a non-conformant image XObject", chk)
		}
	}

	mask := pdf.NewPDFDict()
	mask.Entries["Subtype"] = pdf.PDFName{Value: "Image"}
	mask.Entries["ImageMask"] = pdf.PDFBoolean(true)
	mask.Entries["BitsPerComponent"] = pdf.PDFInteger(2)
	ctx2 := &ValidationContext{}
	validateXObjectDict(mask, ctx2)
	if !hasCheck(ctx2, pdf.Checks.Image.ImageMaskBitsPerComponent) {
		t.Error("expected ImageMaskBitsPerComponent for a mask with BitsPerComponent != 1")
	}

	form := pdf.NewPDFDict()
	form.Entries["Subtype"] = pdf.PDFName{Value: "Form"}
	form.Entries["Ref"] = pdf.NewPDFDict()
	form.Entries["OPI"] = pdf.NewPDFDict()
	form.Entries["PS"] = pdf.PDFString{Value: "x"}
	form.Entries["Subtype2"] = pdf.PDFName{Value: "PS"}
	ctx3 := &ValidationContext{}
	validateXObjectDict(form, ctx3)
	for _, chk := range []pdf.Check{
		pdf.Checks.Image.ReferenceXObject,
		pdf.Checks.Image.FormOPI,
		pdf.Checks.Image.FormPostScript,
		pdf.Checks.Image.FormPSEntry,
		pdf.Checks.Image.FormSubtype2PS,
	} {
		if !hasCheck(ctx3, chk) {
			t.Errorf("expected check %v on a non-conformant form XObject", chk)
		}
	}

	// Unreachable Form XObject: skipped entirely.
	ctx4 := &ValidationContext{ReachableXObjectPtrs: map[uintptr]bool{}}
	validateXObjectDict(form, ctx4)
	if len(ctx4.errs) != 0 {
		t.Error("an unreachable Form XObject should not be checked")
	}

	ps := pdf.NewPDFDict()
	ps.Entries["Subtype"] = pdf.PDFName{Value: "PS"}
	ctx5 := &ValidationContext{}
	validateXObjectDict(ps, ctx5)
	if !hasCheck(ctx5, pdf.Checks.Image.PostScriptXObject) {
		t.Error("expected PostScriptXObject for a PS XObject")
	}

	ctx6 := &ValidationContext{}
	validateXObjectDict(pdf.NewPDFDict(), ctx6)
	if len(ctx6.errs) != 0 {
		t.Error("a dict with no Subtype should be a no-op")
	}
}

func TestCheckAnnotColour(t *testing.T) {
	v := pdf.NewPDFDict()
	ctx := &ValidationContext{}
	checkAnnotColour(v, pdf.PDFArray{pdf.PDFReal(1)}, ctx) // gray
	if !hasCheck(ctx, pdf.Checks.Annotation.ColourWithoutIntent) {
		t.Error("expected ColourWithoutIntent for an uncovered gray colour")
	}

	ctx2 := &ValidationContext{hasOutputIntent: true}
	checkAnnotColour(v, pdf.PDFArray{pdf.PDFReal(1)}, ctx2)
	if hasCheck(ctx2, pdf.Checks.Annotation.ColourWithoutIntent) {
		t.Error("unexpected report when gray is covered by any OutputIntent")
	}

	ctx3 := &ValidationContext{rgbCovered: true, cmykCovered: true}
	checkAnnotColour(v, pdf.PDFArray{pdf.PDFReal(1), pdf.PDFReal(0), pdf.PDFReal(0)}, ctx3)                 // rgb, covered
	checkAnnotColour(v, pdf.PDFArray{pdf.PDFReal(0), pdf.PDFReal(0), pdf.PDFReal(0), pdf.PDFReal(1)}, ctx3) // cmyk, covered
	if len(ctx3.errs) != 0 {
		t.Errorf("unexpected violations for covered rgb/cmyk colours: %v", ctx3.errs)
	}

	// Not an array, or wrong length: no-op.
	ctx4 := &ValidationContext{}
	checkAnnotColour(v, pdf.PDFName{Value: "x"}, ctx4)
	checkAnnotColour(v, pdf.PDFArray{pdf.PDFReal(1), pdf.PDFReal(1)}, ctx4)
	if len(ctx4.errs) != 0 {
		t.Error("unexpected violation for a non-colour value or unsupported array length")
	}
}

func TestValidateFormField(t *testing.T) {
	widget := pdf.NewPDFDict()
	widget.Entries["Type"] = pdf.PDFName{Value: "Annot"}
	widget.Entries["Subtype"] = pdf.PDFName{Value: "Widget"}
	widget.Entries["A"] = pdf.NewPDFDict()
	widget.Entries["AA"] = pdf.NewPDFDict()
	ctx := &ValidationContext{}
	validateFormField(widget, ctx)
	if !hasCheck(ctx, pdf.Checks.Form.FieldAction) {
		t.Error("expected FieldAction for a widget with an A action")
	}
	if !hasCheck(ctx, pdf.Checks.Form.FieldAdditionalActions) {
		t.Error("expected FieldAdditionalActions for a widget with AA")
	}

	field := pdf.NewPDFDict()
	field.Entries["FT"] = pdf.PDFName{Value: "Tx"}
	field.Entries["A"] = pdf.NewPDFDict()
	ctx2 := &ValidationContext{}
	validateFormField(field, ctx2)
	if !hasCheck(ctx2, pdf.Checks.Form.FieldAction) {
		t.Error("expected FieldAction for a field (by FT) with an A action")
	}

	notAField := pdf.NewPDFDict()
	notAField.Entries["A"] = pdf.NewPDFDict()
	ctx3 := &ValidationContext{}
	validateFormField(notAField, ctx3)
	if len(ctx3.errs) != 0 {
		t.Error("a dict that is neither a widget nor has FT should be a no-op")
	}
}

// createPDFWithAcroForm writes a minimal classic-xref PDF whose catalog has
// an AcroForm dictionary with NeedAppearances=true and an XFA entry.
func createPDFWithAcroForm(filename string) error {
	header := "%PDF-1.7\n"
	obj1 := "1 0 obj\n<< /Type /Catalog /Pages 2 0 R /AcroForm 4 0 R >>\nendobj\n"
	obj2 := "2 0 obj\n<< /Type /Pages /Count 0 /Kids [] >>\nendobj\n"
	obj3 := "3 0 obj\n<< /Title (Test) >>\nendobj\n"
	obj4 := "4 0 obj\n<< /NeedAppearances true /XFA (dummy) >>\nendobj\n"

	off1 := len(header)
	off2 := off1 + len(obj1)
	off3 := off2 + len(obj2)
	off4 := off3 + len(obj3)
	xrefOffset := off4 + len(obj4)

	xref := fmt.Sprintf("xref\n0 5\n0000000000 65535 f \n%010d 00000 n \n%010d 00000 n \n%010d 00000 n \n%010d 00000 n \n",
		off1, off2, off3, off4)
	trailer := "trailer\n<< /Size 5 /Root 1 0 R /Info 3 0 R >>\n"
	startxref := fmt.Sprintf("startxref\n%d\n%%EOF", xrefOffset)

	content := header + obj1 + obj2 + obj3 + obj4 + xref + trailer + startxref
	return os.WriteFile(filename, []byte(content), 0644)
}

func TestVerifyInteractiveFormsNeedAppearancesAndXFA(t *testing.T) {
	f := t.TempDir() + "/acroform.pdf"
	if err := createPDFWithAcroForm(f); err != nil {
		t.Fatalf("createPDFWithAcroForm: %v", err)
	}
	doc, err := pdf.Open(f)
	if err != nil {
		t.Fatalf("pdf.Open: %v", err)
	}
	defer doc.Close()
	errs := verifyInteractiveForms(doc)
	var gotNeedAppearances, gotXFA bool
	for _, e := range errs {
		if e.Check() == pdf.Checks.Form.NeedAppearances {
			gotNeedAppearances = true
		}
		if e.Check() == pdf.Checks.Form.XFA {
			gotXFA = true
		}
	}
	if !gotNeedAppearances || !gotXFA {
		t.Errorf("verifyInteractiveForms = %v, want NeedAppearances and XFA", errs)
	}
}

func TestVerifyInteractiveFormsNoAcroForm(t *testing.T) {
	f := t.TempDir() + "/no-acroform.pdf"
	if err := createValidPDF(f); err != nil {
		t.Fatalf("createValidPDF: %v", err)
	}
	doc, err := pdf.Open(f)
	if err != nil {
		t.Fatalf("pdf.Open: %v", err)
	}
	defer doc.Close()
	if errs := verifyInteractiveForms(doc); errs != nil {
		t.Errorf("expected nil for a document with no AcroForm, got %v", errs)
	}
}

// TestValidateViewerPreferencesFlagsPost14Keys confirms that validateViewerPreferences
// reports PostPDF14ViewerPref for PrintScaling and other post-1.4 keys.
func TestValidateViewerPreferencesFlagsPost14Keys(t *testing.T) {
	for _, key := range Post14ViewerPrefKeys {
		vp := pdf.NewPDFDict()
		vp.Entries[key] = pdf.PDFName{Value: "None"}

		catalog := pdf.NewPDFDict()
		catalog.Entries["Type"] = pdf.PDFName{Value: "Catalog"}
		catalog.Entries["ViewerPreferences"] = vp

		ctx := &ValidationContext{}
		validateViewerPreferences(catalog, ctx)

		found := false
		for _, iss := range ctx.Issues() {
			if iss.Check() == pdf.Checks.Structure.PostPDF14ViewerPref {
				found = true
			}
		}
		if !found {
			t.Errorf("key /%s: expected PostPDF14ViewerPref violation, got %v", key, ctx.Issues())
		}
	}
}

// TestValidateViewerPreferencesIgnoresValid14Keys confirms no violation is
// reported for ViewerPreferences keys that are valid in PDF 1.4.
func TestValidateViewerPreferencesIgnoresValid14Keys(t *testing.T) {
	vp := pdf.NewPDFDict()
	vp.Entries["DisplayDocTitle"] = pdf.PDFBoolean(true)
	vp.Entries["HideToolbar"] = pdf.PDFBoolean(false)

	catalog := pdf.NewPDFDict()
	catalog.Entries["Type"] = pdf.PDFName{Value: "Catalog"}
	catalog.Entries["ViewerPreferences"] = vp

	ctx := &ValidationContext{}
	validateViewerPreferences(catalog, ctx)

	for _, iss := range ctx.Issues() {
		if iss.Check() == pdf.Checks.Structure.PostPDF14ViewerPref {
			t.Errorf("unexpected PostPDF14ViewerPref for valid 1.4 keys: %v", iss.Error())
		}
	}
}

// TestResolveInheritedFT confirms FT is found on the parent when absent from
// the child widget, and that a direct value takes precedence over the parent.
func TestResolveInheritedFT(t *testing.T) {
	parent := pdf.NewPDFDict()
	parent.Entries["FT"] = pdf.PDFName{Value: "Btn"}

	child := pdf.NewPDFDict()
	child.Entries["Parent"] = parent

	if got := resolveInheritedFT(child); got != (pdf.PDFName{Value: "Btn"}) {
		t.Errorf("resolveInheritedFT child without FT = %v, want Btn", got)
	}

	child.Entries["FT"] = pdf.PDFName{Value: "Tx"}
	if got := resolveInheritedFT(child); got != (pdf.PDFName{Value: "Tx"}) {
		t.Errorf("resolveInheritedFT child with FT = %v, want Tx (direct wins)", got)
	}
}

// TestValidateAnnotationInheritsBtn confirms AppearanceNNotStream fires when
// FT=Btn is on the Parent, not the widget itself.
func TestValidateAnnotationInheritsBtn(t *testing.T) {
	parent := pdf.NewPDFDict()
	parent.Entries["FT"] = pdf.PDFName{Value: "Btn"}

	nStream := pdf.NewPDFDict()
	nStream.HasStream = true
	nStream.RawStream = []byte("")
	ap := pdf.NewPDFDict()
	ap.Entries["N"] = nStream

	widget := pdf.NewPDFDict()
	widget.Entries["Type"] = pdf.PDFName{Value: "Annot"}
	widget.Entries["Subtype"] = pdf.PDFName{Value: "Widget"}
	widget.Entries["F"] = pdf.PDFInteger(4)
	widget.Entries["Parent"] = parent
	widget.Entries["AP"] = ap

	ctx := &ValidationContext{}
	validateAnnotation(widget, ctx)

	for _, iss := range ctx.Issues() {
		if iss.Check() == pdf.Checks.Annotation.AppearanceNNotStream {
			return // expected
		}
	}
	t.Fatalf("expected AppearanceNNotStream for inherited FT=Btn with direct stream AP/N; got %v", ctx.Issues())
}

// TestValidateAnnotationInheritedBtnSubdictOK confirms that a valid Btn
// widget (N as subdictionary) with inherited FT passes without AppearanceNNotStream.
func TestValidateAnnotationInheritedBtnSubdictOK(t *testing.T) {
	parent := pdf.NewPDFDict()
	parent.Entries["FT"] = pdf.PDFName{Value: "Btn"}

	stateStream := pdf.NewPDFDict()
	stateStream.HasStream = true
	nSubdict := pdf.NewPDFDict()
	nSubdict.Entries["Off"] = stateStream
	ap := pdf.NewPDFDict()
	ap.Entries["N"] = nSubdict

	widget := pdf.NewPDFDict()
	widget.Entries["Type"] = pdf.PDFName{Value: "Annot"}
	widget.Entries["Subtype"] = pdf.PDFName{Value: "Widget"}
	widget.Entries["F"] = pdf.PDFInteger(4)
	widget.Entries["Parent"] = parent
	widget.Entries["AP"] = ap

	ctx := &ValidationContext{}
	validateAnnotation(widget, ctx)

	for _, iss := range ctx.Issues() {
		if iss.Check() == pdf.Checks.Annotation.AppearanceNNotStream {
			t.Errorf("unexpected AppearanceNNotStream for Btn with subdictionary AP/N")
		}
	}
}

func TestValidateAnnotationBranches(t *testing.T) {
	disallowed := pdf.NewPDFDict()
	disallowed.Entries["Type"] = pdf.PDFName{Value: "Annot"}
	disallowed.Entries["Subtype"] = pdf.PDFName{Value: "Sound"}
	ctx := &ValidationContext{}
	validateAnnotation(disallowed, ctx)
	if !hasCheck(ctx, pdf.Checks.Annotation.DisallowedSubtype) {
		t.Error("expected DisallowedSubtype for /Sound")
	}

	flagged := pdf.NewPDFDict()
	flagged.Entries["Type"] = pdf.PDFName{Value: "Annot"}
	flagged.Entries["Subtype"] = pdf.PDFName{Value: "Square"}
	flagged.Entries["F"] = pdf.PDFInteger(AnnotFlagHidden | AnnotFlagInvisible | AnnotFlagNoView)
	flagged.Entries["CA"] = pdf.PDFReal(0.5)
	ctx2 := &ValidationContext{}
	validateAnnotation(flagged, ctx2)
	for _, chk := range []pdf.Check{
		pdf.Checks.Annotation.PrintFlagNotSet,
		pdf.Checks.Annotation.HiddenFlagSet,
		pdf.Checks.Annotation.InvisibleFlagSet,
		pdf.Checks.Annotation.NoViewFlagSet,
		pdf.Checks.Annotation.OpacityNotOne,
		pdf.Checks.Annotation.MissingAppearance,
	} {
		if !hasCheck(ctx2, chk) {
			t.Errorf("expected check %v for a non-conformant Square annotation", chk)
		}
	}

	// An AP dict with an extra entry and no N.
	ap := pdf.NewPDFDict()
	ap.Entries["D"] = pdf.NewPDFDict()
	extraAP := pdf.NewPDFDict()
	extraAP.Entries["Type"] = pdf.PDFName{Value: "Annot"}
	extraAP.Entries["Subtype"] = pdf.PDFName{Value: "Square"}
	extraAP.Entries["F"] = pdf.PDFInteger(AnnotFlagPrint)
	extraAP.Entries["AP"] = ap
	ctx3 := &ValidationContext{}
	validateAnnotation(extraAP, ctx3)
	if !hasCheck(ctx3, pdf.Checks.Annotation.AppearanceMissingN) {
		t.Error("expected AppearanceMissingN when AP has no N entry")
	}
	if !hasCheck(ctx3, pdf.Checks.Annotation.AppearanceExtraEntries) {
		t.Error("expected AppearanceExtraEntries for an AP dict with a D entry")
	}

	// N present but not a stream/subdictionary.
	badN := pdf.NewPDFDict()
	badN.Entries["N"] = pdf.PDFName{Value: "not-a-stream"}
	badNAnnot := pdf.NewPDFDict()
	badNAnnot.Entries["Type"] = pdf.PDFName{Value: "Annot"}
	badNAnnot.Entries["Subtype"] = pdf.PDFName{Value: "Square"}
	badNAnnot.Entries["F"] = pdf.PDFInteger(AnnotFlagPrint)
	badNAnnot.Entries["AP"] = badN
	ctx4 := &ValidationContext{}
	validateAnnotation(badNAnnot, ctx4)
	if !hasCheck(ctx4, pdf.Checks.Annotation.AppearanceNNotStream) {
		t.Error("expected AppearanceNNotStream when N is not a dict")
	}

	// Popup/Link are exempt from the missing-appearance check.
	popup := pdf.NewPDFDict()
	popup.Entries["Type"] = pdf.PDFName{Value: "Annot"}
	popup.Entries["Subtype"] = pdf.PDFName{Value: "Popup"}
	popup.Entries["F"] = pdf.PDFInteger(AnnotFlagPrint)
	ctx5 := &ValidationContext{}
	validateAnnotation(popup, ctx5)
	if hasCheck(ctx5, pdf.Checks.Annotation.MissingAppearance) {
		t.Error("Popup should be exempt from MissingAppearance")
	}
}

func TestIsAllowedBlendMode(t *testing.T) {
	if !IsAllowedBlendMode(pdf.PDFName{Value: "Normal"}) {
		t.Error("Normal should be allowed")
	}
	if !IsAllowedBlendMode(pdf.PDFName{Value: "Compatible"}) {
		t.Error("Compatible should be allowed")
	}
	if IsAllowedBlendMode(pdf.PDFName{Value: "Multiply"}) {
		t.Error("Multiply should not be allowed")
	}
	if !IsAllowedBlendMode(pdf.PDFArray{pdf.PDFName{Value: "Normal"}, pdf.PDFName{Value: "Compatible"}}) {
		t.Error("array of allowed modes should be allowed")
	}
	if IsAllowedBlendMode(pdf.PDFArray{pdf.PDFName{Value: "Normal"}, pdf.PDFName{Value: "Screen"}}) {
		t.Error("array containing a disallowed mode should be rejected")
	}
	if IsAllowedBlendMode(pdf.PDFArray{pdf.PDFInteger(1)}) {
		t.Error("array with a non-name element should be rejected")
	}
	if IsAllowedBlendMode(pdf.PDFInteger(1)) {
		t.Error("non-name/array should be false")
	}
}
