package convert

import (
	"image"
	"os"
	"path/filepath"
	"testing"

	"github.com/voidrab/gopdfrab/internal/corpustest"
	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfa"
)

// a1b returns a fresh copy of PDF/A-1b's spec, for a test to bind a fixer to
// or to change one rule of.
func a1b() pdfa.Spec {
	s, _ := pdfa.For(pdf.A1B)
	return s
}

// a1bSpec is PDF/A-1b's spec for tests that only read it.
var a1bSpec = func() *pdfa.Spec { s := a1b(); return &s }()

// PDF/A-1b's limits, under the names the tests use for them.
var (
	maxPDFArrayElements = a1bSpec.Limits.MaxArrayElements
	maxDictEntries      = a1bSpec.Limits.MaxDictEntries
	maxNameLength       = a1bSpec.Limits.MaxNameBytes
	maxCMapCID          = a1bSpec.Limits.MaxCID
	maxQNesting         = a1bSpec.Limits.MaxQNesting
	realLimit           = a1bSpec.Limits.MaxReal
)

// isartorDir and veraDir locate the reference test corpora relative to this
// package's directory (two levels under the repo root).
const (
	isartorDir = "../../tests/Isartor/PDFA-1b"
	veraDir    = "../../tests/veraPDF/PDF_A-1b"
)

// issueClauses returns the PDF/A-1b clause of each issue, for diagnostics.
func issueClauses(issues []pdf.PDFError) []string {
	return corpustest.Clauses(issues, pdf.A1B)
}

// passFixtures returns the path of every PDF/A-1b pass fixture in the
// committed corpora, or nil if none is present.
func passFixtures(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, f := range corpustest.AllFixtures(false) {
		if f.Corpus.Level() == pdf.A1B {
			out = append(out, f.Path)
		}
	}
	return out
}

// mustOutput returns cr's converted bytes, failing the test on error. Use it
// where a conversion is expected to have produced output.
func mustOutput(t *testing.T, cr ConvertResult) []byte {
	t.Helper()
	b, err := cr.Output()
	if err != nil {
		t.Fatalf("Output(): %v", err)
	}
	return b
}

// assertNotRasterized fails the test if the conversion fell back to
// rasterizing any page. That fallback clears every residual on the pages it
// rebuilds, so an end-to-end test asserting only that a check is gone passes
// even when the fixer under test did nothing; this makes it prove the repair
// happened in place.
func assertNotRasterized(t *testing.T, cr ConvertResult) {
	t.Helper()
	if len(cr.RasterizedPages) != 0 {
		t.Errorf("pages %v were rasterized; the violation should have been repaired in place", cr.RasterizedPages)
	}
}

// inMemoryResult builds a ConvertResult holding data in memory, for tests that
// construct a result directly rather than via a conversion.
func inMemoryResult(data []byte) ConvertResult {
	return ConvertResult{backing: &outputBacking{mem: data}}
}

// writeTempPDF writes data to a temp file named name and returns its path.
func writeTempPDF(t *testing.T, name string, data []byte) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

// assertOnePageGraph walks Root -> Pages -> Kids[0] and returns the resolved
// Page dict, failing the test on any structural mismatch.
func assertOnePageGraph(t *testing.T, graph pdf.PDFValue) pdf.PDFDict {
	t.Helper()
	root, ok := graph.(pdf.PDFDict).Entries.Get("Root").(pdf.PDFDict)
	if !ok {
		t.Fatalf("Root did not resolve to a dict")
	}
	if !pdf.EqualPDFValue(root.Entries.Get("Type"), pdf.PDFName{Value: "Catalog"}) {
		t.Fatalf("Root/Type = %v, want /Catalog", root.Entries.Get("Type"))
	}

	pages, ok := root.Entries.Get("Pages").(pdf.PDFDict)
	if !ok {
		t.Fatalf("Root/Pages did not resolve to a dict")
	}
	kids, ok := pages.Entries.Get("Kids").(pdf.PDFArray)
	if !ok || len(kids) != 1 {
		t.Fatalf("Pages/Kids = %v, want a 1-element array", pages.Entries.Get("Kids"))
	}
	page, ok := kids[0].(pdf.PDFDict)
	if !ok {
		t.Fatalf("Kids[0] did not resolve to a dict")
	}
	if !pdf.EqualPDFValue(page.Entries.Get("Type"), pdf.PDFName{Value: "Page"}) {
		t.Fatalf("Kids[0]/Type = %v, want /Page", page.Entries.Get("Type"))
	}
	return page
}

// assertContentStream decodes page's /Contents stream and checks it matches want.
func assertContentStream(t *testing.T, page pdf.PDFDict, want string) {
	t.Helper()
	contents, ok := page.Entries.Get("Contents").(pdf.PDFDict)
	if !ok || !contents.IsStream() {
		t.Fatalf("Page/Contents did not resolve to a stream dict")
	}
	data, err := pdf.DecodeStream(contents)
	if err != nil {
		t.Fatalf("DecodeStream(Contents): %v", err)
	}
	if string(data) != want {
		t.Errorf("content stream = %q, want %q", data, want)
	}
}

// inkThreshold is the share of non-white pixels under which a render counts
// as blank, the fidelity gate's own bar.
const inkThreshold = 0.004

// inkFraction returns the share of img's pixels that are clearly not white,
// measured the way the fidelity gate measures a page.
func inkFraction(img *image.RGBA) float64 {
	if img == nil {
		return 0
	}
	b := img.Bounds()
	total := b.Dx() * b.Dy()
	if total == 0 {
		return 0
	}
	ink := 0
	pix := img.Pix
	for i := 0; i+3 < len(pix); i += 4 {
		if pix[i] < 245 || pix[i+1] < 245 || pix[i+2] < 245 {
			ink++
		}
	}
	return float64(ink) / float64(total)
}
