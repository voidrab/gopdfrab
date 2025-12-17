package pdfrab

import (
	"fmt"
	"io"
	"strconv"
)

// validateStream performs a partial validation of requirements 6.1.7
func (d *Document) validateStream(l *Lexer, dict PDFDict) error {
	if err := l.skipEOL(); err != nil {
		return err
	}

	lengthRef, ok := dict["Length"]
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
