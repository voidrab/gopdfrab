package convert

import (
	"os"
	"path/filepath"
)

// replaceFile renames from over to. Windows refuses to replace a file that is
// open, even one opened with FILE_SHARE_DELETE as pdf.Open opens it, but
// allows that file to be renamed. So when the plain rename fails and to
// exists, the file at to is renamed aside to a fresh name, from is renamed to
// to, and the aside file is removed. While the old file is still open, its
// removal completes when the last handle closes. If from cannot be renamed to
// to, the file at to is renamed back.
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
