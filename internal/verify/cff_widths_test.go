package verify

import (
	"testing"
)

func TestCFFAdvanceWidths(t *testing.T) {
	widths := CFFAdvanceWidths(buildMinimalCFF())
	if widths == nil {
		t.Fatal("CFFAdvanceWidths returned nil for a valid name-keyed CFF")
	}
	if _, ok := widths["A"]; !ok {
		t.Errorf("CFFAdvanceWidths missing glyph A: %v", widths)
	}
}
