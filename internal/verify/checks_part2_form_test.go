package verify

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// catalogDoc returns a classic-xref PDF whose catalog carries the given
// extra entries.
func catalogDoc(extra string) []byte {
	header := "%PDF-1.7\n"
	obj1 := "1 0 obj\n<< /Type /Catalog /Pages 2 0 R " + extra + " >>\nendobj\n"
	obj2 := "2 0 obj\n<< /Type /Pages /Count 0 /Kids [] >>\nendobj\n"
	off1 := len(header)
	off2 := off1 + len(obj1)
	xref := fmt.Sprintf("xref\n0 3\n0000000000 65535 f \n%010d 00000 n \n%010d 00000 n \n", off1, off2)
	tail := fmt.Sprintf("trailer\n<< /Size 3 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF", off2+len(obj2))
	return []byte(header + obj1 + obj2 + xref + tail)
}

func TestVerifyInteractiveFormsNeedsRendering(t *testing.T) {
	for _, tc := range []struct {
		name  string
		extra string
		part2 bool
		want  bool
	}{
		{"true", "/NeedsRendering true", true, true},
		{"false", "/NeedsRendering false", true, false},
		{"not a boolean", "/NeedsRendering 1", true, true},
		{"absent", "", true, false},
		{"under Part 1", "/NeedsRendering true", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, err := pdf.OpenBytes(catalogDoc(tc.extra))
			if err != nil {
				t.Fatalf("OpenBytes: %v", err)
			}
			defer d.Close()
			errs := verifyInteractiveForms(d, tc.part2)
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
	if errs := verifyInteractiveForms(d, true); errs != nil {
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
