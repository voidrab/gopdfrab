package pdf

import "bytes"

// sectionWindowStep is how much of a section a SectionCursor reads at first.
const sectionWindowStep = 8192

// SectionCursor reads a region of the file whose length is not known in
// advance, such as a cross-reference section, a line or a fixed number of
// bytes at a time. Over the in-memory file it reads the bytes in place;
// otherwise it reads from the file, widening its window as it goes.
type SectionCursor struct {
	d    *Reader
	base int64  // file offset the section starts at
	buf  []byte // bytes read from base so far
	pos  int    // read position within buf
	eof  bool   // the file ends inside buf, or could not be read past it
	err  error  // why the file could not be read past buf, if not its end
}

// SectionAt returns a SectionCursor positioned at offset.
func (d *Reader) SectionAt(offset int64) *SectionCursor {
	c := &SectionCursor{d: d, base: offset}
	c.fill(sectionWindowStep)
	return c
}

// fill reads n bytes from the section's start, noting whether the file ran
// out first. Only then is a line without an end-of-line marker the file's
// last, rather than cut short by the window.
//
// A read that fails returns less than was read before; what was read before
// stays, so the read position stays inside the window.
func (c *SectionCursor) fill(n int) {
	buf, err := c.d.src.window(c.base, n)
	if len(buf) < len(c.buf) {
		buf = c.buf
	}
	c.buf, c.err = buf, err
	c.eof = len(buf) < n
}

// Err returns the error a read from the file failed with, which ended the
// section early; nil when the section was read to the end of the file.
func (c *SectionCursor) Err() error { return c.err }

// widen grows the window, doubling it so that reading a long section from
// the file stays linear in its length.
func (c *SectionCursor) widen() {
	c.fill(2*len(c.buf) + sectionWindowStep)
}

// ReadLine returns the next line without its end-of-line marker, which may be
// CR, LF or CRLF (ISO 32000-1 7.2.3), or false once the section has been read
// to the end of the file. A line reaching the end of the window is held back
// until the window has been widened far enough to show where it ends.
func (c *SectionCursor) ReadLine() ([]byte, bool) {
	for {
		if i := bytes.IndexAny(c.buf[c.pos:], "\r\n"); i >= 0 {
			end := c.pos + i
			// A CR as the last byte read may be half of a CRLF split by the
			// window; widen before deciding where the next line starts.
			if c.buf[end] == '\r' && end == len(c.buf)-1 && !c.eof {
				c.widen()
				continue
			}
			line := c.buf[c.pos:end]
			c.pos = end + 1
			if c.buf[end] == '\r' && c.pos < len(c.buf) && c.buf[c.pos] == '\n' {
				c.pos++
			}
			return line, true
		}
		if !c.eof {
			c.widen()
			continue
		}
		if c.pos >= len(c.buf) {
			return nil, false
		}
		line := c.buf[c.pos:]
		c.pos = len(c.buf)
		return line, true
	}
}

// Peek returns the next byte without reading it, or false at the end of the
// file.
func (c *SectionCursor) Peek() (byte, bool) {
	if !c.ensure(1) {
		return 0, false
	}
	return c.buf[c.pos], true
}

// Next returns the next n bytes, or false when the file ends before them.
func (c *SectionCursor) Next(n int) ([]byte, bool) {
	if !c.ensure(n) {
		return nil, false
	}
	b := c.buf[c.pos : c.pos+n]
	c.pos += n
	return b, true
}

// Rest returns up to n bytes from the read position on, without advancing
// past them.
func (c *SectionCursor) Rest(n int) []byte {
	c.ensure(n)
	return c.buf[c.pos:min(c.pos+n, len(c.buf))]
}

// ensure widens the window until it holds n bytes past the read position, and
// reports whether the file has them.
func (c *SectionCursor) ensure(n int) bool {
	for c.pos+n > len(c.buf) {
		if c.eof {
			return false
		}
		c.widen()
	}
	return true
}
