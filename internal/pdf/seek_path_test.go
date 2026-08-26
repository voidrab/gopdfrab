package pdf

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdfgen"
)

// openSeekFile writes data to a temp file and opens it through the ReadAt/seek
// read path with a real *os.File source and no mmap -- the faithful equivalent
// of what Open does on a platform without file mapping (Windows).
func openSeekFile(t *testing.T, data []byte) *Reader {
	t.Helper()
	path := filepath.Join(t.TempDir(), "in.pdf")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	d, err := newDocument(f, info.Size(), nil, nil, nil)
	if err != nil {
		t.Fatalf("newDocument (seek): %v", err)
	}
	return d
}

// TestSeekPathParsesLikeBytes pins the Windows read path at the parse layer:
// with d.data == nil (parsing via NewLexerAt over the file source) resolution
// must succeed or fail exactly as the byte-slice path does and expose the same
// full bytes, driven from both an in-memory source (OpenBytesSeek) and a real
// *os.File, on clean, generated, and damaged inputs. The damaged case exercises
// the ReadAt-backed brute-force recovery scan (scanForObjectHeader/fullBytes).
// Deep graph-content equivalence is proven end-to-end by the verify-result
// parity test (internal/verify), which is cycle-safe where a page graph is not.
func TestSeekPathParsesLikeBytes(t *testing.T) {
	inputs := map[string][]byte{
		"plain":   pdfgen.PlainThreeIssue(),
		"damaged": pdfgen.BreakStartxref(pdfgen.PlainThreeIssue()),
	}
	// A real fixture adds embedded fonts, image streams, and a richer xref;
	// optional so the test still runs without the corpus checked out.
	const corpusFixture = "../../tests/Isartor/PDFA-1b/6.3 Fonts/6.3.6 Font metrics/isartor-6-3-6-t01-fail-b.pdf"
	if b, err := os.ReadFile(corpusFixture); err == nil {
		inputs["corpus"] = b
	}
	for name, data := range inputs {
		t.Run(name, func(t *testing.T) {
			ref, err := OpenBytes(data)
			if err != nil {
				t.Fatalf("OpenBytes: %v", err)
			}
			defer ref.Close()
			_, refErr := ref.ResolveGraph()
			refBytes, _ := ref.FullBytes()

			seekOpeners := map[string]*Reader{
				"OpenBytesSeek": mustOpenBytesSeek(t, data),
				"os.File":       openSeekFile(t, data),
			}
			for src, seek := range seekOpeners {
				func() {
					defer seek.Close()
					if seek.data != nil {
						t.Fatalf("%s: seek reader unexpectedly holds a data slice", src)
					}
					_, seekErr := seek.ResolveGraph()
					if (refErr != nil) != (seekErr != nil) {
						t.Fatalf("%s: resolve error mismatch: bytes %v vs seek %v", src, refErr, seekErr)
					}
					seekBytes, err := seek.FullBytes()
					if err != nil || !bytes.Equal(refBytes, seekBytes) {
						t.Errorf("%s: FullBytes mismatch (err=%v)", src, err)
					}
				}()
			}
		})
	}
}

func mustOpenBytesSeek(t *testing.T, data []byte) *Reader {
	t.Helper()
	d, err := OpenBytesSeek(data)
	if err != nil {
		t.Fatalf("OpenBytesSeek: %v", err)
	}
	return d
}
