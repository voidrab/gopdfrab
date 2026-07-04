package verify

import (
	"fmt"
	"os"
	"strings"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// hasCheck reports whether ctx recorded a violation of c.
func hasCheck(ctx *ValidationContext, c pdf.Check) bool {
	for _, e := range ctx.errs {
		if e.Check() == c {
			return true
		}
	}
	return false
}

// createValidPDF writes a minimal but structurally valid classic-xref PDF to
// filename, for tests that need a real file Open can parse.
func createValidPDF(filename string) error {
	header := "%PDF-1.7\n"
	comment := "%äüöß\n"
	obj1 := "1 0 obj\n<< /Type /Catalog /Pages 2 0 R /OCProperties (Test) >>\nendobj\n"
	obj2 := "2 0 obj\n<< /Type /Pages /Count 5 >>\nendobj\n"
	obj3 := "3 0 obj\n<< /Title (Test PDF) /Producer (GoLib) >>\nendobj\n"

	offset1 := len(header) + len(comment)
	offset2 := offset1 + len(obj1)
	offset3 := offset2 + len(obj2)
	xrefOffset := offset3 + len(obj3)

	xref := fmt.Sprintf("xref\n0 4\n0000000000 65535 f \n%010d 00000 n \n%010d 00000 n \n%010d 00000 n \n",
		offset1, offset2, offset3)

	trailer := "trailer\n<< /Size 4 /Root 1 0 R /Info 3 0 R >>\n"
	startxref := fmt.Sprintf("startxref\n%d\n%%EOF", xrefOffset)

	content := header + comment + obj1 + obj2 + obj3 + xref + trailer + startxref
	return os.WriteFile(filename, []byte(content), 0644)
}

// isartorDir and veraPDFDir locate the reference test corpora relative to
// this package's directory (two levels under the repo root).
const (
	isartorDir = "../../tests/Isartor/PDFA-1b"
	veraPDFDir = "../../tests/veraPDF/PDF_A-1b"
)

// clauseMatches reports whether a reported clause satisfies the expected clause.
// A match holds when the two are equal or one is a dot-boundary prefix of the
// other (so "6.2.3" satisfies an expected "6.2.3.3", and vice versa).
func clauseMatches(got, expected string) bool {
	if got == expected {
		return true
	}
	return strings.HasPrefix(got+".", expected+".") ||
		strings.HasPrefix(expected+".", got+".")
}
