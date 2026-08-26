package verify

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// TestSeekPathVerifyMatchesBytes drives the whole verify pipeline over the
// committed corpora through the ReadAt/seek read path (pdf.OpenBytesSeek, the
// path Windows takes with no mmap) and asserts, file by file, the same open
// outcome and the same verify result as the byte-slice path. It is the cycle-
// safe, end-to-end proof that the seek path resolves and is interpreted exactly
// like the mmap/bytes path -- any mis-parse there would move an issue here.
func TestSeekPathVerifyMatchesBytes(t *testing.T) {
	var files []string
	for _, dir := range []string{isartorDir, veraPDFDir} {
		if _, err := os.Stat(dir); err != nil {
			continue
		}
		_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(path, ".pdf") {
				files = append(files, path)
			}
			return nil
		})
	}
	if len(files) == 0 {
		t.Skip("no corpus present")
	}
	if testing.Short() && len(files) > 50 {
		files = files[:50]
	}

	prof := pdf.NewFullProfile(pdf.A1B)
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		bytesOK, bytesRes := verifyThrough(pdf.OpenBytes, data, prof)
		seekOK, seekRes := verifyThrough(pdf.OpenBytesSeek, data, prof)

		name := filepath.Base(path)
		if bytesOK != seekOK {
			t.Errorf("%s: open outcome differs (bytes ok=%v, seek ok=%v)", name, bytesOK, seekOK)
			continue
		}
		if !bytesOK {
			continue
		}
		if bytesRes.Valid != seekRes.Valid {
			t.Errorf("%s: verdict differs (bytes valid=%v, seek valid=%v)", name, bytesRes.Valid, seekRes.Valid)
		}
		if b, s := issueSummary(bytesRes), issueSummary(seekRes); !slices.Equal(b, s) {
			t.Errorf("%s: issues differ via seek path:\n bytes %v\n seek  %v", name, b, s)
		}
	}
}

// verifyThrough opens data with the given opener and verifies it. It reports
// whether the open succeeded and, if so, the result.
func verifyThrough(open func([]byte) (*pdf.Reader, error), data []byte, prof *pdf.Profile) (bool, pdf.Result) {
	d, err := open(data)
	if err != nil {
		return false, pdf.Result{}
	}
	defer d.Close()
	res, err := Verify(d, prof)
	if err != nil {
		return false, pdf.Result{}
	}
	return true, res
}

// issueSummary reduces a result's issues to sorted, order-insensitive strings.
func issueSummary(res pdf.Result) []string {
	out := make([]string, 0, len(res.Issues))
	for _, e := range res.Issues {
		ref, _ := e.ObjectRef()
		out = append(out, fmt.Sprintf("%s|%d|%d|%v|%v",
			e.Check().Clause(), e.Check().Subclause(), e.Page(), ref, e.Messages()))
	}
	slices.Sort(out)
	return out
}
