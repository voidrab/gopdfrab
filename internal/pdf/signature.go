package pdf

// ContentsSpan returns the offsets of the first byte of the direct Contents
// string of the dictionary that is object sig, and of the byte after it. ok is
// false when the dictionary cannot be located in the file, such as one held in
// an object stream, and when its Contents is a reference, which veraPDF cannot
// follow either.
func (d *Reader) ContentsSpan(sig PDFRef) (start, end int64, ok bool) {
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

// skipValue consumes one dictionary value, including an "N G R" reference,
// and reports whether it parsed. A bare integer leaves l.pos just after it:
// the lookahead for a generation number is undone, along with any token it
// pushed back.
func skipValue(l *Lexer) bool {
	tok := l.NextToken()
	mark := l.pos
	v, err := parseObject(l, tok)
	if _, isInt := v.(PDFInteger); isInt {
		l.pos, l.pushed = mark, l.pushed[:0]
	}
	return err == nil
}

// RevisionEnd scans from off for the %%EOF that ends a revision: the first one
// alone on its line, outside any stream body. The keywords are matched as bare
// bytes, as veraPDF does. eof is the offset after the %%EOF and the EOL marker
// (LF, CR or CR LF) that may follow. slack is that marker's length, or 0 when
// nothing follows it. With no such %%EOF, eof is the file size and slack is 0.
func (d *Reader) RevisionEnd(off int64) (eof, slack int64) {
	l := d.src.lexer(off)
	for ; l.pos < d.Size(); l.pos++ {
		switch {
		case l.hasPrefix("stream"):
			end, ok := d.indexFrom(l.pos+int64(len("stream")), endstreamKeyword)
			if !ok {
				return d.Size(), 0
			}
			l.pos = end + int64(len(endstreamKeyword)) - 1
		case l.hasPrefix("%%EOF") && l.aloneOnLine():
			l.pos += int64(len("%%EOF"))
			eof = l.pos
			_ = l.skipEOL()
			if l.pos >= d.Size() {
				return l.pos, 0
			}
			return l.pos, l.pos - eof
		}
	}
	return d.Size(), 0
}

// hasPrefix reports whether the bytes at the lexer's position start with p.
func (l *Lexer) hasPrefix(p string) bool {
	for i := range len(p) {
		if b, ok := l.byteAt(l.pos + int64(i)); !ok || b != p[i] {
			return false
		}
	}
	return true
}

// aloneOnLine reports whether only spaces stand between the lexer's position
// and the EOL marker before it. The start of the file counts as no line.
func (l *Lexer) aloneOnLine() bool {
	for i := l.pos - 1; i >= 0; i-- {
		switch b, _ := l.byteAt(i); b {
		case '\n', '\r':
			return true
		case ' ':
		default:
			return false
		}
	}
	return false
}
