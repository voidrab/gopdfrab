package verify

import (
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// TestScanStreamReportsUndecodable covers the readerless path: a context with
// no reader must still report a broken stream, since that is how most unit
// tests and convert's throwaway contexts are built.
func TestScanStreamReportsUndecodable(t *testing.T) {
	broken := pdf.NewPDFDict()
	broken.HasStream = true
	broken.Entries.Set("Filter", pdf.PDFName{Value: "FlateDecode"})
	broken.RawStream = []byte("not a zlib stream")

	noOp := func(string, []pdf.PDFValue) { t.Error("a broken stream reported an operator") }
	ctx := &ValidationContext{} // nil reader
	if err := ctx.scanStream(broken, noOp); err == nil {
		t.Fatal("expected a decode error")
	}
	if !hasCheck(ctx, pdf.Checks.Structure.StreamUndecodable) {
		t.Error("readerless scan did not report StreamUndecodable")
	}

	// A second read of the same stream must not report twice.
	ctx.scanStream(broken, noOp)
	n := 0
	for _, e := range ctx.errs {
		if e.Check() == pdf.Checks.Structure.StreamUndecodable {
			n++
		}
	}
	if n != 1 {
		t.Errorf("StreamUndecodable reported %d times, want 1", n)
	}

	// The reader-backed path reports the same way.
	withReader := &ValidationContext{reader: &pdf.Reader{}}
	if err := withReader.scanStream(broken, noOp); err == nil {
		t.Fatal("expected a decode error through the reader")
	}
	if !hasCheck(withReader, pdf.Checks.Structure.StreamUndecodable) {
		t.Error("reader-backed scan did not report StreamUndecodable")
	}
}
