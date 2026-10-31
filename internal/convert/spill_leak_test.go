package convert

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfgen"
)

// isolateTempDir points the process temp directory at a fresh empty dir, so a
// test can assert no spill file survives.
func isolateTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(key, dir)
	}
	return dir
}

func assertEmptyDir(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		t.Errorf("leaked temp file %s", e.Name())
	}
}

func TestSpillWriterAbortRemovesTempFile(t *testing.T) {
	dir := isolateTempDir(t)
	setSpillThreshold(t, 8)
	var sw spillWriter
	if _, err := sw.Write(make([]byte, 64)); err != nil {
		t.Fatal(err)
	}
	if !sw.spilled {
		t.Fatal("writer did not spill under the lowered threshold")
	}
	sw.abort()
	assertEmptyDir(t, dir)
}

// TestSerializeAndVerifyReleasesOutputOnError drives a final verify that fails
// after the output was written (a nil profile): the spilled output must be
// removed and not left on the result the caller is about to discard.
func TestSerializeAndVerifyReleasesOutputOnError(t *testing.T) {
	dir := isolateTempDir(t)
	setSpillThreshold(t, 64)
	doc, err := pdf.OpenBytes(pdfgen.PlainThreeIssue())
	if err != nil {
		t.Fatal(err)
	}
	defer doc.Close()
	graph, err := doc.ResolveGraph()
	if err != nil {
		t.Fatal(err)
	}
	r := &run{doc: doc, trailer: graph.(pdf.PDFDict), graphClean: true}
	if err := r.serializeAndVerify(); err == nil {
		t.Fatal("expected the nil-profile verify to fail")
	}
	if r.cr.backing != nil {
		t.Error("failed serializeAndVerify left its backing on the result")
	}
	assertEmptyDir(t, dir)
}

// TestConvertEachAbortClosesUndeliveredResults pins that results finishing
// after the callback has failed -- which never reach the callback, and so are
// never closed by it -- do not leave spill files behind.
func TestConvertEachAbortClosesUndeliveredResults(t *testing.T) {
	inputs := t.TempDir()
	var paths []string
	for i := range 8 {
		p := filepath.Join(inputs, "in"+string(rune('a'+i))+".pdf")
		if err := os.WriteFile(p, pdfgen.PlainThreeIssue(), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	dir := isolateTempDir(t)
	setSpillThreshold(t, 64)

	sentinel := errors.New("stop")
	err := convertEach(t.Context(), paths, pdf.PDFA1B, Options{Workers: 8}, func(_ int, fr pdf.FileResult[ConvertResult]) error {
		fr.Result.Close()
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want sentinel", err)
	}
	assertEmptyDir(t, dir)
}
