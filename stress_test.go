package gopdfrab_test

import (
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
