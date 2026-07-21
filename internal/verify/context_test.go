package verify

import (
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// TestScanStreamCachedReportsUndecodable covers the uncached path: a context
// with no reader must still report a broken stream, since that is how most
// unit tests and convert's throwaway contexts are built.
func TestScanStreamCachedReportsUndecodable(t *testing.T) {
	broken := pdf.NewPDFDict()
	broken.HasStream = true
	broken.Entries["Filter"] = pdf.PDFName{Value: "FlateDecode"}
	broken.RawStream = []byte("not a zlib stream")

	ctx := &ValidationContext{} // nil reader
	if _, err := ctx.scanStreamCached(broken); err == nil {
		t.Fatal("expected a decode error")
	}
	if !hasCheck(ctx, pdf.Checks.Structure.StreamUndecodable) {
		t.Error("uncached scan did not report StreamUndecodable")
	}

	// A second read of the same stream must not report twice.
	ctx.scanStreamCached(broken)
	n := 0
	for _, e := range ctx.errs {
		if e.Check() == pdf.Checks.Structure.StreamUndecodable {
			n++
		}
	}
	if n != 1 {
		t.Errorf("StreamUndecodable reported %d times, want 1", n)
	}
}
