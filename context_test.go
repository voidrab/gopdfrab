package gopdfrab_test

import (
	"context"
	"errors"
	"testing"

	gopdfrab "github.com/voidrab/gopdfrab"
)

// TestConvertContextCancelled: a context cancelled before the call returns the
// cancellation error rather than doing the work.
func TestConvertContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := gopdfrab.ConvertBytesContext(ctx, []byte(plainPDF), gopdfrab.PDFA1B, gopdfrab.Options{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ConvertBytesContext with cancelled ctx: err=%v, want context.Canceled", err)
	}
}

// TestVerifyContextCancelled: the same for verification.
func TestVerifyContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := gopdfrab.VerifyBytesContext(ctx, []byte(plainPDF), gopdfrab.PDFA1B, gopdfrab.Options{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("VerifyBytesContext with cancelled ctx: err=%v, want context.Canceled", err)
	}
}

// TestConvertContextBackgroundStillWorks: a live context behaves exactly like
// the non-context form.
func TestConvertContextBackgroundStillWorks(t *testing.T) {
	cr, err := gopdfrab.ConvertBytesContext(context.Background(), []byte(plainPDF), gopdfrab.PDFA1B, gopdfrab.Options{})
	if err != nil {
		t.Fatalf("ConvertBytesContext(Background): %v", err)
	}
	if len(cr.Output) == 0 {
		t.Error("no output from a live-context convert")
	}
}

// TestVerifyAllContextCancelled: a cancelled batch records the cancellation for
// every file rather than verifying them.
func TestVerifyAllContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	paths := []string{"a.pdf", "b.pdf", "c.pdf"}
	results, err := gopdfrab.VerifyAllContext(ctx, paths, gopdfrab.PDFA1B, gopdfrab.Options{})
	if err != nil {
		t.Fatalf("VerifyAllContext returned a top-level error: %v", err)
	}
	if len(results) != len(paths) {
		t.Fatalf("got %d results, want %d", len(results), len(paths))
	}
	for _, r := range results {
		if !errors.Is(r.Err, context.Canceled) {
			t.Errorf("%s: err=%v, want context.Canceled", r.Path, r.Err)
		}
	}
}
