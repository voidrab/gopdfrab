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
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/voidrab/gopdfrab"
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
	"small": filepath.Join("tests", "Isartor", "PDFA-1b",
		"6.7 Metadata", "6.7.2 Properties", "isartor-6-7-2-t01-fail-a.pdf"),
	// ~3.6 KB: at the corpus median.
	"median": filepath.Join("tests", "veraPDF", "PDF_A-1b",
		"6.7 Metadata", "6.7.2 Properties", "veraPDF test suite 6-7-2-t13-fail-g.pdf"),
	// ~21 KB: near the corpus 90th percentile.
	"p90": filepath.Join("tests", "Isartor", "PDFA-1b",
		"6.3 Fonts", "6.3.7 Character encodings", "isartor-6-3-7-t01-fail-a.pdf"),
	// ~203 KB: exercises embedded font program parsing (TrueType/CFF).
	"fonts": filepath.Join("tests", "Isartor", "PDFA-1b",
		"6.3 Fonts", "6.3.6 Font metrics", "isartor-6-3-6-t01-fail-b.pdf"),
	// ~492 KB: exercises colour space checks (Separation/DeviceN).
	"large_color": filepath.Join("tests", "veraPDF", "PDF_A-1b",
		"6.2 Graphics", "6.2.3.4 Separation and DeviceN colour spaces",
		"veraPDF test suite 6-2-3-4-t01-pass-b.pdf"),
	// ~3.9 MB: the corpus maximum; a 6.1.12 implementation-limits torture
	// test with 40,015 indirect objects (a /Pages node with /Count 10000
	// and ~10k page kids). The worst case for per-object resolution cost.
	"large": filepath.Join("tests", "Isartor", "PDFA-1b",
		"6.1 File structure", "6.1.12 Implementation Limits", "isartor-6-1-12-t01-fail-a.pdf"),
	// ~12 KB: a q/Q-nesting StringTooLong no in-place fixer can clamp, so
	// Convert falls back to whole-page rasterization -- the conversion worst
	// case (renders the page through the native rasterizer).
	"raster": filepath.Join("tests", "veraPDF", "PDF_A-1b",
		"6.1 File structure", "6.1.12 Implementation limits",
		"veraPDF test suite 6-1-12-t08-fail-a.pdf"),
}

// BenchmarkOpenVerify measures Open+Verify(A1B) for each representative
// file size. Run with: go test -bench=. -benchmem ./benchmarks/micro/...
func BenchmarkOpenVerify(b *testing.B) {
	root := repoRoot()
	for name, rel := range sampleFiles {
		path := filepath.Join(root, rel)
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				doc, err := gopdfrab.Open(path)
				if err != nil {
					b.Fatalf("Open(%s): %v", path, err)
				}
				if _, err := doc.Verify(gopdfrab.PDFA1B); err != nil {
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
				if _, err := gopdfrab.ConvertBytes(data, gopdfrab.PDFA1B); err != nil {
					b.Fatalf("ConvertBytes(%s): %v", name, err)
				}
			}
		})
	}
}

// maxLargeFileAllocs is a regression ceiling on allocations for one
// Open+Verify(A1B) pass over the "large" sample, the 6.1.12 torture test with
// 40,015 indirect objects. Resolved references and decoded streams are cached
// while the document is open, so code that re-parses or re-decodes them per
// use shows up as a jump in the count. Allocs/op is deterministic and independent of the
// environment, so this check is not flaky.
//
// Lower this value if further optimization reduces it further.
const maxLargeFileAllocs = 960_000

// TestLargeFileAllocationsBounded guards against reintroducing quadratic-ish
// re-parsing/re-decoding behavior on large, object-heavy PDFs. See
// maxLargeFileAllocs for context.
func TestLargeFileAllocationsBounded(t *testing.T) {
	path := filepath.Join(repoRoot(), sampleFiles["large"])
	if _, err := os.Stat(path); err != nil {
		t.Skip("large sample file not present")
	}

	allocs := testing.AllocsPerRun(3, func() {
		doc, err := gopdfrab.Open(path)
		if err != nil {
			t.Fatalf("Open(%s): %v", path, err)
		}
		defer doc.Close()
		if _, err := doc.Verify(gopdfrab.PDFA1B); err != nil {
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
// ConvertBytes pass over the "large" sample. Its 10k-element /Kids array makes
// each verify pass scan 10k page content streams, which is the dominant
// allocation source. Lower this value if further optimization reduces it.
const maxConvertLargeAllocs = 1_475_000

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
		if _, err := gopdfrab.ConvertBytes(data, gopdfrab.PDFA1B); err != nil {
			t.Fatalf("ConvertBytes(large): %v", err)
		}
	})

	if allocs > maxConvertLargeAllocs {
		t.Errorf("ConvertBytes(large) allocated %.0f times per run, want <= %d; "+
			"likely regained a verify pass or reintroduced per-object re-parsing",
			allocs, maxConvertLargeAllocs)
	}
}

// allocCase is one guarded cost path: VerifyBytes or ConvertBytes over a
// representative sample, with a regression ceiling on allocs/op.
type allocCase struct {
	sample  string
	convert bool // false = VerifyBytes, true = ConvertBytes
	ceiling float64
}

// costPathAllocCeilings extends the "large" torture-test guards above to the
// other distinct cost paths, so a regression in embedded font-program parsing,
// Separation/DeviceN colour handling, or the raster fallback is caught too. Each
// ceiling is a regression ceiling set with headroom over the measured value, not
// a tight target; allocs/op is deterministic and environment-independent, so the
// checks are not flaky. Lower a ceiling if optimization reduces it.
var costPathAllocCeilings = []allocCase{
	{sample: "fonts", convert: false, ceiling: 1_700},       // measured ~1517
	{sample: "fonts", convert: true, ceiling: 3_250},        // measured ~2928
	{sample: "large_color", convert: false, ceiling: 1_200}, // measured ~1081
	{sample: "raster", convert: true, ceiling: 2_400},       // measured ~2135
}

// TestCostPathAllocationsBounded guards the font, colour, and raster cost paths
// against reintroducing per-object re-parsing/re-decoding. See
// costPathAllocCeilings.
func TestCostPathAllocationsBounded(t *testing.T) {
	root := repoRoot()
	for _, c := range costPathAllocCeilings {
		name := "verify/" + c.sample
		if c.convert {
			name = "convert/" + c.sample
		}
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(root, sampleFiles[c.sample]))
			if err != nil {
				t.Skipf("%s sample not present", c.sample)
			}
			allocs := testing.AllocsPerRun(3, func() {
				if c.convert {
					if _, err := gopdfrab.ConvertBytes(data, gopdfrab.PDFA1B); err != nil {
						t.Fatalf("ConvertBytes(%s): %v", c.sample, err)
					}
				} else {
					if _, err := gopdfrab.VerifyBytes(data, gopdfrab.PDFA1B); err != nil {
						t.Fatalf("VerifyBytes(%s): %v", c.sample, err)
					}
				}
			})
			t.Logf("%s: %.0f allocs/run (ceiling %.0f)", name, allocs, c.ceiling)
			if allocs > c.ceiling {
				t.Errorf("%s allocated %.0f times per run, want <= %.0f; "+
					"likely reintroduced per-object re-parsing or re-decoding", name, allocs, c.ceiling)
			}
		})
	}
}

// TestConvertSeededVerifyMatchesFreshVerify proves that the seeded verify path
// (Reader.SeedResolvedGraph, used by the convert loop) produces the same
// Valid flag and issue set as an independent from-scratch VerifyBytes of the
// same output bytes, for every sample file.
func TestConvertSeededVerifyMatchesFreshVerify(t *testing.T) {
	root := repoRoot()
	for name, rel := range sampleFiles {
		path := filepath.Join(root, rel)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Logf("%s: skipping (file not present)", name)
			continue
		}
		t.Run(name, func(t *testing.T) {
			cr, err := gopdfrab.ConvertBytes(data, gopdfrab.PDFA1B)
			if err != nil {
				t.Fatalf("ConvertBytes: %v", err)
			}
			defer cr.Close()
			out, err := cr.Output()
			if err != nil {
				t.Fatalf("Output: %v", err)
			}

			// Independent fresh verify of the same output bytes.
			fresh, err := gopdfrab.VerifyBytes(out, gopdfrab.PDFA1B)
			if err != nil {
				t.Fatalf("VerifyBytes: %v", err)
			}

			if cr.Result.Valid != fresh.Valid {
				t.Errorf("seeded Valid=%v, fresh Valid=%v", cr.Result.Valid, fresh.Valid)
			}

			seededClauses := issueClauses(cr.Result.Issues)
			freshClauses := issueClauses(fresh.Issues)
			if fmt.Sprint(seededClauses) != fmt.Sprint(freshClauses) {
				t.Errorf("seeded issues %v != fresh issues %v", seededClauses, freshClauses)
			}
		})
	}
}

// issueClauses extracts sorted clause strings from a result's issues.
func issueClauses(issues []gopdfrab.PDFError) []string {
	out := make([]string, len(issues))
	for i, iss := range issues {
		out[i] = fmt.Sprintf("%s/%d", iss.Check().Clause(), iss.Check().Subclause())
	}
	return out
}
