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
		{"structure element", kv("Type", name("StructElem"), "S", name("Sound")), ""},
		{"typed action", kv("Type", name("Action"), "S", name("GoTo")), "Next"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ActionOwnerKey(tc.d); got != tc.want {
				t.Errorf("ActionOwnerKey = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestValidateActionsNextArrayWithoutS verifies that an action in a /Next
// array with no /S is reported, as a single one is.
func TestValidateActionsNextArrayWithoutS(t *testing.T) {
	owner := kv("S", name("GoTo"), "Next", pdf.PDFArray{kv("S", name("URI")), kv()})
	ctx := &ValidationContext{Spec: a2bSpec}
	validateActions(owner, ctx)
	if !hasCheck(ctx, pdf.Checks.Action.ForbiddenActionType) {
		t.Errorf("errs = %v, want ForbiddenActionType", ctx.errs)
	}
}

// TestValidateActionsStructElemNamedLikeAction verifies that a structure
// element whose type is spelt like a forbidden action is no action.
func TestValidateActionsStructElemNamedLikeAction(t *testing.T) {
	ctx := &ValidationContext{Spec: a2bSpec}
	validateActions(kv("Type", name("StructElem"), "S", name("Sound")), ctx)
	if len(ctx.errs) != 0 {
		t.Errorf("errs = %v, want none", ctx.errs)
	}
}

// TestValidateActionsTriggerWithoutS verifies that a trigger of an AA entry
// naming a dictionary with no /S is reported.
func TestValidateActionsTriggerWithoutS(t *testing.T) {
	annot := kv("Type", name("Annot"), "Subtype", name("Text"), "AA", kv("E", kv()))
	ctx := &ValidationContext{Spec: a2bSpec}
	validateActions(annot, ctx)
	if !hasCheck(ctx, pdf.Checks.Action.ForbiddenActionType) {
		t.Errorf("errs = %v, want ForbiddenActionType", ctx.errs)
	}
}

// TestValidateActionsOpenActionDestination verifies that an /OpenAction
// naming a destination array is no action of unknown type.
func TestValidateActionsOpenActionDestination(t *testing.T) {
	catalog := kv("Type", name("Catalog"), "OpenAction", pdf.PDFArray{kv("Type", name("Page")), name("Fit")})
	ctx := &ValidationContext{Spec: a1bSpec}
	validateActions(catalog, ctx)
	if len(ctx.errs) != 0 {
		t.Errorf("errs = %v, want none", ctx.errs)
	}
}
