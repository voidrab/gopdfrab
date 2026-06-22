package pdfrab

import (
	"bytes"
	"testing"
)

// TestWriteContentStreamRoundTrips scans a hand-built content stream into
// (op, operands) records, serializes them back via writeContentStream, and
// re-scans the result, asserting the two scans agree -- writeContentStream
// must be the exact inverse of newContentScanner's scan for every operand
// type a synthesized appearance stream uses (names, strings, numbers,
// arrays, inline dicts).
func TestWriteContentStreamRoundTrips(t *testing.T) {
	src := []byte("q 0 0 100 50 re W n BT /F0 12 Tf 0 g 1 0 0 1 2 3 Tm (Hello World) Tj ET Q\n")

	var want []contentOp
	newContentScanner(src).scan(func(op string, operands []PDFValue) {
		want = append(want, contentOp{Op: op, Operands: append([]PDFValue{}, operands...)})
	})
	if len(want) == 0 {
		t.Fatalf("scanning the source content stream produced no operators")
	}

	out, err := writeContentStream(want)
	if err != nil {
		t.Fatalf("writeContentStream: %v", err)
	}

	var got []contentOp
	newContentScanner(out).scan(func(op string, operands []PDFValue) {
		got = append(got, contentOp{Op: op, Operands: append([]PDFValue{}, operands...)})
	})

	if len(got) != len(want) {
		t.Fatalf("re-scanned %d operators, want %d (re-scanned bytes: %q)", len(got), len(want), out)
	}
	for i := range want {
		if got[i].Op != want[i].Op {
			t.Errorf("op[%d] = %q, want %q", i, got[i].Op, want[i].Op)
		}
		if len(got[i].Operands) != len(want[i].Operands) {
			t.Fatalf("op[%d] (%s) operands = %d, want %d", i, want[i].Op, len(got[i].Operands), len(want[i].Operands))
		}
		for j := range want[i].Operands {
			if !EqualPDFValue(got[i].Operands[j], want[i].Operands[j]) {
				t.Errorf("op[%d] (%s) operand[%d] = %#v, want %#v", i, want[i].Op, j, got[i].Operands[j], want[i].Operands[j])
			}
		}
	}
}

// TestWriteContentStreamRoundTripsInlineImage confirms an inline image's
// verbatim "BI...EI" span survives scan -> writeContentStream -> rescan
// byte-for-byte, alongside the surrounding ops -- the gap Phase 11 closed
// (writeContentStream previously had no representation for inline images at
// all, since the scanner discarded their binary payload).
func TestWriteContentStreamRoundTripsInlineImage(t *testing.T) {
	src := []byte("q 1 0 0 1 0 0 cm BI /W 2 /H 1 /BPC 8 /CS /G /F /AHx ID ff00> EI Q\n")

	var ops []contentOp
	newContentScanner(src).scan(func(op string, operands []PDFValue) {
		ops = append(ops, contentOp{Op: op, Operands: append([]PDFValue{}, operands...)})
	})

	var sawInlineImage bool
	for _, op := range ops {
		if op.Op == "INLINEIMAGE" {
			sawInlineImage = true
		}
	}
	if !sawInlineImage {
		t.Fatalf("scanning the source content stream produced no INLINEIMAGE op")
	}

	out, err := writeContentStream(ops)
	if err != nil {
		t.Fatalf("writeContentStream: %v", err)
	}

	var rescanned []contentOp
	newContentScanner(out).scan(func(op string, operands []PDFValue) {
		rescanned = append(rescanned, contentOp{Op: op, Operands: append([]PDFValue{}, operands...)})
	})

	if len(rescanned) != len(ops) {
		t.Fatalf("rescanned %d ops, want %d (rewritten bytes: %q)", len(rescanned), len(ops), out)
	}
	for i := range ops {
		if ops[i].Op != rescanned[i].Op {
			t.Errorf("op[%d] = %q, want %q", i, rescanned[i].Op, ops[i].Op)
			continue
		}
		if ops[i].Op != "INLINEIMAGE" {
			continue
		}
		want, ok := inlineImageBytes(ops[i].Operands)
		if !ok {
			t.Fatalf("original INLINEIMAGE op carries no raw bytes")
		}
		got, ok := inlineImageBytes(rescanned[i].Operands)
		if !ok {
			t.Fatalf("rescanned INLINEIMAGE op carries no raw bytes")
		}
		if !bytes.Equal(got, want) {
			t.Errorf("inline image bytes = %q, want %q", got, want)
		}
	}
}
