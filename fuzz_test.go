package gopdfrab_test

import (
	"bytes"
	"testing"

	gopdfrab "github.com/voidrab/gopdfrab"
	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfgen"
)

// fuzzInputCap bounds accepted input size so the fuzzer does not flag
// legitimately-slow work on pathologically large inputs. Convert is the
// heaviest path, hence a modest cap.
const fuzzInputCap = 512 << 10 // 512 KiB

func seedFuzz(f *testing.F) {
	f.Helper()
	for _, s := range pdfgen.Seeds() {
		f.Add(s)
	}
	for _, b := range pdfgen.GenerateN(0, 128) {
		f.Add(b)
	}
}

// FuzzVerifyBytes drives the full verification pipeline against both the
// PDF/A-1b and generic object-model profiles. The invariant is no panic on any
// input; a returned error (or a Result reporting violations) is expected.
func FuzzVerifyBytes(f *testing.F) {
	seedFuzz(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > fuzzInputCap {
			return
		}
		gopdfrab.VerifyBytes(data, gopdfrab.PDFA1B)
		gopdfrab.VerifyObjectModelBytes(data)
	})
}

// FuzzGeneratedSeed lets the native fuzzer explore the generator's seed space
// under coverage guidance: it mutates the int64 seed, turns it into a broken
// PDF via pdfgen.Generate, and runs the whole pipeline. Panics propagate so the
// fuzzer flags them (do not recover here).
func FuzzGeneratedSeed(f *testing.F) {
	for i := int64(0); i < 128; i++ {
		f.Add(i)
	}
	f.Fuzz(func(t *testing.T, seed int64) {
		data := pdfgen.Generate(seed)
		if len(data) > fuzzInputCap {
			return
		}
		gopdfrab.VerifyBytes(data, gopdfrab.PDFA1B)
		gopdfrab.VerifyObjectModelBytes(data)
		gopdfrab.ConvertBytes(data, gopdfrab.PDFA1B)
	})
}

// FuzzConvertBytes drives the conversion pipeline (fixups, writer, raster
// fallback) -- the deepest code path -- asserting only that no input makes it
// panic.
func FuzzConvertBytes(f *testing.F) {
	seedFuzz(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > fuzzInputCap {
			return
		}
		gopdfrab.ConvertBytes(data, gopdfrab.PDFA1B)
	})
}

// FuzzConvertRoundTrip is the holistic invariant: whenever the converter emits
// output bytes, re-parsing those bytes with the library's own parser must not
// panic. In other words, Convert must never produce a PDF that crashes
// gopdfrab when read back.
func FuzzConvertRoundTrip(f *testing.F) {
	seedFuzz(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > fuzzInputCap {
			return
		}
		res, err := gopdfrab.ConvertBytes(data, gopdfrab.PDFA1B)
		if err != nil || len(res.Output) == 0 {
			return
		}
		r, err := pdf.OpenBytes(res.Output)
		if err != nil {
			return
		}
		defer r.Close()
		r.ResolveGraph()
		r.PageCount()
	})
}

// These targets check semantic invariants beyond "does not panic": the library
// must be deterministic, and the converter must not lie about its output. They
// seed from the same corpus as the crash targets (seedFuzz, fuzz_test.go).

// FuzzVerifyDeterministic: verifying the same bytes twice must yield the same
// verdict. A mismatch means nondeterminism (e.g. map-iteration order) leaking
// into the result.
func FuzzVerifyDeterministic(f *testing.F) {
	seedFuzz(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > fuzzInputCap {
			return
		}
		a, ea := gopdfrab.VerifyBytes(data, gopdfrab.PDFA1B)
		b, eb := gopdfrab.VerifyBytes(data, gopdfrab.PDFA1B)
		if (ea == nil) != (eb == nil) || a.Valid != b.Valid || a.Count() != b.Count() {
			t.Fatalf("VerifyBytes nondeterministic: (%v,%d,err=%v) vs (%v,%d,err=%v)",
				a.Valid, a.Count(), ea, b.Valid, b.Count(), eb)
		}
	})
}

// FuzzConvertDeterministic: converting the same bytes twice must produce
// byte-identical output. Catches nondeterminism in object numbering/writing.
func FuzzConvertDeterministic(f *testing.F) {
	seedFuzz(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > fuzzInputCap {
			return
		}
		a, ea := gopdfrab.ConvertBytes(data, gopdfrab.PDFA1B)
		b, eb := gopdfrab.ConvertBytes(data, gopdfrab.PDFA1B)
		if (ea == nil) != (eb == nil) {
			t.Fatalf("ConvertBytes error nondeterminism: %v vs %v", ea, eb)
		}
		if ea == nil && !bytes.Equal(a.Output, b.Output) {
			t.Fatalf("ConvertBytes output nondeterministic (%d vs %d bytes)", len(a.Output), len(b.Output))
		}
	})
}

// FuzzConvertHonest: when Convert reports its output is valid PDF/A, an
// independent re-verification of that output must agree. If it doesn't, the
// converter claimed success while emitting a non-conformant PDF.
func FuzzConvertHonest(f *testing.F) {
	seedFuzz(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > fuzzInputCap {
			return
		}
		res, err := gopdfrab.ConvertBytes(data, gopdfrab.PDFA1B)
		if err != nil || !res.Result.Valid || len(res.Output) == 0 {
			return
		}
		v, err := gopdfrab.VerifyBytes(res.Output, gopdfrab.PDFA1B)
		if err != nil || !v.Valid {
			t.Fatalf("Convert reported Valid but independent verify disagreed: err=%v valid=%v", err, v.Valid)
		}
	})
}

// FuzzConvertConverges: re-converting output that Convert already declared valid
// must stay valid -- Convert is a fixed point on conformant input.
func FuzzConvertConverges(f *testing.F) {
	seedFuzz(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > fuzzInputCap {
			return
		}
		r1, err := gopdfrab.ConvertBytes(data, gopdfrab.PDFA1B)
		if err != nil || !r1.Result.Valid || len(r1.Output) == 0 {
			return
		}
		r2, err := gopdfrab.ConvertBytes(r1.Output, gopdfrab.PDFA1B)
		if err != nil || !r2.Result.Valid {
			t.Fatalf("re-converting already-valid output regressed: err=%v valid=%v", err, r2.Result.Valid)
		}
	})
}
