package pdf_test

import (
	"errors"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// TestOpenBytesErrorClassification pins the typed-error contract: callers can
// tell "not a PDF" from "damaged structure" with errors.Is instead of matching
// on message text.
func TestOpenBytesErrorClassification(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want error
	}{
		{"no header anywhere", []byte("just some bytes with no marker anywhere in here at all"), pdf.ErrNotPDF},
		{"too short for a header", []byte("PDF"), pdf.ErrNotPDF},
		{"header but no startxref", []byte("%PDF-1.7\n1 0 obj\n<< /Type /Catalog >>\nendobj\n"), pdf.ErrDamaged},
		{"header but unparseable startxref", []byte("%PDF-1.7\nstuff\nstartxref\nNOTANUMBER\n%%EOF\n"), pdf.ErrDamaged},
	}
	for _, c := range cases {
		if _, err := pdf.OpenBytes(c.data); !errors.Is(err, c.want) {
			t.Errorf("%s: err=%v, want errors.Is(%v)", c.name, err, c.want)
		}
	}
}

// TestGarbagePrefixIsNotErrNotPDF confirms a tolerated garbage prefix before the
// %PDF- marker is not misclassified as "not a PDF" (it is a 6.1.2 concern, not a
// parse failure).
func TestGarbagePrefixIsNotErrNotPDF(t *testing.T) {
	minimal := "%PDF-1.7\n1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n"
	if _, err := pdf.OpenBytes([]byte("XXXXX" + minimal)); errors.Is(err, pdf.ErrNotPDF) {
		t.Error("garbage prefix before %PDF- must not be ErrNotPDF")
	}
}
