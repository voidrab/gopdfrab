package pdf

import (
	"errors"
	"strings"
	"testing"
)

// TestPDFErrorString covers PDFError.String/Error across the clause, page,
// document-level, object-ref, and message-list branches.
func TestPDFErrorString(t *testing.T) {
	c := Check{clause: "6.3.4", subclause: 2}
	ref := PDFRef{ObjNum: 7}
	e := NewError(c, []error{errors.New("first"), errors.New("second")}, 3, &ref)
	s := e.String()
	for _, want := range []string{"6.3.4", "/2", "page 3", "ref", "first", "second"} {
		if !strings.Contains(s, want) {
			t.Errorf("String()=%q missing %q", s, want)
		}
	}
	if e.Error() != s {
		t.Error("Error() should equal String()")
	}

	// Document-level, no clause, no ref, no messages.
	plain := NewError(Check{}, nil, 0, nil)
	ps := plain.String()
	if !strings.Contains(ps, "document-level") {
		t.Errorf("document-level String()=%q", ps)
	}
	if strings.Contains(ps, "page") || strings.Contains(ps, "ref") {
		t.Errorf("plain String() should have no page/ref: %q", ps)
	}
}

// TestEqualPDFValue exercises every type branch of EqualPDFValue for both the
// equal and unequal (value- and type-mismatch) cases.
func TestEqualPDFValue(t *testing.T) {
	equal := []struct{ a, b PDFValue }{
		{PDFHexString{Value: "AB"}, PDFHexString{Value: "AB"}},
		{PDFString{Value: "x"}, PDFString{Value: "x"}},
		{PDFInteger(1), PDFInteger(1)},
		{PDFReal(1.5), PDFReal(1.5)},
		{PDFBoolean(true), PDFBoolean(true)},
		{PDFName{Value: "N"}, PDFName{Value: "N"}},
		{PDFRef{ObjNum: 1, GenNum: 2}, PDFRef{ObjNum: 1, GenNum: 2}},
		{PDFArray{PDFInteger(1)}, PDFArray{PDFInteger(1)}},
		{
			PDFDict{Entries: map[string]PDFValue{"K": PDFInteger(1)}},
			PDFDict{Entries: map[string]PDFValue{"K": PDFInteger(1)}},
		},
		{nil, nil},
	}
	for i, c := range equal {
		if !EqualPDFValue(c.a, c.b) {
			t.Errorf("equal[%d]: EqualPDFValue(%v,%v)=false, want true", i, c.a, c.b)
		}
	}

	unequal := []struct{ a, b PDFValue }{
		{PDFHexString{Value: "AB"}, PDFHexString{Value: "CD"}},
		{PDFString{Value: "x"}, PDFString{Value: "y"}},
		{PDFInteger(1), PDFInteger(2)},
		{PDFReal(1.5), PDFReal(2.5)},
		{PDFBoolean(true), PDFBoolean(false)},
		{PDFName{Value: "N"}, PDFName{Value: "M"}},
		{PDFRef{ObjNum: 1}, PDFRef{ObjNum: 2}},
		{PDFArray{PDFInteger(1)}, PDFArray{PDFInteger(1), PDFInteger(2)}},
		{PDFArray{PDFInteger(1)}, PDFArray{PDFInteger(2)}},
		{
			PDFDict{Entries: map[string]PDFValue{"K": PDFInteger(1)}},
			PDFDict{Entries: map[string]PDFValue{"K": PDFInteger(2)}},
		},
		{
			PDFDict{Entries: map[string]PDFValue{"K": PDFInteger(1)}},
			PDFDict{Entries: map[string]PDFValue{"J": PDFInteger(1)}},
		},
		{
			PDFDict{Entries: map[string]PDFValue{"K": PDFInteger(1)}, HasStream: true},
			PDFDict{Entries: map[string]PDFValue{"K": PDFInteger(1)}},
		},
		{PDFInteger(1), PDFName{Value: "N"}}, // type mismatch
		{PDFInteger(1), nil},                 // one nil
		{1, 1},                               // default branch: not a recognized PDFValue variant
	}
	for i, c := range unequal {
		if EqualPDFValue(c.a, c.b) {
			t.Errorf("unequal[%d]: EqualPDFValue(%v,%v)=true, want false", i, c.a, c.b)
		}
	}
}

// TestPDFErrorAccessors covers Page, IsDocumentLevel, ObjectRef, and Messages
// for both a document-level error with no ref and a page-level error with one.
func TestPDFErrorAccessors(t *testing.T) {
	plain := NewError(Check{}, []error{errors.New("a"), errors.New("b")}, 0, nil)
	if plain.Page() != 0 || !plain.IsDocumentLevel() {
		t.Errorf("plain: Page()=%d IsDocumentLevel()=%v, want 0, true", plain.Page(), plain.IsDocumentLevel())
	}
	if _, ok := plain.ObjectRef(); ok {
		t.Error("plain: ObjectRef() ok should be false")
	}
	if msgs := plain.Messages(); len(msgs) != 2 || msgs[0] != "a" || msgs[1] != "b" {
		t.Errorf("plain: Messages() = %v, want [a b]", msgs)
	}

	ref := PDFRef{ObjNum: 5, GenNum: 1}
	withRef := NewError(Check{}, nil, 3, &ref)
	if withRef.Page() != 3 || withRef.IsDocumentLevel() {
		t.Errorf("withRef: Page()=%d IsDocumentLevel()=%v, want 3, false", withRef.Page(), withRef.IsDocumentLevel())
	}
	if got, ok := withRef.ObjectRef(); !ok || got != ref {
		t.Errorf("withRef: ObjectRef() = %v, %v; want %v, true", got, ok, ref)
	}
}
