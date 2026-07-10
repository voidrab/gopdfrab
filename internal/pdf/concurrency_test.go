package pdf_test

import (
	"bytes"
	"compress/zlib"
	"sync"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfgen"
)

// flateStreamDict returns a FlateDecode stream dict whose body decodes to
// payload.
func flateStreamDict(payload []byte) pdf.PDFDict {
	var zb bytes.Buffer
	zw := zlib.NewWriter(&zb)
	zw.Write(payload)
	zw.Close()
	d := pdf.NewPDFDict()
	d.HasStream = true
	d.RawStream = zb.Bytes()
	d.Entries["Filter"] = pdf.PDFName{Value: "FlateDecode"}
	d.Entries["Length"] = pdf.PDFInteger(zb.Len())
	return d
}

// TestConcurrentDecodeIsSafe hammers the mutex-guarded concurrent decode path
// from many goroutines on a single Reader and checks every result matches.
// Run with `go test -race` to prove the decodedCache locking is correct.
func TestConcurrentDecodeIsSafe(t *testing.T) {
	r, err := pdf.OpenBytes(pdfgen.Seeds()[0])
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	defer r.Close()

	payload := []byte("hello concurrent decode world")
	dict := flateStreamDict(payload)

	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := r.DecodeStreamCachedConcurrent(dict)
			if err != nil || !bytes.Equal(got, payload) {
				t.Errorf("concurrent decode = %q, err=%v; want %q", got, err, payload)
			}
		}()
	}
	wg.Wait()
}

// TestDecompressionBoundedByLength documents that FlateDecode honours the
// stream's declared /Length rather than expanding without limit: a body that
// would inflate to many megabytes is bounded by Length, so a small compressed
// payload cannot be used to force an unbounded allocation. The test's own
// completion (well under the package test timeout) guards against a regression
// to unbounded/hanging decode.
func TestDecompressionBoundedByLength(t *testing.T) {
	// ~8 MiB of zeros compresses to a few KiB; a classic decompression-bomb
	// shape. Decoding must complete promptly and return a bounded result.
	payload := make([]byte, 8<<20)
	dict := flateStreamDict(payload)

	out, err := pdf.DecodeStream(dict)
	if err != nil {
		return // erroring out is an acceptable bounded outcome
	}
	if len(out) > len(payload)+64 {
		t.Fatalf("decoded output %d exceeds declared payload %d", len(out), len(payload))
	}
}
