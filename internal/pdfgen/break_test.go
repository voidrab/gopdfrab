package pdfgen_test

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdfgen"
)

// TestBreakXrefOffset checks that BreakXrefOffset rewrites exactly the
// requested object's 10-digit offset and returns an unchanged copy when the
// document has no standard free entry or the object lies past the table.
func TestBreakXrefOffset(t *testing.T) {
	in := pdfgen.PlainThreeIssue()
	orig := bytes.Clone(in)

	out := pdfgen.BreakXrefOffset(in, 1, 42)
	if !bytes.Equal(in, orig) {
		t.Fatal("BreakXrefOffset mutated its input")
	}
	free := bytes.Index(in, []byte("0000000000 65535 f"))
	start := free + 20
	if got := string(out[start : start+10]); got != fmt.Sprintf("%010d", 42) {
		t.Errorf("object 1 offset = %q, want 0000000042", got)
	}
	if !bytes.Equal(out[:start], in[:start]) || !bytes.Equal(out[start+10:], in[start+10:]) {
		t.Error("BreakXrefOffset changed bytes outside the target offset")
	}

	for name, tc := range map[string]struct {
		in     []byte
		objNum int
	}{
		"no free entry":       {[]byte("%PDF-1.7\nno xref table here"), 1},
		"free entry too near": {[]byte("0 65535 f"), 1},
		"object past table":   {in, 1 << 20},
	} {
		if got := pdfgen.BreakXrefOffset(tc.in, tc.objNum, 42); !bytes.Equal(got, tc.in) {
			t.Errorf("%s: BreakXrefOffset changed the document", name)
		}
	}
}

// TestBreakStartxref checks that BreakStartxref overwrites the digits after the
// last startxref keyword in place and leaves documents it cannot target
// unchanged.
func TestBreakStartxref(t *testing.T) {
	in := []byte("startxref\n7\n%%EOF\nstartxref\r\n 123456789012\n%%EOF")
	orig := bytes.Clone(in)
	want := []byte("startxref\n7\n%%EOF\nstartxref\r\n 999999999999\n%%EOF")
	if got := pdfgen.BreakStartxref(in); !bytes.Equal(got, want) {
		t.Errorf("BreakStartxref = %q, want %q", got, want)
	}
	if !bytes.Equal(in, orig) {
		t.Fatal("BreakStartxref mutated its input")
	}

	for name, in := range map[string][]byte{
		"no keyword": []byte("%PDF-1.7\n%%EOF"),
		"no digits":  []byte("startxref\n%%EOF"),
		"at end":     []byte("startxref"),
	} {
		if got := pdfgen.BreakStartxref(in); !bytes.Equal(got, in) {
			t.Errorf("%s: BreakStartxref changed the document to %q", name, got)
		}
	}
}
