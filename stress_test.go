package gopdfrab_test

import (
	"testing"

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
