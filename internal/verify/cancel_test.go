package verify

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfgen"
)

// cancelAfterCtx reports cancellation from its (after+1)th Err call on, so a
// test can cancel deterministically in the middle of a verify.
type cancelAfterCtx struct {
	after int64
	calls atomic.Int64
}

func (c *cancelAfterCtx) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *cancelAfterCtx) Done() <-chan struct{}       { return nil }
func (c *cancelAfterCtx) Value(any) any               { return nil }
func (c *cancelAfterCtx) Err() error {
	if c.calls.Add(1) > c.after {
		return context.Canceled
	}
	return nil
}

// manyNodeDoc builds a one-page document whose page carries n small dicts, so
// the graph walk visits well over the 256-node polling interval.
func manyNodeDoc(n int) []byte {
	var arr strings.Builder
	for i := range n {
		fmt.Fprintf(&arr, "<< /K %d >> ", i)
	}
	b := pdfgen.NewBuilder("%PDF-1.4\n")
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.Obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Resources << >> /X ["+arr.String()+"] >>")
	return b.FinishClassic("<< /Size 4 /Root 1 0 R >>")
}

// TestVerifyContextCancelledMidWalk pins that VerifyContext notices a
// cancellation that happens after its entry check: the graph walk polls the
// context and the call returns ctx.Err() instead of a verdict.
func TestVerifyContextCancelledMidWalk(t *testing.T) {
	d, err := pdf.OpenBytes(manyNodeDoc(5000))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ctx := &cancelAfterCtx{after: 2}
	res, err := VerifyContext(ctx, d, pdf.PDFA1B)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if res.Issues != nil || res.Valid {
		t.Errorf("result = %+v, want the zero Result on cancellation", res)
	}
}

// TestValidationContextStoppedPolls pins the walk's poll: it consults the
// context only every 256 nodes, and once cancelled stays cancelled.
func TestValidationContextStoppedPolls(t *testing.T) {
	run := &cancelAfterCtx{after: 0}
	vc := &ValidationContext{run: run}
	for i := 1; i < 256; i++ {
		if vc.stopped() {
			t.Fatalf("stopped at call %d, before the first poll", i)
		}
	}
	if !vc.stopped() {
		t.Fatal("not stopped at the 256th call")
	}
	if got := run.calls.Load(); got != 1 {
		t.Errorf("ctx.Err called %d times, want 1", got)
	}
	if !vc.stopped() || run.calls.Load() != 1 {
		t.Error("a stopped context must stay stopped without polling again")
	}
	if (&ValidationContext{}).stopped() {
		t.Error("a nil run context must never stop")
	}
}

// TestVerifyAllContextWorkers pins the batch pool: every worker count gives
// the same per-path results in path order, and a pre-cancelled context records
// ctx.Err() for every file without verifying any.
func TestVerifyAllContextWorkers(t *testing.T) {
	dir := t.TempDir()
	var paths []string
	for i := range 5 {
		p := filepath.Join(dir, fmt.Sprintf("f%d.pdf", i))
		if err := os.WriteFile(p, pdfgen.PlainThreeIssue(), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	want, _ := VerifyAllContext(context.Background(), paths, pdf.PDFA1B, nil, 0)
	for _, workers := range []int{1, 2, 16} {
		got, err := VerifyAllContext(context.Background(), paths, pdf.PDFA1B, nil, workers)
		if err != nil {
			t.Fatal(err)
		}
		for i := range paths {
			if got[i].Path != paths[i] || got[i].Err != nil || len(got[i].Result.Issues) != len(want[i].Result.Issues) {
				t.Errorf("workers=%d result %d = %+v, want %+v", workers, i, got[i], want[i])
			}
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, _ := VerifyAllContext(ctx, paths, pdf.PDFA1B, nil, 2)
	for i, fr := range got {
		if fr.Path != paths[i] || !errors.Is(fr.Err, context.Canceled) {
			t.Errorf("cancelled result %d = %+v, want Path set and context.Canceled", i, fr)
		}
	}
}

// TestVerifyPartsCancelledBeforePageChecks pins the poll between building the
// page index and running the page-level checks: a run already cancelled
// there returns before any graph check, leaving Graph empty.
func TestVerifyPartsCancelledBeforePageChecks(t *testing.T) {
	d, err := pdf.OpenBytes(pdfgen.PlainThreeIssue())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if full := verifyPdfA1bParts(context.Background(), d, pdf.PDFA1B); len(full.Graph) == 0 {
		t.Fatal("fixture must have graph-level issues for the check to be meaningful")
	}
	run := &cancelAfterCtx{after: 0}
	pt := verifyPdfA1bParts(run, d, pdf.PDFA1B)
	if len(pt.Graph) != 0 {
		t.Errorf("cancelled run produced %d graph issues, want none", len(pt.Graph))
	}
	if got := run.calls.Load(); got != 1 {
		t.Errorf("ctx.Err called %d times, want 1", got)
	}
}
