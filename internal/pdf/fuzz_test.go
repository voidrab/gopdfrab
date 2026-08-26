package pdf_test

import (
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfgen"
)

// fuzzInputCap bounds the input size the fuzz targets accept, so a fuzzer does
// not flag legitimately-slow work on pathologically large inputs as a failure.
const fuzzInputCap = 1 << 20 // 1 MiB

// seedFuzz feeds a fuzz target the valid baselines plus a deterministic batch
// of generated broken PDFs, so a plain `go test` (no -fuzz) replays them all as
// the seed corpus -- no external or committed corpus files required.
func seedFuzz(f *testing.F) {
	f.Helper()
	for _, s := range pdfgen.Seeds() {
		f.Add(s)
	}
	for _, b := range pdfgen.GenerateN(0, 128) {
		f.Add(b)
	}
}

// FuzzOpenBytes drives the full structural parse and object-graph resolution.
// The parser has no panic recovery of its own, so the invariant is simply that
// no input -- however malformed -- makes it panic; returned errors are fine.
func FuzzOpenBytes(f *testing.F) {
	seedFuzz(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > fuzzInputCap {
			return
		}
		r, err := pdf.OpenBytes(data)
		if err != nil {
			return
		}
		defer r.Close()
		// Exercise the resolution / accessor paths; ignore their errors.
		r.ResolveGraph()
		r.PageCount()
		r.Version()
		r.Metadata()
		r.XMPMetadata()
		r.ClaimedConformance()
	})
}

// FuzzLexer drives just the token scanner over arbitrary bytes, isolating
// lexing crashes from the higher-level structure parser.
func FuzzLexer(f *testing.F) {
	seedFuzz(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > fuzzInputCap {
			return
		}
		l := pdf.NewLexerBytes(data, 0)
		for i := 0; i < 4*fuzzInputCap; i++ {
			tok := l.NextToken()
			if tok.Type == pdf.TokenEOF || tok.Type == pdf.TokenError {
				return
			}
		}
	})
}
