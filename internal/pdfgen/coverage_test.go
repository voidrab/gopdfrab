package pdfgen_test

import (
	"bytes"
	"math/rand"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfgen"
)

// TestCorruptorsCoverBothPaths runs every corruptor against a structurally
// valid classic-xref PDF (its target-token "found" path) and against an input
// that lacks every target token (its generic bit-flip fallback), so each
// corruptor is exercised on both branches. It also asserts each always returns
// a non-nil slice and never mutates its input.
func TestCorruptorsCoverBothPaths(t *testing.T) {
	valid := pdfgen.Seeds()[0] // minimalClassic: has xref, startxref, streams
	noTokens := []byte("plain text with no pdf tokens at all")
	// startxref keyword present but no offset digits after it, exercising the
	// "no digits" fallbacks in the startxref corruptors.
	startxrefNoDigits := []byte("xref\ntrailer\n<< >>\nstartxref\n%%EOF")
	// A stream with no endstream keyword, exercising corruptStreamPayload's
	// missing-endstream fallback.
	streamNoEnd := []byte("1 0 obj\n<< /Length 3 >>\nstream\nabc")
	// A CRLF-framed stream, exercising corruptStreamPayload's \r handling.
	streamCRLF := []byte("1 0 obj\n<< /Length 5 >>\nstream\r\nabcde\r\nendstream\nendobj")
	// A startxref offset too long to fit an int, exercising startxrefOffBy's
	// integer-overflow (ParseInt error) fallback.
	startxrefOverflow := []byte("startxref\n999999999999999999999999999999\n%%EOF")

	for _, c := range pdfgen.Corruptors() {
		inputs := [][]byte{valid, noTokens, startxrefNoDigits, streamNoEnd, streamCRLF, startxrefOverflow, {0x01}, {}}
		for _, in := range inputs {
			orig := append([]byte(nil), in...)
			rng := rand.New(rand.NewSource(1))
			out := c.Apply(in, rng)
			if out == nil {
				t.Errorf("corruptor %q returned nil", c.Name)
			}
			if !bytes.Equal(in, orig) {
				t.Errorf("corruptor %q mutated its input", c.Name)
			}
		}
	}
}

// TestBuilderAPI exercises every Builder method, including the raw
// Write/WriteString/Bytes helpers and both finishers.
func TestBuilderAPI(t *testing.T) {
	b := pdfgen.NewBuilder("%PDF-1.7\n")
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.Obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 10 10] >>")
	if b.OffsetOf(2) <= b.OffsetOf(1) {
		t.Error("OffsetOf not monotonic")
	}
	b.WriteString("% a comment\n")
	b.Write([]byte("% more\n"))
	if b.Len() != int64(len(b.Bytes())) {
		t.Errorf("Len %d != len(Bytes) %d", b.Len(), len(b.Bytes()))
	}
	doc := b.FinishClassic("<< /Size 4 /Root 1 0 R >>")
	if _, err := pdf.OpenBytes(doc); err != nil {
		t.Errorf("FinishClassic output does not parse: %v", err)
	}

	// FinishStartxref path (used by the xref-stream builders).
	b2 := pdfgen.NewBuilder("%PDF-1.7\n")
	b2.StreamObj(1, "<< /Type /XRef", []byte("raw"))
	if got := b2.FinishStartxref(b2.OffsetOf(1)); len(got) == 0 {
		t.Error("FinishStartxref returned empty")
	}
}

// TestGenerateNAndGrammar covers GenerateN and the grammar generator (package
// and method forms), asserting determinism and non-empty output.
func TestGenerateNAndGrammar(t *testing.T) {
	batch := pdfgen.GenerateN(0, 16)
	if len(batch) != 16 {
		t.Fatalf("GenerateN returned %d, want 16", len(batch))
	}
	for i, b := range batch {
		if !bytes.Equal(b, pdfgen.Generate(int64(i))) {
			t.Errorf("GenerateN[%d] != Generate(%d)", i, i)
		}
	}

	g := pdfgen.New()
	if len(g.GenerateN(5, 3)) != 3 {
		t.Error("method GenerateN wrong length")
	}

	for seed := int64(0); seed < 32; seed++ {
		a := pdfgen.GenerateGrammar(seed)
		b := pdfgen.GenerateGrammar(seed)
		if !bytes.Equal(a, b) {
			t.Fatalf("GenerateGrammar(%d) not deterministic", seed)
		}
		if len(a) == 0 {
			t.Fatalf("GenerateGrammar(%d) empty", seed)
		}
	}
}

// TestGrammarSeedsParse checks that the un-corrupted grammar output is itself
// well-formed enough to open (the grammar builds a valid Catalog/Pages
// skeleton), so the generative path starts from parseable documents.
func TestGrammarSeedsParse(t *testing.T) {
	parsed := 0
	for seed := int64(0); seed < 64; seed++ {
		if r, err := pdf.OpenBytes(pdfgen.GenerateGrammar(seed)); err == nil {
			parsed++
			r.Close()
		}
	}
	if parsed == 0 {
		t.Error("no grammar-generated document parsed; skeleton may be malformed")
	}
}

// TestGenerateGarbageAndCorruptPaths drives Generate across a seed range wide
// enough to hit its pure-garbage, grammar, and multi-corruptor branches.
func TestGenerateGarbageAndCorruptPaths(t *testing.T) {
	for seed := int64(0); seed < 300; seed++ {
		if pdfgen.Generate(seed) == nil {
			t.Fatalf("Generate(%d) returned nil", seed)
		}
	}
}
