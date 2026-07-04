package verify

import (
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

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
