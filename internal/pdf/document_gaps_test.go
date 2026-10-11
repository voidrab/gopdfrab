package pdf

import (
	"errors"
	"fmt"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdfgen"
)

// TestHybridReferenceFile: in a hybrid-reference file the classic table lists
// an object as free and the trailer's /XRefStm cross-reference stream gives its
// real offset (ISO 32000-1 7.5.8.4). The object must resolve.
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
