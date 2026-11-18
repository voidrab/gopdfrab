package pdf

import (
	"bytes"
	"fmt"
	"io"
	"slices"
)

// ContentsSpan returns the offsets of the first byte of the direct Contents
// string of the dictionary that is object sig, and of the byte after it. ok is
// false when the dictionary cannot be located in the file, as for one held in
// an object stream, or when its Contents is a reference, which veraPDF does not
// follow.
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

// ReadRange returns the n bytes of the file from off.
func (d *Reader) ReadRange(off, n int64) ([]byte, error) {
	var buf bytes.Buffer
	if n > 0 {
		buf.Grow(int(n))
	}
	if err := d.CopyRange(&buf, off, n); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// CopyRange writes the n bytes of the file from off to w, such as a hash, a
// piece at a time. It fails for a range the file does not hold.
func (d *Reader) CopyRange(w io.Writer, off, n int64) error {
	if off < 0 || n < 0 || off > d.Size() || n > d.Size()-off {
		return fmt.Errorf("pdf: range %d+%d outside a file of %d bytes", off, n, d.Size())
	}
	const piece = 1 << 20
	for n > 0 {
		b, err := d.src.window(off, int(min(n, piece)))
		if err != nil {
			return err
		}
		if len(b) == 0 {
			return io.ErrUnexpectedEOF
		}
		if _, err := w.Write(b); err != nil {
			return err
		}
		off, n = off+int64(len(b)), n-int64(len(b))
	}
	return nil
}

// RevisionEnds returns where each revision of the file ends, in file order:
// the offset after its %%EOF marker and the EOL marker that may follow, as
// RevisionEnd finds them. Bytes after the last %%EOF are no revision.
func (d *Reader) RevisionEnds() []int64 {
	var ends []int64
	for off := int64(0); off < d.Size(); {
		eof, slack := d.RevisionEnd(off)
		if eof <= off || !d.endsWithEOF(eof-slack) {
			break
		}
		ends = append(ends, eof)
		off = eof
	}
	return ends
}

// endsWithEOF reports whether the bytes before off, less an EOL marker, end
// with %%EOF.
func (d *Reader) endsWithEOF(off int64) bool {
	start := max(off-8, 0)
	b, err := d.src.window(start, int(off-start))
	if err != nil {
		return false
	}
	return bytes.HasSuffix(bytes.TrimRight(b, "\r\n"), []byte("%%EOF"))
}

// ObjectsFrom returns the numbers of the objects whose current definition the
// file holds at or after offset off, in increasing order: those an
// incremental update written there added or replaced. An object kept in an
// object stream counts where its stream is.
func (d *Reader) ObjectsFrom(off int64) []int {
	var nums []int
	for n, at := range d.xrefTable {
		if at >= off {
			nums = append(nums, n)
		}
	}
	for n, e := range d.compressedXref {
		if at, ok := d.xrefTable[e.streamObjNum]; ok && at >= off {
			nums = append(nums, n)
		}
	}
	slices.Sort(nums)
	return slices.Compact(nums)
}

// CoversRevision reports whether byteRange, the ByteRange of the signature
// dictionary that is object sig, covers the revision that dictionary was
// written in, apart from the signature value itself.
//
// The expected range is derived from the file's bytes, as veraPDF does: it
// starts at offset 0, skips exactly the signature dictionary's Contents
// string, and ends after the first %%EOF that follows the next
// cross-reference section (or the Contents string, when no section follows),
// plus at most one EOL marker unless that marker ends the file. A signature
// dictionary whose Contents cannot be located covers nothing. sections holds
// the file's cross-reference sections, as XRefSectionOffsets returns them.
func (d *Reader) CoversRevision(sig PDFRef, byteRange [4]int64, sections []int64) bool {
	start, end, ok := d.ContentsSpan(sig)
	if !ok || byteRange[0] != 0 || byteRange[1] != start || byteRange[2] != end {
		return false
	}
	eof, slack := d.RevisionEnd(nextXRefSection(sections, d.PDFStart(), end))
	want := eof - end
	return byteRange[3] >= want-slack && byteRange[3] <= want
}

// nextXRefSection returns the smallest of sections, each shifted by pdfStart,
// that lies after off, or off when none does.
func nextXRefSection(sections []int64, pdfStart, off int64) int64 {
	next := off
	for _, o := range sections {
		if o += pdfStart; o > off && (next == off || o < next) {
			next = o
		}
	}
	return next
}

// ByteRangeOf returns a signature dictionary's ByteRange, which must be an
// array of four non-negative integers.
func ByteRangeOf(sig PDFDict) (br [4]int64, ok bool) {
	arr, ok := sig.Entries.Get("ByteRange").(PDFArray)
	if !ok || len(arr) != 4 {
		return br, false
	}
	for i, v := range arr {
		n, ok := v.(PDFInteger)
		if !ok || n < 0 {
			return br, false
		}
		br[i] = int64(n)
	}
	return br, true
}

// nopCloser is a file source whose Close leaves the file open, for a Reader
// that shares another's file.
type nopCloser struct{ io.ReaderAt }

func (nopCloser) Close() error { return nil }

// Prefix returns a Reader over the first n bytes of the file, such as an
// earlier revision of it, decrypting with d's password. It shares d's bytes
// without copying them, and its Close leaves d's file open; d must outlive
// it.
func (d *Reader) Prefix(n int64) (*Reader, error) {
	if n < 0 || n > d.Size() {
		return nil, fmt.Errorf("pdf: prefix of %d bytes of a file of %d", n, d.Size())
	}
	var mem []byte
	if d.src.mem != nil {
		mem = d.src.mem[:n:n]
	}
	return newDocument(nopCloser{io.NewSectionReader(d.src.file, 0, n)}, n, mem, nil, d.password)
}

// IsDefined reports whether the cross-reference data define object num.
func (d *Reader) IsDefined(num int) bool {
	if _, ok := d.xrefTable[num]; ok {
		return true
	}
	_, ok := d.compressedXref[num]
	return ok
}
