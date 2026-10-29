package writer

import (
	"bytes"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// TestWriteContentStreamRoundTrips scans a hand-built content stream into
// (op, operands) records, serializes them back via writeContentStream, and
// re-scans the result, asserting that the two scans agree. writeContentStream
// must be the exact inverse of newContentScanner's scan for every operand
// type a synthesized appearance stream uses (names, strings, numbers,
// arrays, inline dicts).
func TestWriteContentStreamRoundTrips(t *testing.T) {
	src := []byte("q 0 0 100 50 re W n BT /F0 12 Tf 0 g 1 0 0 1 2 3 Tm (Hello World) Tj ET Q\n")

	var want []ContentOp
	pdf.NewContentScanner(src).Scan(func(op string, operands []pdf.PDFValue) {
		want = append(want, ContentOp{Op: op, Operands: append([]pdf.PDFValue{}, operands...)})
	})
	if len(want) == 0 {
		t.Fatalf("scanning the source content stream produced no operators")
	}

	out, err := WriteContentStream(want)
	if err != nil {
		t.Fatalf("writeContentStream: %v", err)
	}

	var got []ContentOp
	pdf.NewContentScanner(out).Scan(func(op string, operands []pdf.PDFValue) {
		got = append(got, ContentOp{Op: op, Operands: append([]pdf.PDFValue{}, operands...)})
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
			if !pdf.EqualPDFValue(got[i].Operands[j], want[i].Operands[j]) {
				t.Errorf("op[%d] (%s) operand[%d] = %#v, want %#v", i, want[i].Op, j, got[i].Operands[j], want[i].Operands[j])
			}
		}
	}
}

// TestWriteContentStreamRoundTripsInlineImage confirms an inline image's
// verbatim "BI...EI" span survives scan -> writeContentStream -> rescan
// byte-for-byte, alongside the surrounding ops.
func TestWriteContentStreamRoundTripsInlineImage(t *testing.T) {
	src := []byte("q 1 0 0 1 0 0 cm BI /W 2 /H 1 /BPC 8 /CS /G /F /AHx ID ff00> EI Q\n")

	var ops []ContentOp
	pdf.NewContentScanner(src).Scan(func(op string, operands []pdf.PDFValue) {
		ops = append(ops, ContentOp{Op: op, Operands: append([]pdf.PDFValue{}, operands...)})
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

	out, err := WriteContentStream(ops)
	if err != nil {
		t.Fatalf("writeContentStream: %v", err)
	}

	var rescanned []ContentOp
	pdf.NewContentScanner(out).Scan(func(op string, operands []pdf.PDFValue) {
		rescanned = append(rescanned, ContentOp{Op: op, Operands: append([]pdf.PDFValue{}, operands...)})
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

// TestContentStreamWriterErrorIsSticky checks that after a failed WriteOp the
// error is kept: Err reports it and later WriteOps return it without writing.
func TestContentStreamWriterErrorIsSticky(t *testing.T) {
	var w ContentStreamWriter
	if err := w.WriteOp("q", nil); err != nil {
		t.Fatalf("WriteOp(q): %v", err)
	}
	if w.Err() != nil {
		t.Fatalf("Err() = %v before any failure, want nil", w.Err())
	}

	first := w.WriteOp("Tj", []pdf.PDFValue{pdf.PDFRef{ObjNum: 1}})
	if first == nil {
		t.Fatal("expected an error for an unserializable operand")
	}
	if w.Err() != first {
		t.Errorf("Err() = %v, want the first failure %v", w.Err(), first)
	}

	before := string(w.Bytes())
	if err := w.WriteOp("Q", nil); err != first {
		t.Errorf("WriteOp after failure = %v, want the sticky %v", err, first)
	}
	if string(w.Bytes()) != before {
		t.Errorf("WriteOp after failure changed output: %q -> %q", before, w.Bytes())
	}
}
