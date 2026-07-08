package convert

import (
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

func TestIndirectRequiredFixerPromotesDirectDicts(t *testing.T) {
	trailer := pdf.NewPDFDict()
	info := pdf.NewPDFDict()
	info.Entries["Title"] = pdf.PDFString{Value: "T"}
	trailer.Entries["Info"] = info
	custom := pdf.NewPDFDict()
	trailer.Entries["Custom"] = custom

	changed, err := indirectRequiredFixer{}.Fix(&trailer, nil)
	if err != nil || !changed {
		t.Fatalf("Fix: changed=%v err=%v, want changed", changed, err)
	}
	if _, ok := info.Entries["_ref"].(pdf.PDFRef); !ok {
		t.Error("direct trailer Info was not promoted to an indirect object")
	}
	if _, ok := custom.Entries["_ref"]; ok {
		t.Error("a key outside the indirect-required set must stay direct")
	}

	changed, err = indirectRequiredFixer{}.Fix(&trailer, nil)
	if err != nil || changed {
		t.Errorf("second Fix: changed=%v err=%v, want idempotent no-op", changed, err)
	}
}
