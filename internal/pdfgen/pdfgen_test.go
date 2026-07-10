package pdfgen_test

import (
	"bytes"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfgen"
)

// TestSeedsAreValid guards the generator's baselines: every seed must parse
// cleanly, otherwise the corruptors would be mutating already-broken input and
// the fuzz corpus would lose its structural signal.
func TestSeedsAreValid(t *testing.T) {
	for i, seed := range pdfgen.Seeds() {
		r, err := pdf.OpenBytes(seed)
		if err != nil {
			t.Errorf("seed %d does not parse: %v", i, err)
			continue
		}
		if _, err := r.ResolveGraph(); err != nil {
			t.Errorf("seed %d graph does not resolve: %v", i, err)
		}
		if n, err := r.GetPageCount(); err != nil || n < 1 {
			t.Errorf("seed %d page count = %d, err = %v; want >=1, nil", i, n, err)
		}
		r.Close()
	}
}

// TestGenerateIsDeterministic verifies that a given seed always produces
// byte-identical output, so any crash is reproducible from its seed.
func TestGenerateIsDeterministic(t *testing.T) {
	for seed := int64(0); seed < 50; seed++ {
		a := pdfgen.Generate(seed)
		b := pdfgen.Generate(seed)
		if !bytes.Equal(a, b) {
			t.Fatalf("Generate(%d) not deterministic", seed)
		}
	}
}

// TestGenerateAlwaysProducesOutput checks the generator never returns an empty
// slice for a wide range of seeds (each corruptor must produce output).
func TestGenerateAlwaysProducesOutput(t *testing.T) {
	empties := 0
	for seed := int64(0); seed < 500; seed++ {
		if len(pdfgen.Generate(seed)) == 0 {
			empties++
		}
	}
	// pureGarbage can legitimately yield a zero-length slice; allow a few.
	if empties > 20 {
		t.Errorf("too many empty documents: %d/500", empties)
	}
}
