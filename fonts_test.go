package gopdfrab

import (
	"errors"
	"slices"
	"sync"
	"testing"
)

// TestFontSource verifies that PreloadFonts asks the source for every one of
// FontFiles and returns its error, and that FontFiles returns a copy the
// caller may change.
func TestFontSource(t *testing.T) {
	defer SetFontSource(nil)
	offline := errors.New("offline")
	var mu sync.Mutex // Preload calls the source from several goroutines
	var asked []string
	SetFontSource(func(name string) ([]byte, error) {
		mu.Lock()
		defer mu.Unlock()
		asked = append(asked, name)
		return nil, offline
	})
	if err := PreloadFonts(); !errors.Is(err, offline) {
		t.Errorf("PreloadFonts() = %v, want the source's error", err)
	}
	files := FontFiles()
	if !slices.Equal(slices.Sorted(slices.Values(asked)), slices.Sorted(slices.Values(files))) {
		t.Errorf("the source was asked for %v, want FontFiles() = %v", asked, files)
	}
	files[0] = "changed"
	if FontFiles()[0] == "changed" {
		t.Error("changing FontFiles' result changed the list")
	}
}
