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
		d.recordStreamFraming(objNum, DiagStreamKeywordEOL,
			"'stream' keyword not followed by a single EOL marker")
	}

	lengthRef, ok := dict.Entries.Lookup("Length")
	if !ok {
		return fmt.Errorf("stream missing Length")
	}

	lengthObj, err := d.ResolveObject(lengthRef)
	if err != nil {
		return fmt.Errorf("could not resolve stream Length: %w", err)
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

	// 'endstream' follows the body, normally after a single EOL. Tolerate a
	// little stray framing (some writers leave a stray byte before the EOL) by
	// scanning a bounded window for the keyword rather than demanding it
	// immediately.
	end := streamStart + int64(length)
	kwPos, stray := int64(-1), false
	// end < streamStart is int64 overflow from a pathologically large Length.
	if end >= streamStart && end <= d.sourceSize() {
		w := d.windowAt(end, 64+len(endstreamKeyword))
		if k := bytes.Index(w, endstreamKeyword); k >= 0 {
			kwPos = end + int64(k)
			stray = bytes.IndexFunc(w[:k], func(r rune) bool { return !IsWhitespace(byte(r)) }) >= 0
		}
	}
	recovered := false
	if kwPos < 0 {
		// /Length is wrong. Dropping the stream would lose the object (often
		// page content), so take the extent from the first endstream keyword
		// instead, as other readers do, and report the mismatch (6.1.7).
		// Locating the keyword first also keeps an untrusted /Length from ever
		// sizing an allocation.
		k, ok := d.indexFrom(streamStart, endstreamKeyword)
		if !ok {
			return fmt.Errorf("expected endstream after stream body of object %d", objNum)
		}
		kwPos, recovered = k, true
		end = d.trimTrailingEOL(streamStart, kwPos)
		length = int(end - streamStart)
	}

	if d.data != nil {
		// Fast path: alias the backing slice directly (mmap or caller buffer).
		dict.RawStream = d.data[streamStart:end:end]
	} else {
		data := make([]byte, length)
		if _, err := d.file.ReadAt(data, streamStart); err != nil {
			return err
		}
		dict.RawStream = data
	}

	// 6.1.7: Length must not include the EOL before endstream.
	if !recovered && kwPos == end {
		d.recordStreamFraming(objNum, DiagStreamLengthIncludesEOL,
			"stream Length value includes the EOL marker before endstream")
	}

	d.checkEndstreamFraming(objNum, streamStart, length)

	switch {
	case recovered:
		d.recordStreamFraming(objNum, DiagStreamLengthMismatch,
			"stream Length does not match the body; extent recovered from the endstream keyword")
	case stray:
		d.recordStreamFraming(objNum, DiagStreamLengthMismatch,
			"stray bytes between the stream body and the endstream keyword")
	}
	posAfter := kwPos + int64(len(endstreamKeyword))
	if d.data == nil {
		if _, err := d.file.Seek(posAfter, io.SeekStart); err != nil {
			return err
		}
		l.reader.Reset(d.file)
	}
	l.pos = posAfter

	return nil
}

var endstreamKeyword = []byte("endstream")

// indexFrom returns the offset of the first occurrence of pat at or after off.
// The seek path reads in bounded chunks, never the whole remainder.
func (d *Reader) indexFrom(off int64, pat []byte) (int64, bool) {
	if d.data != nil {
		if off > int64(len(d.data)) {
			return 0, false
		}
		k := bytes.Index(d.data[off:], pat)
		return off + int64(k), k >= 0
	}
	const chunk = 64 << 10
	buf := make([]byte, chunk+len(pat)-1)
	for pos := off; pos < d.size; pos += chunk {
		n, _ := d.file.ReadAt(buf, pos)
		if n == 0 {
			break
		}
		if k := bytes.Index(buf[:n], pat); k >= 0 {
			return pos + int64(k), true
		}
	}
	return 0, false
}

// trimTrailingEOL returns kwPos moved back over the single EOL marker (CRLF,
// LF or CR) that conventionally precedes endstream, never before start.
func (d *Reader) trimTrailingEOL(start, kwPos int64) int64 {
	w := d.windowAt(max(kwPos-2, start), int(kwPos-max(kwPos-2, start)))
	switch {
	case len(w) == 2 && w[0] == '\r' && w[1] == '\n':
		return kwPos - 2
	case len(w) > 0 && (w[len(w)-1] == '\n' || w[len(w)-1] == '\r'):
		return kwPos - 1
	}
	return kwPos
}

// sourceSize is the byte length of the underlying file.
func (d *Reader) sourceSize() int64 {
	if d.data != nil {
		return int64(len(d.data))
	}
	return d.size
}

// consumeStreamEOL advances the lexer past the EOL following the 'stream'
// keyword. It reports whether non-EOL bytes preceded the line break (6.1.7).
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
	// Only the tail of the body matters: read from one byte before the search
	// start (to see what precedes the keyword) rather than the whole stream.
	start := max(length-2, 0)
	ws := max(start-1, 0)
	window := d.windowAt(streamStart+int64(ws), length+64-ws)
	off := start - ws
	if off > len(window) {
		return
	}
	rel := bytes.Index(window[off:], []byte("endstream"))
	if rel < 0 {
		return
	}
	i := off + rel
	if ws+i == 0 || (window[i-1] != '\n' && window[i-1] != '\r') {
		d.recordStreamFraming(objNum, DiagEndstreamEOL,
			"endstream keyword not preceded by an EOL marker")
	}
}
