package writer

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// TestWritePDFResolveError checks that WritePDF returns ResolveGraph's error
// and writes nothing: a /Root chain of dictionaries longer than the resolver's
// depth cap cannot be resolved.
func TestWritePDFResolveError(t *testing.T) {
	const n = 140000 // above the resolver's 1<<17 depth cap
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	offs := make([]int, n+1)
	for i := 1; i <= n; i++ {
		offs[i] = b.Len()
		if i < n {
			fmt.Fprintf(&b, "%d 0 obj\n<< /N %d 0 R >>\nendobj\n", i, i+1)
		} else {
			fmt.Fprintf(&b, "%d 0 obj\n<< >>\nendobj\n", i)
		}
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", n+1)
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "%010d 00000 n \n", offs[i])
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", n+1, xref)

	r, err := pdf.OpenBytes(b.Bytes())
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	defer r.Close()

	var out bytes.Buffer
	err = WritePDF(r, &out)
	if err == nil || err.Error() != "resolve depth limit exceeded" {
		t.Fatalf("WritePDF error = %v, want \"resolve depth limit exceeded\"", err)
	}
	if out.Len() != 0 {
		t.Errorf("WritePDF wrote %d bytes before failing, want 0", out.Len())
	}
}

// failingWriter fails every write with errBoom.
type failingWriter struct{}

var errBoom = errors.New("boom")

func (failingWriter) Write([]byte) (int, error) { return 0, errBoom }

// TestWriteDocumentIndexedWriteFailures fails the destination the first time
// the 64 KiB output buffer flushes. Growing a leading stream object moves that
// first flush through every later stage of the document (the object bodies,
// the xref header and entries, the trailer, and the startxref tail), so each
// stage's error return is exercised, and the error always surfaces.
func TestWriteDocumentIndexedWriteFailures(t *testing.T) {
	build := func(pad int) pdf.PDFDict {
		stream := pdf.PDFDict{
			Entries:   pdf.DictOf(map[string]pdf.PDFValue{pdf.RefKey: pdf.PDFRef{ObjNum: 9}}),
			HasStream: true,
			RawStream: bytes.Repeat([]byte{'x'}, pad),
		}
		catalog := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			pdf.RefKey: pdf.PDFRef{ObjNum: 1},
			"Type":     pdf.PDFName{Value: "Catalog"},
			"Data":     stream,
		})}
		return pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Root": catalog})}
	}

	// Overhead of the non-stream parts is a few hundred bytes, so this range
	// straddles the buffer size at every stage.
	const bufSize = 64 << 10
	for pad := bufSize - 600; pad <= bufSize+100; pad++ {
		_, err := WriteDocumentIndexed(failingWriter{}, build(pad), 0)
		if !errors.Is(err, errBoom) {
			t.Fatalf("pad %d: error = %v, want errBoom", pad, err)
		}
	}
}

// TestWriteDocumentIndexedFlushError checks that a failure surfacing only at
// the final flush (a small document that never fills the buffer) is returned.
func TestWriteDocumentIndexedFlushError(t *testing.T) {
	trailer := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Root": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			pdf.RefKey: pdf.PDFRef{ObjNum: 1}, "Type": pdf.PDFName{Value: "Catalog"},
		})},
	})}
	if _, err := WriteDocumentIndexed(failingWriter{}, trailer, 0); !errors.Is(err, errBoom) {
		t.Fatalf("error = %v, want errBoom", err)
	}
}

// TestDiscoverSharedArrayVisitedOnce checks that an array reachable from two
// places is walked once, so an indirect object inside it is numbered once.
func TestDiscoverSharedArrayVisitedOnce(t *testing.T) {
	inner := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{pdf.RefKey: pdf.PDFRef{ObjNum: 7}})}
	shared := pdf.PDFArray{inner}
	catalog := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		pdf.RefKey: pdf.PDFRef{ObjNum: 1},
		"A":        shared,
		"B":        shared,
	})}
	trailer := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Root": catalog})}

	order, err := WriteDocumentIndexed(io.Discard, trailer, 0)
	if err != nil {
		t.Fatalf("WriteDocumentIndexed: %v", err)
	}
	if len(order) != 2 {
		t.Errorf("wrote %d indirect objects, want 2 (catalog + the shared array's dict)", len(order))
	}
}
