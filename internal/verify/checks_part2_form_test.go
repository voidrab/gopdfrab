package verify

import (
	"bytes"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfgen"
)

// catalogDoc returns a classic-xref PDF whose catalog carries the given
// extra entries.
func catalogDoc(extra string) []byte {
	b := pdfgen.NewBuilder("%PDF-1.7\n")
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R "+extra+" >>")
	b.Obj(2, "<< /Type /Pages /Count 0 /Kids [] >>")
	return b.FinishClassic("<< /Size 3 /Root 1 0 R >>")
}

func TestVerifyInteractiveFormsNeedsRendering(t *testing.T) {
	for _, tc := range []struct {
		name  string
		extra string
		want  bool
	}{
		{"true", "/NeedsRendering true", true},
		{"false", "/NeedsRendering false", false},
		{"not a boolean", "/NeedsRendering 1", true},
		{"absent", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, err := pdf.OpenBytes(catalogDoc(tc.extra))
			if err != nil {
				t.Fatalf("OpenBytes: %v", err)
			}
			defer d.Close()
			errs := verifyInteractiveForms(d)
			got := len(errs) == 1 && errs[0].Check() == pdf.Checks.Form.NeedsRendering
			if got != tc.want || (!tc.want && len(errs) != 0) {
				t.Errorf("verifyInteractiveForms = %v, want NeedsRendering %v", errs, tc.want)
			}
		})
	}
}

func TestVerifyInteractiveFormsCatalogNotADict(t *testing.T) {
	doc := bytes.Replace(catalogDoc(""), []byte("/Root 1 0 R"), []byte("/Root 1    "), 1)
	d, err := pdf.OpenBytes(doc)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	defer d.Close()
	if errs := verifyInteractiveForms(d); errs != nil {
		t.Errorf("verifyInteractiveForms = %v, want nil", errs)
	}
}

// TestValidateFormFieldTypelessWidget verifies that a widget without the
// optional Type entry is a widget too.
func TestValidateFormFieldTypelessWidget(t *testing.T) {
	widget := kv("Subtype", name("Widget"), "Rect", pdf.PDFArray{}, "A", pdf.NewPDFDict())
	ctx := &ValidationContext{Spec: a2bSpec}
	validateFormField(widget, ctx)
	if len(ctx.errs) != 1 || ctx.errs[0].Check() != pdf.Checks.Form.FieldAction {
		t.Errorf("errs = %v, want FieldAction", ctx.errs)
	}
}
