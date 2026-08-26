package gopdfrab_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
	defer cr.Close()
	if b, _ := cr.Output(); len(b) == 0 {
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

// TestVerifyContextPath covers the path form of VerifyContext: cancellation, a
// live context agreeing with Verify, and Options.Password reaching the open.
func TestVerifyContextPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plain.pdf")
	if err := os.WriteFile(path, []byte(plainPDF), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := gopdfrab.VerifyContext(ctx, path, gopdfrab.PDFA1B, gopdfrab.Options{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("VerifyContext with cancelled ctx: err=%v, want context.Canceled", err)
	}

	live, err := gopdfrab.VerifyContext(context.Background(), path, gopdfrab.PDFA1B, gopdfrab.Options{})
	if err != nil {
		t.Fatalf("VerifyContext(Background): %v", err)
	}
	want, err := gopdfrab.Verify(path, gopdfrab.PDFA1B)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if live.Valid != want.Valid || len(live.Issues) != len(want.Issues) {
		t.Errorf("VerifyContext = {valid=%v issues=%d}, Verify = {valid=%v issues=%d}",
			live.Valid, len(live.Issues), want.Valid, len(want.Issues))
	}

	enc := filepath.Join("internal", "pdf", "testdata", "crypt", "enc_aesv2_pw.pdf")
	if _, err := os.Stat(enc); err != nil {
		t.Skipf("encrypted fixture absent: %v", err)
	}
	if _, err := gopdfrab.VerifyContext(context.Background(), enc, gopdfrab.PDFA1B, gopdfrab.Options{}); !errors.Is(err, gopdfrab.ErrPasswordRequired) {
		t.Errorf("VerifyContext without password: err=%v, want ErrPasswordRequired", err)
	}
	opts := gopdfrab.Options{Password: []byte("ownerpw")}
	if _, err := gopdfrab.VerifyContext(context.Background(), enc, gopdfrab.PDFA1B, opts); err != nil {
		t.Errorf("VerifyContext with password: %v", err)
	}
}

// TestConvertAllContextCancelled: a cancelled batch records the cancellation
// for every file rather than converting them.
func TestConvertAllContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	paths := []string{"a.pdf", "b.pdf", "c.pdf"}
	results, err := gopdfrab.ConvertAllContext(ctx, paths, gopdfrab.PDFA1B, gopdfrab.Options{})
	if err != nil {
		t.Fatalf("ConvertAllContext returned a top-level error: %v", err)
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

// TestConvertAllContextMatchesConvertAll: under a live context the batch does
// the same work as ConvertAll.
func TestConvertAllContextMatchesConvertAll(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plain.pdf")
	if err := os.WriteFile(path, []byte(plainPDF), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := gopdfrab.ConvertAllContext(context.Background(), []string{path}, gopdfrab.PDFA1B, gopdfrab.Options{})
	if err != nil {
		t.Fatalf("ConvertAllContext(Background): %v", err)
	}
	want, err := gopdfrab.ConvertAll([]string{path}, gopdfrab.PDFA1B)
	if err != nil {
		t.Fatalf("ConvertAll: %v", err)
	}
	defer func() {
		for _, r := range got {
			r.Result.Close()
		}
		for _, r := range want {
			r.Result.Close()
		}
	}()

	if len(got) != 1 || len(want) != 1 {
		t.Fatalf("got %d results, want %d, each want 1", len(got), len(want))
	}
	if got[0].Err != nil || want[0].Err != nil {
		t.Fatalf("per-file errors: ConvertAllContext=%v ConvertAll=%v", got[0].Err, want[0].Err)
	}
	if got[0].Result.Result.Valid != want[0].Result.Result.Valid || got[0].Result.Iterations != want[0].Result.Iterations {
		t.Errorf("ConvertAllContext = {valid=%v iters=%d}, ConvertAll = {valid=%v iters=%d}",
			got[0].Result.Result.Valid, got[0].Result.Iterations,
			want[0].Result.Result.Valid, want[0].Result.Iterations)
	}
}

// TestDocumentContextMethods covers (*Document).VerifyContext and
// (*Document).ConvertContext, cancelled and live.
func TestDocumentContextMethods(t *testing.T) {
	doc, err := gopdfrab.OpenBytes([]byte(plainPDF))
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	defer doc.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := doc.VerifyContext(ctx, gopdfrab.PDFA1B); !errors.Is(err, context.Canceled) {
		t.Errorf("Document.VerifyContext with cancelled ctx: err=%v, want context.Canceled", err)
	}
	if _, err := doc.ConvertContext(ctx, gopdfrab.PDFA1B, gopdfrab.Options{}); !errors.Is(err, context.Canceled) {
		t.Errorf("Document.ConvertContext with cancelled ctx: err=%v, want context.Canceled", err)
	}

	live, err := doc.VerifyContext(context.Background(), gopdfrab.PDFA1B)
	if err != nil {
		t.Fatalf("Document.VerifyContext(Background): %v", err)
	}
	want, err := doc.Verify(gopdfrab.PDFA1B)
	if err != nil {
		t.Fatalf("Document.Verify: %v", err)
	}
	if live.Valid != want.Valid || len(live.Issues) != len(want.Issues) {
		t.Errorf("VerifyContext = {valid=%v issues=%d}, Verify = {valid=%v issues=%d}",
			live.Valid, len(live.Issues), want.Valid, len(want.Issues))
	}

	cr, err := doc.ConvertContext(context.Background(), gopdfrab.PDFA1B, gopdfrab.Options{})
	if err != nil {
		t.Fatalf("Document.ConvertContext(Background): %v", err)
	}
	defer cr.Close()
	if b := mustOutputExt(t, cr); len(b) == 0 {
		t.Error("no output from a live-context Document convert")
	}
}
