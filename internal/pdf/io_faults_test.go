package pdf

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

var errInjected = errors.New("injected I/O failure")

// faultySource is a fileSource over in-memory bytes whose reads can be made to
// fail, to reach the error paths a healthy file or buffer never takes. The hook
// receives the 1-based number of the call and returns whether to fail it.
type faultySource struct {
	*bytes.Reader
	failReadAt func(n int) bool // a failed ReadAt reads nothing
	readAts    int
}

func newFaulty(data string) *faultySource {
	return &faultySource{Reader: bytes.NewReader([]byte(data))}
}

func (f *faultySource) Close() error { return nil }

func (f *faultySource) ReadAt(p []byte, off int64) (int, error) {
	f.readAts++
	if f.failReadAt != nil && f.failReadAt(f.readAts) {
		return 0, errInjected
	}
	return f.Reader.ReadAt(p, off)
}

const streamObj = "4 0 obj\n<< /Length 5 >>\nstream\nhello\nendstream\nendobj\n"

// faultyReader opens data[...] as a Reader over src with object 4 at offset 0.
// Errors are not degraded, so they surface from ResolveReference.
func faultyReader(src *faultySource) *Reader {
	return &Reader{file: src, src: source{file: src, size: int64(src.Len())}, xrefTable: map[int]int64{4: 0}}
}

func TestResolveReadFailure(t *testing.T) {
	src := newFaulty(streamObj)
	src.failReadAt = func(int) bool { return true }
	if _, err := faultyReader(src).ResolveReference(PDFRef{ObjNum: 4}); !errors.Is(err, errInjected) {
		t.Errorf("err = %v, want the injected read failure", err)
	}
}

func TestStreamBodyReadFailure(t *testing.T) {
	src := newFaulty(streamObj)
	// ReadAt calls: 1 = the lexer's window, 2 = look for endstream after
	// /Length, 3 = read the body.
	src.failReadAt = func(n int) bool { return n == 3 }
	if _, err := faultyReader(src).ResolveReference(PDFRef{ObjNum: 4}); !errors.Is(err, errInjected) {
		t.Errorf("err = %v, want the injected ReadAt failure", err)
	}
}

func TestXRefStreamReadFailure(t *testing.T) {
	src := newFaulty(streamObj)
	src.failReadAt = func(int) bool { return true }
	if _, err := faultyReader(src).tryParseXRefStream(0, false); !errors.Is(err, errInjected) {
		t.Errorf("err = %v, want the injected failure to read the object", err)
	}
}

func TestParseClassicXRefTableReaderPath(t *testing.T) {
	parse := func(data string) error {
		return newTestFileReader([]byte(data)).parseXRefTable(0)
	}
	if err := parse("xref"); !errors.Is(err, io.EOF) {
		t.Errorf("xref with nothing after it: err = %v, want EOF", err)
	}
	if err := parse("nope\n"); err == nil || !strings.Contains(err.Error(), "expected 'xref'") {
		t.Errorf("wrong keyword: err = %v", err)
	}
	if err := parse("xref\njunk\n"); err != nil {
		t.Errorf("subsection header with the wrong field count ends the table: err = %v", err)
	}

	// A section longer than the first window, whose widening read fails.
	src := newFaulty("xref\n0 1000\n" + strings.Repeat("0000000017 00000 n \n", 1000))
	src.failReadAt = func(n int) bool { return n == 2 }
	d := &Reader{file: src, src: source{file: src, size: int64(src.Len())}}
	if err := d.parseXRefTable(0); !errors.Is(err, errInjected) {
		t.Errorf("read failure mid section: err = %v, want the injected failure", err)
	}
	src = newFaulty("xref\n")
	src.failReadAt = func(int) bool { return true }
	d = &Reader{file: src, src: source{file: src, size: int64(src.Len())}}
	if err := d.parseXRefTable(0); !errors.Is(err, errInjected) {
		t.Errorf("read failure at the keyword: err = %v, want the injected failure", err)
	}
}

func TestParseXRefSectionAtReadFailureMidSection(t *testing.T) {
	src := newFaulty("xref\n0 1000\n" + strings.Repeat("0000000017 00000 n \n", 1000))
	src.failReadAt = func(n int) bool { return n == 2 }
	d := &Reader{file: src, src: source{file: src, size: int64(src.Len())}, xrefTable: map[int]int64{}}
	if _, err := d.ParseXRefSectionAt(0, false); err == nil {
		t.Error("expected an error: the trailer is unreachable")
	}
}

func TestOpenFailsWhenTailUnreadable(t *testing.T) {
	// The source claims to be larger than it is, so the trailing read fails.
	data := "%PDF-1.4\n" + strings.Repeat(" ", 64)
	_, err := newDocument(bytesFileSource{bytes.NewReader([]byte(data))}, int64(len(data))+4096, nil, nil, nil)
	if err == nil {
		t.Error("expected an error for a source shorter than its declared size")
	}
}

func TestCloseReportsUnmapFailureOnce(t *testing.T) {
	d := &Reader{file: newFaulty(""), unmap: func() error { return errInjected }}
	if err := d.Close(); !errors.Is(err, errInjected) {
		t.Errorf("first Close = %v, want the unmap failure", err)
	}
	if err := d.Close(); err != nil {
		t.Errorf("second Close = %v, want nil (idempotent)", err)
	}
}
