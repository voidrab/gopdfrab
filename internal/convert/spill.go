package convert

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"runtime"
	"sync"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// spillThreshold is the in-memory ceiling for a single conversion's output. A
// larger output spills to a temp file so the whole PDF need not stay resident in
// the Go heap. A var, not a const, so tests can lower it to
// exercise the spill path on small inputs. Ignored where spillSupported is false.
var spillThreshold = 8 << 20 // 8 MB

// errNoOutput is returned by the backing accessors when there is nothing to
// read -- the zero backing, or one already closed.
var errNoOutput = errors.New("convert: no output")

// spillWriter is the io.Writer the document writer targets. It accumulates in
// memory until the total exceeds spillThreshold, then creates a temp file,
// flushes what it has, and streams the remainder to disk -- so write-time heap
// is bounded by the threshold regardless of output size. finish seals it into an
// outputBacking. A temp-file failure degrades silently to in-memory, so the
// writer is never worse than the previous always-in-memory behaviour.
type spillWriter struct {
	buf         bytes.Buffer
	file        *os.File
	bw          *bufio.Writer
	path        string
	spilled     bool
	spillFailed bool
	n           int64
}

// grow pre-sizes the in-memory buffer so it does not double its way up.
func (s *spillWriter) grow(n int) {
	if n > 0 && n <= spillThreshold {
		s.buf.Grow(n)
	}
}

func (s *spillWriter) Write(p []byte) (int, error) {
	var n int
	var err error
	switch {
	case s.spilled:
		n, err = s.bw.Write(p)
	case !spillSupported || s.spillFailed || s.buf.Len()+len(p) <= spillThreshold:
		n, err = s.buf.Write(p)
	default:
		if serr := s.spill(); serr != nil {
			s.spillFailed = true
			n, err = s.buf.Write(p)
		} else {
			n, err = s.bw.Write(p)
		}
	}
	s.n += int64(n)
	return n, err
}

// spill creates the temp file and flushes the in-memory buffer into it.
func (s *spillWriter) spill() error {
	f, err := os.CreateTemp("", "gopdfrab-out-*.pdf")
	if err != nil {
		return err
	}
	bw := bufio.NewWriter(f)
	if _, err := bw.Write(s.buf.Bytes()); err != nil {
		f.Close()
		os.Remove(f.Name())
		return err
	}
	s.file, s.bw, s.path = f, bw, f.Name()
	s.buf.Reset()
	s.spilled = true
	return nil
}

// finish seals the writer into a backing, flushing and closing the temp file if
// one was used. The writer must not be used afterwards.
func (s *spillWriter) finish() (*outputBacking, error) {
	if !s.spilled {
		return &outputBacking{mem: s.buf.Bytes()}, nil
	}
	if err := s.bw.Flush(); err != nil {
		s.file.Close()
		os.Remove(s.path)
		return nil, err
	}
	if err := s.file.Close(); err != nil {
		os.Remove(s.path)
		return nil, err
	}
	b := &outputBacking{path: s.path, size: s.n}
	// Backstop a forgotten Close: if the backing becomes unreachable with the
	// temp file still present, the finalizer removes it (Close clears it). This
	// mirrors os.File and keeps a dropped result from leaking a temp file.
	runtime.SetFinalizer(b, (*outputBacking).close)
	return b, nil
}

// outputBacking is what a ConvertResult holds instead of a resident []byte: the
// output either in memory (small) or in a temp file (large). It is referenced by
// a pointer so value copies of ConvertResult share one backing and a single
// idempotent Close. The zero value and a nil backing both read as "no output".
type outputBacking struct {
	mem  []byte // non-nil (possibly empty) when the output stayed in memory
	path string // non-empty when spilled to a temp file
	size int64  // output length when spilled
	once sync.Once
}

// len reports the output size without materializing it.
func (b *outputBacking) len() int {
	switch {
	case b == nil:
		return 0
	case b.path != "":
		return int(b.size)
	default:
		return len(b.mem)
	}
}

// bytes materializes the output as a []byte, reading the temp file on demand.
func (b *outputBacking) bytes() ([]byte, error) {
	switch {
	case b.len() == 0:
		return nil, errNoOutput
	case b.path != "":
		return os.ReadFile(b.path)
	default:
		return b.mem, nil
	}
}

// writeTo streams the output to w without materializing a second copy.
func (b *outputBacking) writeTo(w io.Writer) (int64, error) {
	if b.len() == 0 {
		return 0, errNoOutput
	}
	if b.path == "" {
		return bytes.NewReader(b.mem).WriteTo(w)
	}
	f, err := os.Open(b.path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return io.Copy(w, f)
}

// open reopens the output as a Reader for the final verify: pdf.Open mmaps the
// temp file (bounded heap), pdf.OpenBytes wraps the in-memory bytes.
func (b *outputBacking) open() (*pdf.Reader, error) {
	if b.len() == 0 {
		return nil, errNoOutput
	}
	if b.path == "" {
		return pdf.OpenBytes(b.mem)
	}
	return pdf.Open(b.path)
}

// close releases the backing: it removes the temp file and drops the in-memory
// bytes. Idempotent and safe on a nil backing, so value copies of a
// ConvertResult may each Close without double-freeing.
func (b *outputBacking) close() error {
	if b == nil {
		return nil
	}
	var err error
	b.once.Do(func() {
		if b.path != "" {
			runtime.SetFinalizer(b, nil)
			err = os.Remove(b.path)
			b.path = ""
		}
		b.mem = nil
	})
	return err
}
