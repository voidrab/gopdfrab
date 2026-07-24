package convert

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfgen"
)

// convertPlain converts the standard three-issue fixture and fails on error.
func convertPlain(t *testing.T) ConvertResult {
	t.Helper()
	cr, err := ConvertBytes(pdfgen.PlainThreeIssue(), pdf.PDFA1B, Options{})
	if err != nil {
		t.Fatalf("ConvertBytes: %v", err)
	}
	return cr
}

// TestConvertSmallOutputStaysInMemory: under the default threshold the output
// is not spilled, so no temp file is created and Close is a no-op on the bytes.
func TestConvertSmallOutputStaysInMemory(t *testing.T) {
	cr := convertPlain(t)
	defer cr.Close()
	if cr.backing.path != "" {
		t.Errorf("small output spilled to %q, want in-memory", cr.backing.path)
	}
	if cr.backing.mem == nil {
		t.Error("in-memory backing has nil bytes")
	}
}

// TestConvertSpillsLargeOutput: with the threshold lowered, the output spills to
// a temp file, the output bytes are no longer resident in the heap
// (backing.mem is nil -- the item-8/18 win), and Close removes the file.
func TestConvertSpillsLargeOutput(t *testing.T) {
	setSpillThreshold(t, 64)
	cr := convertPlain(t)

	if cr.backing.path == "" {
		t.Fatal("output over the lowered threshold stayed in memory, want a temp file")
	}
	if cr.backing.mem != nil {
		t.Error("spilled backing still holds output bytes in the heap")
	}
	if _, err := os.Stat(cr.backing.path); err != nil {
		t.Fatalf("spill temp file missing: %v", err)
	}

	path := cr.backing.path
	if err := cr.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("temp file %q survived Close: %v", path, err)
	}
	if err := cr.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	if _, err := cr.Output(); !errors.Is(err, errNoOutput) {
		t.Errorf("Output after Close = %v, want errNoOutput", err)
	}
}

// TestConvertResultAccessorsAgree: Output, WriteTo, and Save yield identical
// bytes, whether the output stayed in memory or spilled.
func TestConvertResultAccessorsAgree(t *testing.T) {
	for _, tc := range []struct {
		name      string
		threshold int
	}{
		{"in-memory", 8 << 20},
		{"spilled", 64},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setSpillThreshold(t, tc.threshold)
			cr := convertPlain(t)
			defer cr.Close()

			viaOutput := mustOutput(t, cr)

			var viaWriteTo bytes.Buffer
			n, err := cr.WriteTo(&viaWriteTo)
			if err != nil {
				t.Fatalf("WriteTo: %v", err)
			}
			if n != int64(len(viaOutput)) {
				t.Errorf("WriteTo n=%d, want %d", n, len(viaOutput))
			}

			path := filepath.Join(t.TempDir(), "out.pdf")
			if err := cr.Save(path); err != nil {
				t.Fatalf("Save: %v", err)
			}
			viaSave, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read saved: %v", err)
			}

			if !bytes.Equal(viaOutput, viaWriteTo.Bytes()) || !bytes.Equal(viaOutput, viaSave) {
				t.Errorf("accessors disagree: Output=%d WriteTo=%d Save=%d bytes",
					len(viaOutput), viaWriteTo.Len(), len(viaSave))
			}
			// The spilled output must still open and verify as a real PDF.
			if _, err := pdf.OpenBytes(viaOutput); err != nil {
				t.Errorf("converted output does not open: %v", err)
			}
		})
	}
}

// TestConvertEachClosesResults: ConvertEach closes each result after its
// callback returns, so spill temp files do not accumulate across a batch.
func TestConvertEachClosesResults(t *testing.T) {
	setSpillThreshold(t, 64)

	dir := t.TempDir()
	var paths []string
	for i := range 3 {
		p := filepath.Join(dir, fmt.Sprintf("in%d.pdf", i))
		if err := os.WriteFile(p, pdfgen.PlainThreeIssue(), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}

	var spillPaths []string
	err := ConvertEach(paths, pdf.PDFA1B, Options{}, func(fr pdf.FileResult[ConvertResult]) error {
		if fr.Err != nil {
			t.Errorf("convert %s: %v", fr.Path, fr.Err)
			return nil
		}
		if fr.Result.backing.path == "" {
			t.Errorf("%s did not spill under the lowered threshold", fr.Path)
		}
		// The temp file must exist inside the callback, before auto-close.
		if _, err := os.Stat(fr.Result.backing.path); err != nil {
			t.Errorf("temp file missing inside callback: %v", err)
		}
		spillPaths = append(spillPaths, fr.Result.backing.path)
		return nil
	})
	if err != nil {
		t.Fatalf("ConvertEach: %v", err)
	}

	if len(spillPaths) != len(paths) {
		t.Fatalf("saw %d spill files, want %d", len(spillPaths), len(paths))
	}
	for _, p := range spillPaths {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("ConvertEach leaked temp file %q: %v", p, err)
		}
	}
}
