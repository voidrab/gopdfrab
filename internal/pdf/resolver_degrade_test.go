package pdf

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdfgen"
)

// buildFourObjectDoc builds a minimal classic document whose object 4 is a
// string, so recovery tests can assert the exact resolved value.
func buildFourObjectDoc() []byte {
	b := pdfgen.NewBuilder("%PDF-1.4\n")
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.Obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Resources << >> >>")
	b.Obj(4, "(hello)")
	return b.FinishClassic("<< /Size 5 /Root 1 0 R >>")
}

func graphResolutionDiags(d *Reader) []PDFError {
	var out []PDFError
	for _, e := range d.StructErrors() {
		if e.Check() == Checks.Structure.GraphResolutionFailure {
			out = append(out, e)
		}
	}
	return out
}

func framingDiagsFor(d *Reader, objNum int) []PDFError {
	var out []PDFError
	for _, e := range d.StructErrors() {
		if e.Check() == Checks.Structure.ObjectFraming && e.Page() == objNum {
			out = append(out, e)
		}
	}
	return out
}

// TestResolveRecoversWrongOffsetHeader: object 4's xref entry points at object
// 3's well-formed header. Resolution must detect the header mismatch, recover
// object 4 at its real header, update the xref table, and record exactly one
// recovery diagnostic even across repeated resolves.
func TestResolveRecoversWrongOffsetHeader(t *testing.T) {
	data := buildFourObjectDoc()
	off3 := int64(bytes.Index(data, []byte("3 0 obj")))
	if off3 < 0 {
		t.Fatal("object 3 header not found")
	}
	broken := pdfgen.BreakXrefOffset(data, 4, off3)
	if bytes.Equal(broken, data) {
		t.Fatal("BreakXrefOffset changed nothing")
	}

	d, err := OpenBytes(broken)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	defer d.Close()

	for i := range 2 {
		v, err := d.ResolveReference(PDFRef{ObjNum: 4})
		if err != nil {
			t.Fatalf("resolve %d: %v", i, err)
		}
		if v != (PDFString{Value: "hello"}) {
			t.Fatalf("resolve %d = %v, want object 4's own value", i, v)
		}
	}

	realOff := int64(bytes.Index(data, []byte("4 0 obj")))
	if got := d.XRefTable()[4]; got != realOff {
		t.Errorf("xrefTable[4] = %d, want recovered offset %d", got, realOff)
	}
	diags := graphResolutionDiags(d)
	if len(diags) != 1 || !strings.Contains(diags[0].Messages()[0], "recovered") {
		t.Errorf("GraphResolutionFailure diagnostics = %v, want exactly one recovery record", diags)
	}
	if d.HasDegradedObjects() {
		t.Error("HasDegradedObjects = true, want false for a recovered object")
	}
	if len(framingDiagsFor(d, 4)) != 0 {
		t.Errorf("object 4 has framing diagnostics from the failed attempt: %v", framingDiagsFor(d, 4))
	}
}

// TestResolveRecoversGarbageOffset: object 4's xref entry points into the
// middle of another object where parsing fails outright. The failed attempt
// must leave no framing noise and the object must be recovered by scan.
func TestResolveRecoversGarbageOffset(t *testing.T) {
	data := buildFourObjectDoc()
	junk := int64(bytes.Index(data, []byte(">>\nendobj")))
	if junk < 0 {
		t.Fatal("no dict close found")
	}
	broken := pdfgen.BreakXrefOffset(data, 4, junk)

	d, err := OpenBytes(broken)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	defer d.Close()

	v, err := d.ResolveReference(PDFRef{ObjNum: 4})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if v != (PDFString{Value: "hello"}) {
		t.Fatalf("resolve = %v, want object 4's own value", v)
	}
	if diags := graphResolutionDiags(d); len(diags) != 1 {
		t.Errorf("GraphResolutionFailure diagnostics = %v, want exactly one", diags)
	}
	if len(framingDiagsFor(d, 4)) != 0 {
		t.Errorf("object 4 has framing diagnostics from the failed attempt: %v", framingDiagsFor(d, 4))
	}
}

// TestResolveDegradesUnrecoverableObject: object 4's body is unparseable at
// its only header, so recovery finds no alternative and the object resolves to
// null with a degradation diagnostic, cached across resolves. The rest of the
// graph still resolves.
func TestResolveDegradesUnrecoverableObject(t *testing.T) {
	b := pdfgen.NewBuilder("%PDF-1.4\n")
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.Obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] >>")
	b.Obj(4, "<< /Broken ]")
	data := b.FinishClassic("<< /Size 5 /Root 1 0 R >>")

	d, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	defer d.Close()

	for i := range 2 {
		v, err := d.ResolveReference(PDFRef{ObjNum: 4})
		if err != nil {
			t.Fatalf("resolve %d: %v", i, err)
		}
		if v != nil {
			t.Fatalf("resolve %d = %v, want null", i, v)
		}
	}

	if !d.HasDegradedObjects() {
		t.Error("HasDegradedObjects = false, want true")
	}
	degraded := d.DegradedObjects()
	if len(degraded) != 1 || !strings.Contains(degraded[0].Messages()[0], "treated as null") {
		t.Errorf("DegradedObjects() = %v, want exactly one null-degradation record", degraded)
	}
	if ref, ok := degraded[0].ObjectRef(); !ok || ref.ObjNum != 4 {
		t.Errorf("degraded diagnostic ref = %v, want object 4", degraded[0])
	}
	if diags := graphResolutionDiags(d); len(diags) != 1 {
		t.Errorf("StructErrors GraphResolutionFailure count = %d, want 1", len(diags))
	}
	if _, err := d.ResolveGraph(); err != nil {
		t.Errorf("ResolveGraph after degradation: %v", err)
	}
}

// TestResolveDegradesBrokenCompressedObject: a type-2 xref entry whose
// container is not an object stream degrades the member to null instead of
// failing resolution.
func TestResolveDegradesBrokenCompressedObject(t *testing.T) {
	d, err := OpenBytes(buildFourObjectDoc())
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	defer d.Close()

	d.compressedXref = map[int]compressedXrefEntry{9: {streamObjNum: 4, index: 0}}
	v, err := d.ResolveReference(PDFRef{ObjNum: 9})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if v != nil {
		t.Fatalf("resolve = %v, want null", v)
	}
	if len(d.DegradedObjects()) != 1 {
		t.Errorf("DegradedObjects() = %v, want one record for object 9", d.DegradedObjects())
	}
}

// TestDiscardKeepsNestedObjectDiagnostics: object 4's parse fails after it
// nested-resolved object 5 (its indirect /Length, whose value overflows the
// file). The rollback must drop only object 4's own framing records; object
// 5's survive alongside object 4's degradation.
func TestDiscardKeepsNestedObjectDiagnostics(t *testing.T) {
	b := pdfgen.NewBuilder("%PDF-1.4\n")
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.Obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Contents 4 0 R >>")
	b.Obj(4, "<< /Length 5 0 R >>\nstream\nhi\nendstream")
	b.Obj(5, "99999")
	data := b.FinishClassic("<< /Size 6 /Root 1 0 R >>")
	data = bytes.Replace(data, []byte("5 0 obj\n99999"), []byte("5 0 obj 99999"), 1)

	d, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	defer d.Close()

	v, err := d.ResolveReference(PDFRef{ObjNum: 4})
	if err != nil || v != nil {
		t.Fatalf("resolve object 4 = %v, %v, want null without error", v, err)
	}
	if len(framingDiagsFor(d, 5)) != 1 {
		t.Errorf("object 5 framing diagnostics = %v, want the nested record to survive", framingDiagsFor(d, 5))
	}
	if len(framingDiagsFor(d, 4)) != 0 {
		t.Errorf("object 4 framing diagnostics = %v, want none after rollback", framingDiagsFor(d, 4))
	}
	if !d.HasDegradedObjects() {
		t.Error("HasDegradedObjects = false, want true for object 4")
	}
}

// TestResolveDegradesUndecryptableStream: an encrypted stream whose ciphertext
// is not block-aligned fails decryption; the object degrades to null instead
// of failing the graph.
func TestResolveDegradesUndecryptableStream(t *testing.T) {
	data, err := os.ReadFile("testdata/crypt/enc_aesv2.pdf")
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	broken := bytes.Replace(data, []byte("/Length 80 /Filter"), []byte("/Length 79 /Filter"), 1)
	if bytes.Equal(broken, data) {
		t.Fatal("fixture no longer declares /Length 80; test input needs updating")
	}

	d, err := OpenBytes(broken)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	defer d.Close()

	v, err := d.ResolveReference(PDFRef{ObjNum: 5})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if v != nil {
		t.Fatalf("resolve = %v, want null for undecryptable stream", v)
	}
	if !d.HasDegradedObjects() {
		t.Error("HasDegradedObjects = false, want true")
	}
	if _, err := d.ResolveGraph(); err != nil {
		t.Errorf("ResolveGraph: %v", err)
	}
}

// TestRecoverTrailerFromCatalog: a document whose whole tail (trailer keyword
// and startxref) is gone is reopened by scanning for objects and synthesizing
// a trailer from the /Type /Catalog object, with the recovery reported as a
// 6.1.4 diagnostic.
func TestRecoverTrailerFromCatalog(t *testing.T) {
	base := buildFourObjectDoc()
	trimmed := base[:bytes.Index(base, []byte("trailer"))]

	d, err := OpenBytes(trimmed)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	defer d.Close()

	root, ok := d.EffectiveTrailer().Entries["Root"].(PDFRef)
	if !ok || root.ObjNum != 1 {
		t.Fatalf("recovered Root = %v, want ref to object 1", d.EffectiveTrailer().Entries["Root"])
	}
	v, err := d.ResolveReference(PDFRef{ObjNum: 4})
	if err != nil || v != (PDFString{Value: "hello"}) {
		t.Fatalf("resolve object 4 = %v, %v; want (hello)", v, err)
	}
	var got6014 bool
	for _, e := range d.StructErrors() {
		if e.Check() == Checks.Structure.XRefKeyword {
			got6014 = true
		}
	}
	if !got6014 {
		t.Error("trailer/xref recovery not reported as a 6.1.4 diagnostic")
	}
}

// TestRecoverTrailerPicksLatestCatalog: with two /Type /Catalog objects
// (an incremental-update shape), trailer recovery must deterministically
// choose the one appearing later in the file.
func TestRecoverTrailerPicksLatestCatalog(t *testing.T) {
	b := pdfgen.NewBuilder("%PDF-1.4\n")
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.Obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] >>")
	b.Obj(5, "<< /Type /Catalog /Pages 2 0 R >>") // later revision's catalog
	data := b.FinishClassic("<< /Size 6 /Root 5 0 R >>")
	trimmed := data[:bytes.Index(data, []byte("trailer"))]

	for range 3 {
		d, err := OpenBytes(trimmed)
		if err != nil {
			t.Fatalf("OpenBytes: %v", err)
		}
		root, ok := d.EffectiveTrailer().Entries["Root"].(PDFRef)
		if !ok || root.ObjNum != 5 {
			t.Fatalf("recovered Root = %v, want object 5 (latest catalog)", d.EffectiveTrailer().Entries["Root"])
		}
		d.Close()
	}
}
