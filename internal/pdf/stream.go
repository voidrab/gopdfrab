package pdf

import (
	"bytes"
	"fmt"
	"io"
	"strconv"
)

// validateStream performs a partial validation of requirements 6.1.7 and
// captures the raw stream bytes.
func (d *Reader) validateStream(l *Lexer, dict *PDFDict, objNum int) error {
	// 6.1.7: the 'stream' keyword shall be followed by CRLF or a single LF.
	if d.consumeStreamEOL(l) {
		d.recordStreamFraming(objNum, "StreamKeywordEOL", "'stream' keyword not followed by a single EOL marker")
	}

	lengthRef, ok := dict.Entries["Length"]
	if !ok {
		return fmt.Errorf("stream missing Length")
	}

	lengthObj, err := d.ResolveObject(lengthRef)
	if err != nil {
		return fmt.Errorf("could not resolve stream Length: %v", lengthObj)
	}

	var length int
	lengthStr, ok := lengthObj.(PDFString)
	if !ok {
		lengthInt, ok := lengthObj.(PDFInteger)
		if !ok {
			return fmt.Errorf("could not parse stream Length")
		}
		length = int(lengthInt)
	} else {
		length, err = strconv.Atoi(lengthStr.Value)
		if err != nil {
			return fmt.Errorf("could not parse stream Length as integer: %v", err)
		}
	}

	streamStart := l.pos

	if d.data != nil {
		// Fast path: alias the backing slice directly (mmap or caller buffer).
		end := streamStart + int64(length)
		if end > int64(len(d.data)) {
			return fmt.Errorf("stream body extends past end of file")
		}
		dict.RawStream = d.data[streamStart:end:end]

		// 6.1.7: Length must not include the EOL before endstream.
		if end+9 <= int64(len(d.data)) && string(d.data[end:end+9]) == "endstream" {
			d.recordStreamFraming(objNum, "StreamLengthIncludesEOL", "stream Length value includes the EOL marker before endstream")
		}

		d.checkEndstreamFraming(objNum, streamStart, length)
		l.pos = end
	} else {
		data := make([]byte, length)
		if _, err := d.file.ReadAt(data, streamStart); err != nil {
			return err
		}
		dict.RawStream = data

		var peek [9]byte
		if n, _ := d.file.ReadAt(peek[:], streamStart+int64(length)); n >= 9 && string(peek[:9]) == "endstream" {
			d.recordStreamFraming(objNum, "StreamLengthIncludesEOL", "stream Length value includes the EOL marker before endstream")
		}

		d.checkEndstreamFraming(objNum, streamStart, length)

		if _, err := d.file.Seek(streamStart+int64(length), io.SeekStart); err != nil {
			return err
		}
		l.reader.Reset(d.file)
		l.pos = streamStart + int64(length)
	}

	t := l.NextToken()
	if t.Type != TokenStreamEnd {
		return fmt.Errorf("expected endstream, got: %v", t.Value)
	}

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
		d.recordStreamFraming(objNum, "EndstreamEOL", "endstream keyword not preceded by an EOL marker")
	}
}
