package gopdfrab_test

import (
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
		gopdfrab.VerifyBytes(data, gopdfrab.PDFA_1B)
		gopdfrab.VerifyObjectModelBytes(data)
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
		gopdfrab.ConvertBytes(data, gopdfrab.PDFA_1B)
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
		res, err := gopdfrab.ConvertBytes(data, gopdfrab.PDFA_1B)
		if err != nil || len(res.Output) == 0 {
			return
		}
		r, err := pdf.OpenBytes(res.Output)
		if err != nil {
			return
		}
		defer r.Close()
		r.ResolveGraph()
		r.GetPageCount()
	})
}
