package pdf

import (
	"os"
	"syscall"
)

// openFile opens path for reading, sharing it for deletion as well as for
// reading and writing, so that the file can be renamed, replaced or removed
// while a Reader holds it, as on unix. os.Open does not share for deletion,
// so a file open in a Reader could not be replaced, not even by saving a
// converted copy over it.
func openFile(path string) (*os.File, error) {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	h, err := syscall.CreateFile(name, syscall.GENERIC_READ,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(h), path), nil
}
