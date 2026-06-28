package writer

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/verify"
)

// veraDir locates the veraPDF reference corpus relative to this package's
// directory (two levels under the repo root).
const veraDir = "../../tests/veraPDF/PDF_A-1b"

// issueClauses returns the violated clause for each issue, for diagnostics.
func issueClauses(issues []pdf.PDFError) []string {
	out := make([]string, 0, len(issues))
	for _, iss := range issues {
		out = append(out, iss.Check().Clause())
	}
	return out
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
	root, ok := graph.(pdf.PDFDict).Entries["Root"].(pdf.PDFDict)
	if !ok {
		t.Fatalf("Root did not resolve to a dict")
	}
	if !pdf.EqualPDFValue(root.Entries["Type"], pdf.PDFName{Value: "Catalog"}) {
		t.Fatalf("Root/Type = %v, want /Catalog", root.Entries["Type"])
	}

	pages, ok := root.Entries["Pages"].(pdf.PDFDict)
	if !ok {
		t.Fatalf("Root/Pages did not resolve to a dict")
	}
	kids, ok := pages.Entries["Kids"].(pdf.PDFArray)
	if !ok || len(kids) != 1 {
		t.Fatalf("Pages/Kids = %v, want a 1-element array", pages.Entries["Kids"])
	}
	page, ok := kids[0].(pdf.PDFDict)
	if !ok {
		t.Fatalf("Kids[0] did not resolve to a dict")
	}
	if !pdf.EqualPDFValue(page.Entries["Type"], pdf.PDFName{Value: "Page"}) {
		t.Fatalf("Kids[0]/Type = %v, want /Page", page.Entries["Type"])
	}
	return page
}

// assertContentStream decodes page's /Contents stream and checks it matches want.
func assertContentStream(t *testing.T, doc *pdf.Reader, page pdf.PDFDict, want string) {
	t.Helper()
	contents, ok := page.Entries["Contents"].(pdf.PDFDict)
	if !ok || !contents.HasStream {
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

// TestWriterRoundTripSyntheticGraph builds a small object graph by hand
// (Catalog -> Pages -> Page -> Contents, with a Page/Parent back-reference to
// Pages, deliberately creating a cycle) and checks that WriteDocument's
// output, once re-parsed, reconstructs the same structure: page count,
// MediaBox, decoded content bytes, and -- the part a naive serializer would
// get wrong -- the shared Pages/Page objects are still the same object on
// both sides of the cycle rather than being duplicated.
func TestWriterRoundTripSyntheticGraph(t *testing.T) {
	contents := pdf.PDFDict{
		Entries:   map[string]pdf.PDFValue{"_ref": pdf.PDFRef{ObjNum: 10}, "Length": pdf.PDFInteger(4)},
		HasStream: true,
		RawStream: []byte("q\nQ\n"),
	}
	page := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"_ref":     pdf.PDFRef{ObjNum: 3},
		"Type":     pdf.PDFName{Value: "Page"},
		"MediaBox": pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFInteger(200), pdf.PDFInteger(300)},
		"Contents": contents,
	}}
	pages := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"_ref":  pdf.PDFRef{ObjNum: 2},
		"Type":  pdf.PDFName{Value: "Pages"},
		"Kids":  pdf.PDFArray{page},
		"Count": pdf.PDFInteger(1),
	}}
	page.Entries["Parent"] = pages // cycle: Page -> Pages -> Kids[0] -> Page
	catalog := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"_ref":  pdf.PDFRef{ObjNum: 1},
		"Type":  pdf.PDFName{Value: "Catalog"},
		"Pages": pages,
	}}
	trailer := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Root": catalog,
		"ID":   pdf.PDFArray{pdf.PDFHexString{Value: "ABCD"}, pdf.PDFHexString{Value: "ABCD"}},
	}}

	var buf bytes.Buffer
	if err := WriteDocument(&buf, trailer); err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}

	path := writeTempPDF(t, "synthetic.pdf", buf.Bytes())
	doc, err := pdf.Open(path)
	if err != nil {
		t.Fatalf("pdf.Open(written output): %v\n--- output ---\n%s", err, buf.String())
	}
	defer doc.Close()

	if n, err := doc.GetPageCount(); err != nil || n != 1 {
		t.Fatalf("GetPageCount() = %d, %v; want 1, nil", n, err)
	}

	graph, err := doc.ResolveGraph()
	if err != nil {
		t.Fatalf("ResolveGraph: %v", err)
	}
	gotPage := assertOnePageGraph(t, graph)
	assertContentStream(t, doc, gotPage, "q\nQ\n")

	wantMediaBox := pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFInteger(200), pdf.PDFInteger(300)}
	if !pdf.EqualPDFValue(gotPage.Entries["MediaBox"], wantMediaBox) {
		t.Errorf("MediaBox = %v, want %v", gotPage.Entries["MediaBox"], wantMediaBox)
	}

	// The cycle must survive: Page/Parent must point back to the very same
	// Pages object that Pages/Kids[0] is, not to a duplicate.
	gotParent, ok := gotPage.Entries["Parent"].(pdf.PDFDict)
	if !ok {
		t.Fatalf("Page/Parent did not resolve to a dict")
	}
	gotPages, ok := graph.(pdf.PDFDict).Entries["Root"].(pdf.PDFDict).Entries["Pages"].(pdf.PDFDict)
	if !ok {
		t.Fatalf("Root/Pages did not resolve to a dict")
	}
	parentRef, _ := gotParent.Entries["_ref"].(pdf.PDFRef)
	pagesRef, _ := gotPages.Entries["_ref"].(pdf.PDFRef)
	if parentRef != pagesRef {
		t.Errorf("Page/Parent _ref = %v, want it to match Root/Pages _ref = %v (shared object was duplicated)", parentRef, pagesRef)
	}
}

// TestWriterRoundTripDirtyStream checks that SetStreamFlate stores a
// Flate-encoded stream that the writer emits verbatim and that still decodes
// back to the original content.
func TestWriterRoundTripDirtyStream(t *testing.T) {
	contents := pdf.PDFDict{
		Entries: map[string]pdf.PDFValue{"_ref": pdf.PDFRef{ObjNum: 5}},
	}
	if err := SetStreamFlate(&contents, []byte("0 0 0 rg 0 0 100 100 re f")); err != nil {
		t.Fatalf("SetStreamFlate: %v", err)
	}

	page := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"_ref":     pdf.PDFRef{ObjNum: 2},
		"Type":     pdf.PDFName{Value: "Page"},
		"MediaBox": pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFInteger(100), pdf.PDFInteger(100)},
		"Contents": contents,
	}}
	catalog := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"_ref":  pdf.PDFRef{ObjNum: 1},
		"Type":  pdf.PDFName{Value: "Catalog"},
		"Pages": pdf.PDFDict{Entries: map[string]pdf.PDFValue{"_ref": pdf.PDFRef{ObjNum: 3}, "Type": pdf.PDFName{Value: "Pages"}, "Kids": pdf.PDFArray{page}, "Count": pdf.PDFInteger(1)}},
	}}
	trailer := pdf.PDFDict{Entries: map[string]pdf.PDFValue{"Root": catalog}}

	var buf bytes.Buffer
	if err := WriteDocument(&buf, trailer); err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}
	if bytes.Contains(buf.Bytes(), []byte("0 0 0 rg")) {
		t.Errorf("dirty stream content appeared unencoded in output; expected it to be Flate-compressed")
	}

	doc, err := pdf.Open(writeTempPDF(t, "dirty.pdf", buf.Bytes()))
	if err != nil {
		t.Fatalf("pdf.Open(written output): %v", err)
	}
	defer doc.Close()

	graph, err := doc.ResolveGraph()
	if err != nil {
		t.Fatalf("ResolveGraph: %v", err)
	}
	gotPage := assertOnePageGraph(t, graph)

	gotContents, ok := gotPage.Entries["Contents"].(pdf.PDFDict)
	if !ok || !gotContents.HasStream {
		t.Fatalf("Page/Contents did not resolve to a stream dict")
	}
	if !pdf.EqualPDFValue(gotContents.Entries["Filter"], pdf.PDFName{Value: "FlateDecode"}) {
		t.Errorf("Contents/Filter = %v, want /FlateDecode", gotContents.Entries["Filter"])
	}
	decoded, err := pdf.DecodeStream(gotContents)
	if err != nil {
		t.Fatalf("decodeStream: %v", err)
	}
	if string(decoded) != "0 0 0 rg 0 0 100 100 re f" {
		t.Errorf("decoded content = %q, want the original dirty bytes", decoded)
	}
}

// TestWriterRoundTripConformantCorpusFiles takes every PDF/A-1b-conformant
// "pass" fixture from the veraPDF corpus, round-trips each through WritePDF,
// and checks the rewritten file is still reported fully conformant -- the
// practical proof that the writer's renumbering and verbatim stream
// pass-through don't silently corrupt a real, richly-structured (and
// potentially cyclic, e.g. Page/Parent) document. The corpus's "pass" files
// span annotations, forms, optional content, and transparency groups, giving
// much broader structural coverage than any one hand-picked fixture.
func TestWriterRoundTripConformantCorpusFiles(t *testing.T) {
	paths := passFixtures(t)
	if len(paths) == 0 {
		t.Skip("veraPDF suite not present")
	}

	for _, path := range paths {
		rel, _ := filepath.Rel(veraDir, path)
		t.Run(rel, func(t *testing.T) {
			orig, err := pdf.Open(path)
			if err != nil {
				t.Fatalf("pdf.Open(%s): %v", path, err)
			}
			defer orig.Close()

			origRes, err := verify.Verify(orig, pdf.PDFA_1B)
			if err != nil {
				t.Fatalf("Verify(original): %v", err)
			}
			if !origRes.Valid {
				t.Fatalf("fixture is not actually conformant (issues: %v)", issueClauses(origRes.Issues))
			}
			origPages, err := orig.GetPageCount()
			if err != nil {
				t.Fatalf("GetPageCount(original): %v", err)
			}

			var buf bytes.Buffer
			if err := WritePDF(orig, &buf); err != nil {
				t.Fatalf("WritePDF: %v", err)
			}

			rewritten, err := pdf.Open(writeTempPDF(t, "rewritten.pdf", buf.Bytes()))
			if err != nil {
				t.Fatalf("pdf.Open(rewritten output): %v\n--- output ---\n%s", err, buf.String())
			}
			defer rewritten.Close()

			if n, err := rewritten.GetPageCount(); err != nil || n != origPages {
				t.Errorf("GetPageCount(rewritten) = %d, %v; want %d, nil", n, err, origPages)
			}

			res, err := verify.Verify(rewritten, pdf.PDFA_1B)
			if err != nil {
				t.Fatalf("Verify(rewritten): %v", err)
			}
			if !res.Valid {
				t.Errorf("round-tripped output is no longer PDF/A-1b conformant; issues: %v", issueClauses(res.Issues))
			}
		})
	}
}

// passFixtures returns every "pass" fixture path in the veraPDF corpus, or
// nil if the corpus is not present.
func passFixtures(t *testing.T) []string {
	t.Helper()
	if _, err := os.Stat(veraDir); err != nil {
		return nil
	}
	var found []string
	filepath.WalkDir(veraDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() && strings.Contains(strings.ToLower(d.Name()), "-pass-") {
			found = append(found, path)
		}
		return nil
	})
	return found
}
