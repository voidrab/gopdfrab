package pdf

import "testing"

// TestCursorReadLine covers the exhausted-input guard, the no-trailing-EOL
// last-line branch, a bare CR not at end of input, and a CRLF pair.
func TestCursorReadLine(t *testing.T) {
	t.Run("exhausted input", func(t *testing.T) {
		c := NewCursor(nil)
		if line, ok := c.ReadLine(); ok || line != "" {
			t.Errorf("ReadLine() = %q, %v; want \"\", false", line, ok)
		}
	})

	t.Run("last line with no trailing EOL", func(t *testing.T) {
		c := NewCursor([]byte("abc"))
		line, ok := c.ReadLine()
		if !ok || line != "abc" {
			t.Errorf("ReadLine() = %q, %v; want \"abc\", true", line, ok)
		}
		if _, ok := c.ReadLine(); ok {
			t.Error("expected a second ReadLine to report exhausted input")
		}
	})

	t.Run("bare CR followed by more data", func(t *testing.T) {
		c := NewCursor([]byte("abc\rdef"))
		line, ok := c.ReadLine()
		if !ok || line != "abc" {
			t.Errorf("ReadLine() = %q, %v; want \"abc\", true", line, ok)
		}
		line, ok = c.ReadLine()
		if !ok || line != "def" {
			t.Errorf("second ReadLine() = %q, %v; want \"def\", true", line, ok)
		}
	})

	t.Run("CRLF pair", func(t *testing.T) {
		c := NewCursor([]byte("abc\r\ndef"))
		line, ok := c.ReadLine()
		if !ok || line != "abc" {
			t.Errorf("ReadLine() = %q, %v; want \"abc\", true", line, ok)
		}
		line, ok = c.ReadLine()
		if !ok || line != "def" {
			t.Errorf("second ReadLine() = %q, %v; want \"def\", true", line, ok)
		}
	})

	t.Run("bare LF", func(t *testing.T) {
		c := NewCursor([]byte("abc\ndef"))
		line, ok := c.ReadLine()
		if !ok || line != "abc" {
			t.Errorf("ReadLine() = %q, %v; want \"abc\", true", line, ok)
		}
	})
}
