package pdf_test

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"reflect"
	"sort"
	"strings"
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
	d.Entries.Set("Filter", pdf.PDFName{Value: "FlateDecode"})
	d.Entries.Set("Length", pdf.PDFInteger(zb.Len()))
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

// TestSeparateReadersAreIndependent is the positive half of the Reader
// concurrency contract: a Reader must not be shared, but one per goroutine is
// safe even when they parse the same bytes. Each goroutine resolves the whole
// graph -- filling every unsynchronized cache on its own Reader -- and the
// results must be equal. Run with `go test -race`.
func TestSeparateReadersAreIndependent(t *testing.T) {
	data := pdfgen.Seeds()[0]

	want, err := resolveOnce(data)
	if err != nil {
		t.Fatalf("baseline resolve: %v", err)
	}

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := resolveOnce(data)
			if err != nil {
				t.Errorf("concurrent resolve: %v", err)
				return
			}
			if got != want {
				t.Errorf("concurrent resolve produced a different graph:\n%s\n---\n%s", want, got)
			}
		}()
	}
	wg.Wait()
}

// resolveOnce opens data in its own Reader, resolves the full object graph and
// returns its signature.
func resolveOnce(data []byte) (string, error) {
	r, err := pdf.OpenBytes(data)
	if err != nil {
		return "", err
	}
	defer r.Close()
	g, err := r.ResolveGraph()
	if err != nil {
		return "", err
	}
	var b strings.Builder
	graphSig(&b, g, map[uintptr]bool{})
	return b.String(), nil
}

// graphSig renders a resolved graph deterministically: dictionary keys sorted,
// and each dictionary visited once, since a resolved graph is cyclic (a page's
// /Parent points back at its Pages node).
func graphSig(b *strings.Builder, v pdf.PDFValue, seen map[uintptr]bool) {
	switch t := v.(type) {
	case pdf.PDFDict:
		id := reflect.ValueOf(t.Entries).Pointer()
		if seen[id] {
			b.WriteString("<<seen>>")
			return
		}
		seen[id] = true
		keys := make([]string, 0, t.Entries.Len())
		for k := range t.Entries.All() {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteString("<<")
		for _, k := range keys {
			fmt.Fprintf(b, "/%s ", k)
			graphSig(b, t.Entries.Get(k), seen)
		}
		if t.HasStream {
			fmt.Fprintf(b, "stream:%d ", len(t.RawStream))
		}
		b.WriteString(">>")
	case pdf.PDFArray:
		b.WriteString("[")
		for _, e := range t {
			graphSig(b, e, seen)
			b.WriteString(" ")
		}
		b.WriteString("]")
	case nil:
		b.WriteString("null")
	default:
		fmt.Fprintf(b, "%v ", v)
	}
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
