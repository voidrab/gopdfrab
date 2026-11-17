package gopdfrab

import (
	"errors"
	"slices"
	"sync"
	"testing"
)

// TestFontSource verifies that the substitute fonts come from the source
// set, every one of FontFiles, which is a copy the caller may change.
func TestFontSource(t *testing.T) {
	defer SetFontSource(nil)
	offline := errors.New("offline")
	var mu sync.Mutex // the source is asked for the fonts all at once
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
