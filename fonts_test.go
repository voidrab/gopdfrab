package gopdfrab

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// TestFontSource verifies that the substitute fonts come from the source
// set, every one of FontFiles, and that a failing source fails PreloadFonts.
func TestFontSource(t *testing.T) {
	defer SetFontSource(nil)
	var asked []string
	SetFontSource(func(name string) ([]byte, error) {
		asked = append(asked, name)
		return os.ReadFile(filepath.Join("internal", "fontsubst", "fonts", name))
	})
	if err := PreloadFonts(); err != nil {
		t.Fatalf("PreloadFonts() = %v", err)
	}
	if !slices.Equal(asked, FontFiles()) {
		t.Errorf("the source was asked for %v, want FontFiles() = %v", asked, FontFiles())
	}

	offline := errors.New("offline")
	SetFontSource(func(string) ([]byte, error) { return nil, offline })
	if err := PreloadFonts(); !errors.Is(err, offline) {
		t.Errorf("PreloadFonts() = %v, want the source's error", err)
	}
}

// TestFontFilesCopy verifies that FontFiles returns a copy the caller may
// change.
func TestFontFilesCopy(t *testing.T) {
	f := FontFiles()
	f[0] = "changed"
	if FontFiles()[0] == "changed" {
		t.Error("changing FontFiles' result changed the list")
	}
}
