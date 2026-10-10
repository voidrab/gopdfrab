package main

import (
	"errors"
	"path/filepath"
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

var (
	gray       = gopdfrab.Checks.Colour.DeviceColourContentStream // 6.2.3.3/2
	xobjColour = gopdfrab.Checks.Colour.DeviceColourSpaceUsage    // 6.2.3.3/1
	font       = gopdfrab.Checks.Font.SimpleNotEmbedded           // 6.3.4/1
	objModel   = gopdfrab.Checks.ObjectModel.MissingRequiredKey   // objmodel/1
)

func TestGroupIssues(t *testing.T) {
	ref4 := &gopdfrab.PDFRef{ObjNum: 4}
	ref5 := &gopdfrab.PDFRef{ObjNum: 5}

	got := groupIssues([]gopdfrab.PDFError{
		issue(objModel, 1, nil, "missing key"),
		issue(gray, 2, ref4, "a"),
		issue(font, 1, ref5, "not embedded"),
		issue(gray, 1, ref4, "a"),
		issue(gray, 0, nil, "a"),
		issue(gray, 1, ref5, "a"),
		issue(gray, 4, ref4, "a"),
		issue(gray, 1, nil, "b"),
		issue(xobjColour, 3, nil, "a"),
	})

	type want struct {
		clause string
		msg    string
		count  int
		pages  string
		objs   int
	}
	wants := []want{
		{"6.2.3.3/1", "a", 1, "3", 0},
		{"6.2.3.3/2", "a", 5, "doc,1-2,4", 2},
		{"6.2.3.3/2", "b", 1, "1", 0},
		{"6.3.4/1", "not embedded", 1, "1", 1},
		{"objmodel/1", "missing key", 1, "1", 0},
	}
	if len(got) != len(wants) {
		t.Fatalf("groupIssues gave %d groups, want %d: %+v", len(got), len(wants), got)
	}
	for i, w := range wants {
		g := got[i]
		have := want{clauseField(g.first.Check()), g.first.Messages()[0], g.count, pageRanges(g.pages), g.objs}
		if have != w {
			t.Errorf("group %d = %+v, want %+v", i, have, w)
		}
	}

	if got := groupIssues(nil); len(got) != 0 {
		t.Errorf("groupIssues(nil) = %+v, want empty", got)
	}
}

func TestFoldIssues(t *testing.T) {
	ref4 := &gopdfrab.PDFRef{ObjNum: 4}
	ref4gen1 := &gopdfrab.PDFRef{ObjNum: 4, GenNum: 1}
	ref5 := &gopdfrab.PDFRef{ObjNum: 5}
	repeat := issue(gray, 1, ref4, "device gray")

	got := foldIssues([]gopdfrab.PDFError{
		repeat,
		issue(gray, 2, ref4, "device gray"), // another page
		repeat,
		issue(gray, 1, ref5, "device gray"),         // another object
		issue(gray, 1, ref4gen1, "device gray"),     // another generation
		issue(gray, 1, nil, "device gray"),          // no object
		issue(gray, 1, ref4, "device gray", "more"), // more messages
		issue(gray, 1, ref4, "device gray; more"),   // same text, one message
		repeat,
	})

	// Sorted by message, then page, then object: no object sorts first.
	type want struct {
		page, obj, gen, count int
		msgs                  int
	}
	wants := []want{
		{1, 0, 0, 1, 1},
		{1, 4, 0, 3, 1},
		{1, 4, 1, 1, 1},
		{1, 5, 0, 1, 1},
		{2, 4, 0, 1, 1},
		{1, 4, 0, 1, 2}, // "device gray" + "more" sorts before "device gray; more"
		{1, 4, 0, 1, 1},
	}
	if len(got) != len(wants) {
		t.Fatalf("foldIssues gave %d entries, want %d: %+v", len(got), len(wants), got)
	}
	for i, w := range wants {
		ref, _ := got[i].issue.ObjectRef()
		have := want{got[i].issue.Page(), ref.ObjNum, ref.GenNum, got[i].count, len(got[i].issue.Messages())}
		if have != w {
			t.Errorf("entry %d = %+v, want %+v", i, have, w)
		}
	}
	if _, ok := got[0].issue.ObjectRef(); ok {
		t.Error("entry 0 should be the issue without an object")
	}

	if got := foldIssues(nil); len(got) != 0 {
		t.Errorf("foldIssues(nil) = %+v, want empty", got)
	}
}

func TestCompareClause(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		sign int
	}{
		{"6.9", "6.10", -1},
		{"6.10", "6.9", 1},
		{"6.2.3", "6.2.3.3", -1},
		{"6.2.3.3", "6.2.3", 1},
		{"6.2", "6.2", 0},
		{"6.9", "objmodel", -1},
		{"objmodel", "6.9", 1},
		{"a", "b", -1},
		{"", "6.1", 1},
	} {
		got := compareClause(tc.a, tc.b)
		if (got < 0 && tc.sign >= 0) || (got > 0 && tc.sign <= 0) || (got == 0 && tc.sign != 0) {
			t.Errorf("compareClause(%q, %q) = %d, want sign %d", tc.a, tc.b, got, tc.sign)
		}
	}
}

func TestPageRanges(t *testing.T) {
	for _, tc := range []struct {
		pages []int
		want  string
	}{
		{nil, ""},
		{[]int{0}, "doc"},
		{[]int{5}, "5"},
		{[]int{2, 3}, "2-3"},
		{[]int{0, 1, 2, 3, 7}, "doc,1-3,7"},
		{[]int{1, 3, 4, 5, 9, 10}, "1,3-5,9-10"},
	} {
		if got := pageRanges(tc.pages); got != tc.want {
			t.Errorf("pageRanges(%v) = %q, want %q", tc.pages, got, tc.want)
		}
	}
}

func TestWriteGroupsLayout(t *testing.T) {
	ref := &gopdfrab.PDFRef{ObjNum: 12}
	groups := groupIssues([]gopdfrab.PDFError{
		issue(gray, 3, ref, `quote " inside`),
		issue(gray, 4, ref, `quote " inside`),
		issue(gopdfrab.Check{}, 0, nil, "first", "second"),
	})

	var b strings.Builder
	writeGroups(&b, groups)
	want := "" +
		"  6.2.3.3/2  count=2  pages=3-4  objs=1  \"quote \\\" inside\"\n" +
		"  -          count=1  pages=doc  objs=0  \"first; second\"\n"
	if b.String() != want {
		t.Errorf("writeGroups:\n got %q\nwant %q", b.String(), want)
	}
}

func TestWriteIssuesLayout(t *testing.T) {
	ref := &gopdfrab.PDFRef{ObjNum: 12, GenNum: 1}
	folded := foldIssues([]gopdfrab.PDFError{
		issue(gray, 3, ref, `quote " inside`),
		issue(gray, 3, ref, `quote " inside`),
		issue(gopdfrab.Check{}, 0, nil, "first", "second"),
	})

	var b strings.Builder
	writeIssues(&b, folded)
	want := "" +
		"  6.2.3.3/2  page=3    obj=12.1  count=2  \"quote \\\" inside\"\n" +
		"  -          page=doc  obj=-     count=1  \"first; second\"\n"
	if b.String() != want {
		t.Errorf("writeIssues:\n got %q\nwant %q", b.String(), want)
	}
}

func TestWriteColumnsEmpty(t *testing.T) {
	var b strings.Builder
	writeColumns(&b, nil)
	if b.Len() != 0 {
		t.Errorf("writeColumns(nil) wrote %q", b.String())
	}
}

// TestPrintVerifyTextLayout pins every line shape of the verify report: the
// profile header, PASS/FAIL/ERROR file lines, grouped and --detail issue
// lines, and the summary.
func TestPrintVerifyTextLayout(t *testing.T) {
	ref := &gopdfrab.PDFRef{ObjNum: 7}
	results := []gopdfrab.FileResult[gopdfrab.Result]{
		{Path: "ok.pdf", Result: gopdfrab.Result{Valid: true}},
		{Path: "bad file.pdf", Result: gopdfrab.Result{Issues: []gopdfrab.PDFError{
			issue(gray, 1, nil, "gray"),
			issue(gray, 2, ref, "gray"),
			issue(gray, 1, nil, "gray"),
		}}},
		{Path: "broken.pdf", Err: errors.New(`not a "PDF"`)},
	}
	head := "profile: pdfa1b\n" +
		"\n" +
		"PASS  ok.pdf\n"
	tail := "ERROR broken.pdf\n" +
		"  error \"not a \\\"PDF\\\"\"\n" +
		"\n" +
		"summary: pass=1 fail=1 error=1 files=3\n"

	for _, tc := range []struct {
		detail bool
		issues string
	}{
		{false, "FAIL  issues=3 distinct=1  bad file.pdf\n" +
			"  6.2.3.3/2  count=3  pages=1-2  objs=1  \"gray\"\n"},
		{true, "FAIL  issues=3 distinct=2  bad file.pdf\n" +
			"  6.2.3.3/2  page=1  obj=-    count=2  \"gray\"\n" +
			"  6.2.3.3/2  page=2  obj=7.0  count=1  \"gray\"\n"},
	} {
		var b strings.Builder
		if code := printVerifyText(results, "pdfa1b", tc.detail, &b); code != exitError {
			t.Errorf("detail=%v: exit=%d, want %d", tc.detail, code, exitError)
		}
		if want := head + tc.issues + tail; b.String() != want {
			t.Errorf("printVerifyText detail=%v:\n got %q\nwant %q", tc.detail, b.String(), want)
		}
		if strings.Contains(b.String(), "PDF/A violation") {
			t.Error("the profile header replaces the per-line \"PDF/A violation\" prefix")
		}
	}
}

func TestPrintConvertTextGroupsResidualAndLost(t *testing.T) {
	lost := issue(gopdfrab.Check{}, 0, &gopdfrab.PDFRef{ObjNum: 9}, "unresolvable")
	cr := gopdfrab.ConvertResult{
		Result: gopdfrab.Result{Issues: []gopdfrab.PDFError{
			issue(gray, 2, nil, "gray"),
			issue(gray, 3, nil, "gray"),
		}},
		Iterations:  4,
		LostObjects: []gopdfrab.PDFError{lost, lost},
	}
	head := "profile: pdf\n" +
		"\n" +
		"input: in.pdf\n" +
		"output: out.pdf\n" +
		"iterations: 4\n"

	for _, tc := range []struct {
		detail bool
		body   string
	}{
		{false, "lost: objects=2 distinct=1\n" +
			"  -  count=2  pages=doc  objs=1  \"unresolvable\"\n" +
			"result: residual issues=2 distinct=1\n" +
			"  6.2.3.3/2  count=2  pages=2-3  objs=0  \"gray\"\n"},
		{true, "lost: objects=2 distinct=1\n" +
			"  -  page=doc  obj=9.0  count=2  \"unresolvable\"\n" +
			"result: residual issues=2 distinct=2\n" +
			"  6.2.3.3/2  page=2  obj=-  count=1  \"gray\"\n" +
			"  6.2.3.3/2  page=3  obj=-  count=1  \"gray\"\n"},
	} {
		var b strings.Builder
		if code := printConvertText("in.pdf", "out.pdf", "pdf", tc.detail, cr, &b); code != exitInvalid {
			t.Errorf("detail=%v: exit=%d, want %d", tc.detail, code, exitInvalid)
		}
		if want := head + tc.body; b.String() != want {
			t.Errorf("printConvertText detail=%v:\n got %q\nwant %q", tc.detail, b.String(), want)
		}
	}
}

// TestDetailFlag drives --detail through the command line: the default report
// groups by check and message, --detail lists locations.
func TestDetailFlag(t *testing.T) {
	_, fail := veraFixture(t)

	_, grouped, _ := runCLI("verify", fail)
	_, detailed, _ := runCLI("verify", "--detail", fail)
	if !strings.Contains(grouped, " pages=") || strings.Contains(grouped, " page=") {
		t.Errorf("verify without --detail should print pages=, got %q", grouped)
	}
	if !strings.Contains(detailed, " page=") || strings.Contains(detailed, " pages=") {
		t.Errorf("verify --detail should print page=, got %q", detailed)
	}

	out := filepath.Join(t.TempDir(), "out.pdf")
	if code, _, stderr := runCLI("convert", "--detail", "-o", out, fail); code != exitValid {
		t.Errorf("convert --detail: exit=%d stderr=%q", code, stderr)
	}
}
