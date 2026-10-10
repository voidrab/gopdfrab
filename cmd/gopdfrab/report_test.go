package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/voidrab/gopdfrab"
	"github.com/voidrab/gopdfrab/internal/pdf"
)

// issue builds a PDFError the way the verifier does, for printer tests that
// need exact control over clause, page, object and messages.
func issue(c gopdfrab.Check, page int, ref *gopdfrab.PDFRef, msgs ...string) gopdfrab.PDFError {
	errs := make([]error, len(msgs))
	for i, m := range msgs {
		errs[i] = errors.New(m)
	}
	return pdf.NewError(c, errs, page, ref)
}

func TestFoldIssues(t *testing.T) {
	gray := gopdfrab.Checks.Colour.DeviceColourContentStream
	ref4 := &gopdfrab.PDFRef{ObjNum: 4}
	ref5 := &gopdfrab.PDFRef{ObjNum: 5}
	repeat := issue(gray, 1, ref4, "device gray")

	got := foldIssues([]gopdfrab.PDFError{
		repeat,
		issue(gray, 2, ref4, "device gray"), // another page
		repeat,
		issue(gray, 1, ref5, "device gray"), // another object
		issue(gray, 1, nil, "device gray"),  // no object
		issue(gray, 1, ref4, "device gray", "more"), // more messages
		issue(gray, 1, ref4, "device gray; more"),   // same text, one message
		repeat,
	})

	wantCounts := []int{3, 1, 1, 1, 1, 1}
	if len(got) != len(wantCounts) {
		t.Fatalf("foldIssues gave %d entries, want %d: %+v", len(got), len(wantCounts), got)
	}
	for i, want := range wantCounts {
		if got[i].count != want {
			t.Errorf("entry %d: count=%d, want %d", i, got[i].count, want)
		}
	}
	if got[0].issue.Page() != 1 || got[1].issue.Page() != 2 {
		t.Errorf("entries are not in order of first occurrence: %+v", got)
	}

	if got := foldIssues(nil); len(got) != 0 {
		t.Errorf("foldIssues(nil) = %+v, want empty", got)
	}
}

func TestWriteIssuesLayout(t *testing.T) {
	gray := gopdfrab.Checks.Colour.DeviceColourContentStream
	ref := &gopdfrab.PDFRef{ObjNum: 12, GenNum: 1}
	folded := foldIssues([]gopdfrab.PDFError{
		issue(gray, 3, ref, `quote " inside`),
		issue(gray, 3, ref, `quote " inside`),
		issue(gopdfrab.Check{}, 0, nil, "first", "second"),
	})

	var b strings.Builder
	writeIssues(&b, folded)
	clause := "6.2.3.3/2"
	pad := strings.Repeat(" ", len(clause)-1)
	want := "  " + clause + "  page=3    obj=12.1  count=2  \"quote \\\" inside\"\n" +
		"  -" + pad + "  page=doc  obj=-     count=1  \"first; second\"\n"
	if b.String() != want {
		t.Errorf("writeIssues:\n got %q\nwant %q", b.String(), want)
	}
}

// TestPrintVerifyTextLayout pins every line shape of the verify report: the
// profile header, PASS/FAIL/ERROR file lines, folded issues and the summary.
func TestPrintVerifyTextLayout(t *testing.T) {
	gray := gopdfrab.Checks.Colour.DeviceColourContentStream
	dup := issue(gray, 1, nil, "gray")
	results := []gopdfrab.FileResult[gopdfrab.Result]{
		{Path: "ok.pdf", Result: gopdfrab.Result{Valid: true}},
		{Path: "bad file.pdf", Result: gopdfrab.Result{Issues: []gopdfrab.PDFError{dup, dup, dup}}},
		{Path: "broken.pdf", Err: errors.New(`not a "PDF"`)},
	}

	var b strings.Builder
	if code := printVerifyText(results, "pdfa1b", &b); code != exitError {
		t.Errorf("exit=%d, want %d", code, exitError)
	}
	clause := "6.2.3.3/2"
	want := "profile: pdfa1b\n" +
		"\n" +
		"PASS  ok.pdf\n" +
		"FAIL  issues=3 distinct=1  bad file.pdf\n" +
		"  " + clause + "  page=1  obj=-  count=3  \"gray\"\n" +
		"ERROR broken.pdf\n" +
		"  error \"not a \\\"PDF\\\"\"\n" +
		"\n" +
		"summary: pass=1 fail=1 error=1 files=3\n"
	if b.String() != want {
		t.Errorf("printVerifyText:\n got %q\nwant %q", b.String(), want)
	}
	if strings.Contains(b.String(), "PDF/A violation") {
		t.Error("the profile header replaces the per-line \"PDF/A violation\" prefix")
	}
}

func TestPrintConvertTextFoldsResidualAndLost(t *testing.T) {
	gray := gopdfrab.Checks.Colour.DeviceColourContentStream
	dup := issue(gray, 2, nil, "gray")
	lost := issue(gopdfrab.Check{}, 0, &gopdfrab.PDFRef{ObjNum: 9}, "unresolvable")
	cr := gopdfrab.ConvertResult{
		Result:      gopdfrab.Result{Issues: []gopdfrab.PDFError{dup, dup}},
		Iterations:  4,
		LostObjects: []gopdfrab.PDFError{lost, lost},
	}

	var b strings.Builder
	if code := printConvertText("in.pdf", "out.pdf", "pdf", cr, &b); code != exitInvalid {
		t.Errorf("exit=%d, want %d", code, exitInvalid)
	}
	clause := "6.2.3.3/2"
	want := "profile: pdf\n" +
		"\n" +
		"input: in.pdf\n" +
		"output: out.pdf\n" +
		"iterations: 4\n" +
		"lost: objects=2 distinct=1\n" +
		"  -  page=doc  obj=9.0  count=2  \"unresolvable\"\n" +
		"result: residual issues=2 distinct=1\n" +
		"  " + clause + "  page=2  obj=-  count=2  \"gray\"\n"
	if b.String() != want {
		t.Errorf("printConvertText:\n got %q\nwant %q", b.String(), want)
	}
}
