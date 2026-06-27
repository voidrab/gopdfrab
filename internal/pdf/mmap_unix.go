//go:build unix

package pdf

import (
	"os"
	"syscall"
)

// mmapFile maps the file into the address space for zero-copy byte-indexed reads.
// The caller must call unmap() after all reads are done (typically in Close).
func mmapFile(f *os.File, size int64) (data []byte, unmap func() error, err error) {
	if size == 0 {
		return nil, func() error { return nil }, nil
	}
	data, err = syscall.Mmap(int(f.Fd()), 0, int(size), syscall.PROT_READ, syscall.MAP_PRIVATE)
	if err != nil {
		return nil, nil, err
	}
	return data, func() error { return syscall.Munmap(data) }, nil
}
