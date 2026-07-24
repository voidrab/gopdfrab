package convert

import (
	"os"
	"runtime"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// largeSamplePath is the corpus maximum (~3.9 MB): the 6.1.12 implementation-
// limits torture test. Its converted output is the largest a committed fixture
// produces, so it is the honest sample for attributing a conversion's heap
// footprint.
const largeSamplePath = isartorDir + "/6.1 File structure/6.1.12 Implementation Limits/isartor-6-1-12-t01-fail-a.pdf"

// heapRetainedBy returns the live heap, in bytes, that the value built by build
// keeps reachable: it reads HeapAlloc with the value alive, drops it, and reads
// again after a GC. It is a deterministic attribution (no sampling), accurate
// for a large steady allocation such as a held output buffer; small values sit
// within GC noise and read as ~0.
func heapRetainedBy(build func() any) uint64 {
	var before, after runtime.MemStats
	obj := build()
	runtime.GC()
	runtime.ReadMemStats(&before)
	runtime.KeepAlive(obj)
	obj = nil
	runtime.GC()
	runtime.ReadMemStats(&after)
	if before.HeapAlloc < after.HeapAlloc {
		return 0
	}
	return before.HeapAlloc - after.HeapAlloc
}

// TestHeapRetainedBy pins the attribution primitive: a value holding an 8 MB
// slice must be measured as retaining roughly that, and a value retaining
// nothing as ~0.
func TestHeapRetainedBy(t *testing.T) {
	const size = 8 << 20
	got := heapRetainedBy(func() any { return make([]byte, size) })
	if got < size/2 {
		t.Errorf("retained %d bytes for an %d-byte slice, want >= %d", got, size, size/2)
	}

	if got := heapRetainedBy(func() any { return 42 }); got > size/2 {
		t.Errorf("retained %d bytes for a scalar, want ~0", got)
	}
}

// TestConvertMemoryReport converts the large sample and records how much heap
// the returned result retains versus the output size. It grounds roadmap item 8
// (measure before deciding how far to take it): today the whole output rides in
// ConvertResult.Output, so the retained heap tracks the output size. The strict
// spill guard lives in the lazy-output tests.
func TestConvertMemoryReport(t *testing.T) {
	data, err := os.ReadFile(largeSamplePath)
	if err != nil {
		t.Skip("large sample not present")
	}

	var outputLen int
	retained := heapRetainedBy(func() any {
		cr, err := ConvertBytes(data, pdf.PDFA1B, Options{})
		if err != nil {
			t.Fatalf("ConvertBytes: %v", err)
		}
		outputLen = len(cr.Output)
		return cr
	})
	if outputLen == 0 {
		t.Fatal("converted output is empty")
	}
	t.Logf("input %d KB -> output %d KB, result retains ~%d KB of heap",
		len(data)>>10, outputLen>>10, retained>>10)
}

// BenchmarkConvertMemory reports the converted output size as a metric, so a
// change to the per-conversion output footprint is visible in benchstat output
// alongside the standard allocs/op.
func BenchmarkConvertMemory(b *testing.B) {
	data, err := os.ReadFile(largeSamplePath)
	if err != nil {
		b.Skip("large sample not present")
	}
	var outputLen int
	for b.Loop() {
		cr, err := ConvertBytes(data, pdf.PDFA1B, Options{})
		if err != nil {
			b.Fatalf("ConvertBytes: %v", err)
		}
		outputLen = len(cr.Output)
	}
	b.ReportMetric(float64(outputLen)/(1<<20), "output_MB")
}
