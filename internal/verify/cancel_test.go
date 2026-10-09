package verify

import (
	"context"
	"errors"
	"fmt"
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
