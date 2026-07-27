package convert

import (
	"bytes"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"runtime/metrics"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/verify"
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

// liveHeapMetric is the runtime metric peakHeapDuring samples. Unlike
// ReadMemStats it does not stop the world, so it can be polled tightly.
const liveHeapMetric = "/memory/classes/heap/objects:bytes"

// liveHeap reads the current live heap in bytes.
func liveHeap() uint64 {
	s := []metrics.Sample{{Name: liveHeapMetric}}
	metrics.Read(s)
	return s[0].Value.Uint64()
}

// peakHeapDuring runs fn and returns the highest live heap observed while it
// ran, measured above the heap at entry. It answers what heapRetainedBy
// cannot: heapRetainedBy attributes what a *returned* value keeps, but a
// conversion's resolved graph is dropped before Run returns, so only a sample
// taken during the call sees it.
//
// Sampling means the number moves a little between runs -- treat it as a
// magnitude, not a deterministic figure like allocs/op.
func peakHeapDuring(fn func()) uint64 {
	runtime.GC()
	base := liveHeap()

	stop := make(chan struct{})
	done := make(chan uint64)
	go func() {
		var peak uint64
		for {
			select {
			case <-stop:
				done <- peak
				return
			default:
				if h := liveHeap(); h > peak {
					peak = h
				}
				time.Sleep(200 * time.Microsecond)
			}
		}
	}()

	fn()
	close(stop)
	peak := <-done

	if peak < base {
		return 0
	}
	return peak - base
}

// TestPeakHeapDuring pins the sampling primitive against a known transient: a
// 64 MB slice held only for the duration of the call must show up, and a
// function allocating nothing must not.
func TestPeakHeapDuring(t *testing.T) {
	const size = 64 << 20
	got := peakHeapDuring(func() {
		buf := make([]byte, size)
		for i := 0; i < len(buf); i += 4096 {
			buf[i] = 1 // touch it so the allocation is real
		}
		time.Sleep(5 * time.Millisecond)
		runtime.KeepAlive(buf)
	})
	if got < size/2 {
		t.Errorf("peak %d bytes for a %d-byte transient, want >= %d", got, size, size/2)
	}

	if got := peakHeapDuring(func() { time.Sleep(time.Millisecond) }); got > size/2 {
		t.Errorf("peak %d bytes for a no-op, want ~0", got)
	}
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
		outputLen = len(mustOutput(t, cr))
		return cr
	})
	if outputLen == 0 {
		t.Fatal("converted output is empty")
	}
	t.Logf("input %d KB -> output %d KB, result retains ~%d KB of heap",
		len(data)>>10, outputLen>>10, retained>>10)
}

// footprintSamples are the two shapes a conversion's memory can take. The
// large sample is object-heavy and content-light; the colour sample is the
// reverse. Attributing only the first would overfit: it says the graph
// dominates, which is not true of a file whose weight is in its streams.
var footprintSamples = map[string]string{
	"large": largeSamplePath,
	"colour": veraDir + "/6.2 Graphics/6.2.3.4 Separation and DeviceN colour spaces/" +
		"veraPDF test suite 6-2-3-4-t01-pass-b.pdf",
}

// TestConvertFootprintReport attributes a conversion's heap: the peak while it
// runs, what the resolved graph alone costs, and what the Reader's caches hold
// when it finishes. This is the roadmap item 8 baseline -- it reports, it does
// not gate, because only allocs/op is deterministic enough to assert on.
func TestConvertFootprintReport(t *testing.T) {
	for _, name := range slices.Sorted(maps.Keys(footprintSamples)) {
		t.Run(name, func(t *testing.T) { reportFootprint(t, footprintSamples[name]) })
	}
}

func reportFootprint(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skip("sample not present")
	}

	peak := peakHeapDuring(func() {
		cr, err := ConvertBytes(data, pdf.PDFA1B, Options{})
		if err != nil {
			t.Errorf("ConvertBytes: %v", err)
			return
		}
		cr.Close()
	})

	// The graph on its own, isolated from the conversion around it.
	var graphFootprint pdf.Footprint
	graphRetained := heapRetainedBy(func() any {
		doc, err := pdf.OpenBytes(data)
		if err != nil {
			t.Errorf("OpenBytes: %v", err)
			return nil
		}
		g, err := doc.ResolveGraph()
		if err != nil {
			t.Errorf("ResolveGraph: %v", err)
			return nil
		}
		graphFootprint = doc.Footprint()
		return []any{doc, g}
	})

	// Cache occupancy is read after a verify rather than a convert, because
	// convert releases the caches on its way out (TestConvertReleasesCaches).
	doc, err := pdf.OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	defer doc.Close()
	if _, err := verify.Verify(doc, pdf.PDFA1B); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	after := doc.Footprint()

	t.Logf("input %d KB", len(data)>>10)
	t.Logf("peak heap during convert ~%d KB", peak>>10)
	t.Logf("resolved graph alone retains ~%d KB (%d objects, %d nodes)",
		graphRetained>>10, graphFootprint.Objects, graphFootprint.Nodes)
	t.Logf("caches after verify: decoded %d streams/%d KB, scanned %d streams/%d KB, objstm %d streams/%d objects",
		after.DecodedStreams, after.DecodedBytes>>10,
		after.ScannedStreams, after.ScannedBytes>>10,
		after.ObjStreams, after.ObjStmObjects)
}

// BenchmarkConvertMemory reports the converted output size and the peak heap a
// conversion reaches, so a change to either footprint is visible in benchstat
// output alongside the standard allocs/op. peak_heap_MB is sampled, so unlike
// allocs/op it carries run-to-run noise; read it as a magnitude.
func BenchmarkConvertMemory(b *testing.B) {
	data, err := os.ReadFile(largeSamplePath)
	if err != nil {
		b.Skip("large sample not present")
	}
	var outputLen int
	var peak uint64
	for b.Loop() {
		p := peakHeapDuring(func() {
			cr, err := ConvertBytes(data, pdf.PDFA1B, Options{})
			if err != nil {
				b.Errorf("ConvertBytes: %v", err)
				return
			}
			out, err := cr.Output()
			if err != nil {
				b.Errorf("Output(): %v", err)
				return
			}
			outputLen = len(out)
			cr.Close()
		})
		if p > peak {
			peak = p
		}
	}
	b.ReportMetric(float64(outputLen)/(1<<20), "output_MB")
	b.ReportMetric(float64(peak)/(1<<20), "peak_heap_MB")
}

// TestConvertUnderTightBudgetMatchesDefault: the resident budget governs
// memoization only, so converting with caching effectively off -- which also
// puts every content scan on the streaming path (pdf.Reader.ScanStreamFunc) --
// must produce byte-identical output and the same verdict as converting with
// the default budget. This is the correctness gate for the budget being a speed
// dial rather than a behaviour switch.
func TestConvertUnderTightBudgetMatchesDefault(t *testing.T) {
	fixtures := sampleFixturePaths(failFixturesByExpectedClause(t), 20)
	if len(fixtures) == 0 {
		t.Skip("no corpora present")
	}
	defer pdf.SetMaxResidentBytes(-1)

	convert := func(data []byte) ([]byte, bool, int) {
		cr, err := ConvertBytes(data, pdf.PDFA1B, Options{})
		if err != nil {
			t.Fatalf("ConvertBytes: %v", err)
		}
		defer cr.Close()
		return mustOutput(t, cr), cr.Result.Valid, len(cr.Result.Issues)
	}

	tested := 0
	for _, path := range fixtures {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		pdf.SetMaxResidentBytes(-1)
		wantOut, wantValid, wantIssues := convert(data)

		pdf.SetMaxResidentBytes(1) // nothing fits
		gotOut, gotValid, gotIssues := convert(data)

		if !bytes.Equal(gotOut, wantOut) {
			t.Errorf("%s: output differs with caching off (%d vs %d bytes)",
				filepath.Base(path), len(gotOut), len(wantOut))
		}
		if gotValid != wantValid || gotIssues != wantIssues {
			t.Errorf("%s: verdict differs with caching off: got{valid=%v issues=%d} want{valid=%v issues=%d}",
				filepath.Base(path), gotValid, gotIssues, wantValid, wantIssues)
		}
		tested++
	}
	if tested == 0 {
		t.Skip("no readable fixtures")
	}
}

// sampleFixturePaths picks n fixtures spread evenly across the sorted corpus.
// Ranging over the map instead would sample differently on every run, so a
// fixture that breaks the property under test would fail one run in thirty --
// which is how a nondeterministic resource prune stayed hidden.
func sampleFixturePaths(fixtures map[string]string, n int) []string {
	paths := make([]string, 0, len(fixtures))
	for path := range fixtures {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	if len(paths) <= n {
		return paths
	}
	out := make([]string, 0, n)
	for i := range n {
		out = append(out, paths[i*len(paths)/n])
	}
	return out
}

// TestConvertReleasesCaches: a Document kept open past Convert must not still
// be holding the run's decoded and tokenized streams.
func TestConvertReleasesCaches(t *testing.T) {
	data, err := os.ReadFile(largeSamplePath)
	if err != nil {
		t.Skip("large sample not present")
	}
	doc, err := pdf.OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	defer doc.Close()

	cr, err := Run(doc, pdf.PDFA1B, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	defer cr.Close()

	if got := doc.Footprint(); got.Total() != 0 || got.DecodedStreams != 0 || got.ScannedStreams != 0 {
		t.Errorf("caches retained after Run: %+v", got)
	}
}
