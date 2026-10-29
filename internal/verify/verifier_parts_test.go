package verify

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfgen"
)

// failingSource is a file source whose every read fails, driving the
// error branches of byte-level checks on file-backed readers.
type failingSource struct{}

func (failingSource) Read([]byte) (int, error)          { return 0, errors.New("read failed") }
func (failingSource) ReadAt([]byte, int64) (int, error) { return 0, errors.New("read failed") }
func (failingSource) Seek(int64, int) (int64, error)    { return 0, errors.New("seek failed") }
func (failingSource) Close() error                      { return nil }

// TestVerifyStructuralGuards pins the profile guards of the structural-only
// verify entry point.
func TestVerifyStructuralGuards(t *testing.T) {
	d := pdf.NewRawReader(failingSource{}, pdf.NewPDFDict(), 0, 0)

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
	ctx := &ValidationContext{Spec: a1bSpec}
	if p := ctx.type1ProgramFor(nil); p.Names != nil || p.Widths != nil {
		t.Errorf("type1ProgramFor(nil) = %+v, want zero value", p)
	}

	font := buildType1Font()
	p1 := ctx.type1ProgramFor(font)
	if len(p1.Names) != 1 || p1.Names[0] != "A" || p1.Widths["A"] != 500 {
		t.Fatalf("type1ProgramFor = names %v widths %v, want [A] and A:500", p1.Names, p1.Widths)
	}
	p2 := ctx.type1ProgramFor(font)
	if &p1.Names[0] != &p2.Names[0] {
		t.Error("second type1ProgramFor call did not return the cached extraction")
	}
}

// partsTestDoc is a minimal one-page document with no PDF/A metadata and no
// binary header comment, so it fails both byte-level and graph-level checks.
func partsTestDoc() []byte {
	b := pdfgen.NewBuilder("%PDF-1.4\n")
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.Obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] >>")
	return b.FinishClassic("<< /Size 4 /Root 1 0 R >>")
}

func openPartsTestDoc(t *testing.T) *pdf.Reader {
	t.Helper()
	d, err := pdf.OpenBytes(partsTestDoc())
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func issueKeys(issues []pdf.PDFError) []string {
	out := make([]string, len(issues))
	for i, e := range issues {
		out[i] = fmt.Sprintf("%s/%d %v", e.Check().Clause(), e.Check().Subclause(), e.Messages())
	}
	return out
}

// TestVerifyPartsMatchesVerify checks that the split entry point reports
// exactly the issues Verify does, in the same order, and that the split puts
// byte-level findings outside Graph.
func TestVerifyPartsMatchesVerify(t *testing.T) {
	res, err := Verify(openPartsTestDoc(t), pdf.PDFA1B)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if res.Valid || len(res.Issues) == 0 {
		t.Fatal("test document unexpectedly verifies clean")
	}
	pt := VerifyParts(openPartsTestDoc(t), pdf.PDFA1B, a1b())
	if got, want := issueKeys(pt.Issues()), issueKeys(res.Issues); !slices.Equal(got, want) {
		t.Errorf("VerifyParts issues differ from Verify:\n got %v\nwant %v", got, want)
	}
	if len(pt.Graph) == 0 {
		t.Error("Graph part is empty; the document lacks XMP metadata")
	}
}

// TestVerifyPartsFiltersByProfile checks that every part is narrowed to the
// profile's enabled checks.
func TestVerifyPartsFiltersByProfile(t *testing.T) {
	prof := pdf.NewProfile(pdf.A1B).AddCheck(pdf.Checks.Structure.FileHeaderComment)
	pt := VerifyParts(openPartsTestDoc(t), prof, a1b())
	issues := pt.Issues()
	if len(issues) == 0 {
		t.Fatal("expected the binary-comment finding")
	}
	for _, e := range issues {
		if e.Check() != pdf.Checks.Structure.FileHeaderComment {
			t.Errorf("unexpected check %v survived the profile filter", e.Check().Clause())
		}
	}
	if len(pt.Graph) != 0 {
		t.Errorf("Graph = %v, want empty under a byte-level-only profile", issueKeys(pt.Graph))
	}
}

// TestVerifyStructuralSkipsGraphChecks checks that VerifyStructural yields the
// byte-level parts VerifyParts does and nothing from the graph, and that an
// object-model-only profile has no byte-level checks to run.
func TestVerifyStructuralSkipsGraphChecks(t *testing.T) {
	full := VerifyParts(openPartsTestDoc(t), pdf.PDFA1B, a1b())
	st, err := VerifyStructural(openPartsTestDoc(t), pdf.PDFA1B)
	if err != nil {
		t.Fatalf("VerifyStructural: %v", err)
	}
	if len(st.Graph) != 0 {
		t.Errorf("VerifyStructural reported graph issues: %v", issueKeys(st.Graph))
	}
	if !slices.Equal(issueKeys(st.PreStructural), issueKeys(full.PreStructural)) ||
		!slices.Equal(issueKeys(st.PostStructural), issueKeys(full.PostStructural)) {
		t.Errorf("structural parts differ from VerifyParts:\n pre  %v vs %v\n post %v vs %v",
			issueKeys(st.PreStructural), issueKeys(full.PreStructural),
			issueKeys(st.PostStructural), issueKeys(full.PostStructural))
	}
	if len(st.PreStructural) == 0 {
		t.Error("expected a header finding in the pre-structural part")
	}

	om, err := VerifyStructural(openPartsTestDoc(t), pdf.ObjectModelOnly())
	if err != nil {
		t.Fatalf("VerifyStructural(ObjectModelOnly): %v", err)
	}
	if n := len(om.Issues()); n != 0 {
		t.Errorf("ObjectModelOnly structural pass reported %d issues, want 0", n)
	}
}

// TestResultFromIssues pins the Result built from an already-filtered list.
func TestResultFromIssues(t *testing.T) {
	if r := ResultFromIssues(pdf.PDFA1B, nil); !r.Valid || r.Type != pdf.A1B || len(r.Issues) != 0 {
		t.Errorf("ResultFromIssues(nil) = %+v, want valid A1B", r)
	}
	issues := []pdf.PDFError{pdf.NewError(pdf.Checks.Structure.TrailerEOF, []error{errors.New("x")}, 0, nil)}
	r := ResultFromIssues(pdf.PDFA1B, issues)
	if r.Valid || r.Type != pdf.A1B || len(r.Issues) != 1 {
		t.Errorf("ResultFromIssues(1 issue) = %+v, want invalid A1B with 1 issue", r)
	}
}

// TestVerifyContextCancelledBeforeStart checks that an already-cancelled
// context stops the run before any work and surfaces the context's error.
func TestVerifyContextCancelledBeforeStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := VerifyContext(ctx, openPartsTestDoc(t), pdf.PDFA1B); !errors.Is(err, context.Canceled) {
		t.Errorf("VerifyContext(cancelled) error = %v, want context.Canceled", err)
	}
}

// TestUnsupportedLevelRejected verifies that every verify entry point refuses a
// level it has no checks for, naming it, instead of running nothing.
func TestUnsupportedLevelRejected(t *testing.T) {
	d := pdf.NewRawReader(failingSource{}, pdf.NewPDFDict(), 0, 0)
	for _, level := range []pdf.LevelType{"A-2b", ""} {
		p := pdf.NewFullProfile(level)
		want := fmt.Sprintf("unsupported conformance level %q", string(level))
		if _, err := Verify(d, p); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Verify(%q) err = %v, want %q", level, err, want)
		}
		if _, err := VerifyStructural(d, p); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("VerifyStructural(%q) err = %v, want %q", level, err, want)
		}
	}
}

// TestPlainPDFLevelRejectsPDFAChecks verifies that the object-model level,
// which has no PDF/A rules, refuses a PDF/A check enabled on it, so the check
// has nothing to be judged by. Every entry point refuses the profile, naming
// the check, rather than quietly judging it by PDF/A-1b's rules.
func TestPlainPDFLevelRejectsPDFAChecks(t *testing.T) {
	d := pdf.NewRawReader(failingSource{}, pdf.NewPDFDict(), 0, 0)
	p := pdf.ObjectModelOnly().AddCheck(pdf.Checks.Transparency.BlendMode)
	want := "BlendMode is not a check of conformance level \"ObjectModel\""
	if _, err := Verify(d, p); err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("Verify err = %v, want %q", err, want)
	}
	if _, err := VerifyStructural(d, p); err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("VerifyStructural err = %v, want %q", err, want)
	}
}
