package fontprog

import (
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// TestParseCIDWidths covers both the "c [w...]" and "cfirst clast w" forms.
func TestParseCIDWidths(t *testing.T) {
	w := pdf.PDFArray{
		pdf.PDFInteger(1), pdf.PDFArray{pdf.PDFInteger(500), pdf.PDFInteger(600)},
		pdf.PDFInteger(5), pdf.PDFInteger(7), pdf.PDFInteger(400),
	}
	got := ParseCIDWidths(w)
	m := map[int]int{}
	for _, pair := range got {
		m[pair[0]] = pair[1]
	}
	if m[1] != 500 || m[2] != 600 || m[5] != 400 || m[7] != 400 {
		t.Errorf("ParseCIDWidths = %v", got)
	}
}

func TestParseCIDWidthsMalformedEntries(t *testing.T) {
	// A non-numeric leading entry is skipped rather than misinterpreted.
	w := pdf.PDFArray{pdf.PDFName{Value: "oops"}, pdf.PDFInteger(1), pdf.PDFArray{pdf.PDFInteger(500)}}
	got := ParseCIDWidths(w)
	if len(got) != 1 || got[0][0] != 1 || got[0][1] != 500 {
		t.Errorf("ParseCIDWidths with a leading non-numeric entry = %v", got)
	}

	// Truncated range form (missing width) yields nothing extra.
	w2 := pdf.PDFArray{pdf.PDFInteger(1), pdf.PDFInteger(5)}
	if got := ParseCIDWidths(w2); got != nil {
		t.Errorf("ParseCIDWidths(truncated range) = %v, want nil", got)
	}

	// Non-numeric second/third elements in range form are skipped.
	w3 := pdf.PDFArray{pdf.PDFInteger(1), pdf.PDFName{Value: "x"}}
	if got := ParseCIDWidths(w3); got != nil {
		t.Errorf("ParseCIDWidths(non-numeric c2) = %v, want nil", got)
	}
	w4 := pdf.PDFArray{pdf.PDFInteger(1), pdf.PDFInteger(2), pdf.PDFName{Value: "x"}}
	if got := ParseCIDWidths(w4); got != nil {
		t.Errorf("ParseCIDWidths(non-numeric width) = %v, want nil", got)
	}
}
