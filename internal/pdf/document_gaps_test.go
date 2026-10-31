package pdf

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdfgen"
)

// TestHybridReferenceFile verifies that an object the classic table lists as
// free resolves through the trailer's /XRefStm cross-reference stream, which
// gives its real offset (ISO 32000-1 7.5.8.4).
func TestHybridReferenceFile(t *testing.T) {
	b := pdfgen.NewBuilder("%PDF-1.5\n")
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.Obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] >>")
	b.Obj(4, "<< /Hybrid true >>")
	entry := []byte{1, 0, 0, 0, byte(b.OffsetOf(4)), 0, 0} // type 1, offset, gen 0
	xrefStm := b.Len()
	b.StreamObj(5, "<< /Type /XRef /Size 6 /W [1 4 2] /Index [4 1]", entry)
	xref := b.Len()
	b.WriteString("xref\n0 5\n0000000000 65535 f \n")
	for i := 1; i <= 3; i++ {
		fmt.Fprintf(bufWriter{b}, "%010d 00000 n \n", b.OffsetOf(i))
	}
	b.WriteString("0000000000 00000 f \n")
	fmt.Fprintf(bufWriter{b}, "trailer\n<< /Size 6 /Root 1 0 R /XRefStm %d >>\nstartxref\n%d\n%%%%EOF", xrefStm, xref)

	r, err := OpenBytes(b.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if got, want := r.xrefTable[4], b.OffsetOf(4); got != want {
		t.Errorf("xrefTable[4] = %d, want %d (offset from the XRefStm section)", got, want)
	}
	v, err := r.ResolveReference(PDFRef{ObjNum: 4})
	d, ok := v.(PDFDict)
	if err != nil || !ok || d.Entries.Get("Hybrid") != PDFBoolean(true) {
		t.Errorf("object 4 = %#v, %v; want the dictionary from the XRefStm section", v, err)
	}
}

type bufWriter struct{ b *pdfgen.Builder }

func (w bufWriter) Write(p []byte) (int, error) { w.b.Write(p); return len(p), nil }

var _ = errors.New

// TestResolvePathStopsAtUnparseableObject verifies that a path whose subtree
// holds a reference to an unparseable object reports the error instead of a
// value.
func TestResolvePathStopsAtUnparseableObject(t *testing.T) {
	doc := "1 0 obj\n<< /A 2 0 R >>\nendobj\n2 0 obj\n<< /B\nendobj\n"
	d := &Reader{src: memSource([]byte(doc)), xrefTable: map[int]int64{
		1: 0, 2: int64(len("1 0 obj\n<< /A 2 0 R >>\nendobj\n")),
	}}
	root := PDFDict{Entries: DictOf(map[string]PDFValue{"A": PDFRef{ObjNum: 2}})}
	if v, err := d.resolvePath(root, []string{"A", "B"}); err == nil {
		t.Errorf("resolvePath = %#v, want an error", v)
	}
}

// TestObjectHeaderExistsWithoutAnyHeader verifies that, when the file holds no
// "N G obj" header at all, the answer is "not scanned", since absence proves
// nothing.
func TestObjectHeaderExistsWithoutAnyHeader(t *testing.T) {
	d := &Reader{src: memSource([]byte("no objects in here"))}
	if exists, scanned := d.objectHeaderExists(1); exists || scanned {
		t.Errorf("objectHeaderExists = %v, %v; want false, false", exists, scanned)
	}
}

func TestRecordDiagnosticsOncePerObject(t *testing.T) {
	d := &Reader{}
	d.recordDegraded(7, errors.New("a"))
	d.recordDegraded(7, errors.New("b"))
	if len(d.degradedDiags) != 1 || len(d.parseDiagnostics) != 1 {
		t.Errorf("degraded: %d diags, %d parse diags; want 1 each", len(d.degradedDiags), len(d.parseDiagnostics))
	}
	d = &Reader{}
	d.recordRecovered(7, 10, 20, errors.New("a"))
	d.recordRecovered(7, 11, 21, errors.New("b"))
	if len(d.parseDiagnostics) != 1 {
		t.Errorf("recovered: %d parse diags, want 1", len(d.parseDiagnostics))
	}
}

func TestAdoptStreamCachesSharesDecodeErrors(t *testing.T) {
	key := StreamKey{ptr: 1, len: 2}
	src := &Reader{decodeErrs: map[StreamKey]error{key: errors.New("bad")}}
	d := &Reader{}
	d.AdoptStreamCaches(src)
	if d.decodeErrs[key] == nil {
		t.Error("decode errors not adopted")
	}
}

func TestNegativeXRefOffsetIsAnError(t *testing.T) {
	d := &Reader{src: memSource([]byte("x")), xrefTable: map[int]int64{3: -1}}
	if _, err := d.ResolveReference(PDFRef{ObjNum: 3}); err == nil || !strings.Contains(err.Error(), "negative offset") {
		t.Errorf("err = %v, want a negative-offset error", err)
	}
}

// TestEncryptedBareObjectsAreDecrypted verifies that strings in an object whose
// body is a bare string or an array are decrypted too, not only those inside
// dictionaries.
func TestEncryptedBareObjectsAreDecrypted(t *testing.T) {
	h := &stdSecurityHandler{r: 3, v: 2, keyLen: 5, fileKey: []byte{1, 2, 3, 4, 5},
		stmMethod: cryptRC4, strMethod: cryptRC4, encryptObjNum: 99}
	seal := func(objNum int, plain string) string {
		out := make([]byte, len(plain))
		newRC4(h.objectKey(objNum, 0, cryptRC4)).XORKeyStream(out, []byte(plain))
		return fmt.Sprintf("<%x>", out)
	}
	doc := fmt.Sprintf("5 0 obj\n%s\nendobj\n6 0 obj\n[%s]\nendobj\n", seal(5, "secret"), seal(6, "hidden"))
	d := &Reader{src: memSource([]byte(doc)), crypt: h, xrefTable: map[int]int64{
		5: 0, 6: int64(strings.Index(doc, "6 0 obj")),
	}}
	if v, err := d.ResolveReference(PDFRef{ObjNum: 5}); err != nil || v != (PDFString{Value: "secret"}) {
		t.Errorf("object 5 = %#v, %v; want the decrypted string", v, err)
	}
	v, err := d.ResolveReference(PDFRef{ObjNum: 6})
	if arr, ok := v.(PDFArray); err != nil || !ok || len(arr) != 1 || arr[0] != (PDFString{Value: "hidden"}) {
		t.Errorf("object 6 = %#v, %v; want [(hidden)]", v, err)
	}
}
