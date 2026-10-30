package convert

import (
	"errors"
	"slices"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfa"
)

// fakeFixer is a minimal Fixer stub for exercising the overlap guard
// without depending on any real fixer's behavior.
type fakeFixer struct{ check pdf.Check }

func (f fakeFixer) Applies(c pdf.Check) bool { return c == f.check }

// TestFixersForPanicsOnOverlap verifies that fixersFor panics when two fixers
// claim the same check, instead of letting one silently displace the other.
// Every real Fixer's own tests rely on this invariant; see, for example,
// TestLZWStreamFixerAppliesOnlyToStreamLZWFilter.
func TestFixersForPanicsOnOverlap(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("fixersFor with two fixers on one check did not panic")
		}
	}()
	rs := standardRepairs
	rs.fixers = append(slices.Clone(rs.fixers), func(*fixerRun) Fixer {
		return fakeFixer{check: pdf.Checks.Structure.StreamLZWFilter}
	})
	rs.fixersFor(&fixerRun{})
}

// TestStandardFixersCanRepair verifies that every standard fixer has a way to
// repair: dispatchFixers silently skips one that has none.
func TestStandardFixersCanRepair(t *testing.T) {
	for _, build := range standardRepairs.fixers {
		switch f := build(&fixerRun{}); f.(type) {
		case graphFixer, targetedFixer:
		default:
			t.Errorf("%T is neither a graphFixer nor a targetedFixer", f)
		}
	}
}

// TestApplyPreemptivePropagatesError verifies that an error from a preemptive
// fixup is returned by applyPreemptive, and that a later fixup does not run
// once an earlier one has failed.
func TestApplyPreemptivePropagatesError(t *testing.T) {
	wantErr := errors.New("boom")
	ranSecond := false
	rs := repairs{preFixups: []preemptiveFixup{
		func(*pdf.PDFDict, *pdf.Reader, pdfa.Spec) error { return wantErr },
		func(*pdf.PDFDict, *pdf.Reader, pdfa.Spec) error { ranSecond = true; return nil },
	}}

	trailer := pdf.NewPDFDict()
	if err := rs.applyPreemptive(&trailer, nil, a1b()); err != wantErr {
		t.Errorf("applyPreemptive error = %v, want %v", err, wantErr)
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

// untargetableFixer is a targeted-only Fixer that never manages to target.
type untargetableFixer struct{ fakeFixer }

func (untargetableFixer) fixTargeted(*fixPass, []pdf.PDFError) (bool, bool, error) {
	return false, false, nil
}

// TestDispatchSkipsAnUntargetableTargetedOnlyFixer verifies that a fixer with
// no whole-graph Fix to fall back on changes nothing when it cannot target
// its issues.
func TestDispatchSkipsAnUntargetableTargetedOnlyFixer(t *testing.T) {
	c := pdf.Checks.Structure.StreamLZWFilter
	f := untargetableFixer{fakeFixer{check: c}}
	trailer := pdf.NewPDFDict()
	result := pdf.Result{Issues: []pdf.PDFError{pdf.NewError(c, nil, 0, nil)}}
	changed, err := dispatchFixers(&fixPass{trailer: &trailer}, result, map[pdf.Check]Fixer{c: f})
	if err != nil || changed {
		t.Errorf("dispatchFixers = (%v, %v), want (false, nil)", changed, err)
	}
}
