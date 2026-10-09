package pdf

import (
	"errors"
	"strings"
	"sync/atomic"
	"testing"
)

// TestParallelForRunsEveryIndex pins the pool's basic contract, including the
// degenerate sizes the hand-rolled pools it replaced special-cased.
func TestParallelForRunsEveryIndex(t *testing.T) {
	for _, tc := range []struct{ n, workers int }{{0, 4}, {5, 0}, {1, 8}, {100, 3}} {
		var seen [100]atomic.Int32
		ParallelFor(tc.n, tc.workers, func(i int) { seen[i].Add(1) })
		for i := range tc.n {
			want := int32(1)
			if tc.workers < 1 {
				want = 0
			}
			if got := seen[i].Load(); got != want {
				t.Fatalf("n=%d workers=%d: index %d ran %d times, want %d", tc.n, tc.workers, i, got, want)
			}
		}
	}
}

// TestParallelForWorkerPanicIsRecoverable pins that a panicking worker no
// longer kills the process: the panic reaches the caller's goroutine, where
// RecoverAsError turns it into an ErrInternal error carrying the worker's
// stack, and the remaining indices still run.
func TestParallelForWorkerPanicIsRecoverable(t *testing.T) {
	var ran atomic.Int32
	run := func() (err error) {
		defer RecoverAsError(&err)
		ParallelFor(50, 4, func(i int) {
			ran.Add(1)
			if i == 7 {
				panic("boom at seven")
			}
		})
		return nil
	}
	err := run()
	if !errors.Is(err, ErrInternal) {
		t.Fatalf("err = %v, want ErrInternal", err)
	}
	if msg := err.Error(); !strings.Contains(msg, "boom at seven") || !strings.Contains(msg, "panics_test.go") {
		t.Fatalf("error lacks the panic value or the worker stack: %q", msg)
	}
	if got := ran.Load(); got != 50 {
		t.Fatalf("%d of 50 indices ran", got)
	}
}

// TestRecoverAsErrorLeavesNormalReturnsAlone pins that the deferred recover is
// a no-op on the non-panicking path.
func TestRecoverAsErrorLeavesNormalReturnsAlone(t *testing.T) {
	sentinel := errors.New("ordinary")
	f := func() (err error) {
		defer RecoverAsError(&err)
		return sentinel
	}
	if err := f(); err != sentinel {
		t.Fatalf("err = %v, want the ordinary error unchanged", err)
	}
}
