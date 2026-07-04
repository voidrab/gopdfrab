package convert

import (
	"errors"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
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
	preemptiveFixups = []func(*pdf.PDFDict, *pdf.Reader) error{
		func(*pdf.PDFDict, *pdf.Reader) error { return wantErr },
		func(*pdf.PDFDict, *pdf.Reader) error { ranSecond = true; return nil },
	}

	trailer := pdf.NewPDFDict()
	if err := applyPreemptiveFixups(&trailer, nil); err != wantErr {
		t.Errorf("applyPreemptiveFixups error = %v, want %v", err, wantErr)
	}
	if ranSecond {
		t.Error("second fixup ran despite the first returning an error")
	}
}
