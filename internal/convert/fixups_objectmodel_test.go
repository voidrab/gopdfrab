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

func TestDescentSignFixerNegatesPositiveDescent(t *testing.T) {
	trailer := pdf.NewPDFDict()
	fd := pdf.NewPDFDict()
	fd.Entries["Type"] = pdf.PDFName{Value: "FontDescriptor"}
	fd.Entries["Descent"] = pdf.PDFInteger(205)
	trailer.Entries["FD"] = fd
	fdReal := pdf.NewPDFDict()
	fdReal.Entries["Type"] = pdf.PDFName{Value: "FontDescriptor"}
	fdReal.Entries["Descent"] = pdf.PDFReal(12.5)
	trailer.Entries["FDReal"] = fdReal
	other := pdf.NewPDFDict()
	other.Entries["Descent"] = pdf.PDFInteger(300) // not a FontDescriptor
	trailer.Entries["Other"] = other

	changed, err := descentSignFixer{}.Fix(&trailer, nil)
	if err != nil || !changed {
		t.Fatalf("Fix: changed=%v err=%v, want changed", changed, err)
	}
	if got := fd.Entries["Descent"]; got != pdf.PDFInteger(-205) {
		t.Errorf("integer Descent = %v, want -205", got)
	}
	if got := fdReal.Entries["Descent"]; got != pdf.PDFReal(-12.5) {
		t.Errorf("real Descent = %v, want -12.5", got)
	}
	if got := other.Entries["Descent"]; got != pdf.PDFInteger(300) {
		t.Errorf("non-descriptor Descent = %v, want untouched 300", got)
	}

	changed, err = descentSignFixer{}.Fix(&trailer, nil)
	if err != nil || changed {
		t.Errorf("second Fix: changed=%v err=%v, want idempotent no-op", changed, err)
	}
}
