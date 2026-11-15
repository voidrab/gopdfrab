package pdf

import (
	"os"
	"path/filepath"
	"testing"
)

// TestOpenFileLetsTheFileBeRenamedAndRemoved verifies that a file open in a
// Reader can be renamed and then removed, and that the Reader still reads its
// contents afterwards.
func TestOpenFileLetsTheFileBeRenamedAndRemoved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.pdf")
	if err := createValidPDF(path); err != nil {
		t.Fatal(err)
	}
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	moved := filepath.Join(dir, "b.pdf")
	if err := os.Rename(path, moved); err != nil {
		t.Fatalf("renaming the open file: %v", err)
	}
	if err := os.Remove(moved); err != nil {
		t.Fatalf("removing the open file: %v", err)
	}
	if meta, err := r.Metadata(); err != nil || meta["Title"] != "Test PDF" {
		t.Errorf("reading after the removal: %v, %v", meta, err)
	}
}

func TestOpenFileReportsErrors(t *testing.T) {
	if _, err := openFile(filepath.Join(t.TempDir(), "missing.pdf")); !os.IsNotExist(err) {
		t.Errorf("opening a missing file: %v, want not-exist", err)
	}
	if _, err := openFile("bad\x00name"); err == nil {
		t.Error("a name with a NUL opened")
	}
}
