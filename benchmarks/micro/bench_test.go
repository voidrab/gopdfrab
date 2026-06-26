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
	// ~12 KB: a q/Q-nesting StringTooLong no in-place fixer can clamp, so
	// Convert falls back to whole-page rasterization -- the conversion worst
	// case (renders the page through the native rasterizer).
	"raster": filepath.Join("test documents", "veraPDF", "PDF_A-1b",
		"6.1 File structure", "6.1.12 Implementation limits",
		"veraPDF test suite 6-1-12-t08-fail-a.pdf"),
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
				if _, err := doc.Verify(pdfrab.PDFA_1B); err != nil {
					doc.Close()
					b.Fatalf("Verify(%s): %v", path, err)
				}
				doc.Close()
			}
		})
	}
}

// BenchmarkConvert measures the full PDF/A-1b conversion pipeline
// (ConvertBytes: pre-emptive fixups -> bounded serialize/verify/fix loop ->
// raster last resort) for each representative file, with the bytes read once
// up front. Run with: go test -bench=BenchmarkConvert -benchmem ./benchmarks/micro/...
func BenchmarkConvert(b *testing.B) {
	root := repoRoot()
	for name, rel := range sampleFiles {
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			b.Run(name, func(b *testing.B) { b.Skip("sample file not present") })
			continue
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := pdfrab.ConvertBytes(data, pdfrab.PDFA_1B); err != nil {
					b.Fatalf("ConvertBytes(%s): %v", name, err)
				}
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
// context.go), down to ~1.76M after. Pooling the zlib decoder (content.go's
// zlibReaderPool) and the per-object lexer's bufio.Reader (lexer.go's
// bufioReaderPool) cut both allocs and bytes/op further: ~1.76M allocs /
// ~1.18GB bytes down to ~1.55M allocs / ~80MB bytes. Making ResolveGraph
// resolve references in place into the cached d.objCache instances instead
// of building a parallel deep copy of the graph (document.go's
// resolveInPlace, shared with resolver.go's resolveObject) removed ~90k more,
// down to ~1.46M. Allocs/op is deterministic and environment-independent,
// unlike wall-clock timing, so this check is not flaky. The remaining allocs
// are now dominated by ContentScanner's per-token operand boxing, not by
// re-parsing or graph resolution.
//
// Lower this value if further optimization reduces it further.
const maxLargeFileAllocs = 1_600_000

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
		if _, err := doc.Verify(pdfrab.PDFA_1B); err != nil {
			t.Fatalf("Verify(%s): %v", path, err)
		}
	})

	if allocs > maxLargeFileAllocs {
		t.Errorf("Open+Verify(large) allocated %.0f times per run, want <= %d; "+
			"likely reintroduced per-object re-parsing or re-decoding",
			allocs, maxLargeFileAllocs)
	}
}

// maxConvertLargeAllocs is a regression ceiling on allocations for one
// ConvertBytes pass over the "large" sample. Conversion re-parses the graph
// once per verify pass, so this tracks the pass count as much as per-object
// cost.
// Lower this value if further optimization reduces it.
const maxConvertLargeAllocs = 3_700_000

// TestConvertLargeAllocationsBounded guards conversion against regaining a
// verify pass (or reintroducing per-object re-parsing) on large, object-heavy
// PDFs. See maxConvertLargeAllocs.
func TestConvertLargeAllocationsBounded(t *testing.T) {
	path := filepath.Join(repoRoot(), sampleFiles["large"])
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skip("large sample file not present")
	}

	allocs := testing.AllocsPerRun(3, func() {
		if _, err := pdfrab.ConvertBytes(data, pdfrab.PDFA_1B); err != nil {
			t.Fatalf("ConvertBytes(large): %v", err)
		}
	})

	if allocs > maxConvertLargeAllocs {
		t.Errorf("ConvertBytes(large) allocated %.0f times per run, want <= %d; "+
			"likely regained a verify pass or reintroduced per-object re-parsing",
			allocs, maxConvertLargeAllocs)
	}
}
