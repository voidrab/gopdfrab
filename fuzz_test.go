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

// fuzzProfiles are the profiles a fuzz input's selector byte picks from. A
// profile for a new conformance level is added here, so every target that
// takes a selector covers it.
var fuzzProfiles = []*gopdfrab.Profile{gopdfrab.PDFA1B, gopdfrab.Legacy1B}

// fuzzProfile returns the profile a selector byte picks.
func fuzzProfile(sel byte) *gopdfrab.Profile {
	return fuzzProfiles[int(sel)%len(fuzzProfiles)]
}

// seedFuzz seeds a target that takes a profile selector and the input bytes,
// pairing every seed with every profile.
func seedFuzz(f *testing.F) {
	f.Helper()
	seeds := append(pdfgen.Seeds(), pdfgen.GenerateN(0, 128)...)
	for i := range fuzzProfiles {
		for _, s := range seeds {
			f.Add(byte(i), s)
		}
	}
}

// FuzzVerifyBytes drives the full verification pipeline against the selected
// PDF/A profile and the generic object-model profile. The invariant is that no
// input panics; a returned error or a Result reporting violations is expected.
func FuzzVerifyBytes(f *testing.F) {
	seedFuzz(f)
	f.Fuzz(func(t *testing.T, sel byte, data []byte) {
		p := fuzzProfile(sel)
		if len(data) > fuzzInputCap {
			return
		}
		gopdfrab.VerifyBytes(data, p)
		gopdfrab.VerifyObjectModelBytes(data)
	})
}

// FuzzGeneratedSeed lets the native fuzzer explore the generator's seed space
// under coverage guidance: it mutates the int64 seed, turns it into a broken
// PDF via pdfgen.Generate, and runs the whole pipeline. Panics propagate so the
// fuzzer flags them (do not recover here).
func FuzzGeneratedSeed(f *testing.F) {
	for i := int64(0); i < 128; i++ {
		f.Add(byte(i), i)
	}
	f.Fuzz(func(t *testing.T, sel byte, seed int64) {
		p := fuzzProfile(sel)
		data := pdfgen.Generate(seed)
		if len(data) > fuzzInputCap {
			return
		}
		gopdfrab.VerifyBytes(data, p)
		gopdfrab.VerifyObjectModelBytes(data)
		gopdfrab.ConvertBytes(data, p)
	})
}

// FuzzConvertBytes drives the conversion pipeline (fixups, writer, raster
// fallback) -- the deepest code path -- asserting only that no input makes it
// panic.
func FuzzConvertBytes(f *testing.F) {
	seedFuzz(f)
	f.Fuzz(func(t *testing.T, sel byte, data []byte) {
		p := fuzzProfile(sel)
		if len(data) > fuzzInputCap {
			return
		}
		gopdfrab.ConvertBytes(data, p)
	})
}

// FuzzConvertRoundTrip is the holistic invariant: whenever the converter emits
// output bytes, re-parsing those bytes with the library's own parser must not
// panic. In other words, Convert must never produce a PDF that crashes
// gopdfrab when read back.
func FuzzConvertRoundTrip(f *testing.F) {
	seedFuzz(f)
	f.Fuzz(func(t *testing.T, sel byte, data []byte) {
		p := fuzzProfile(sel)
		if len(data) > fuzzInputCap {
			return
		}
		res, err := gopdfrab.ConvertBytes(data, p)
		if err != nil {
			return
		}
		defer res.Close()
		out, err := res.Output()
		if err != nil || len(out) == 0 {
			return
		}
		r, err := pdf.OpenBytes(out)
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
	f.Fuzz(func(t *testing.T, sel byte, data []byte) {
		p := fuzzProfile(sel)
		if len(data) > fuzzInputCap {
			return
		}
		a, ea := gopdfrab.VerifyBytes(data, p)
		b, eb := gopdfrab.VerifyBytes(data, p)
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
	f.Fuzz(func(t *testing.T, sel byte, data []byte) {
		p := fuzzProfile(sel)
		if len(data) > fuzzInputCap {
			return
		}
		a, ea := gopdfrab.ConvertBytes(data, p)
		b, eb := gopdfrab.ConvertBytes(data, p)
		defer a.Close()
		defer b.Close()
		if (ea == nil) != (eb == nil) {
			t.Fatalf("ConvertBytes error nondeterminism: %v vs %v", ea, eb)
		}
		if ea != nil {
			return
		}
		ao, aerr := a.Output()
		bo, berr := b.Output()
		if aerr != nil || berr != nil {
			t.Fatalf("Output() failed: %v / %v", aerr, berr)
		}
		if !bytes.Equal(ao, bo) {
			t.Fatalf("ConvertBytes output nondeterministic (%d vs %d bytes)", len(ao), len(bo))
		}
	})
}

// FuzzConvertHonest: when Convert reports its output is valid PDF/A, an
// independent re-verification of that output must agree. If it doesn't, the
// converter claimed success while emitting a non-conformant PDF.
func FuzzConvertHonest(f *testing.F) {
	seedFuzz(f)
	f.Fuzz(func(t *testing.T, sel byte, data []byte) {
		checkConvertHonest(t, fuzzProfile(sel), data)
	})
}

func checkConvertHonest(t *testing.T, p *gopdfrab.Profile, data []byte) {
	if len(data) > fuzzInputCap {
		return
	}
	res, err := gopdfrab.ConvertBytes(data, p)
	if err != nil || !res.Result.Valid {
		return
	}
	defer res.Close()
	out, err := res.Output()
	if err != nil || len(out) == 0 {
		return
	}
	v, err := gopdfrab.VerifyBytes(out, p)
	if err != nil || !v.Valid {
		t.Fatalf("Convert reported Valid but independent verify disagreed: err=%v valid=%v", err, v.Valid)
	}
}

// FuzzConvertConverges: re-converting output that Convert already declared valid
// must stay valid -- Convert is a fixed point on conformant input.
func FuzzConvertConverges(f *testing.F) {
	seedFuzz(f)
	f.Fuzz(func(t *testing.T, sel byte, data []byte) {
		checkConvertConverges(t, fuzzProfile(sel), data)
	})
}

func checkConvertConverges(t *testing.T, p *gopdfrab.Profile, data []byte) {
	if len(data) > fuzzInputCap {
		return
	}
	r1, err := gopdfrab.ConvertBytes(data, p)
	if err != nil || !r1.Result.Valid {
		return
	}
	defer r1.Close()
	out1, err := r1.Output()
	if err != nil || len(out1) == 0 {
		return
	}
	r2, err := gopdfrab.ConvertBytes(out1, p)
	if err == nil {
		defer r2.Close()
	}
	if err != nil || !r2.Result.Valid {
		t.Fatalf("re-converting already-valid output regressed: err=%v valid=%v", err, r2.Result.Valid)
	}
}
