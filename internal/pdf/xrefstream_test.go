package pdf_test

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/verify"
)

// clauseMatches reports whether a reported clause satisfies the expected clause.
func clauseMatches(got, expected string) bool {
	if got == expected {
		return true
	}
	return strings.HasPrefix(got+".", expected+".") ||
		strings.HasPrefix(expected+".", got+".")
}

func issueClauses(issues []pdf.PDFError) []string {
	out := make([]string, 0, len(issues))
	for _, iss := range issues {
		out = append(out, iss.Check().Clause())
	}
	return out
}

// pdfBuilder assembles a minimal hand-crafted PDF byte-for-byte, tracking the
// byte offset of each indirect object as it is written, so a cross-reference
// stream's /W-encoded offsets can be computed exactly like a real writer
// would. Used to construct synthetic fixtures exercising the xref-stream and
// object-stream (ObjStm) read paths added in xrefstream.go/objstm.go, which
// the real-world Isartor/veraPDF corpora (PDF/A-1-only, hence classic xref
// only) never exercise.
type pdfBuilder struct {
	buf     bytes.Buffer
	offsets map[int]int64
}

func newPDFBuilder(header string) *pdfBuilder {
	b := &pdfBuilder{offsets: map[int]int64{}}
	b.buf.WriteString(header)
	return b
}

// obj writes a non-stream indirect object with framing that satisfies 6.1.8
// (single LF after "obj" and around "endobj").
func (b *pdfBuilder) obj(num int, body string) {
	b.offsets[num] = int64(b.buf.Len())
	fmt.Fprintf(&b.buf, "%d 0 obj\n%s\nendobj\n", num, body)
}

// streamObj writes an indirect stream object. dictHead is the dictionary
// without its closing ">>" (e.g. "<< /Type /ObjStm /N 3 /First 18"); /Length
// and the closing ">>" are appended automatically.
func (b *pdfBuilder) streamObj(num int, dictHead string, raw []byte) {
	b.offsets[num] = int64(b.buf.Len())
	fmt.Fprintf(&b.buf, "%d 0 obj\n%s /Length %d >>\nstream\n", num, dictHead, len(raw))
	b.buf.Write(raw)
	b.buf.WriteString("\nendstream\nendobj\n")
}

func (b *pdfBuilder) offsetOf(num int) int64 { return b.offsets[num] }

// finish appends "startxref\n<offset>\n%%EOF" and returns the full file bytes.
func (b *pdfBuilder) finish(startxrefOffset int64) []byte {
	fmt.Fprintf(&b.buf, "startxref\n%d\n%%%%EOF", startxrefOffset)
	return b.buf.Bytes()
}

func deflate(t *testing.T, data []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	w := zlib.NewWriter(&out)
	if _, err := w.Write(data); err != nil {
		t.Fatalf("deflate: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("deflate: %v", err)
	}
	return out.Bytes()
}

// beField encodes v as a big-endian field of the given byte width.
func beField(v int, width int) []byte {
	out := make([]byte, width)
	for i := width - 1; i >= 0; i-- {
		out[i] = byte(v)
		v >>= 8
	}
	return out
}

func writeTempPDF(t *testing.T, name string, data []byte) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

// buildXRefStreamOnlyPDF builds a one-page PDF whose objects 1-4 are all
// classically stored, but whose cross-reference table is a PDF 1.5+
// cross-reference stream (object 5, self-referential) instead of a classic
// "xref" table, with no literal "trailer" keyword anywhere in the file --
// the shape that previously only worked via brute-force recovery.
func buildXRefStreamOnlyPDF(t *testing.T) []byte {
	t.Helper()
	b := newPDFBuilder("%PDF-1.5\n%\xc2\xb5\xc2\xb6\n")

	b.obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Resources << >> /Contents 4 0 R >>")
	b.streamObj(4, "<<", []byte("q\nQ\n"))

	// Object 5: the cross-reference stream itself, covering objects 0-5.
	const w0, w1, w2 = 1, 4, 1
	var raw bytes.Buffer
	raw.Write(beField(0, w0+w1+w2)) // object 0: always free; value unused by the reader
	for objNum := 1; objNum <= 4; objNum++ {
		raw.Write([]byte{1})
		raw.Write(beField(int(b.offsetOf(objNum)), w1))
		raw.Write([]byte{0})
	}
	xrefStreamOffset := int64(b.buf.Len())
	raw.Write([]byte{1})
	raw.Write(beField(int(xrefStreamOffset), w1))
	raw.Write([]byte{0})

	dictHead := fmt.Sprintf("<< /Type /XRef /Size 6 /W [%d %d %d] /Root 1 0 R /Filter /FlateDecode", w0, w1, w2)
	b.streamObj(5, dictHead, deflate(t, raw.Bytes()))

	return b.finish(xrefStreamOffset)
}

// buildXRefStreamWithObjStmPDF builds a one-page PDF where the Catalog, Pages
// and Page dictionaries (objects 1-3) are packed inside a compressed object
// stream (object 6), while the content stream (object 4) and the object
// stream container (object 6) and the cross-reference stream (object 7)
// remain classically stored, since neither streams nor object streams may
// themselves be compressed objects (ISO 32000-1 7.5.7). The cross-reference
// stream's /Index uses two disjoint ranges to also exercise that path.
func buildXRefStreamWithObjStmPDF(t *testing.T) []byte {
	t.Helper()
	b := newPDFBuilder("%PDF-1.5\n%\xc2\xb5\xc2\xb6\n")

	obj1Body := "<< /Type /Catalog /Pages 2 0 R >>"
	obj2Body := "<< /Type /Pages /Kids [3 0 R] /Count 1 >>"
	obj3Body := "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Resources << >> /Contents 4 0 R >>"

	off1 := 0
	off2 := off1 + len(obj1Body) + 1
	off3 := off2 + len(obj2Body) + 1
	header := fmt.Sprintf("1 %d 2 %d 3 %d ", off1, off2, off3)
	objStmData := header + obj1Body + " " + obj2Body + " " + obj3Body

	b.streamObj(6, fmt.Sprintf("<< /Type /ObjStm /N 3 /First %d /Filter /FlateDecode", len(header)),
		deflate(t, []byte(objStmData)))

	b.streamObj(4, "<<", []byte("q\nQ\n"))

	// Object 7: the cross-reference stream, covering 0 (free), 1-4
	// (1-3 compressed in object 6, 4 classic), 6-7 (classic).
	const w0, w1, w2 = 1, 4, 1
	var raw bytes.Buffer
	raw.Write(beField(0, w0+w1+w2)) // object 0: always free

	raw.Write([]byte{2}) // object 1: compressed, index 0 in object stream 6
	raw.Write(beField(6, w1))
	raw.Write([]byte{0})

	raw.Write([]byte{2}) // object 2: compressed, index 1
	raw.Write(beField(6, w1))
	raw.Write([]byte{1})

	raw.Write([]byte{2}) // object 3: compressed, index 2
	raw.Write(beField(6, w1))
	raw.Write([]byte{2})

	raw.Write([]byte{1}) // object 4: classic
	raw.Write(beField(int(b.offsetOf(4)), w1))
	raw.Write([]byte{0})

	raw.Write([]byte{1}) // object 6: classic (the ObjStm container)
	raw.Write(beField(int(b.offsetOf(6)), w1))
	raw.Write([]byte{0})

	xrefStreamOffset := int64(b.buf.Len())
	raw.Write([]byte{1}) // object 7: classic (itself)
	raw.Write(beField(int(xrefStreamOffset), w1))
	raw.Write([]byte{0})

	dictHead := fmt.Sprintf("<< /Type /XRef /Size 8 /Index [0 1 1 4 6 2] /W [%d %d %d] /Root 1 0 R /Filter /FlateDecode",
		w0, w1, w2)
	b.streamObj(7, dictHead, deflate(t, raw.Bytes()))

	return b.finish(xrefStreamOffset)
}

// TestXRefStreamOnly verifies a PDF/1.5+ file using only a cross-reference
// stream (no classic "xref" table, no literal "trailer" keyword) is fully
// readable: page count, page resolution and content all resolve correctly.
// As an additional end-to-end smoke test, Verify(A_1B) must run to
// completion and correctly flag the file non-conformant (PDF/A-1b is based
// on PDF 1.4 and does not permit cross-reference streams), without losing
// the ability to resolve the rest of the graph.
func TestXRefStreamOnly(t *testing.T) {
	path := writeTempPDF(t, "xrefstream-only.pdf", buildXRefStreamOnlyPDF(t))

	doc, err := pdf.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer doc.Close()

	if n, err := doc.GetPageCount(); err != nil || n != 1 {
		t.Fatalf("GetPageCount() = %d, %v; want 1, nil", n, err)
	}

	graph, err := doc.ResolveGraph()
	if err != nil {
		t.Fatalf("ResolveGraph: %v", err)
	}
	page := assertOnePageGraph(t, graph)
	assertContentStream(t, doc, page, "q\nQ\n")

	res, err := verify.Verify(doc, pdf.PDFA_1B)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if res.Valid {
		t.Fatalf("expected a cross-reference-stream-only file to be flagged non-conformant (6.1.4)")
	}
	foundXRefClause := false
	for _, iss := range res.Issues {
		if clauseMatches(iss.Check().Clause(), "6.1.4") {
			foundXRefClause = true
		}
	}
	if !foundXRefClause {
		t.Errorf("expected a 6.1.4 issue among %v", issueClauses(res.Issues))
	}
}

// TestXRefStreamWithObjStm verifies a PDF using both a cross-reference stream
// and a compressed object stream (ObjStm) is fully readable: the Catalog,
// Pages and Page dictionaries packed inside the ObjStm must resolve exactly
// as if they had been classically stored.
func TestXRefStreamWithObjStm(t *testing.T) {
	path := writeTempPDF(t, "xrefstream-objstm.pdf", buildXRefStreamWithObjStmPDF(t))

	doc, err := pdf.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer doc.Close()

	if n, err := doc.GetPageCount(); err != nil || n != 1 {
		t.Fatalf("GetPageCount() = %d, %v; want 1, nil", n, err)
	}

	graph, err := doc.ResolveGraph()
	if err != nil {
		t.Fatalf("ResolveGraph: %v", err)
	}
	page := assertOnePageGraph(t, graph)
	assertContentStream(t, doc, page, "q\nQ\n")

	// The graph-resolved Page dict (from inside the ObjStm) must carry the
	// synthetic _ref stamp like any classically-stored object, since
	// buildPageIndex and every checker key off it.
	if ref, ok := page.Entries["_ref"].(pdf.PDFRef); !ok || ref.ObjNum != 3 {
		t.Errorf("Page dict _ref = %v, ok=%v; want {ObjNum:3 ...}", ref, ok)
	}

	res, err := verify.Verify(doc, pdf.PDFA_1B)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if res.Valid {
		t.Fatalf("expected a cross-reference-stream file to be flagged non-conformant (6.1.4)")
	}
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
		t.Fatalf("pdf.DecodeStream(Contents): %v", err)
	}
	if string(data) != want {
		t.Errorf("content stream = %q, want %q", data, want)
	}
}
