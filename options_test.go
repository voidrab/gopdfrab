package gopdfrab_test

import (
	"bytes"
	"compress/zlib"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	gopdfrab "github.com/voidrab/gopdfrab"
	"github.com/voidrab/gopdfrab/internal/pdfgen"
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
	defer cr.Close()
	if b, _ := cr.Output(); len(b) == 0 {
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
	defer low.Close()
	defer high.Close()
	if !low.Result.Valid || !high.Result.Valid {
		t.Fatalf("fixture did not rasterize to conformance (low=%v high=%v)", low.Result.Valid, high.Result.Valid)
	}
	lowOut, highOut := len(mustOutputExt(t, low)), len(mustOutputExt(t, high))
	if highOut <= lowOut {
		t.Errorf("300-DPI output (%d bytes) not larger than 72-DPI output (%d bytes); DPI option had no effect",
			highOut, lowOut)
	}
}

// TestSetLimitsRoundTrip confirms the Limits accessors: DefaultLimits and a
// freshly-reset CurrentLimits report the default, SetLimits reflects a raised
// value, and a zero field resets to the default.
func TestSetLimitsRoundTrip(t *testing.T) {
	defer gopdfrab.SetLimits(gopdfrab.DefaultLimits())

	def := gopdfrab.DefaultLimits().MaxDecodedStreamBytes
	if def <= 0 {
		t.Fatalf("DefaultLimits.MaxDecodedStreamBytes = %d, want positive", def)
	}
	gopdfrab.SetLimits(gopdfrab.DefaultLimits())
	if got := gopdfrab.CurrentLimits().MaxDecodedStreamBytes; got != def {
		t.Errorf("CurrentLimits after default = %d, want %d", got, def)
	}

	gopdfrab.SetLimits(gopdfrab.Limits{MaxDecodedStreamBytes: def * 2})
	if got := gopdfrab.CurrentLimits().MaxDecodedStreamBytes; got != def*2 {
		t.Errorf("CurrentLimits after raise = %d, want %d", got, def*2)
	}

	gopdfrab.SetLimits(gopdfrab.Limits{}) // zero field resets to the default
	if got := gopdfrab.CurrentLimits().MaxDecodedStreamBytes; got != def {
		t.Errorf("CurrentLimits after zero = %d, want default %d", got, def)
	}
}

// TestSetLimitsAffectsDecoding confirms the public knob reaches the decode
// chokepoint: a tiny cap makes a FlateDecode content stream report a
// StreamUndecodable (6.1.7) issue that is absent at the default cap.
func TestSetLimitsAffectsDecoding(t *testing.T) {
	defer gopdfrab.SetLimits(gopdfrab.DefaultLimits())
	data := flateContentPDF(t)

	gopdfrab.SetLimits(gopdfrab.DefaultLimits())
	base, err := gopdfrab.VerifyBytes(data, gopdfrab.PDFA1B)
	if err != nil {
		t.Fatalf("VerifyBytes at default cap: %v", err)
	}
	if issuesHaveCheck(base, "StreamUndecodable") {
		t.Fatal("default cap already reports StreamUndecodable")
	}

	gopdfrab.SetLimits(gopdfrab.Limits{MaxDecodedStreamBytes: 16})
	low, err := gopdfrab.VerifyBytes(data, gopdfrab.PDFA1B)
	if err != nil {
		t.Fatalf("VerifyBytes at tiny cap: %v", err)
	}
	if !issuesHaveCheck(low, "StreamUndecodable") {
		t.Error("tiny cap did not report StreamUndecodable")
	}
}

// flateContentPDF builds a minimal one-page PDF whose page content is a
// FlateDecode stream that decodes to several hundred bytes.
func flateContentPDF(t *testing.T) []byte {
	t.Helper()
	content := bytes.Repeat([]byte("0 0 0 rg 0 0 10 10 re f\n"), 40)
	var zbuf bytes.Buffer
	zw := zlib.NewWriter(&zbuf)
	if _, err := zw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	b := pdfgen.NewBuilder("%PDF-1.4\n")
	b.Obj(1, "<</Type/Catalog/Pages 2 0 R>>")
	b.Obj(2, "<</Type/Pages/Kids[3 0 R]/Count 1>>")
	b.Obj(3, "<</Type/Page/Parent 2 0 R/MediaBox[0 0 595 842]/Contents 4 0 R>>")
	b.StreamObj(4, "<< /Filter /FlateDecode", zbuf.Bytes())
	return b.FinishClassic("<</Size 5/Root 1 0 R>>")
}

func issuesHaveCheck(res gopdfrab.Result, name string) bool {
	for _, iss := range res.Issues {
		if iss.Check().Name() == name {
			return true
		}
	}
	return false
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

// mustOutputExt returns cr's converted bytes, failing the test on error (the
// external gopdfrab_test package's counterpart to the internal mustOutput).
func mustOutputExt(t *testing.T, cr gopdfrab.ConvertResult) []byte {
	t.Helper()
	b, err := cr.Output()
	if err != nil {
		t.Fatalf("Output(): %v", err)
	}
	return b
}
