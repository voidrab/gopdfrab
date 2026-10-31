package verify

import (
	"strings"
	"testing"

	"github.com/voidrab/gopdfrab/internal/arlington"
	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfa"
)

// pdf14 is the object model PDF/A-1b checks against.
var pdf14 = arlington.ModelFor("1.4")

// specWithModel returns a copy of PDF/A-1b's spec that checks against the
// object model of the given PDF version.
func specWithModel(version string) *pdfa.Spec {
	s := a1b()
	s.ModelVersion = version
	return &s
}

// TestEveryLevelHasAnObjectModel verifies that every level with a spec names
// a PDF version an Arlington model is vendored for, so objectModel cannot
// panic on a real level.
func TestEveryLevelHasAnObjectModel(t *testing.T) {
	for _, level := range []pdf.LevelType{pdf.A1B, pdf.ObjectModel} {
		s, ok := pdfa.For(level)
		if !ok {
			t.Fatalf("pdfa.For(%v) has no spec", level)
		}
		if arlington.ModelFor(s.ModelVersion) == nil {
			t.Errorf("level %v names PDF %q, which has no Arlington model", level, s.ModelVersion)
		}
	}
}

// TestObjectModelFollowsSpecVersion verifies that the schema checks use the
// model of the spec's PDF version: a JPEG 2000 image is a disallowed /Filter
// value under PDF 1.4 and a legal one under PDF 1.7.
func TestObjectModelFollowsSpecVersion(t *testing.T) {
	img := pdf.NewPDFDict()
	img.Entries.Set("Type", pdf.PDFName{Value: "XObject"})
	img.Entries.Set("Subtype", pdf.PDFName{Value: "Image"})
	img.Entries.Set("Width", pdf.PDFInteger(8))
	img.Entries.Set("Height", pdf.PDFInteger(8))
	img.Entries.Set("Filter", pdf.PDFName{Value: "JPXDecode"})
	img.SetStream([]byte("codestream"))

	ctx := &ValidationContext{Spec: specWithModel("1.4")}
	validateAgainstSchema(img, "XObjectImage", ctx)
	if !hasCheck(ctx, pdf.Checks.ObjectModel.DisallowedValue) {
		t.Errorf("PDF 1.4: want DisallowedValue for /Filter /JPXDecode, got %v", ctx.errs)
	}

	ctx = &ValidationContext{Spec: specWithModel("1.7")}
	validateAgainstSchema(img, "XObjectImage", ctx)
	if len(ctx.errs) != 0 {
		t.Errorf("PDF 1.7: want no issues for a JPX image, got %v", ctx.errs)
	}
}

// TestObjectStreamSchemaOnlyInPDF17 verifies that an object stream is an
// unknown type to the PDF 1.4 model and a checked one to the PDF 1.7 model:
// it self-identifies as ObjectStream there, and a missing /N is reported.
func TestObjectStreamSchemaOnlyInPDF17(t *testing.T) {
	objStm := pdf.NewPDFDict()
	objStm.Entries.Set("Type", pdf.PDFName{Value: "ObjStm"})
	objStm.Entries.Set("First", pdf.PDFInteger(4))
	objStm.SetStream([]byte("1 0 "))

	if got := selfIdentifiedType(pdf14, objStm); got != "" {
		t.Errorf("PDF 1.4: selfIdentifiedType(ObjStm) = %q, want \"\"", got)
	}
	m17 := arlington.ModelFor("1.7")
	got := selfIdentifiedType(m17, objStm)
	if got != "ObjectStream" {
		t.Fatalf("PDF 1.7: selfIdentifiedType(ObjStm) = %q, want ObjectStream", got)
	}

	ctx := &ValidationContext{Spec: specWithModel("1.7")}
	validateAgainstSchema(objStm, got, ctx)
	if len(ctx.errs) != 1 || ctx.errs[0].Check() != pdf.Checks.ObjectModel.MissingRequiredKey {
		t.Fatalf("PDF 1.7: want one MissingRequiredKey (/N), got %v", ctx.errs)
	}

	objStm.Entries.Set("N", pdf.PDFInteger(1))
	ctx = &ValidationContext{Spec: specWithModel("1.7")}
	validateAgainstSchema(objStm, got, ctx)
	if len(ctx.errs) != 0 {
		t.Errorf("PDF 1.7: want no issues for a complete object stream, got %v", ctx.errs)
	}
}

// TestKeyIntroducedAfterNamesModelVersion verifies that a key the model's
// version lacks is reported with that version, and that a key PDF 1.7 defines
// is not reported against the PDF 1.7 model.
func TestKeyIntroducedAfterNamesModelVersion(t *testing.T) {
	vp := pdf.NewPDFDict()
	vp.Entries.Set("PrintScaling", pdf.PDFName{Value: "None"})

	ctx := &ValidationContext{Spec: specWithModel("1.4")}
	validateAgainstSchema(vp, "ViewerPreferences", ctx)
	if len(ctx.errs) != 1 || ctx.errs[0].Check() != pdf.Checks.ObjectModel.KeyIntroducedAfterPDF14 {
		t.Fatalf("PDF 1.4: want one KeyIntroducedAfterPDF14, got %v", ctx.errs)
	}
	if want := `ViewerPreferences key "PrintScaling" was introduced after PDF 1.4`; !strings.Contains(ctx.errs[0].Error(), want) {
		t.Errorf("PDF 1.4: message = %q, want it to contain %q", ctx.errs[0].Error(), want)
	}

	ctx = &ValidationContext{Spec: specWithModel("1.7")}
	validateAgainstSchema(vp, "ViewerPreferences", ctx)
	if len(ctx.errs) != 0 {
		t.Errorf("PDF 1.7: want no issues for /PrintScaling, got %v", ctx.errs)
	}
}

// TestObjectModelUnknownVersionPanics verifies that a spec naming a PDF
// version with no vendored model panics instead of checking against a
// guessed one.
func TestObjectModelUnknownVersionPanics(t *testing.T) {
	defer func() {
		if r, _ := recover().(string); r != "verify: no Arlington model for PDF 9.9" {
			t.Errorf("recover() = %q, want the no-model panic", r)
		}
	}()
	ctx := &ValidationContext{Spec: specWithModel("9.9")}
	ctx.objectModel()
}
