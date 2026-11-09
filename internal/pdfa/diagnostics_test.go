package pdfa

import (
	"errors"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// TestIssueMapsEveryKind pins the check each reader diagnostic raises under
// PDF/A-1b, and that the issue carries the diagnostic's object and messages.
func TestIssueMapsEveryKind(t *testing.T) {
	s := mustFor(t, pdf.A1B)
	for kind, want := range map[pdf.DiagKind]pdf.Check{
		pdf.DiagObjectFraming:           pdf.Checks.Structure.ObjectFraming,
		pdf.DiagStreamKeywordEOL:        pdf.Checks.Structure.StreamKeywordEOL,
		pdf.DiagStreamLengthIncludesEOL: pdf.Checks.Structure.StreamLengthIncludesEOL,
		pdf.DiagStreamLengthMismatch:    pdf.Checks.Structure.StreamLengthMismatch,
		pdf.DiagEndstreamEOL:            pdf.Checks.Structure.EndstreamEOL,
		pdf.DiagUsedXRefStream:          pdf.Checks.Structure.XRefStream,
		pdf.DiagXRefRebuilt:             pdf.Checks.Structure.XRefKeyword,
		pdf.DiagObjectDegraded:          pdf.Checks.Structure.GraphResolutionFailure,
		pdf.DiagObjectRecovered:         pdf.Checks.Structure.GraphResolutionFailure,
		pdf.DiagStringUndecryptable:     pdf.Checks.Structure.GraphResolutionFailure,
	} {
		ref := &pdf.PDFRef{ObjNum: 7}
		e, ok := s.Issue(pdf.Diagnostic{Kind: kind, Ref: ref, Errs: []error{errors.New("why")}})
		if !ok {
			t.Errorf("kind %d raised no issue, want %s", kind, want.Name())
			continue
		}
		if e.Check() != want {
			t.Errorf("kind %d raised %s, want %s", kind, e.Check().Name(), want.Name())
		}
		if got, ok := e.ObjectRef(); !ok || got.ObjNum != 7 || e.Page() != 0 {
			t.Errorf("kind %d: object %v (%v), page %d; want object 7 on no page", kind, got, ok, e.Page())
		}
		if msgs := e.Messages(); len(msgs) != 1 || msgs[0] != "why" {
			t.Errorf("kind %d: messages %q", kind, msgs)
		}
	}
}

// TestIssueAboutTheFile verifies that a diagnostic about no object is reported
// on page 1.
func TestIssueAboutTheFile(t *testing.T) {
	e, ok := mustFor(t, pdf.A1B).Issue(pdf.Diagnostic{Kind: pdf.DiagXRefRebuilt})
	if !ok || e.Page() != 1 {
		t.Errorf("issue %v (%v) on page %d, want page 1", e, ok, e.Page())
	}
	if _, isObj := e.ObjectRef(); isObj {
		t.Error("a file-level issue names an object")
	}
}

// TestIssueFollowsTheLevel verifies that whether a diagnostic is a violation is
// the level's call. A level that permits xref streams raises nothing for one,
// and plain PDF has none of the structure rules.
func TestIssueFollowsTheLevel(t *testing.T) {
	s := mustFor(t, pdf.A1B)
	s.XRefStreams = true
	if e, ok := s.Issue(pdf.Diagnostic{Kind: pdf.DiagUsedXRefStream}); ok {
		t.Errorf("a level permitting xref streams raised %v", e)
	}
	if _, ok := s.Issue(pdf.Diagnostic{Kind: pdf.DiagXRefRebuilt}); !ok {
		t.Error("permitting xref streams also excused a rebuilt xref table")
	}

	s = mustFor(t, pdf.A1B)
	s.ExactXRefOffsets = false
	if e, ok := s.Issue(pdf.Diagnostic{Kind: pdf.DiagXRefRebuilt}); ok {
		t.Errorf("a level not requiring exact xref offsets raised %v", e)
	}

	om := mustFor(t, pdf.ObjectModel)
	for kind := pdf.DiagObjectFraming; kind <= pdf.DiagStringUndecryptable; kind++ {
		if e, ok := om.Issue(pdf.Diagnostic{Kind: kind}); ok {
			t.Errorf("plain PDF raised %v for kind %d", e, kind)
		}
	}
}

func TestIssueUnknownKind(t *testing.T) {
	s := mustFor(t, pdf.A1B)
	for _, kind := range []pdf.DiagKind{0, pdf.DiagStringUndecryptable + 1} {
		if e, ok := s.Issue(pdf.Diagnostic{Kind: kind}); ok {
			t.Errorf("kind %d raised %v", kind, e)
		}
	}
}
