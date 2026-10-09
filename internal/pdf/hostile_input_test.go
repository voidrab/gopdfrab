package pdf

import (
	"fmt"
	"strings"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdfgen"
)

// hostileStreamDoc builds a document whose object 4 is a stream declaring the
// given /Length over a three-byte body.
func hostileStreamDoc(length string, extra ...string) []byte {
	b := pdfgen.NewBuilder("%PDF-1.4\n")
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.Obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Resources << >> >>")
	b.Obj(4, "<< /Length "+length+" >>\nstream\nabc\nendstream")
	for i, body := range extra {
		b.Obj(5+i, body)
	}
	return b.FinishClassic(fmt.Sprintf("<< /Size %d /Root 1 0 R >>", 5+len(extra)))
}

// TestStreamLengthBoundedOnBothPaths pins that a /Length past the end of the
// file is rejected before anything is allocated, on the mmap/bytes path and
// on the seek path alike: the seek path used to make([]byte, length) first.
func TestStreamLengthBoundedOnBothPaths(t *testing.T) {
	data := hostileStreamDoc("99999999999")
	open := map[string]func([]byte) (*Reader, error){
		"bytes": OpenBytes,
		"seek":  OpenBytesSeek,
	}
	for name, fn := range open {
		t.Run(name, func(t *testing.T) {
			d, err := fn(data)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			defer d.Close()
			v, _ := d.ResolveReference(PDFRef{ObjNum: 4})
			if sd, ok := v.(PDFDict); ok && len(sd.RawStream) > len(data) {
				t.Fatalf("RawStream is %d bytes, larger than the %d-byte file", len(sd.RawStream), len(data))
			}
		})
	}
}

// TestStreamLengthResolveErrorKeepsCause pins that a failed indirect /Length
// reports the resolution error rather than the nil value it failed to produce.
func TestStreamLengthResolveErrorKeepsCause(t *testing.T) {
	// Object 5 is in the xref but does not parse, so resolving it errors.
	d, err := OpenBytesSeek(hostileStreamDoc("5 0 R", "<< /A"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer d.Close()
	d.degradeUnresolvable = false
	_, err = d.ResolveReference(PDFRef{ObjNum: 4})
	if err == nil {
		t.Fatal("expected an error for an unparseable indirect /Length")
	}
	if got := err.Error(); strings.Contains(got, "<nil>") {
		t.Fatalf("error lost its cause: %q", got)
	}
}
