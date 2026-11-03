package verify

import (
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfa"
)

// TestAdditionalActionsBarred verifies that PDF/A-1 bars an AA entry from
// every dictionary and PDF/A-2 only from the catalog and the pages.
func TestAdditionalActionsBarred(t *testing.T) {
	aa := kv("O", kv("S", name("GoTo")))
	for _, tc := range []struct {
		name string
		d    pdf.PDFDict
		spec *pdfa.Spec
		want bool
	}{
		{"1b annotation", kv("Type", name("Annot"), "AA", aa), a1bSpec, true},
		{"2b annotation", kv("Type", name("Annot"), "AA", aa), a2bSpec, false},
		{"2b catalog", kv("Type", name("Catalog"), "AA", aa), a2bSpec, true},
		{"2b page", kv("Type", name("Page"), "AA", aa), a2bSpec, true},
		{"2b page without AA", kv("Type", name("Page")), a2bSpec, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := AdditionalActionsBarred(tc.d, tc.spec); got != tc.want {
				t.Errorf("AdditionalActionsBarred = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestActionOwnerKey verifies which entry of a dictionary names its action.
func TestActionOwnerKey(t *testing.T) {
	for _, tc := range []struct {
		name string
		d    pdf.PDFDict
		want string
	}{
		{"annotation", kv("Type", name("Annot"), "Subtype", name("Link")), "A"},
		{"outline item", kv("Title", pdf.PDFString{Value: "x"}, "Parent", kv()), "A"},
		{"information dictionary", kv("Title", pdf.PDFString{Value: "x"}), ""},
		{"catalog", kv("Type", name("Catalog")), "OpenAction"},
		{"action", kv("S", name("GoTo")), "Next"},
		{"unknown action type", kv("S", name("Foo")), ""},
		{"structure element", kv("Type", name("StructElem"), "S", name("P")), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ActionOwnerKey(tc.d); got != tc.want {
				t.Errorf("ActionOwnerKey = %q, want %q", got, tc.want)
			}
		})
	}
}
