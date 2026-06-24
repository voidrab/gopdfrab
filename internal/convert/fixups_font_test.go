package convert

import (
	"testing"

	"github.com/voidrab/gopdfrab/internal/check"
	"github.com/voidrab/gopdfrab/internal/pdf"
)

// TestFontDictFixerAddsIdentityCIDToGIDMap builds a minimal trailer with a
// Type0 font whose CIDFontType2 descendant lacks /CIDToGIDMap, runs
// fontDictFixer.Fix, and checks that /CIDToGIDMap /Identity is added -- and
// that a second pass is a no-op, since the fixer must be idempotent for the
// bounded convert loop's progress detection (sameMultiset, convert.go) to
// terminate.
func TestFontDictFixerAddsIdentityCIDToGIDMap(t *testing.T) {
	cidFont := pdf.NewPDFDict()
	cidFont.Entries["Type"] = pdf.PDFName{Value: "Font"}
	cidFont.Entries["Subtype"] = pdf.PDFName{Value: "CIDFontType2"}
	cidFont.Entries["BaseFont"] = pdf.PDFName{Value: "Test"}

	type0 := pdf.NewPDFDict()
	type0.Entries["Type"] = pdf.PDFName{Value: "Font"}
	type0.Entries["Subtype"] = pdf.PDFName{Value: "Type0"}
	type0.Entries["DescendantFonts"] = pdf.PDFArray{cidFont}

	trailer := pdf.NewPDFDict()
	trailer.Entries["Root"] = pdf.PDFDict{Entries: map[string]pdf.PDFValue{"Font": type0}}

	fixer := fontDictFixer{}

	changed, err := fixer.Fix(&trailer, nil)
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if !changed {
		t.Fatalf("changed = false, want true (CIDToGIDMap was missing)")
	}
	got, ok := cidFont.Entries["CIDToGIDMap"].(pdf.PDFName)
	if !ok || got.Value != "Identity" {
		t.Fatalf("CIDToGIDMap = %#v, want pdf.PDFName{Identity}", cidFont.Entries["CIDToGIDMap"])
	}

	changed, err = fixer.Fix(&trailer, nil)
	if err != nil {
		t.Fatalf("Fix (second pass): %v", err)
	}
	if changed {
		t.Errorf("changed = true on second pass, want false (fixer must be idempotent)")
	}
}

// TestFontDictFixerAppliesOnlyToCIDToGIDMapMissing checks the Fixer/Applies
// contract (mirroring the registration pattern in fixups_dict.go): the
// fixer must claim exactly check.Checks.Font.CIDToGIDMapMissing and nothing else,
// since registerFixer panics on a check.Check claimed by more than one Fixer.
func TestFontDictFixerAppliesOnlyToCIDToGIDMapMissing(t *testing.T) {
	fixer := fontDictFixer{}
	for _, c := range check.AllChecks() {
		want := c == check.Checks.Font.CIDToGIDMapMissing
		if got := fixer.Applies(c); got != want {
			t.Errorf("Applies(%s) = %v, want %v", c.Name(), got, want)
		}
	}
}
