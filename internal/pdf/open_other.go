//go:build !windows

package pdf

import "os"

// openFile opens path for reading. Unix lets a file open here be renamed,
// replaced or removed meanwhile.
func openFile(path string) (*os.File, error) { return os.Open(path) }
