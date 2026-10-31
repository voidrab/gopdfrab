package pdf

import (
	"bytes"
	"fmt"
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
	if end >= streamStart && end <= d.src.size {
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

	// A body in memory aliases the file rather than being copied.
	body, err := d.src.window(streamStart, length)
	if err != nil {
		return err
	}
	dict.SetStream(body)

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
	l.pos = kwPos + int64(len(endstreamKeyword))

	return nil
}

var endstreamKeyword = []byte("endstream")

// indexFrom returns the offset of the first occurrence of pat at or after off.
// For a file read on demand, the search goes in bounded chunks, never over the
// whole remainder at once.
func (d *Reader) indexFrom(off int64, pat []byte) (int64, bool) {
	found := int64(-1)
	d.src.chunks(off, 64<<10, len(pat)-1, func(base int64, buf []byte, _ bool) bool {
		if k := bytes.Index(buf, pat); k >= 0 {
			found = base + int64(k)
			return false
		}
		return true
	})
	return found, found >= 0
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

// consumeStreamEOL advances the lexer past the EOL following the 'stream'
// keyword. It reports whether that EOL is other than CRLF or a single LF
// (6.1.7): non-EOL bytes before the line break, or a lone CR. The stream data
// starts after a lone CR all the same.
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
				return bad
			}
			return true
		}
		bad = true
	}
}

// windowAt returns up to n bytes starting at off, or fewer when the file ends
// or cannot be read.
func (d *Reader) windowAt(off int64, n int) []byte {
	b, _ := d.src.window(off, n)
	return b
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
