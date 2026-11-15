package convert

import (
	"os"
	"path/filepath"
)

// replaceFile renames from over to. Windows refuses to replace a file that is
// open, even one opened sharing deletion, as pdf.Open opens it, but lets such
// a file be renamed. So when the plain rename fails and to exists, to is
// moved aside to a fresh name first, from takes its place, and the old file
// is removed: at once, or, while it is still open, when its last handle
// closes. If from cannot take its place, to is moved back.
func replaceFile(from, to string) error {
	err := os.Rename(from, to)
	if err == nil {
		return nil
	}
	if _, serr := os.Lstat(to); serr != nil {
		return err
	}
	aside, aerr := os.CreateTemp(filepath.Dir(to), ".gopdfrab-old-*.pdf")
	if aerr != nil {
		return err
	}
	aside.Close()
	if os.Rename(to, aside.Name()) != nil {
		os.Remove(aside.Name())
		return err
	}
	if err := os.Rename(from, to); err != nil {
		os.Rename(aside.Name(), to)
		return err
	}
	os.Remove(aside.Name())
	return nil
}
