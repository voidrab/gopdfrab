package convert

import (
	"bytes"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/verify"
	"github.com/voidrab/gopdfrab/internal/writer"
)

const onePageContent = "0 0 100 100 re f"

// buildOnePageDoc serializes a minimal objmodel-clean one-page document after
// applying mutate to its trailer graph.
func buildOnePageDoc(t *testing.T, mutate func(trailer, catalog, page pdf.PDFDict)) []byte {
	t.Helper()

	contents := pdf.NewPDFDict()
	contents.HasStream = true
	contents.RawStream = []byte(onePageContent)
	contents.Entries["_ref"] = pdf.PDFRef{ObjNum: 4}

	pages := pdf.NewPDFDict()
	pages.Entries["Type"] = pdf.PDFName{Value: "Pages"}
	pages.Entries["Count"] = pdf.PDFInteger(1)
	pages.Entries["_ref"] = pdf.PDFRef{ObjNum: 2}

	page := pdf.NewPDFDict()
	page.Entries["Type"] = pdf.PDFName{Value: "Page"}
	page.Entries["Parent"] = pages
	page.Entries["MediaBox"] = pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFInteger(612), pdf.PDFInteger(792)}
	page.Entries["Contents"] = contents
	page.Entries["_ref"] = pdf.PDFRef{ObjNum: 3}
	pages.Entries["Kids"] = pdf.PDFArray{page}

	catalog := pdf.NewPDFDict()
	catalog.Entries["Type"] = pdf.PDFName{Value: "Catalog"}
	catalog.Entries["Pages"] = pages
	catalog.Entries["_ref"] = pdf.PDFRef{ObjNum: 1}

	trailer := pdf.NewPDFDict()
	trailer.Entries["Root"] = catalog

	if mutate != nil {
		mutate(trailer, catalog, page)
	}

	var buf bytes.Buffer
	if err := writer.WriteDocument(&buf, trailer); err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}
	return buf.Bytes()
}

// TestHasFixableIssueRasterGate pins the raster gate's objmodel exemptions:
// dict-structural findings justify flattening only the page they sit on,
// never a document-wide flatten.
func TestHasFixableIssueRasterGate(t *testing.T) {
	fixers := map[pdf.Check]Fixer{
		pdf.Checks.ObjectModel.DisallowedValue:   nil,
		pdf.Checks.Structure.FileHeaderSignature: nil,
	}
	mk := func(c pdf.Check, page int) []pdf.PDFError {
		return []pdf.PDFError{pdf.NewError(c, nil, page, nil)}
	}

	cases := []struct {
		name          string
		issues        []pdf.PDFError
		page, docWide bool
	}{
		{"no issues", nil, false, false},
		{"no registered fixer", mk(pdf.Checks.ObjectModel.WrongValueType, 1), false, false},
		{"non-objmodel doc-level", mk(pdf.Checks.Structure.FileHeaderSignature, 0), true, true},
		{"objmodel page-attributed", mk(pdf.Checks.ObjectModel.DisallowedValue, 2), true, false},
		{"objmodel doc-level", mk(pdf.Checks.ObjectModel.DisallowedValue, 0), false, false},
	}
	for _, tc := range cases {
		if got := hasFixableIssue(tc.issues, fixers, false); got != tc.page {
			t.Errorf("%s: hasFixableIssue(docWide=false) = %v, want %v", tc.name, got, tc.page)
		}
		if got := hasFixableIssue(tc.issues, fixers, true); got != tc.docWide {
			t.Errorf("%s: hasFixableIssue(docWide=true) = %v, want %v", tc.name, got, tc.docWide)
		}
	}
}

// TestConvertObjectModelDeletesDisallowedTrapped: a document-level
// DisallowedValue on an optional key (/Trapped /Maybe) is repaired by
// deletion, and the page content stays byte-identical -- proving the fix came
// from the fixer, never from the raster backstop (which can neither reach nor
// repair trailer-level dict structure; TestHasFixableIssueRasterGate pins the
// gate itself).
func TestConvertObjectModelDeletesDisallowedTrapped(t *testing.T) {
	data := buildOnePageDoc(t, func(trailer, _, _ pdf.PDFDict) {
		info := pdf.NewPDFDict()
		info.Entries["Trapped"] = pdf.PDFName{Value: "Maybe"} // enum allows True/False/Unknown
		info.Entries["_ref"] = pdf.PDFRef{ObjNum: 5}
		trailer.Entries["Info"] = info
	})

	res, err := verify.VerifyBytes(data, pdf.PDF)
	if err != nil {
		t.Fatalf("VerifyBytes: %v", err)
	}
	if res.Valid || !hasIssueForCheck(res.Issues, pdf.Checks.ObjectModel.DisallowedValue) {
		t.Fatalf("fixture must fail with a document-level DisallowedValue, got %v", res.Issues)
	}

	cr, err := ConvertBytes(data, pdf.PDF)
	if err != nil {
		t.Fatalf("ConvertBytes: %v", err)
	}
	if !cr.Result.Valid || len(cr.Residual()) != 0 {
		t.Fatalf("Valid=%v, residual %v", cr.Result.Valid, issueClauses(cr.Residual()))
	}

	out, err := pdf.OpenBytes(cr.Output)
	if err != nil {
		t.Fatalf("OpenBytes(output): %v", err)
	}
	defer out.Close()
	graph, err := out.ResolveGraph()
	if err != nil {
		t.Fatalf("ResolveGraph(output): %v", err)
	}
	if info, ok := graph.(pdf.PDFDict).Entries["Info"].(pdf.PDFDict); ok {
		if _, still := info.Entries["Trapped"]; still {
			t.Error("Trapped must be deleted from the output Info dict")
		}
	}
	page := assertOnePageGraph(t, graph)
	assertContentStream(t, page, onePageContent)
}

func hasIssueForCheck(issues []pdf.PDFError, c pdf.Check) bool {
	for _, iss := range issues {
		if iss.Check() == c {
			return true
		}
	}
	return false
}
