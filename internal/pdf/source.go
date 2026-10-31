package pdf

import (
	"errors"
	"io"
)

// source holds the bytes of the file a Reader parses. Either the whole file is
// in memory (mapped, or the slice OpenBytes was given) or it is read from the
// file on demand. Every read of the file goes through source, so the two cases
// differ nowhere else.
type source struct {
	mem  []byte      // the whole file; nil when it is read on demand
	file io.ReaderAt // read when mem is nil
	size int64
}

// window returns up to n bytes from off on, fewer at the end of the file. Bytes
// from memory alias the file and must not be changed; bytes read from the file
// are a fresh copy. The error is why the file could not be read, including its
// ending before size.
func (s *source) window(off int64, n int) ([]byte, error) {
	if off < 0 || off >= s.size || n <= 0 {
		return nil, nil
	}
	end := off + min(int64(n), s.size-off)
	if s.mem != nil {
		return s.mem[off:end:end], nil
	}
	buf := make([]byte, end-off)
	read, err := s.file.ReadAt(buf, off)
	switch {
	case read == len(buf):
		err = nil // a full read may come with io.EOF
	case errors.Is(err, io.EOF):
		err = io.ErrUnexpectedEOF
	}
	return buf[:read], err
}

// chunks calls fn with the file from off on, in order, until fn returns false.
// From memory the rest of the file is one piece. Read from the file, the pieces
// start chunk bytes apart and each runs overlap bytes into the next, so a match
// of up to overlap+1 bytes is whole in at least one piece. atEOF reports that a
// piece reaches the end of the file; it is the last one.
func (s *source) chunks(off int64, chunk, overlap int, fn func(base int64, buf []byte, atEOF bool) bool) {
	if off < 0 || off >= s.size {
		return
	}
	if s.mem != nil {
		fn(off, s.mem[off:], true)
		return
	}
	buf := make([]byte, chunk+overlap)
	for pos := off; pos < s.size; pos += int64(chunk) {
		n, _ := s.file.ReadAt(buf, pos)
		if n == 0 {
			return
		}
		atEOF := pos+int64(n) >= s.size
		if !fn(pos, buf[:n], atEOF) || atEOF {
			return
		}
	}
}
