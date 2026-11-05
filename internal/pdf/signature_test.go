package pdf

import (
	"bytes"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdfgen"
)

// sigDoc returns a document whose object 2 is sigBody, followed by tail
// after its final %%EOF, and the ByteRange that covers the document's one
// revision, computed from the first "<abcd>" in sigBody, the Contents string.
func sigDoc(t *testing.T, sigBody, tail string) (*Reader, [4]int64) {
	t.Helper()
	b := pdfgen.NewBuilder("%PDF-1.7\n")
	b.Obj(1, "<< /Type /Catalog /Pages 3 0 R >>")
	b.Obj(2, sigBody)
	b.Obj(3, "<< /Type /Pages /Count 0 /Kids [] >>")
	data := append(bytes.Clone(b.FinishClassic("<< /Size 4 /Root 1 0 R >>")), tail...)
	start := int64(bytes.Index(data, []byte("<abcd>")))
	end := start + int64(len("<abcd>"))
	eof := int64(bytes.LastIndex(data, []byte("%%EOF"))) + int64(len("%%EOF"))
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r, [4]int64{0, start, end, eof - end}
}

func TestContentsSpanRejects(t *testing.T) {
	r, br := sigDoc(t, "<< /Contents <abcd> >>", "")
	if start, end, ok := r.ContentsSpan(PDFRef{ObjNum: 2}); !ok || start != br[1] || end != br[2] {
		t.Errorf("ContentsSpan = %d, %d, %v, want %d, %d, true", start, end, ok, br[1], br[2])
	}
	if _, _, ok := r.ContentsSpan(PDFRef{ObjNum: 9}); ok {
		t.Error("an object missing from the xref table has a Contents span")
	}
	r.xrefTable[9] = r.xrefTable[2]
	if _, _, ok := r.ContentsSpan(PDFRef{ObjNum: 9}); ok {
		t.Error("an xref entry pointing at another object has a Contents span")
	}
}

func TestRevisionEnd(t *testing.T) {
	r, _ := sigDoc(t, "<< /Contents <abcd> >>",
		"\nx %%EOF\n1 0 obj << >> stream\n%%EOF\nendstream endobj\n  %%EOF")
	data, err := r.FullBytes()
	if err != nil {
		t.Fatal(err)
	}
	first := int64(bytes.Index(data, []byte("%%EOF")))
	for _, tc := range []struct {
		from int64
		want int64
	}{
		// From the %%EOF itself: the byte before it is outside the first window.
		{first, first + int64(len("%%EOF\n"))},
		// Past it: the one after "x" and the one in the stream do not count.
		{first + 1, int64(len(data))},
		// From the end of the file no %%EOF is found.
		{int64(len(data)), int64(len(data))},
	} {
		if got, _ := r.RevisionEnd(tc.from); got != tc.want {
			t.Errorf("RevisionEnd(%d) = %d, want %d", tc.from, got, tc.want)
		}
	}
}

// TestRevisionEndUnterminatedStream verifies that a stream with no endstream
// runs to the end of the file.
func TestRevisionEndUnterminatedStream(t *testing.T) {
	r, _ := sigDoc(t, "<< /Contents <abcd> >>", "\nstream\n%%EOF\n")
	data, err := r.FullBytes()
	if err != nil {
		t.Fatal(err)
	}
	from := int64(bytes.LastIndex(data, []byte("stream")))
	if eof, slack := r.RevisionEnd(from); eof != int64(len(data)) || slack != 0 {
		t.Errorf("RevisionEnd = %d, %d, want %d, 0", eof, slack, len(data))
	}
}

func TestAloneOnLineAtFileStart(t *testing.T) {
	r, _ := sigDoc(t, "<< /Contents <abcd> >>", "")
	if l := r.src.lexer(0); l.aloneOnLine() {
		t.Error("the first byte of the file is alone on its line")
	}
}
