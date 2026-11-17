//go:build gopdfrab_nofonts

package fontsubst

import (
	"errors"
	"testing"
)

// TestNoSource verifies that a build without the fonts has none until a
// source is set.
func TestNoSource(t *testing.T) {
	SetSource(nil)
	if data := LiberationSansRegular(); data != nil {
		t.Error("a font without a source is not nil")
	}
	if err := Preload(); !errors.Is(err, ErrNoSource) {
		t.Errorf("Preload() = %v, want ErrNoSource", err)
	}
}
