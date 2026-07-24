package convert

import (
	"bytes"
	"errors"
	"os"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdfgen"
)

// setSpillThreshold lowers spillThreshold for a test and restores it after.
func setSpillThreshold(t *testing.T, n int) {
	t.Helper()
	old := spillThreshold
	spillThreshold = n
	t.Cleanup(func() { spillThreshold = old })
}

// seal writes data through a spillWriter in two chunks (so the threshold can be
// crossed mid-stream) and returns the sealed backing.
func seal(t *testing.T, data []byte) *outputBacking {
	t.Helper()
	var sw spillWriter
	half := len(data) / 2
	if _, err := sw.Write(data[:half]); err != nil {
		t.Fatalf("Write first half: %v", err)
	}
	if _, err := sw.Write(data[half:]); err != nil {
		t.Fatalf("Write second half: %v", err)
	}
	b, err := sw.finish()
	if err != nil {
		t.Fatalf("finish: %v", err)
	}
	return b
}

// assertRoundTrip checks every backing accessor reproduces data exactly and that
// open() yields a parseable reader.
func assertRoundTrip(t *testing.T, b *outputBacking, data []byte) {
	t.Helper()
	if b.len() != len(data) {
		t.Errorf("len() = %d, want %d", b.len(), len(data))
	}
	got, err := b.bytes()
	if err != nil {
		t.Fatalf("bytes(): %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("bytes() mismatch: got %d bytes, want %d", len(got), len(data))
	}
	var buf bytes.Buffer
	n, err := b.writeTo(&buf)
	if err != nil {
		t.Fatalf("writeTo(): %v", err)
	}
	if n != int64(len(data)) || !bytes.Equal(buf.Bytes(), data) {
		t.Errorf("writeTo() wrote %d bytes, want %d", n, len(data))
	}
	r, err := b.open()
	if err != nil {
		t.Fatalf("open(): %v", err)
	}
	r.Close()
}

func TestSpillWriterInMemory(t *testing.T) {
	setSpillThreshold(t, 1<<20)
	data := pdfgen.PlainThreeIssue()

	b := seal(t, data)
	if b.path != "" {
		t.Fatalf("small output spilled to %q, want in-memory", b.path)
	}
	assertRoundTrip(t, b, data)

	if err := b.close(); err != nil {
		t.Errorf("close(): %v", err)
	}
	if _, err := b.bytes(); !errors.Is(err, errNoOutput) {
		t.Errorf("bytes() after close = %v, want errNoOutput", err)
	}
}

func TestSpillWriterSpills(t *testing.T) {
	data := pdfgen.PlainThreeIssue()
	setSpillThreshold(t, len(data)/4) // force a mid-stream spill

	b := seal(t, data)
	if b.path == "" {
		t.Fatal("output over threshold stayed in memory, want a temp file")
	}
	if _, err := os.Stat(b.path); err != nil {
		t.Fatalf("temp file missing: %v", err)
	}
	assertRoundTrip(t, b, data)

	path := b.path
	if err := b.close(); err != nil {
		t.Errorf("close(): %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("temp file %q survived close: stat err = %v", path, err)
	}
	if err := b.close(); err != nil {
		t.Errorf("second close(): %v", err)
	}
	if _, err := b.bytes(); !errors.Is(err, errNoOutput) {
		t.Errorf("bytes() after close = %v, want errNoOutput", err)
	}
}

// TestSpillWriterDegradesWhenTempFails points TMPDIR at a non-existent
// directory so os.CreateTemp fails, and asserts the writer degrades to
// in-memory rather than failing the write -- never worse than the previous
// always-in-memory behaviour.
func TestSpillWriterDegradesWhenTempFails(t *testing.T) {
	t.Setenv("TMPDIR", filepathJoinNonexistent(t))
	data := pdfgen.PlainThreeIssue()
	setSpillThreshold(t, len(data)/4)

	b := seal(t, data)
	if b.path != "" {
		t.Fatalf("spilled to %q despite unusable TMPDIR, want in-memory fallback", b.path)
	}
	assertRoundTrip(t, b, data)
}

func TestOutputBackingNilAndZero(t *testing.T) {
	var nilB *outputBacking
	if nilB.len() != 0 {
		t.Errorf("nil.len() = %d, want 0", nilB.len())
	}
	if err := nilB.close(); err != nil {
		t.Errorf("nil.close() = %v, want nil", err)
	}

	zero := &outputBacking{}
	if _, err := zero.bytes(); !errors.Is(err, errNoOutput) {
		t.Errorf("zero.bytes() = %v, want errNoOutput", err)
	}
	if _, err := zero.writeTo(&bytes.Buffer{}); !errors.Is(err, errNoOutput) {
		t.Errorf("zero.writeTo() = %v, want errNoOutput", err)
	}
	if _, err := zero.open(); !errors.Is(err, errNoOutput) {
		t.Errorf("zero.open() = %v, want errNoOutput", err)
	}
}

// TestOutputBackingMissingFile covers the accessors' error paths when a backing
// names a temp file that is gone.
func TestOutputBackingMissingFile(t *testing.T) {
	b := &outputBacking{path: t.TempDir() + "/vanished.pdf", size: 5}
	if _, err := b.writeTo(&bytes.Buffer{}); err == nil {
		t.Error("writeTo on a missing temp file returned nil error")
	}
	if _, err := b.bytes(); err == nil {
		t.Error("bytes on a missing temp file returned nil error")
	}
	if _, err := b.open(); err == nil {
		t.Error("open on a missing temp file returned nil error")
	}
}

// TestSpillWriterOpenMmapsFile round-trips a real PDF through the spilled path
// and confirms open() returns a reader that resolves the document -- the mmap
// path the final verify relies on.
func TestSpillWriterOpenMmapsFile(t *testing.T) {
	data := pdfgen.PlainThreeIssue()
	setSpillThreshold(t, 0) // spill from the first byte
	b := seal(t, data)
	defer b.close()
	if b.path == "" {
		t.Fatal("threshold 0 did not spill")
	}
	r, err := b.open()
	if err != nil {
		t.Fatalf("open(): %v", err)
	}
	defer r.Close()
	if _, err := r.ResolveGraph(); err != nil {
		t.Errorf("ResolveGraph on reopened spill file: %v", err)
	}
}

// filepathJoinNonexistent returns a path guaranteed not to exist, under the
// test's temp dir so nothing is created.
func filepathJoinNonexistent(t *testing.T) string {
	t.Helper()
	return t.TempDir() + "/does/not/exist"
}
