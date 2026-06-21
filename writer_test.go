package pdfrab

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWriterRoundTripSyntheticGraph builds a small object graph by hand
// (Catalog -> Pages -> Page -> Contents, with a Page/Parent back-reference to
// Pages, deliberately creating a cycle) and checks that WriteDocument's
// output, once re-parsed, reconstructs the same structure: page count,
// MediaBox, decoded content bytes, and -- the part a naive serializer would
// get wrong -- the shared Pages/Page objects are still the same object on
// both sides of the cycle rather than being duplicated.
func TestWriterRoundTripSyntheticGraph(t *testing.T) {
	contents := PDFDict{
		Entries:   map[string]PDFValue{"_ref": PDFRef{ObjNum: 10}, "Length": PDFInteger(4)},
		HasStream: true,
		RawStream: []byte("q\nQ\n"),
	}
	page := PDFDict{Entries: map[string]PDFValue{
		"_ref":     PDFRef{ObjNum: 3},
		"Type":     PDFName{Value: "Page"},
		"MediaBox": PDFArray{PDFInteger(0), PDFInteger(0), PDFInteger(200), PDFInteger(300)},
		"Contents": contents,
	}}
	pages := PDFDict{Entries: map[string]PDFValue{
		"_ref":  PDFRef{ObjNum: 2},
		"Type":  PDFName{Value: "Pages"},
		"Kids":  PDFArray{page},
		"Count": PDFInteger(1),
	}}
	page.Entries["Parent"] = pages // cycle: Page -> Pages -> Kids[0] -> Page
	catalog := PDFDict{Entries: map[string]PDFValue{
		"_ref":  PDFRef{ObjNum: 1},
		"Type":  PDFName{Value: "Catalog"},
		"Pages": pages,
	}}
	trailer := PDFDict{Entries: map[string]PDFValue{
		"Root": catalog,
		"ID":   PDFArray{PDFHexString{Value: "ABCD"}, PDFHexString{Value: "ABCD"}},
	}}

	var buf bytes.Buffer
	if err := WriteDocument(&buf, trailer); err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}

	path := writeTempPDF(t, "synthetic.pdf", buf.Bytes())
	doc, err := Open(path)
	if err != nil {
		t.Fatalf("Open(written output): %v\n--- output ---\n%s", err, buf.String())
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

	wantMediaBox := PDFArray{PDFInteger(0), PDFInteger(0), PDFInteger(200), PDFInteger(300)}
	if !EqualPDFValue(gotPage.Entries["MediaBox"], wantMediaBox) {
		t.Errorf("MediaBox = %v, want %v", gotPage.Entries["MediaBox"], wantMediaBox)
	}

	// The cycle must survive: Page/Parent must point back to the very same
	// Pages object that Pages/Kids[0] is, not to a duplicate.
	gotParent, ok := gotPage.Entries["Parent"].(PDFDict)
	if !ok {
		t.Fatalf("Page/Parent did not resolve to a dict")
	}
	gotPages, ok := graph.(PDFDict).Entries["Root"].(PDFDict).Entries["Pages"].(PDFDict)
	if !ok {
		t.Fatalf("Root/Pages did not resolve to a dict")
	}
	parentRef, _ := gotParent.Entries["_ref"].(PDFRef)
	pagesRef, _ := gotPages.Entries["_ref"].(PDFRef)
	if parentRef != pagesRef {
		t.Errorf("Page/Parent _ref = %v, want it to match Root/Pages _ref = %v (shared object was duplicated)", parentRef, pagesRef)
	}
}

// TestWriterRoundTripDirtyStream checks that MarkStreamDirty causes
// WriteDocument to Flate-encode the stream's RawStream bytes fresh (setting
// /Filter /FlateDecode) rather than writing them through verbatim, and that
// the result still decodes back to the original content.
func TestWriterRoundTripDirtyStream(t *testing.T) {
	contents := PDFDict{
		Entries:   map[string]PDFValue{"_ref": PDFRef{ObjNum: 5}},
		HasStream: true,
		RawStream: []byte("0 0 0 rg 0 0 100 100 re f"),
	}
	MarkStreamDirty(&contents)

	page := PDFDict{Entries: map[string]PDFValue{
		"_ref":     PDFRef{ObjNum: 2},
		"Type":     PDFName{Value: "Page"},
		"MediaBox": PDFArray{PDFInteger(0), PDFInteger(0), PDFInteger(100), PDFInteger(100)},
		"Contents": contents,
	}}
	catalog := PDFDict{Entries: map[string]PDFValue{
		"_ref":  PDFRef{ObjNum: 1},
		"Type":  PDFName{Value: "Catalog"},
		"Pages": PDFDict{Entries: map[string]PDFValue{"_ref": PDFRef{ObjNum: 3}, "Type": PDFName{Value: "Pages"}, "Kids": PDFArray{page}, "Count": PDFInteger(1)}},
	}}
	trailer := PDFDict{Entries: map[string]PDFValue{"Root": catalog}}

	var buf bytes.Buffer
	if err := WriteDocument(&buf, trailer); err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}
	if bytes.Contains(buf.Bytes(), []byte("0 0 0 rg")) {
		t.Errorf("dirty stream content appeared unencoded in output; expected it to be Flate-compressed")
	}

	doc, err := Open(writeTempPDF(t, "dirty.pdf", buf.Bytes()))
	if err != nil {
		t.Fatalf("Open(written output): %v", err)
	}
	defer doc.Close()

	graph, err := doc.ResolveGraph()
	if err != nil {
		t.Fatalf("ResolveGraph: %v", err)
	}
	gotPage := assertOnePageGraph(t, graph)

	gotContents, ok := gotPage.Entries["Contents"].(PDFDict)
	if !ok || !gotContents.HasStream {
		t.Fatalf("Page/Contents did not resolve to a stream dict")
	}
	if !EqualPDFValue(gotContents.Entries["Filter"], PDFName{Value: "FlateDecode"}) {
		t.Errorf("Contents/Filter = %v, want /FlateDecode", gotContents.Entries["Filter"])
	}
	decoded, err := decodeStream(gotContents)
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
			orig, err := Open(path)
			if err != nil {
				t.Fatalf("Open(%s): %v", path, err)
			}
			defer orig.Close()

			origRes, err := orig.Verify(A_1B)
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
			if err := orig.WritePDF(&buf); err != nil {
				t.Fatalf("WritePDF: %v", err)
			}

			rewritten, err := Open(writeTempPDF(t, "rewritten.pdf", buf.Bytes()))
			if err != nil {
				t.Fatalf("Open(rewritten output): %v\n--- output ---\n%s", err, buf.String())
			}
			defer rewritten.Close()

			if n, err := rewritten.GetPageCount(); err != nil || n != origPages {
				t.Errorf("GetPageCount(rewritten) = %d, %v; want %d, nil", n, err, origPages)
			}

			res, err := rewritten.Verify(A_1B)
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
