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

// TestValidateActionsOwnedActions verifies which dictionaries an owner names
// that are reported as actions of no known type, and that a structure element
// is no action whatever its /S.
func TestValidateActionsOwnedActions(t *testing.T) {
	for _, tc := range []struct {
		name string
		d    pdf.PDFDict
		want bool
	}{
		{"/Next array member without /S",
			kv("S", name("GoTo"), "Next", pdf.PDFArray{kv("S", name("URI")), kv()}), true},
		{"AA trigger without /S",
			kv("Type", name("Annot"), "Subtype", name("Text"), "AA", kv("E", kv())), true},
		{"/OpenAction destination",
			kv("Type", name("Catalog"), "OpenAction", pdf.PDFArray{kv("Type", name("Page")), name("Fit")}), false},
		{"structure element of type /Sound",
			kv("Type", name("StructElem"), "S", name("Sound")), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := &ValidationContext{Spec: a2bSpec}
			validateActions(tc.d, ctx)
			if got := hasCheck(ctx, pdf.Checks.Action.ForbiddenActionType); got != tc.want || !tc.want && len(ctx.errs) != 0 {
				t.Errorf("errs = %v, want ForbiddenActionType %v", ctx.errs, tc.want)
			}
		})
	}
}

// TestActionEntries verifies that ActionEntries yields the owner entry and
// then each AA trigger, and stops when the caller does.
func TestActionEntries(t *testing.T) {
	aa := kv("E", kv(), "X", kv())
	d := kv("S", name("GoTo"), "AA", aa)
	for stopAfter, want := range []string{"Next", "E", "X"} {
		var got []string
		for owner, key := range ActionEntries(d) {
			wantOwner := aa
			if key == "Next" {
				wantOwner = d
			}
			if pdf.ValuePointer(owner.Entries) != pdf.ValuePointer(wantOwner.Entries) {
				t.Errorf("%s yielded with the wrong owner", key)
			}
			got = append(got, key)
			if len(got) == stopAfter+1 {
				break
			}
		}
		if len(got) != stopAfter+1 || got[stopAfter] != want {
			t.Errorf("stopping after %d: got %v, want it to end with %s", stopAfter+1, got, want)
		}
	}
}
