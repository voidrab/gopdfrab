package pdf

import (
	"bytes"
	"fmt"
	"io"
)

// validateStream performs a partial validation of requirements 6.1.7 and
// captures the raw stream bytes.
func (d *Reader) validateStream(l *Lexer, dict *PDFDict, objNum int) error {
	// 6.1.7: the 'stream' keyword shall be followed by CRLF or a single LF.
	if d.consumeStreamEOL(l) {
		d.recordStreamFraming(objNum, Checks.Structure.StreamKeywordEOL,
			"'stream' keyword not followed by a single EOL marker")
	}

	lengthRef, ok := dict.Entries["Length"]
	if !ok {
		return fmt.Errorf("stream missing Length")
	}

	lengthObj, err := d.ResolveObject(lengthRef)
	if err != nil {
		return fmt.Errorf("could not resolve stream Length: %v", lengthObj)
	}

	lengthInt, ok := lengthObj.(PDFInteger)
	if !ok {
		return fmt.Errorf("could not parse stream Length")
	}
	length := int(lengthInt)
	if length < 0 {
		return fmt.Errorf("stream Length is negative")
	}

	streamStart := l.pos

	end := streamStart + int64(length)
	if end < streamStart {
		// int64 overflow from a pathologically large declared Length.
		return fmt.Errorf("stream Length overflows file bounds")
	}

	if d.data != nil {
		// Fast path: alias the backing slice directly (mmap or caller buffer).
		if end > int64(len(d.data)) {
			return fmt.Errorf("stream body extends past end of file")
		}
		dict.RawStream = d.data[streamStart:end:end]

		// 6.1.7: Length must not include the EOL before endstream.
		if end+9 <= int64(len(d.data)) && string(d.data[end:end+9]) == "endstream" {
			d.recordStreamFraming(objNum, Checks.Structure.StreamLengthIncludesEOL,
				"stream Length value includes the EOL marker before endstream")
		}
	} else {
		data := make([]byte, length)
		if _, err := d.file.ReadAt(data, streamStart); err != nil {
			return err
		}
		dict.RawStream = data

		var peek [9]byte
		if n, _ := d.file.ReadAt(peek[:], end); n >= 9 && string(peek[:9]) == "endstream" {
			d.recordStreamFraming(objNum, Checks.Structure.StreamLengthIncludesEOL,
				"stream Length value includes the EOL marker before endstream")
		}
	}

	d.checkEndstreamFraming(objNum, streamStart, length)

	// 'endstream' follows the body, normally after a single EOL. Tolerate a
	// little stray framing (some writers leave a stray byte before the EOL) by
	// scanning a bounded window for the keyword rather than demanding it
	// immediately; the declared /Length already fixed the body extent.
	w := d.windowAt(end, 64+len("endstream"))
	k := bytes.Index(w, []byte("endstream"))
	if k < 0 {
		return fmt.Errorf("expected endstream after stream body of object %d", objNum)
	}
	if bytes.IndexFunc(w[:k], func(r rune) bool { return !IsWhitespace(byte(r)) }) >= 0 {
		d.recordStreamFraming(objNum, Checks.Structure.StreamLengthMismatch,
			"stray bytes between the stream body and the endstream keyword")
	}
	posAfter := end + int64(k+len("endstream"))
	if d.data == nil {
		if _, err := d.file.Seek(posAfter, io.SeekStart); err != nil {
			return err
		}
		l.reader.Reset(d.file)
	}
	l.pos = posAfter

	return nil
}

// consumeStreamEOL advances the lexer past the EOL following the 'stream'
// keyword, returning true if non-EOL bytes preceded the line break (6.1.7).
func (d *Reader) consumeStreamEOL(l *Lexer) bool {
	bad := false
	for {
		b, err := l.readByte()
		if err != nil {
			return bad
		}
		if b == '\n' {
			return bad
		}
		if b == '\r' {
			if next, ok := l.peekByte(); ok && next == '\n' {
				l.readByte()
			}
			return bad
		}
		bad = true
	}
}

// WindowAt returns up to n bytes starting at off, without copying when the
// file is in memory. Callers must treat the returned slice as read-only.
func (d *Reader) WindowAt(off int64, n int) []byte {
	return d.windowAt(off, n)
}

// windowAt returns up to n bytes starting at off, from the in-memory buffer
// when available or the underlying source otherwise.
func (d *Reader) windowAt(off int64, n int) []byte {
	if off < 0 {
		return nil
	}
	if d.data != nil {
		if off >= int64(len(d.data)) {
			return nil
		}
		end := min(off+int64(n), int64(len(d.data)))
		return d.data[off:end]
	}
	buf := make([]byte, n)
	read, _ := d.file.ReadAt(buf, off)
	return buf[:read]
}

// checkEndstreamFraming verifies that the endstream keyword is preceded by an
// EOL marker (6.1.7).
func (d *Reader) checkEndstreamFraming(objNum int, streamStart int64, length int) {
	var window []byte
	if d.data != nil {
		end := min(int(streamStart)+length+64, len(d.data))
		window = d.data[streamStart:end]
	} else {
		w := make([]byte, length+64)
		n, _ := d.file.ReadAt(w, streamStart)
		window = w[:n]
	}

	start := max(length-2, 0)
	if start > len(window) {
		return
	}
	rel := bytes.Index(window[start:], []byte("endstream"))
	if rel < 0 {
		return
	}
	idx := start + rel
	if idx == 0 || (window[idx-1] != '\n' && window[idx-1] != '\r') {
		d.recordStreamFraming(objNum, Checks.Structure.EndstreamEOL,
			"endstream keyword not preceded by an EOL marker")
	}
}
