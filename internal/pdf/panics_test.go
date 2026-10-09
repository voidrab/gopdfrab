package pdf

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdfgen"
)

// TestParallelForRunsEveryIndex pins the pool's basic contract, including the
// degenerate sizes the hand-rolled pools it replaced special-cased.
func TestParallelForRunsEveryIndex(t *testing.T) {
	for _, tc := range []struct{ n, workers int }{{0, 4}, {5, 0}, {1, 8}, {100, 3}} {
		var seen [100]atomic.Int32
		ParallelFor(context.Background(), tc.n, tc.workers, func(i int) { seen[i].Add(1) })
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
		ParallelFor(context.Background(), 50, 4, func(i int) {
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

// TestParallelForStopsDispatchOnCancel pins that a cancelled context stops
// further indices from being dispatched.
func TestParallelForStopsDispatchOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var ran atomic.Int32
	ParallelFor(ctx, 1000, 1, func(i int) {
		ran.Add(1)
		if i == 9 {
			cancel()
		}
	})
	if got := ran.Load(); got >= 1000 || got < 10 {
		t.Fatalf("%d indices ran, want 10 or so of 1000 after cancelling", got)
	}
}

// TestValuePointerNeverPanics pins ValuePointer's fallback: values without an
// identity report 0 instead of panicking in reflect, and pointer-shaped ones
// still report a stable non-zero pointer.
func TestValuePointerNeverPanics(t *testing.T) {
	for _, v := range []PDFValue{nil, PDFInteger(1), PDFReal(1), PDFBoolean(true), PDFName{Value: "N"}, PDFString{Value: "s"}, PDFRef{ObjNum: 1}} {
		if got := ValuePointer(v); got != 0 {
			t.Errorf("ValuePointer(%#v) = %#x, want 0", v, got)
		}
	}
	d := NewDict()
	arr := PDFArray{PDFInteger(1)}
	b := []byte{1}
	for _, v := range []PDFValue{d, arr, b} {
		first, again := ValuePointer(v), ValuePointer(v)
		if first == 0 || first != again {
			t.Errorf("ValuePointer(%T) not a stable non-zero identity", v)
		}
	}
}

// TestReaderCloseIsIdempotent pins that a second Close is a no-op rather than
// an "already closed" error from the file, matching ConvertResult.Close.
func TestReaderCloseIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "in.pdf")
	if err := os.WriteFile(path, pdfgen.PlainThreeIssue(), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("second Close: %v, want nil", err)
	}
}
