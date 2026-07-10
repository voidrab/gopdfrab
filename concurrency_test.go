package gopdfrab_test

import (
	"sync"
	"testing"
	"time"

	gopdfrab "github.com/voidrab/gopdfrab"
	"github.com/voidrab/gopdfrab/internal/pdfgen"
)

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
			gopdfrab.ConvertBytes(data, gopdfrab.PDFA_1B)
			gopdfrab.VerifyBytes(data, gopdfrab.PDFA_1B)
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
