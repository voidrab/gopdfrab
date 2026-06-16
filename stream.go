package pdfrab

import (
	"bytes"
	"fmt"
	"io"
	"strconv"
)

// validateStream performs a partial validation of requirements 6.1.7 and
// captures the raw stream bytes.
func (d *Document) validateStream(l *Lexer, dict *PDFDict, objNum int) error {
	// 6.1.7: the 'stream' keyword shall be followed by CRLF or a single LF.
	if d.consumeStreamEOL(l) {
		d.recordStreamFraming(objNum, 4, "'stream' keyword not followed by a single EOL marker")
	}

	lengthRef, ok := dict.Entries["Length"]
	if !ok {
		return fmt.Errorf("stream missing Length")
	}

	lengthObj, err := d.resolveObject(lengthRef)
	if err != nil {
		return fmt.Errorf("could not resolve stream Length: %v", lengthObj)
	}

	var length int
	lengthStr, ok := lengthObj.(PDFString)
	if !ok {
		// retry as int
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
	data := make([]byte, length)
	_, err = d.file.ReadAt(data, streamStart)
	if err != nil {
		return err
	}
	dict.RawStream = data

	// 6.1.7: the endstream keyword shall be preceded by an EOL marker.
	d.checkEndstreamFraming(objNum, streamStart, length)

	if _, err := d.file.Seek(streamStart+int64(length), io.SeekStart); err != nil {
		return err
	}
	l.reader.Reset(d.file)
	l.pos = streamStart + int64(length)

	// Expect endstream
	t := l.NextToken()
	if t.Type != TokenStreamEnd {
		return fmt.Errorf("expected endstream, got: %v", t.Value)
	}

	return nil
}

// consumeStreamEOL advances the lexer past the EOL following the 'stream'
// keyword, returning true if non-EOL bytes preceded the line break (6.1.7).
func (d *Document) consumeStreamEOL(l *Lexer) bool {
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
			if nb, e := l.reader.Peek(1); e == nil && len(nb) > 0 && nb[0] == '\n' {
				l.readByte()
			}
			return bad
		}
		bad = true
	}
}

// checkEndstreamFraming verifies that the endstream keyword is preceded by an
// EOL marker (6.1.7).
func (d *Document) checkEndstreamFraming(objNum int, streamStart int64, length int) {
	window := make([]byte, length+64)
	n, _ := d.file.ReadAt(window, streamStart)
	window = window[:n]

	start := length - 2
	if start < 0 {
		start = 0
	}
	if start > len(window) {
		return
	}
	rel := bytes.Index(window[start:], []byte("endstream"))
	if rel < 0 {
		return
	}
	idx := start + rel
	if idx == 0 || (window[idx-1] != '\n' && window[idx-1] != '\r') {
		d.recordStreamFraming(objNum, 5, "endstream keyword not preceded by an EOL marker")
	}
}
