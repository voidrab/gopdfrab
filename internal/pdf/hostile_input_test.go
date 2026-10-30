package pdf

import (
	"fmt"
	"runtime/debug"
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
// file never sizes an allocation, on the mmap/bytes path and on the seek path
// alike. The seek path must bound the length before allocating a buffer of
// that size.
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
			if sd, ok := v.(PDFDict); ok && len(sd.Stream()) > len(data) {
				t.Fatalf("Stream() is %d bytes, larger than the %d-byte file", len(sd.Stream()), len(data))
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

// TestWrongStreamLengthRecoversFromEndstream pins the keyword-scan fallback: a
// /Length that misses the body by more than the framing window -- too short,
// too long, or past the end of the file -- recovers the body up to the EOL
// before endstream on both read paths, reports 6.1.7, and degrades nothing.
func TestWrongStreamLengthRecoversFromEndstream(t *testing.T) {
	body := strings.Repeat("0123456789", 20) // 200 bytes, well past the 64-byte window
	cases := map[string]struct {
		length string
		eol    string
	}{
		"too short":   {"10", "\n"},
		"too long":    {"5000", "\n"},
		"past EOF":    {"99999999999", "\n"},
		"CRLF before": {"10", "\r\n"},
	}
	open := map[string]func([]byte) (*Reader, error){"bytes": OpenBytes, "seek": OpenBytesSeek}
	for name, tc := range cases {
		for path, fn := range open {
			t.Run(name+"/"+path, func(t *testing.T) {
				b := pdfgen.NewBuilder("%PDF-1.4\n")
				b.Obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
				b.Obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
				b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Contents 4 0 R >>")
				b.Obj(4, "<< /Length "+tc.length+" >>\nstream\n"+body+tc.eol+"endstream")
				d, err := fn(b.FinishClassic("<< /Size 5 /Root 1 0 R >>"))
				if err != nil {
					t.Fatalf("open: %v", err)
				}
				defer d.Close()

				v, err := d.ResolveReference(PDFRef{ObjNum: 4})
				if err != nil {
					t.Fatalf("resolve: %v", err)
				}
				sd, ok := v.(PDFDict)
				if !ok || string(sd.Stream()) != body {
					t.Fatalf("Stream() = %q, want the %d-byte body", sd.Stream(), len(body))
				}
				if d.HasDegradedObjects() {
					t.Error("object degraded despite a recoverable extent")
				}
				var mismatch bool
				for _, e := range d.Diagnostics() {
					mismatch = mismatch || e.Kind == DiagStreamLengthMismatch
				}
				if !mismatch {
					t.Error("no length-mismatch diagnostic for the wrong /Length")
				}
			})
		}
	}
}

// TestLexerConsecutiveCommentsUseConstantStack pins that a long run of comment
// lines is skipped iteratively: NextToken does not recurse once per comment.
// The stack cap turns a regression into a hard failure (a fatal stack
// overflow) instead of a silent multi-megabyte stack.
func TestLexerConsecutiveCommentsUseConstantStack(t *testing.T) {
	data := []byte(strings.Repeat("%c\n", 1_000_000) + "42")
	l := NewLexerBytes(data, 0)
	defer l.Release()
	old := debug.SetMaxStack(8 << 20)
	defer debug.SetMaxStack(old)
	tok := l.NextToken()
	if tok.Type != TokenInteger || tok.IntValue() != 42 {
		t.Fatalf("token after the comments = %+v, want integer 42", tok)
	}
}
