//go:build !unix

package pdf

import "os"

// mmapFile falls back to a heap read on non-unix platforms.
func mmapFile(f *os.File, size int64) (data []byte, unmap func() error, err error) {
	data = make([]byte, size)
	_, err = f.ReadAt(data, 0)
	return data, func() error { return nil }, err
}
