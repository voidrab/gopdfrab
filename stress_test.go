package gopdfrab_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	gopdfrab "github.com/voidrab/gopdfrab"
	"github.com/voidrab/gopdfrab/internal/pdfgen"
)

// TestGeneratedCorpusDoesNotPanic runs a deterministic batch of generated,
// deliberately-broken PDFs through the whole public pipeline (verify + convert)
// and fails with the reproducing seed if any input triggers a panic. Unlike the
// FuzzXxx targets, this runs on every plain `go test` invocation (including CI's
// `-short` run), so the generator always exercises the library in CI.
//
// A panic here is a real crash bug: the reported seed reproduces it via
// pdfgen.Generate(seed).
func TestGeneratedCorpusDoesNotPanic(t *testing.T) {
	n := 5000
	if testing.Short() {
		n = 500
	}

	for seed := int64(0); seed < int64(n); seed++ {
		data := pdfgen.Generate(seed)
		if p := runPipeline(data); p != nil {
			t.Fatalf("panic on pdfgen.Generate(%d): %v", seed, p)
		}
	}
}

// runPipeline exercises every public in-memory entry point on data, returning
// the recovered panic value (or nil). Errors returned by the calls are
// expected and ignored; only panics are failures.
func runPipeline(data []byte) (recovered any) {
	defer func() { recovered = recover() }()
	gopdfrab.VerifyBytes(data, gopdfrab.PDFA1B)
	gopdfrab.VerifyObjectModelBytes(data)
	gopdfrab.ConvertBytes(data, gopdfrab.PDFA1B)
	return nil
}

// TestGeneratedCorpusRace converts a batch of generated, deliberately-broken
// PDFs concurrently. Convert fans content-stream scanning across NumCPU workers
// that share one Reader, so this exercises the library's real intra-Reader
// concurrency. Run with `go test -race` to surface data races on the shared
// caches; without -race it is just an extra concurrent smoke test.
func TestGeneratedCorpusRace(t *testing.T) {
	n := 200
	if testing.Short() {
		n = 60
	}
	var wg sync.WaitGroup
	for s := int64(0); s < int64(n); s++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			defer func() { _ = recover() }()
			data := pdfgen.Generate(seed)
			gopdfrab.ConvertBytes(data, gopdfrab.PDFA1B)
			gopdfrab.VerifyBytes(data, gopdfrab.PDFA1B)
		}(s)
	}
	wg.Wait()
}

// TestGeneratedCorpusTimeBounded flags any generated input whose full pipeline
// run exceeds a generous budget -- an algorithmic-DoS signal (unbounded loop,
// decompression blow-up, quadratic scan). It reports the reproducing seed. Not
// run under -short (CI); it is a local diagnostic, and a hung input is caught
// by the timeout rather than blocking forever.
func TestGeneratedCorpusTimeBounded(t *testing.T) {
	if testing.Short() {
		t.Skip("time-bound scan is a local diagnostic; skipped in -short")
	}
	const budget = 10 * time.Second
	for s := int64(0); s < 2000; s++ {
		data := pdfgen.Generate(s)
		done := make(chan struct{})
		go func() {
			defer close(done)
			defer func() { _ = recover() }()
			runPipeline(data)
		}()
		select {
		case <-done:
		case <-time.After(budget):
			t.Fatalf("pipeline exceeded %v on pdfgen.Generate(%d): possible algorithmic DoS", budget, s)
		}
	}
}

// concurrencyFixtures are the corpus files the concurrency-contract tests below
// run on: one font-heavy Isartor fixture and one veraPDF pass file, so both a
// failing and a conformant verdict are exercised.
var concurrencyFixtures = []string{
	filepath.Join("tests", "Isartor", "PDFA-1b", "6.3 Fonts",
		"6.3.4 Embedded font programs", "isartor-6-3-4-t01-fail-a.pdf"),
	filepath.Join("tests", "veraPDF", "PDF_A-1b", "6.4 Transparency",
		"veraPDF test suite 6-4-t01-pass-a.pdf"),
}

// presentFixtures returns concurrencyFixtures that exist, skipping the test
// when the corpora are absent.
func presentFixtures(t *testing.T) []string {
	t.Helper()
	var present []string
	for _, p := range concurrencyFixtures {
		if _, err := os.Stat(p); err == nil {
			present = append(present, p)
		}
	}
	if len(present) == 0 {
		t.Skip("conformance corpora not present")
	}
	return present
}

// issueSet renders a Result as a sorted, comparable signature of its verdict
// and issues.
func issueSet(res gopdfrab.Result) string {
	lines := make([]string, 0, len(res.Issues)+1)
	lines = append(lines, fmt.Sprintf("valid=%v", res.Valid))
	for _, iss := range res.Issues {
		lines = append(lines, iss.String())
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// TestDocumentPerGoroutineIsSafe exercises the documented contract: a Document
// is not safe to share, but one per goroutine is, and a single Profile may be
// shared by all of them. Every goroutine must reach the same verdict; run with
// `go test -race` to prove the per-Document caches never touch each other.
//
// The cross-goroutine equality doubles as a determinism check -- the same
// comparison, across two read paths rather than two goroutines, is what caught
// two map-iteration message bugs in the Windows seek path.
func TestDocumentPerGoroutineIsSafe(t *testing.T) {
	fixtures := presentFixtures(t)
	profile := gopdfrab.PDFA1B // shared deliberately: profiles are immutable

	for _, path := range fixtures {
		t.Run(filepath.Base(path), func(t *testing.T) {
			const goroutines = 8
			got := make([]string, goroutines)
			var wg sync.WaitGroup
			for i := range goroutines {
				wg.Add(1)
				go func() {
					defer wg.Done()
					doc, err := gopdfrab.Open(path)
					if err != nil {
						t.Errorf("Open: %v", err)
						return
					}
					defer doc.Close()

					res, err := doc.Verify(profile)
					if err != nil {
						t.Errorf("Verify: %v", err)
						return
					}
					cr, err := doc.Convert(profile)
					if err != nil {
						t.Errorf("Convert: %v", err)
						return
					}
					defer cr.Close()
					got[i] = issueSet(res) + fmt.Sprintf("\nresidual=%d", len(cr.Residual()))
				}()
			}
			wg.Wait()

			for i := 1; i < goroutines; i++ {
				if got[i] != got[0] {
					t.Errorf("goroutine %d disagrees with goroutine 0:\n%s\n---\n%s", i, got[0], got[i])
				}
			}
		})
	}
}

// TestConvertResultConcurrentReads pins the other half of the ConvertResult
// contract: Output, WriteTo and Save may run concurrently (each opens the spill
// backing separately), while Close is the caller's to serialize.
func TestConvertResultConcurrentReads(t *testing.T) {
	path := presentFixtures(t)[0]
	cr, err := gopdfrab.Convert(path, gopdfrab.PDFA1B)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	defer cr.Close()

	want, err := cr.Output()
	if err != nil {
		t.Fatalf("Output: %v", err)
	}

	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			switch i % 3 {
			case 0:
				got, err := cr.Output()
				if err != nil || len(got) != len(want) {
					t.Errorf("Output: %d bytes, err=%v; want %d bytes", len(got), err, len(want))
				}
			case 1:
				var sink countingWriter
				n, err := cr.WriteTo(&sink)
				if err != nil || n != int64(len(want)) {
					t.Errorf("WriteTo: %d bytes, err=%v; want %d", n, err, len(want))
				}
			default:
				out := filepath.Join(t.TempDir(), "out.pdf")
				if err := cr.Save(out); err != nil {
					t.Errorf("Save: %v", err)
				}
			}
		}()
	}
	wg.Wait()
}

// countingWriter discards its input and counts the bytes.
type countingWriter int64

func (c *countingWriter) Write(p []byte) (int, error) {
	*c += countingWriter(len(p))
	return len(p), nil
}

// TestBatchHelpersConcurrentCalls runs the batch entry points against each
// other -- they fan out internally, so two concurrent calls are the case most
// likely to expose shared state -- and checks each matches a serial run.
func TestBatchHelpersConcurrentCalls(t *testing.T) {
	paths := presentFixtures(t)

	serial, err := gopdfrab.VerifyAll(paths, gopdfrab.PDFA1B)
	if err != nil {
		t.Fatalf("VerifyAll: %v", err)
	}

	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := gopdfrab.VerifyAll(paths, gopdfrab.PDFA1B)
			if err != nil {
				t.Errorf("concurrent VerifyAll: %v", err)
				return
			}
			for i := range got {
				if got[i].Path != serial[i].Path || issueSet(got[i].Result) != issueSet(serial[i].Result) {
					t.Errorf("concurrent VerifyAll differs at %s", got[i].Path)
				}
			}
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			var mu sync.Mutex
			seen := map[string]int{}
			err := gopdfrab.ConvertEach(paths, gopdfrab.PDFA1B, gopdfrab.Options{},
				func(fr gopdfrab.FileResult[gopdfrab.ConvertResult]) error {
					mu.Lock()
					defer mu.Unlock()
					seen[fr.Path] = len(fr.Result.Residual())
					return nil
				})
			if err != nil {
				t.Errorf("concurrent ConvertEach: %v", err)
				return
			}
			if len(seen) != len(paths) {
				t.Errorf("ConvertEach saw %d files, want %d", len(seen), len(paths))
			}
		}()
	}
	wg.Wait()
}

// TestSetLimitsConcurrentWithVerify covers the SetLimits contract: the caps are
// atomics, so setting them while verifies run is safe. It writes back the value
// already in effect, so no in-flight decode changes behaviour and parallel
// tests cannot be perturbed.
func TestSetLimitsConcurrentWithVerify(t *testing.T) {
	saved := gopdfrab.CurrentLimits()
	defer gopdfrab.SetLimits(saved)

	path := presentFixtures(t)[0]
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				gopdfrab.SetLimits(gopdfrab.CurrentLimits())
			}
		}
	}()

	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := gopdfrab.VerifyBytes(data, gopdfrab.PDFA1B); err != nil {
				t.Errorf("VerifyBytes: %v", err)
			}
		}()
	}
	close(stop)
	wg.Wait()

	if got := gopdfrab.CurrentLimits(); got != saved {
		t.Errorf("limits changed under concurrency: %+v, want %+v", got, saved)
	}
}

// TestVerifyContextConcurrentCancellation checks the cancellation path is
// race-clean too: a context cancelled from another goroutine while batch
// verifies run must not corrupt the results it does return.
func TestVerifyContextConcurrentCancellation(t *testing.T) {
	paths := presentFixtures(t)
	ctx, cancel := context.WithCancel(context.Background())

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		cancel()
	}()

	res, err := gopdfrab.VerifyAllContext(ctx, paths, gopdfrab.PDFA1B, gopdfrab.Options{})
	wg.Wait()
	if err != nil {
		t.Fatalf("VerifyAllContext: %v", err)
	}
	if len(res) != len(paths) {
		t.Fatalf("VerifyAllContext returned %d results, want %d", len(res), len(paths))
	}
	// Each file either completed or recorded ctx.Err(); nothing in between.
	for _, fr := range res {
		if fr.Err != nil && !errors.Is(fr.Err, context.Canceled) {
			t.Errorf("%s: unexpected error %v", fr.Path, fr.Err)
		}
	}
}
