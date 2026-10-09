package convert

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdfgen"
)

func TestSaveWritesOutput(t *testing.T) {
	data := pdfgen.PlainThreeIssue()
	cr := ConvertResult{backing: &outputBacking{mem: data}}
	dir := t.TempDir()
	path := filepath.Join(dir, "out.pdf")
	if err := cr.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("saved %d bytes (%v), want the %d-byte output", len(got), err, len(data))
	}
	assertOnlyFile(t, dir, "out.pdf")
}

// TestSaveFailureKeepsExistingFile pins Save's atomicity: when the write fails
// part-way, the file already at path -- possibly the conversion's own input --
// is left exactly as it was, and no temp file is left beside it.
func TestSaveFailureKeepsExistingFile(t *testing.T) {
	setSpillThreshold(t, 64)
	b := seal(t, pdfgen.PlainThreeIssue())
	if b.path == "" {
		t.Fatal("output did not spill")
	}
	// Losing the spill file makes writeTo fail after Save has started.
	if err := os.Remove(b.path); err != nil {
		t.Fatal(err)
	}
	defer b.close()

	dir := t.TempDir()
	path := filepath.Join(dir, "in.pdf")
	original := []byte("%PDF-1.4 original input")
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := (ConvertResult{backing: b}).Save(path); err == nil {
		t.Fatal("expected Save to fail")
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, original) {
		t.Fatalf("existing file changed to %q after a failed Save", got)
	}
	assertOnlyFile(t, dir, "in.pdf")
}

func TestSaveKeepsPermissionsAndFollowsSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permissions and symlinks")
	}
	data := pdfgen.PlainThreeIssue()
	cr := ConvertResult{backing: &outputBacking{mem: data}}
	dir := t.TempDir()
	target := filepath.Join(dir, "target.pdf")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.pdf")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := cr.Save(link); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("symlink replaced by a regular file (%v)", err)
	}
	fi, err := os.Stat(target)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("target mode = %v (%v), want 0600 kept", fi.Mode().Perm(), err)
	}
	if got, _ := os.ReadFile(target); !bytes.Equal(got, data) {
		t.Error("target does not hold the saved output")
	}
}

func assertOnlyFile(t *testing.T, dir, name string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != name {
			t.Errorf("unexpected file %s left in %s", e.Name(), dir)
		}
	}
}
