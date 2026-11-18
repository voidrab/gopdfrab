package pdf

import (
	"bytes"
	"errors"
	"fmt"
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

func TestNextXRefSection(t *testing.T) {
	for _, tc := range []struct {
		sections      []int64
		pdfStart, off int64
		want          int64
	}{
		{nil, 0, 10, 10},
		{[]int64{5, 40, 20}, 0, 10, 20},
		{[]int64{5, 40, 20}, 3, 30, 43},
		{[]int64{5}, 0, 10, 10},
	} {
		if got := nextXRefSection(tc.sections, tc.pdfStart, tc.off); got != tc.want {
			t.Errorf("nextXRefSection(%v, %d, %d) = %d, want %d", tc.sections, tc.pdfStart, tc.off, got, tc.want)
		}
	}
}

// twoRevisions returns a file whose first revision, with a classic
// cross-reference table, has a page with a MediaBox 200 wide, and whose
// incremental update rewrites the page 400 wide inside an object stream,
// indexed by a cross-reference stream. first is where the first revision ends.
func twoRevisions() (data []byte, first int64) {
	var b bytes.Buffer
	b.WriteString("%PDF-1.7\n")
	o1 := b.Len()
	b.WriteString("1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")
	o2 := b.Len()
	b.WriteString("2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n")
	o3 := b.Len()
	b.WriteString("3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] >>\nendobj\n")
	xref1 := b.Len()
	fmt.Fprintf(&b, "xref\n0 4\n0000000000 65535 f \n%010d 00000 n \n%010d 00000 n \n%010d 00000 n \n", o1, o2, o3)
	fmt.Fprintf(&b, "trailer\n<< /Size 4 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", xref1)
	first = int64(b.Len())

	body := "3 0 << /Type /Page /Parent 2 0 R /MediaBox [0 0 400 400] >>"
	o4 := b.Len()
	fmt.Fprintf(&b, "4 0 obj\n<< /Type /ObjStm /N 1 /First 4 /Length %d >>\nstream\n%s\nendstream\nendobj\n", len(body), body)
	o5 := b.Len()
	var rows []byte
	row := func(typ byte, f2, f3 int) {
		rows = append(rows, typ, byte(f2>>24), byte(f2>>16), byte(f2>>8), byte(f2), byte(f3>>8), byte(f3))
	}
	row(2, 4, 0) // object 3, first in object stream 4
	row(1, o4, 0)
	row(1, o5, 0)
	fmt.Fprintf(&b, "5 0 obj\n<< /Type /XRef /Size 6 /Root 1 0 R /Prev %d /W [1 4 2] /Index [3 3] /Length %d >>\nstream\n",
		xref1, len(rows))
	b.Write(rows)
	fmt.Fprintf(&b, "\nendstream\nendobj\nstartxref\n%d\n%%%%EOF\n", o5)
	return b.Bytes(), first
}

// mediaBoxWidth returns the width of page object 3 of d.
func mediaBoxWidth(t *testing.T, d *Reader) float64 {
	t.Helper()
	v, err := d.ResolveReference(PDFRef{ObjNum: 3})
	if err != nil {
		t.Fatal(err)
	}
	page, _ := v.(PDFDict)
	box, err := FloatArray(page.Entries.Get("MediaBox"))
	if err != nil || len(box) != 4 {
		t.Fatalf("MediaBox of %v: %v", v, err)
	}
	return box[2]
}

func TestUpdateInObjectStreamWins(t *testing.T) {
	data, _ := twoRevisions()
	d, err := OpenBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if w := mediaBoxWidth(t, d); w != 400 {
		t.Errorf("page width %v, want the update's 400", w)
	}
	if !d.IsDefined(3) || !d.IsDefined(1) || d.IsDefined(9) {
		t.Errorf("IsDefined 3 %v, 1 %v, 9 %v", d.IsDefined(3), d.IsDefined(1), d.IsDefined(9))
	}
}

func TestPrefix(t *testing.T) {
	data, first := twoRevisions()
	for name, open := range map[string]func([]byte) (*Reader, error){"memory": OpenBytes, "seek": OpenBytesSeek} {
		t.Run(name, func(t *testing.T) {
			d, err := open(data)
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			p, err := d.Prefix(first)
			if err != nil {
				t.Fatal(err)
			}
			if w := mediaBoxWidth(t, p); w != 200 {
				t.Errorf("first revision's page width %v, want 200", w)
			}
			p.Close()
			// Closing the prefix leaves the file open.
			if w := mediaBoxWidth(t, d); w != 400 {
				t.Errorf("page width %v after closing the prefix, want 400", w)
			}
			for _, n := range []int64{-1, d.Size() + 1} {
				if _, err := d.Prefix(n); err == nil {
					t.Errorf("Prefix(%d) succeeded", n)
				}
			}
		})
	}
}

// shortReaderAt reads nothing, and reports no error for it.
type shortReaderAt struct{}

func (shortReaderAt) ReadAt([]byte, int64) (int, error) { return 0, nil }

// failingWriter fails every write.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("full") }

func TestCopyRangeFailures(t *testing.T) {
	failing := &Reader{src: source{file: failingReaderAt{}, size: 10}}
	if _, err := failing.ReadRange(0, 4); err == nil {
		t.Error("ReadRange of an unreadable file succeeded")
	}
	if err := failing.CopyRange(&bytes.Buffer{}, 8, 4); err == nil {
		t.Error("CopyRange past the end of the file succeeded")
	}
	short := &Reader{src: source{file: shortReaderAt{}, size: 10}}
	if err := short.CopyRange(&bytes.Buffer{}, 0, 4); err == nil {
		t.Error("CopyRange of a file that reads nothing succeeded")
	}
	mem := &Reader{src: source{mem: []byte("0123456789"), size: 10}}
	if err := mem.CopyRange(failingWriter{}, 0, 4); err == nil {
		t.Error("CopyRange to a failing writer succeeded")
	}
	if failing.endsWithEOF(5) {
		t.Error("endsWithEOF of an unreadable file")
	}
}
