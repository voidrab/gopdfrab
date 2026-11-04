package pdf

import "slices"

// ByteRangeCoversRevision reports whether byteRange, the ByteRange of the
// signature dictionary that is object sig, covers the revision that dictionary
// was written in, apart from the signature value itself (PDF/A-2 6.4.3).
//
// The expected range is derived from the file's bytes, as veraPDF does: it
// starts at offset 0, skips exactly the signature dictionary's Contents
// string, and ends after the first %%EOF that follows the next
// cross-reference section (or the Contents string, when no section follows),
// plus at most one EOL marker unless that marker ends the file. A signature dictionary that cannot be located in the file, such
// as one held in an object stream, covers nothing. Neither does one whose
// Contents is a reference, which veraPDF cannot follow.
func (d *Reader) ByteRangeCoversRevision(sig PDFRef, byteRange [4]int64) bool {
	start, end, ok := d.contentsSpan(sig)
	if !ok {
		return false
	}
	if byteRange[0] != 0 || byteRange[1] != start || byteRange[2] != end {
		return false
	}
	eof, slack, atEnd := d.revisionEnd(d.nextXRefSection(end))
	want := eof - end
	if atEnd {
		return byteRange[3] == want
	}
	return byteRange[3] >= want-int64(slack) && byteRange[3] <= want
}

// contentsSpan returns the offsets of the first byte of the direct Contents
// string of the dictionary that is object sig, and of the byte after it.
func (d *Reader) contentsSpan(sig PDFRef) (start, end int64, ok bool) {
	l, ok := d.objectBodyLexer(sig.ObjNum)
	if !ok || l.NextToken().Type != TokenDictStart {
		return 0, 0, false
	}
	for {
		key := l.NextToken()
		if key.Type != TokenName {
			return 0, 0, false
		}
		if key.Value != "Contents" {
			if !skipValue(l) {
				return 0, 0, false
			}
			continue
		}
		l.skipWhitespace()
		start = l.pos
		if t := l.NextToken().Type; t != TokenString && t != TokenHexString {
			return 0, 0, false
		}
		return start, l.pos, true
	}
}

// objectBodyLexer returns a lexer positioned after the "N G obj" header of
// object num, which must be stored uncompressed.
func (d *Reader) objectBodyLexer(num int) (*Lexer, bool) {
	off, ok := d.xrefTable[num]
	if !ok {
		return nil, false
	}
	l := d.src.lexer(off)
	if n, _, _ := l.validateObjectStart(); n != num {
		return nil, false
	}
	return l, true
}

// skipValue consumes one dictionary value, an "N G R" reference included. It
// returns false at a token that cannot start a value.
func skipValue(l *Lexer) bool {
	switch t := l.NextToken(); t.Type {
	case TokenArrayStart:
		_, err := parseArray(l)
		return err == nil
	case TokenDictStart:
		_, err := parseDictionary(l)
		return err == nil
	case TokenInteger:
		skipRefTail(l)
		return true
	case TokenReal, TokenBoolean, TokenString, TokenHexString, TokenName:
		return true
	case TokenKeyword:
		return t.Value == "null"
	}
	return false
}

// skipRefTail consumes the "G R" that turns a just-read integer into a
// reference. When none follows, the lexer is left just after the integer.
func skipRefTail(l *Lexer) {
	mark := l.pos
	gen := l.NextToken()
	if r := l.NextToken(); gen.Type == TokenInteger && r.Type == TokenKeyword && r.Value == "R" {
		return
	}
	l.pos, l.pushed = mark, l.pushed[:0]
}

// XRefSectionOffsets returns the file offsets of the cross-reference section
// that startxref names and of every earlier one its trailers' Prev entries
// chain to, in chain order. A Prev that repeats an offset, or a trailer that
// cannot be read, ends the chain.
func (d *Reader) XRefSectionOffsets() []int64 {
	offsets := []int64{d.xrefOffset}
	prev := d.Trailer().Entries.Get("Prev")
	for {
		p, ok := prev.(PDFInteger)
		if !ok || slices.Contains(offsets, int64(p)) {
			return offsets
		}
		offsets = append(offsets, int64(p))
		trailer, err := d.XRefTrailerAt(int64(p) + d.pdfStart)
		if err != nil {
			return offsets
		}
		prev = trailer.Entries.Get("Prev")
	}
}

// nextXRefSection returns the offset of the first cross-reference section
// after off, or off when none follows.
func (d *Reader) nextXRefSection(off int64) int64 {
	next := int64(-1)
	for _, o := range d.XRefSectionOffsets() {
		if o += d.pdfStart; o > off && (next < 0 || o < next) {
			next = o
		}
	}
	if next < 0 {
		return off
	}
	return next
}

// revisionEnd scans from off for the %%EOF that ends the revision: the first
// one alone on its line, outside any stream body. eof is the offset after it
// and the EOL marker (LF, CR or CR LF) that may follow; slack is that marker's
// length. atEnd reports whether nothing follows, which is also the case when
// no %%EOF is found and eof is the file size.
func (d *Reader) revisionEnd(off int64) (eof int64, slack int, atEnd bool) {
	s := byteScanner{d: d, pos: off}
	inStream := false
	for ; s.ok(); s.pos++ {
		switch {
		case inStream:
			if s.hasPrefix("endstream") {
				inStream = false
				s.pos += int64(len("endstream")) - 1
			}
		case s.hasPrefix("stream"):
			inStream = true
			s.pos += int64(len("stream")) - 1
		case s.hasPrefix("%%EOF") && s.aloneOnLine():
			eof = s.pos + int64(len("%%EOF"))
			switch {
			case s.at(eof) == '\n':
				eof, slack = eof+1, 1
			case s.at(eof) == '\r' && s.at(eof+1) == '\n':
				eof, slack = eof+2, 2
			case s.at(eof) == '\r':
				eof, slack = eof+1, 1
			}
			return eof, slack, eof >= d.Size()
		}
	}
	return d.Size(), 0, true
}

// byteScanner reads a Reader's bytes through a sliding window, so a scan
// never loads more of the file than the window holds.
type byteScanner struct {
	d    *Reader
	pos  int64
	buf  []byte
	base int64
}

const scanWindow = 64 << 10

// at returns the byte at off, which must not be negative, or 0 past the end
// of the file.
func (s *byteScanner) at(off int64) byte {
	if off < s.base || off >= s.base+int64(len(s.buf)) {
		// A window past the end of the file is empty.
		if s.buf, s.base = s.d.windowAt(off, scanWindow), off; len(s.buf) == 0 {
			return 0
		}
	}
	return s.buf[off-s.base]
}

// ok reports whether pos is inside the file.
func (s *byteScanner) ok() bool { return s.pos >= 0 && s.pos < s.d.Size() }

// hasPrefix reports whether the bytes at pos start with p.
func (s *byteScanner) hasPrefix(p string) bool {
	for i := range len(p) {
		if s.at(s.pos+int64(i)) != p[i] {
			return false
		}
	}
	return true
}

// aloneOnLine reports whether only spaces stand between pos and the EOL
// marker before it. The start of the file counts as no line.
func (s *byteScanner) aloneOnLine() bool {
	for i := s.pos - 1; i >= 0; i-- {
		switch s.at(i) {
		case '\n', '\r':
			return true
		case ' ':
		default:
			return false
		}
	}
	return false
}
