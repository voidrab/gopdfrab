package pdf

import (
	"strings"
	"testing"
)

// bothBackings runs f over a Reader holding data in memory and over one that
// reads it from a file.
func bothBackings(t *testing.T, data string, f func(t *testing.T, d *Reader)) {
	t.Run("in memory", func(t *testing.T) { f(t, &Reader{src: memSource([]byte(data))}) })
	t.Run("file", func(t *testing.T) { f(t, newTestFileReader([]byte(data))) })
}

// TestSectionCursorReadLine verifies that every end-of-line marker ends a
// line, that a CRLF is one marker even when the window splits it, and that
// the file's last line needs none.
func TestSectionCursorReadLine(t *testing.T) {
	// The first line ends with a CR as the window's last byte and its LF
	// just past it.
	long := strings.Repeat("x", sectionWindowStep-1)
	data := "lead " + long[5:] + "\r\na\rb\nc\r\n\nlast"
	bothBackings(t, data, func(t *testing.T, d *Reader) {
		c := d.SectionAt(0)
		var got []string
		for {
			line, ok := c.ReadLine()
			if !ok {
				break
			}
			got = append(got, string(line))
		}
		want := []string{"lead " + long[5:], "a", "b", "c", "", "last"}
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("lines = %q, want %q", got, want)
		}
		if c.Err() != nil {
			t.Errorf("Err() = %v", c.Err())
		}
	})
}

// TestSectionCursorFixedReads verifies Peek, Next and Rest, including reads
// that need the window widened and reads past the end of the file.
func TestSectionCursorFixedReads(t *testing.T) {
	data := "ab" + strings.Repeat("0123456789", 2000)
	bothBackings(t, data, func(t *testing.T, d *Reader) {
		c := d.SectionAt(0)
		if b, ok := c.Peek(); !ok || b != 'a' {
			t.Errorf("Peek() = %q, %v", b, ok)
		}
		if b, ok := c.Next(2); !ok || string(b) != "ab" {
			t.Errorf("Next(2) = %q, %v", b, ok)
		}
		// Past the first window.
		if b, ok := c.Next(sectionWindowStep + 10); !ok || len(b) != sectionWindowStep+10 || b[0] != '0' {
			t.Errorf("Next(window+10) = %d bytes, %v", len(b), ok)
		}
		if r := c.Rest(5); string(r) != "23456" { // 8202 digits read
			t.Errorf("Rest(5) = %q", r)
		}
		if _, ok := c.Next(len(data)); ok {
			t.Error("Next past the end of the file reported ok")
		}
		if r := c.Rest(len(data)); len(r) != len(data)-2-sectionWindowStep-10 {
			t.Errorf("Rest(all) = %d bytes", len(r))
		}
	})
	bothBackings(t, "", func(t *testing.T, d *Reader) {
		c := d.SectionAt(0)
		if _, ok := c.Peek(); ok {
			t.Error("Peek on an empty file reported ok")
		}
		if _, ok := c.ReadLine(); ok {
			t.Error("ReadLine on an empty file reported ok")
		}
	})
}
