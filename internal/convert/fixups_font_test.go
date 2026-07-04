package convert

import (
	"testing"

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
// fixer must claim exactly Checks.Font.CIDToGIDMapMissing and nothing else,
// since registerFixer panics on a Check claimed by more than one Fixer.
func TestFontDictFixerAppliesOnlyToCIDToGIDMapMissing(t *testing.T) {
	fixer := fontDictFixer{}
	for _, c := range pdf.AllChecks() {
		want := c == pdf.Checks.Font.CIDToGIDMapMissing
		if got := fixer.Applies(c); got != want {
			t.Errorf("Applies(%s) = %v, want %v", c.Name(), got, want)
		}
	}
}

func TestType0FontFixerCIDSystemInfo(t *testing.T) {
	cid := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Font"}, "Subtype": pdf.PDFName{Value: "CIDFontType2"},
		"CIDSystemInfo": pdf.PDFDict{Entries: map[string]pdf.PDFValue{
			"Registry": pdf.PDFString{Value: "Adobe"}, "Ordering": pdf.PDFString{Value: "Japan1"},
			"Supplement": pdf.PDFInteger(0),
		}},
	}}
	cmap := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "CMap"},
		"CIDSystemInfo": pdf.PDFDict{Entries: map[string]pdf.PDFValue{
			"Registry": pdf.PDFString{Value: "Adobe"}, "Ordering": pdf.PDFString{Value: "Identity"},
			"Supplement": pdf.PDFInteger(0),
		}},
	}}
	font := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Font"}, "Subtype": pdf.PDFName{Value: "Type0"},
		"Encoding": cmap, "DescendantFonts": pdf.PDFArray{cid},
	}}
	trailer := trailerWith("F1", font)
	changed, err := type0FontFixer{}.Fix(&trailer, nil)
	if err != nil || !changed {
		t.Fatalf("type0FontFixer.Fix = %v, %v", changed, err)
	}
	got := cmap.Entries["CIDSystemInfo"].(pdf.PDFDict).Entries["Ordering"]
	if got != (pdf.PDFString{Value: "Japan1"}) {
		t.Errorf("CMap CIDSystemInfo Ordering = %v, want it copied from the CIDFont (Japan1)", got)
	}
}
