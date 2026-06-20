// Package micro contains an in-process Go microbenchmark for gopdfrab's
// Open+Verify path. Unlike the cmd/gopdfrab-bench CLI (used for cross-tool
// process-level comparisons), this benchmark runs entirely inside the Go
// testing harness, so it measures the verification engine itself with no
// process-startup or I/O-flushing noise: ns/op, B/op, and allocs/op via
// `go test -bench`.
//
// Three representative files from the vendored corpora are exercised
// separately, since the corpora are dominated by tiny files (median ~3.6 KB)
// with a long tail up to ~4 MB; a single aggregate number would hide that.
package micro

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	pdfrab "github.com/voidrab/gopdfrab"
)

// repoRoot locates the module root relative to this source file, so the
// benchmark works regardless of the working directory `go test` is invoked
// from.
func repoRoot() string {
	_, thisFile, _, _ := runtime.Caller(0)
	// thisFile is .../benchmarks/micro/bench_test.go
	return filepath.Join(filepath.Dir(thisFile), "..", "..")
}

var sampleFiles = map[string]string{
	// ~2 KB: near the corpus minimum.
	"small": filepath.Join("test documents", "Isartor testsuite", "PDFA-1b",
		"6.7 Metadata", "6.7.2 Properties", "isartor-6-7-2-t01-fail-a.pdf"),
	// ~3.6 KB: at the corpus median.
	"median": filepath.Join("test documents", "veraPDF", "PDF_A-1b",
		"6.7 Metadata", "6.7.2 Properties", "veraPDF test suite 6-7-2-t13-fail-g.pdf"),
	// ~21 KB: near the corpus 90th percentile.
	"p90": filepath.Join("test documents", "Isartor testsuite", "PDFA-1b",
		"6.3 Fonts", "6.3.7 Character encodings", "isartor-6-3-7-t01-fail-a.pdf"),
	// ~203 KB: exercises embedded font program parsing (TrueType/CFF).
	"fonts": filepath.Join("test documents", "Isartor testsuite", "PDFA-1b",
		"6.3 Fonts", "6.3.6 Font metrics", "isartor-6-3-6-t01-fail-b.pdf"),
	// ~492 KB: exercises colour space checks (Separation/DeviceN).
	"large_color": filepath.Join("test documents", "veraPDF", "PDF_A-1b",
		"6.2 Graphics", "6.2.3.4 Separation and DeviceN colour spaces",
		"veraPDF test suite 6-2-3-4-t01-pass-b.pdf"),
	// ~3.9 MB: the corpus maximum; a 6.1.12 implementation-limits torture
	// test with 40,015 indirect objects (a /Pages node with /Count 10000
	// and ~10k page kids). The worst case for per-object resolution cost.
	"large": filepath.Join("test documents", "Isartor testsuite", "PDFA-1b",
		"6.1 File structure", "6.1.12 Implementation Limits", "isartor-6-1-12-t01-fail-a.pdf"),
}

// BenchmarkOpenVerify measures Open+Verify(A_1B) for each representative
// file size. Run with: go test -bench=. -benchmem ./benchmarks/micro/...
func BenchmarkOpenVerify(b *testing.B) {
	root := repoRoot()
	for name, rel := range sampleFiles {
		path := filepath.Join(root, rel)
		b.Run(name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				doc, err := pdfrab.Open(path)
				if err != nil {
					b.Fatalf("Open(%s): %v", path, err)
				}
				if _, err := doc.Verify(pdfrab.A_1B); err != nil {
					doc.Close()
					b.Fatalf("Verify(%s): %v", path, err)
				}
				doc.Close()
			}
		})
	}
}

// maxLargeFileAllocs is a regression ceiling on allocations for one
// Open+Verify(A_1B) pass over the "large" sample (the 6.1.12 torture test
// with 40,015 indirect objects). It guards against reintroducing the
// per-object re-parsing and re-decoding blowup this count used to track:
// ~4.64M allocs/op before resolveReference/decodeStream results were
// memoized on Document/ValidationContext (resolver.go, content.go,
// context.go), down to ~1.76M after. Allocs/op is deterministic and
// environment-independent, unlike wall-clock timing, so this check is not
// flaky. The remaining allocs are dominated by ResolveGraph's one-time deep
// copy of the (still large) resolved object graph, not by re-parsing.
//
// Lower this value if further optimization reduces it further.
const maxLargeFileAllocs = 2_000_000

// TestLargeFileAllocationsBounded guards against reintroducing quadratic-ish
// re-parsing/re-decoding behavior on large, object-heavy PDFs. See
// maxLargeFileAllocs for context.
func TestLargeFileAllocationsBounded(t *testing.T) {
	path := filepath.Join(repoRoot(), sampleFiles["large"])
	if _, err := os.Stat(path); err != nil {
		t.Skip("large sample file not present")
	}

	allocs := testing.AllocsPerRun(3, func() {
		doc, err := pdfrab.Open(path)
		if err != nil {
			t.Fatalf("Open(%s): %v", path, err)
		}
		defer doc.Close()
		if _, err := doc.Verify(pdfrab.A_1B); err != nil {
			t.Fatalf("Verify(%s): %v", path, err)
		}
	})

	if allocs > maxLargeFileAllocs {
		t.Errorf("Open+Verify(large) allocated %.0f times per run, want <= %d; "+
			"likely reintroduced per-object re-parsing or re-decoding",
			allocs, maxLargeFileAllocs)
	}
}
