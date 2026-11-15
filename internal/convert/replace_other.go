//go:build !windows

package convert

import "os"

// replaceFile renames from over to. Unix replaces to even while it is open.
func replaceFile(from, to string) error { return os.Rename(from, to) }
