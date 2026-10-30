package convert

import (
	"errors"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfa"
)

// fakeFixer is a minimal Fixer stub for exercising registerFixer's overlap
// guard without depending on any real fixer's behavior.
type fakeFixer struct{ check pdf.Check }

func (f fakeFixer) Applies(c pdf.Check) bool { return c == f.check }
func (fakeFixer) Fix(*pdf.PDFDict, []pdf.PDFError) (bool, error) {
	return false, nil
}

// TestRegisterFixerPanicsOnOverlap confirms registerFixer panics rather than
// silently overwriting an already-claimed Check -- every real Fixer's own
// tests rely on this invariant (see e.g.
// TestLZWStreamFixerAppliesOnlyToStreamLZWFilter).
func TestRegisterFixerPanicsOnOverlap(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("registerFixer on an already-claimed Check did not panic")
		}
	}()
	registerFixer(fakeFixer{check: pdf.Checks.Structure.StreamLZWFilter})
}

// TestApplyPreemptiveFixupsPropagatesError confirms a failing preemptive
// fixup's error surfaces from applyPreemptiveFixups, and that a later
// registered fixup never runs once an earlier one has failed.
func TestApplyPreemptiveFixupsPropagatesError(t *testing.T) {
	orig := preemptiveFixups
	defer func() { preemptiveFixups = orig }()

	wantErr := errors.New("boom")
	ranSecond := false
	preemptiveFixups = []func(*pdf.PDFDict, *pdf.Reader, pdfa.Spec) error{
		func(*pdf.PDFDict, *pdf.Reader, pdfa.Spec) error { return wantErr },
		func(*pdf.PDFDict, *pdf.Reader, pdfa.Spec) error { ranSecond = true; return nil },
	}

	trailer := pdf.NewPDFDict()
	if err := applyPreemptiveFixups(&trailer, nil, a1b()); err != wantErr {
		t.Errorf("applyPreemptiveFixups error = %v, want %v", err, wantErr)
	}
	if ranSecond {
		t.Error("second fixup ran despite the first returning an error")
	}
}

// TestStreamUndecodableFixerLeavesRealContentAlone records the limit of the
// remediation: only a stream whose own bytes say what it should have been is
// repaired. Bytes nobody can decode cannot be re-encoded into anything
// truthful, so that stream stays a residual rather than being silently
// emptied.
func TestStreamUndecodableFixerLeavesRealContentAlone(t *testing.T) {
	broken := pdf.NewPDFDict()
	broken.SetStream([]byte("not deflate data at all"))
	broken.Entries.Set("Filter", pdf.PDFName{Value: "FlateDecode"})

	trailer := pdf.NewPDFDict()
	trailer.Entries.Set("Junk", broken)

	changed, err := (undecodableStreamFixer{}).Fix(&trailer, nil)
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if changed {
		t.Error("Fix reported a change for a stream it cannot honestly repair")
	}
	if broken.Entries.Get("Filter") == nil {
		t.Error("the filter was dropped, which would present undecodable bytes as content")
	}
}

func TestRunDictVisitorDeclinedPrepare(t *testing.T) {
	trailer := pdf.NewPDFDict()
	changed, err := runDictVisitor(&trailer, nil, func(*pdf.PDFDict, []pdf.PDFError, *bool) (func(pdf.PDFDict), bool) {
		return func(pdf.PDFDict) { t.Error("visitor ran although prepare declined") }, false
	})
	if changed || err != nil {
		t.Errorf("runDictVisitor = %v, %v; want false, nil", changed, err)
	}
}
