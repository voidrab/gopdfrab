package convert

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfgen"
)

// TestSaveReplacesAnOpenInput verifies that Save can write over a file that a
// Reader holds open, and that the Reader keeps reading the file it opened.
func TestSaveReplacesAnOpenInput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "in.pdf")
	original := pdfgen.PlainThreeIssue()
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := pdf.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	out := append(bytes.Clone(original), "\n% converted\n"...)
	if err := (ConvertResult{backing: &outputBacking{mem: out}}).Save(path); err != nil {
		r.Close()
		t.Fatalf("saving over the open input: %v", err)
	}
	if _, err := r.Version(); err != nil {
		t.Errorf("reading the replaced input: %v", err)
	}
	r.Close()
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, out) {
		t.Fatalf("saved %d bytes (%v), want the %d-byte output", len(got), err, len(out))
	}
	assertOnlyFile(t, dir, "in.pdf")
}

func TestReplaceFileReportsAFailedRename(t *testing.T) {
	dir := t.TempDir()
	missing, to := filepath.Join(dir, "missing.pdf"), filepath.Join(dir, "to.pdf")
	if err := replaceFile(missing, to); !os.IsNotExist(err) {
		t.Errorf("renaming a missing file to a new name: %v, want not-exist", err)
	}
	if err := os.WriteFile(to, []byte("kept"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := replaceFile(missing, to); err == nil {
		t.Error("renaming a missing file over another succeeded")
	}
	if got, _ := os.ReadFile(to); string(got) != "kept" {
		t.Errorf("the file renamed over holds %q after the failure, want it kept", got)
	}
	assertOnlyFile(t, dir, "to.pdf")
}
