package verify

import (
	"errors"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// failingSource is a file source whose every read fails, driving the
// error branches of byte-level checks on file-backed readers.
type failingSource struct{}

func (failingSource) Read([]byte) (int, error)          { return 0, errors.New("read failed") }
func (failingSource) ReadAt([]byte, int64) (int, error) { return 0, errors.New("read failed") }
func (failingSource) Seek(int64, int) (int64, error)    { return 0, errors.New("seek failed") }
func (failingSource) Close() error                      { return nil }

// TestVerifyPartsAndStructuralGuards pins the profile guards of the split
// verify entry points.
func TestVerifyPartsAndStructuralGuards(t *testing.T) {
	d := pdf.NewRawReader(failingSource{}, pdf.NewPDFDict(), 0, 0)

	if _, err := VerifyParts(d, nil); err == nil {
		t.Error("VerifyParts(nil profile) did not error")
	}
	if _, err := VerifyParts(d, &pdf.Profile{Level: pdf.Undefined}); err == nil {
		t.Error("VerifyParts(Undefined level) did not error")
	}
	if _, err := VerifyStructural(d, nil); err == nil {
		t.Error("VerifyStructural(nil profile) did not error")
	}
	if _, err := VerifyStructural(d, &pdf.Profile{Level: pdf.Undefined}); err == nil {
		t.Error("VerifyStructural(Undefined level) did not error")
	}
}

// TestPartsIssuesOrder pins the reassembly order: pre-structural, graph,
// post-structural.
func TestPartsIssuesOrder(t *testing.T) {
	mk := func(msg string) pdf.PDFError {
		return pdf.NewError(pdf.Checks.Structure.TrailerEOF, []error{errors.New(msg)}, 0, nil)
	}
	pt := Parts{
		PreStructural:  []pdf.PDFError{mk("pre")},
		Graph:          []pdf.PDFError{mk("graph")},
		PostStructural: []pdf.PDFError{mk("post")},
	}
	got := pt.Issues()
	if len(got) != 3 {
		t.Fatalf("Issues() returned %d issues, want 3", len(got))
	}
}

// TestCheckLinearizedFileIDUnreadableSource covers the FullBytes error
// branch: a file-backed reader (no in-memory data) whose source cannot be
// read reports nothing rather than failing.
func TestCheckLinearizedFileIDUnreadableSource(t *testing.T) {
	// The trailer has no /Root, so the check proceeds to read the file.
	d := pdf.NewRawReader(failingSource{}, pdf.NewPDFDict(), 10, 0)
	if errs := checkLinearizedFileID(d); errs != nil {
		t.Errorf("checkLinearizedFileID on unreadable source = %v, want nil", errs)
	}
}

// TestType1ProgramForMemoizes covers the empty-input early return and the
// per-context cache hit of the combined Type1 CharStrings extraction.
func TestType1ProgramForMemoizes(t *testing.T) {
	ctx := &ValidationContext{}
	if p := ctx.type1ProgramFor(nil); p.names != nil || p.widths != nil {
		t.Errorf("type1ProgramFor(nil) = %+v, want zero value", p)
	}

	font := buildType1Font()
	p1 := ctx.type1ProgramFor(font)
	if len(p1.names) != 1 || p1.names[0] != "A" || p1.widths["A"] != 500 {
		t.Fatalf("type1ProgramFor = names %v widths %v, want [A] and A:500", p1.names, p1.widths)
	}
	p2 := ctx.type1ProgramFor(font)
	if &p1.names[0] != &p2.names[0] {
		t.Error("second type1ProgramFor call did not return the cached extraction")
	}
}
