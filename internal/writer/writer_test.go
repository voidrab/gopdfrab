package writer

import (
	"bytes"
	"errors"
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

	// The deflate encoder may store tiny inputs verbatim inside the zlib
	// container, so only the declared filter and the decode round-trip below
	// are asserted, not the compressed byte shape.
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

// errAfter is an io.Writer that succeeds for its first n writes, then fails,
// used to drive each successive "if err != nil { return err }" branch.
type errAfter struct{ n int }

func (e *errAfter) Write(p []byte) (int, error) {
	if e.n <= 0 {
		return 0, errors.New("boom")
	}
	e.n--
	return len(p), nil
}

// TestIdentityOfPointerBranch covers identityOf's no-_ref (pointer-identity) path.
func TestIdentityOfPointerBranch(t *testing.T) {
	d := pdf.PDFDict{Entries: map[string]pdf.PDFValue{}, HasStream: true}
	if id := identityOf(d); id.hasRef {
		t.Error("identityOf(no _ref) reported hasRef, want pointer identity")
	}
}

// TestWriteErrorPropagation covers the write-failure error branches in
// writeDictEntries, writeValue, writeOperand, and writeIndirectObject by
// failing the underlying writer at every successive offset.
func TestWriteErrorPropagation(t *testing.T) {
	target := pdf.PDFDict{Entries: map[string]pdf.PDFValue{"_ref": pdf.PDFRef{ObjNum: 5}}}
	wr := &pdfWriter{
		numbers: map[objectIdentity]int{identityOf(target): 1},
		visited: map[uintptr]bool{},
	}

	entries := map[string]pdf.PDFValue{
		"Name": pdf.PDFName{Value: "V"},
		"Arr":  pdf.PDFArray{pdf.PDFInteger(1), pdf.PDFInteger(2)},
		"Sub":  pdf.PDFDict{Entries: map[string]pdf.PDFValue{"X": pdf.PDFInteger(1)}},
		"Ref":  target,
	}
	for n := 0; n < 40; n++ {
		cw := &countingWriter{w: &errAfter{n: n}}
		_ = wr.writeDictEntries(cw, entries)
	}

	op := pdf.PDFArray{pdf.PDFInteger(1), pdf.PDFDict{Entries: map[string]pdf.PDFValue{"K": pdf.PDFInteger(1)}}}
	for n := 0; n < 30; n++ {
		_ = writeOperand(&errAfter{n: n}, op)
	}

	streamObj := pdf.PDFDict{
		Entries:   map[string]pdf.PDFValue{"_ref": pdf.PDFRef{ObjNum: 1}},
		HasStream: true, RawStream: []byte("xyz"),
	}
	plainObj := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"_ref": pdf.PDFRef{ObjNum: 2}, "Type": pdf.PDFName{Value: "X"},
	}}
	for n := 0; n < 25; n++ {
		_ = wr.writeIndirectObject(&countingWriter{w: &errAfter{n: n}}, 1, streamObj)
		_ = wr.writeIndirectObject(&countingWriter{w: &errAfter{n: n}}, 2, plainObj)
	}
}

// TestWriteOperandShapes covers every operand kind writeOperand serializes,
// including the array, inline-dict, and null branches the round-trip content
// tests (which use only scalars) never reach.
func TestWriteOperandShapes(t *testing.T) {
	cases := []struct {
		name string
		v    pdf.PDFValue
		want string
	}{
		{"name", pdf.PDFName{Value: "Foo"}, "/Foo"},
		{"int", pdf.PDFInteger(42), "42"},
		{"real", pdf.PDFReal(1.5), "1.5"},
		{"string", pdf.PDFString{Value: "hi"}, "(hi)"},
		{"hex", pdf.PDFHexString{Value: "ABCD"}, "<ABCD>"},
		{"bool-true", pdf.PDFBoolean(true), "true"},
		{"bool-false", pdf.PDFBoolean(false), "false"},
		{"nil", nil, "null"},
		{"array", pdf.PDFArray{pdf.PDFInteger(1), pdf.PDFName{Value: "X"}}, "[1 /X]"},
		{"nested-array", pdf.PDFArray{pdf.PDFArray{pdf.PDFInteger(1)}}, "[[1]]"},
		{"dict", pdf.PDFDict{Entries: map[string]pdf.PDFValue{
			"A": pdf.PDFInteger(1), "_ref": pdf.PDFRef{ObjNum: 9}, "_dirty": pdf.PDFBoolean(true),
		}}, "<< /A 1 >>"},
	}
	for _, c := range cases {
		var buf bytes.Buffer
		if err := writeOperand(&buf, c.v); err != nil {
			t.Errorf("%s: writeOperand: %v", c.name, err)
			continue
		}
		if got := buf.String(); got != c.want {
			t.Errorf("%s: writeOperand = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestWriteOperandUnsupported covers writeOperand's default (error) branch.
func TestWriteOperandUnsupported(t *testing.T) {
	var buf bytes.Buffer
	if err := writeOperand(&buf, pdf.PDFRef{ObjNum: 1}); err == nil {
		t.Error("expected error for an unsupported operand type")
	}
}

// TestWriteValueErrorBranches covers writeValue's unresolved-reference and
// undiscovered-indirect-dict error paths.
func TestWriteValueErrorBranches(t *testing.T) {
	wr := &pdfWriter{numbers: map[objectIdentity]int{}, visited: map[uintptr]bool{}}
	cw := &countingWriter{w: &bytes.Buffer{}}

	if err := wr.writeValue(cw, pdf.PDFRef{ObjNum: 3}); err == nil {
		t.Error("expected error writing an unresolved PDFRef")
	}

	undiscovered := pdf.PDFDict{Entries: map[string]pdf.PDFValue{"_ref": pdf.PDFRef{ObjNum: 7}}}
	if err := wr.writeValue(cw, undiscovered); err == nil {
		t.Error("expected error writing an undiscovered indirect dict")
	}

	if err := wr.writeValue(cw, pdf.InlineImageRaw{}); err == nil {
		t.Error("expected error writing an unsupported value type")
	}
}

// TestBuildInlineImageBytes covers BuildInlineImageBytes and its param error path.
func TestBuildInlineImageBytes(t *testing.T) {
	params := []pdf.PDFValue{
		pdf.PDFName{Value: "W"}, pdf.PDFInteger(2),
		pdf.PDFName{Value: "H"}, pdf.PDFInteger(1),
	}
	out, err := BuildInlineImageBytes(params, []byte{0xff, 0x00})
	if err != nil {
		t.Fatalf("BuildInlineImageBytes: %v", err)
	}
	want := "BI /W 2 /H 1 ID \xff\x00 EI"
	if string(out) != want {
		t.Errorf("BuildInlineImageBytes = %q, want %q", out, want)
	}

	if _, err := BuildInlineImageBytes([]pdf.PDFValue{pdf.PDFRef{ObjNum: 1}}, nil); err == nil {
		t.Error("expected error for an unserializable inline-image param")
	}
}

// TestInlineImageBytes covers the empty and wrong-type branches of inlineImageBytes.
func TestInlineImageBytes(t *testing.T) {
	if _, ok := inlineImageBytes(nil); ok {
		t.Error("inlineImageBytes(nil) = ok, want false")
	}
	if _, ok := inlineImageBytes([]pdf.PDFValue{pdf.PDFInteger(1)}); ok {
		t.Error("inlineImageBytes(non-raw) = ok, want false")
	}
	if b, ok := inlineImageBytes([]pdf.PDFValue{pdf.InlineImageRaw{Bytes: []byte("x")}}); !ok || string(b) != "x" {
		t.Errorf("inlineImageBytes(raw) = %q,%v; want \"x\",true", b, ok)
	}
}

// TestWriteContentStreamErrors covers WriteContentStream's two error branches:
// an unserializable operand and an INLINEIMAGE op missing its raw bytes.
func TestWriteContentStreamErrors(t *testing.T) {
	if _, err := WriteContentStream([]ContentOp{{Op: "Tj", Operands: []pdf.PDFValue{pdf.PDFRef{ObjNum: 1}}}}); err == nil {
		t.Error("expected error for an unserializable operand")
	}
	if _, err := WriteContentStream([]ContentOp{{Op: "INLINEIMAGE"}}); err == nil {
		t.Error("expected error for an INLINEIMAGE op with no raw bytes")
	}
}

// TestNumberObjects covers NumberObjects: every reachable indirect object gets a
// 1-based number and a matching _ref.
func TestNumberObjects(t *testing.T) {
	page := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"_ref": pdf.PDFRef{ObjNum: 2}, "Type": pdf.PDFName{Value: "Page"},
	}}
	catalog := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"_ref": pdf.PDFRef{ObjNum: 1}, "Type": pdf.PDFName{Value: "Catalog"},
		"Pages": pdf.PDFDict{Entries: map[string]pdf.PDFValue{
			"_ref": pdf.PDFRef{ObjNum: 3}, "Type": pdf.PDFName{Value: "Pages"},
			"Kids": pdf.PDFArray{page},
		}},
	}}
	trailer := pdf.PDFDict{Entries: map[string]pdf.PDFValue{"Root": catalog}}

	objs := NumberObjects(trailer)
	if len(objs) != 3 {
		t.Fatalf("NumberObjects mapped %d objects, want 3", len(objs))
	}
	for n := 1; n <= 3; n++ {
		d, ok := objs[n].(pdf.PDFDict)
		if !ok {
			t.Fatalf("objs[%d] is not a dict", n)
		}
		if ref, _ := d.Entries["_ref"].(pdf.PDFRef); ref.ObjNum != n {
			t.Errorf("objs[%d] _ref = %v, want ObjNum %d", n, d.Entries["_ref"], n)
		}
	}
}

// TestSetStreamFlateVariants covers SetStreamFlateFast and SetStreamFlateRows,
// asserting each stores a FlateDecode stream that decodes back to its input.
func TestSetStreamFlateVariants(t *testing.T) {
	want := []byte("0 0 0 rg 0 0 100 100 re f")

	d := pdf.PDFDict{Entries: map[string]pdf.PDFValue{}}
	if err := SetStreamFlateFast(&d, want); err != nil {
		t.Fatalf("SetStreamFlateFast: %v", err)
	}
	assertFlateDecodes(t, d, want)

	rows := [][]byte{[]byte("row0\n"), []byte("row1\n"), []byte("row2\n")}
	var joined []byte
	for _, r := range rows {
		joined = append(joined, r...)
	}
	d2 := pdf.PDFDict{Entries: map[string]pdf.PDFValue{}}
	if err := SetStreamFlateRows(&d2, len(rows), func(i int) []byte { return rows[i] }); err != nil {
		t.Fatalf("SetStreamFlateRows: %v", err)
	}
	assertFlateDecodes(t, d2, joined)
}

// TestDeflateZlib covers DeflateZlib (success) and deflateZlibLevel's
// invalid-level error branch.
func TestDeflateZlib(t *testing.T) {
	data := []byte("hello deflate")
	compressed, err := DeflateZlib(data)
	if err != nil {
		t.Fatalf("DeflateZlib: %v", err)
	}
	d := pdf.PDFDict{
		Entries:   map[string]pdf.PDFValue{"Filter": pdf.PDFName{Value: "FlateDecode"}},
		HasStream: true, RawStream: compressed,
	}
	assertFlateDecodes(t, d, data)

	if _, err := deflateZlibLevel(data, 99); err == nil {
		t.Error("expected error for an invalid compression level")
	}
}

// assertFlateDecodes checks that d is a FlateDecode stream decoding to want.
func assertFlateDecodes(t *testing.T, d pdf.PDFDict, want []byte) {
	t.Helper()
	if !pdf.EqualPDFValue(d.Entries["Filter"], pdf.PDFName{Value: "FlateDecode"}) {
		t.Errorf("Filter = %v, want /FlateDecode", d.Entries["Filter"])
	}
	got, err := pdf.DecodeStream(d)
	if err != nil {
		t.Fatalf("DecodeStream: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("decoded = %q, want %q", got, want)
	}
}
