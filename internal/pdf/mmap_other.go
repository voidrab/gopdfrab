//go:build !unix

package pdf

import "os"

// mmapFile returns nil on non-unix platforms (notably Windows), so the Reader's
// src.mem stays nil and the file is read on demand through ReadAt. Parsing
// gives the same results as it does over an in-memory file, which the tests
// check on any OS through OpenBytesSeek and TestSeekPathParsesLikeBytes. The
// on-demand path does not get mmap's OS-managed paging, so the larger-than-RAM
// guarantee is unix-only. Implementing Windows file mapping
// (CreateFileMapping/MapViewOfFile) would restore it.
func mmapFile(_ *os.File, _ int64) ([]byte, func() error, error) {
	return nil, func() error { return nil }, nil
}
