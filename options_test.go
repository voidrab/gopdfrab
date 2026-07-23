package gopdfrab_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	gopdfrab "github.com/voidrab/gopdfrab"
)

// TestOptionsPassword confirms Options.Password flows to the Open step: an
// encrypted fixture with a non-empty password verifies and converts with the
// right password and reports ErrPasswordRequired without it.
func TestOptionsPassword(t *testing.T) {
	path := filepath.Join("internal", "pdf", "testdata", "crypt", "enc_aesv2_pw.pdf")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("encrypted fixture absent: %v", err)
	}

	if _, err := gopdfrab.VerifyBytes(data, gopdfrab.PDFA1B); !errors.Is(err, gopdfrab.ErrPasswordRequired) {
		t.Fatalf("VerifyBytes without password: err=%v, want ErrPasswordRequired", err)
	}

	res, err := gopdfrab.VerifyBytesContext(context.Background(), data, gopdfrab.PDFA1B,
		gopdfrab.Options{Password: []byte("ownerpw")})
	if err != nil {
		t.Fatalf("VerifyBytesContext with owner password: %v", err)
	}
	// The fixture is not PDF/A, but it must decrypt and verify (produce issues)
	// rather than fail to open.
	if len(res.Issues) == 0 && !res.Valid {
		t.Error("decrypted fixture produced neither a verdict nor issues")
	}

	cr, err := gopdfrab.ConvertBytesContext(context.Background(), data, gopdfrab.PDFA1B,
		gopdfrab.Options{Password: []byte("ownerpw")})
	if err != nil {
		t.Fatalf("ConvertBytesContext with owner password: %v", err)
	}
	if len(cr.Output) == 0 {
		t.Error("ConvertBytesContext with password produced no output")
	}
}

// TestOptionsRasterDPI confirms Options.RasterDPI changes the rasterizer's
// output: the canonical q/Q-nesting fixture can only reach conformance by
// rasterizing, so a higher DPI produces a larger (more pixels) converted
// document.
func TestOptionsRasterDPI(t *testing.T) {
	path := filepath.Join("tests", "veraPDF", "PDF_A-1b", "6.1 File structure",
		"6.1.12 Implementation limits", "veraPDF test suite 6-1-12-t08-fail-a.pdf")
	if _, err := os.Stat(path); err != nil {
		t.Skip("veraPDF suite not present")
	}

	low, err := gopdfrab.ConvertContext(context.Background(), path, gopdfrab.PDFA1B,
		gopdfrab.Options{RasterDPI: 72})
	if err != nil {
		t.Fatalf("Convert at 72 DPI: %v", err)
	}
	high, err := gopdfrab.ConvertContext(context.Background(), path, gopdfrab.PDFA1B,
		gopdfrab.Options{RasterDPI: 300})
	if err != nil {
		t.Fatalf("Convert at 300 DPI: %v", err)
	}
	if !low.Result.Valid || !high.Result.Valid {
		t.Fatalf("fixture did not rasterize to conformance (low=%v high=%v)", low.Result.Valid, high.Result.Valid)
	}
	if len(high.Output) <= len(low.Output) {
		t.Errorf("300-DPI output (%d bytes) not larger than 72-DPI output (%d bytes); DPI option had no effect",
			len(high.Output), len(low.Output))
	}
}

// TestOptionsTwoArgForm confirms the two-argument call form still compiles and
// behaves as the default (zero Options).
func TestOptionsTwoArgForm(t *testing.T) {
	data := []byte(plainPDF)
	if _, err := gopdfrab.VerifyBytes(data, gopdfrab.PDFA1B); err != nil {
		t.Errorf("two-arg VerifyBytes: %v", err)
	}
	if _, err := gopdfrab.ConvertBytes(data, gopdfrab.PDFA1B); err != nil {
		t.Errorf("two-arg ConvertBytes: %v", err)
	}
}
