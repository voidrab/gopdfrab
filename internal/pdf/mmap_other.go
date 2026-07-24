//go:build !unix

package pdf

import "os"

// mmapFile returns nil on non-unix platforms (notably Windows), so the Reader's
// data slice stays nil and parsing falls back to the incremental seek/ReadAt
// path (NewLexerAt). That path is behaviourally identical to the byte-slice one
// -- exercised on any OS via OpenBytesSeek and the seek-parity tests -- but does
// not get mmap's OS-managed paging, so the larger-than-RAM guarantee is
// unix-only. Implementing Windows file mapping (CreateFileMapping/MapViewOfFile)
// would restore it; see roadmap item 9.
func mmapFile(_ *os.File, _ int64) ([]byte, func() error, error) {
	return nil, func() error { return nil }, nil
}
