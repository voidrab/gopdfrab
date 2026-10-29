package pdf

import (
	"bytes"
	"math"
)

// Whole-file scans for damaged-file recovery. They match bytes directly rather
// than with regular expressions, because RE2 allocates per match and, on the
// seek path, would need the whole file on the heap. These walk the in-memory
// slice directly, or the file source in bounded chunks.

// scanChunk is the read size for chunked scans on the seek path. A var so
// tests can shrink it to exercise matches straddling a chunk boundary.
var scanChunk = 1 << 20

// isLineEnd reports whether b ends a line.
func isLineEnd(b byte) bool { return b == '\n' || b == '\r' }

// isRegexpSpace reports whether b is in the \s class of Go's regexp:
// [\t\n\f\r ].
func isRegexpSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\f' || b == '\r'
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

func isWordByte(b byte) bool {
	return isDigit(b) || b == '_' || (b|0x20 >= 'a' && b|0x20 <= 'z')
}

type headerMatch int

const (
	headerNo   headerMatch = iota
	headerYes              // matched; num is the object number
	headerMore             // buf ended before the match was decided
)

// matchObjHeader tests whether buf[i:] begins with an indirect-object header,
// the regexp `(\d+)\s+\d+\s+obj\b`. atEOF says buf ends at the end of the file;
// otherwise running out of bytes yields headerMore. ok is false when the
// object number overflows int (the header is then skipped, as strconv.Atoi
// rejected it before).
func matchObjHeader(buf []byte, i int, atEOF bool) (num int, ok bool, m headerMatch) {
	more := func() (int, bool, headerMatch) {
		if atEOF {
			return 0, false, headerNo
		}
		return 0, false, headerMore
	}
	j := i
	overflow := false
	for ; j < len(buf) && isDigit(buf[j]); j++ {
		d := int(buf[j] - '0')
		if num > (math.MaxInt-d)/10 {
			overflow = true
		}
		num = num*10 + d
	}
	if j == i {
		return 0, false, headerNo
	}
	for _, class := range []func(byte) bool{isRegexpSpace, isDigit, isRegexpSpace} {
		k := j
		for ; k < len(buf) && class(buf[k]); k++ {
		}
		if k == len(buf) {
			return more()
		}
		if k == j {
			return 0, false, headerNo
		}
		j = k
	}
	if len(buf)-j < 3 {
		if bytes.HasPrefix([]byte("obj"), buf[j:]) {
			return more()
		}
		return 0, false, headerNo
	}
	if string(buf[j:j+3]) != "obj" {
		return 0, false, headerNo
	}
	j += 3
	if j == len(buf) && !atEOF {
		return more()
	}
	if j < len(buf) && isWordByte(buf[j]) {
		return 0, false, headerNo
	}
	return num, !overflow, headerYes
}

// scanObjectHeaders calls fn for every "N G obj" header that starts a line
// (at the start of the file or right after CR or LF), in file order, with the
// object number and the offset of its first digit.
func (d *Reader) scanObjectHeaders(fn func(objNum int, off int64)) {
	if d.data != nil {
		scanHeadersIn(d.data, '\n', true, 0, fn) // the start of the file is a line start
		return
	}
	buf := make([]byte, scanChunk)
	prev := byte('\n') // the start of the file counts as a line start
	for pos := int64(0); pos < d.size; pos += int64(scanChunk) {
		n, _ := d.file.ReadAt(buf, pos)
		if n == 0 {
			return
		}
		atEOF := pos+int64(n) >= d.size
		for _, i := range scanHeadersIn(buf[:n], prev, atEOF, pos, fn) {
			// A header straddling the chunk end: re-read from its start
			// with a growing window until it is decided.
			for size := 256; ; size *= 2 {
				w := d.windowAt(pos+int64(i), size)
				end := pos+int64(i)+int64(len(w)) >= d.size
				num, ok, m := matchObjHeader(w, 0, end)
				if m == headerYes && ok {
					fn(num, pos+int64(i))
				}
				if m != headerMore {
					break
				}
			}
		}
		prev = buf[n-1]
	}
}

// scanHeadersIn reports the headers that start a line in buf, where prev is
// the byte before buf[0] and base is buf's file offset. It returns the indices
// of candidates left undecided because buf ended first.
func scanHeadersIn(buf []byte, prev byte, atEOF bool, base int64, fn func(int, int64)) []int {
	var pending []int
	for i := range buf {
		if !isDigit(buf[i]) {
			continue
		}
		before := prev
		if i > 0 {
			before = buf[i-1]
		}
		if !isLineEnd(before) {
			continue
		}
		num, ok, m := matchObjHeader(buf, i, atEOF)
		switch {
		case m == headerYes && ok:
			fn(num, base+int64(i))
		case m == headerMore:
			pending = append(pending, i)
		}
	}
	return pending
}

// forEachIndex calls fn with the offset of every occurrence of pat in the
// file, in order, until fn returns false. Occurrences may overlap.
func (d *Reader) forEachIndex(pat []byte, fn func(off int64) bool) {
	if len(pat) == 0 {
		return
	}
	if d.data != nil {
		for off := 0; ; {
			k := bytes.Index(d.data[off:], pat)
			if k < 0 || !fn(int64(off+k)) {
				return
			}
			off += k + 1
		}
	}
	// Overlap chunks by len(pat)-1 so a match straddling the boundary is
	// seen, and only report matches starting in this chunk's own span.
	buf := make([]byte, scanChunk+len(pat)-1)
	for pos := int64(0); pos < d.size; pos += int64(scanChunk) {
		n, _ := d.file.ReadAt(buf, pos)
		if n == 0 {
			return
		}
		for off := 0; off < min(n, scanChunk); {
			k := bytes.Index(buf[off:n], pat)
			if k < 0 || off+k >= scanChunk {
				break
			}
			if !fn(pos + int64(off+k)) {
				return
			}
			off += k + 1
		}
	}
}
