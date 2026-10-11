package pdf

import "testing"

// TestCursorReadLineTrailingCR: a bare CR as the last byte ends the final line
// without reading past the data.
func TestCursorReadLineTrailingCR(t *testing.T) {
	c := NewCursor([]byte("abc\r"))
	if line, ok := c.ReadLine(); !ok || line != "abc" {
		t.Errorf("first ReadLine = %q, %v; want abc, true", line, ok)
	}
	if line, ok := c.ReadLine(); ok {
		t.Errorf("second ReadLine = %q, true; want no more lines", line)
	}
}
