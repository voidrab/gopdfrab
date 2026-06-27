//go:build !unix

package pdf

import "os"

// mmapFile returns nil on non-unix platforms; callers fall back to the seek-based reader path.
func mmapFile(_ *os.File, _ int64) ([]byte, func() error, error) {
	return nil, func() error { return nil }, nil
}
