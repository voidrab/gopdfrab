//go:build gopdfrab_nofonts

package fontsubst

import (
	"errors"
	"testing"
)

// TestNoSource verifies that, in a build without the fonts, a font is nil and
// Preload fails with ErrNoSource until a source is set.
func TestNoSource(t *testing.T) {
	SetSource(nil)
	if data := LiberationSansRegular(); data != nil {
		t.Error("a font without a source is not nil")
	}
	if err := Preload(); !errors.Is(err, ErrNoSource) {
		t.Errorf("Preload() = %v, want ErrNoSource", err)
	}
}
