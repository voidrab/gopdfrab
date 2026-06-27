package convert

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// isartorDir and veraDir locate the reference test corpora relative to this
// package's directory (two levels under the repo root).
const (
	isartorDir = "../../tests/Isartor testsuite/PDFA-1b"
	veraDir    = "../../tests/veraPDF/PDF_A-1b"
)

// veraPDF filenames: optional "veraPDF test suite " prefix, then clause
// segments separated by dashes, then -tNN-(pass|fail)-<letter>.pdf, e.g.
// "veraPDF test suite 6-1-2-t01-fail-a.pdf" → clause 6.1.2, fail.
var veraPDFNameRe = regexp.MustCompile(`^(?:veraPDF test suite )?((?:\d+-)+)t\d+-(pass|fail)-`)

// veraClauseAndKind returns the expected clause and whether the file is a
// fail (negative) test. Returns ("", false) if the name does not match.
func veraClauseAndKind(name string) (clause string, wantFail bool) {
	m := veraPDFNameRe.FindStringSubmatch(name)
	if m == nil {
		return "", false
	}
	segs := strings.Split(strings.TrimSuffix(m[1], "-"), "-")
	clause = strings.Join(segs, ".")
	wantFail = m[2] == "fail"
	return clause, wantFail
}

// isartorNameRe matches an Isartor filename like "isartor-6-6-1-t01-fail-a.pdf".
var isartorNameRe = regexp.MustCompile(`^isartor-((?:\d+-)+)t\d+`)

// expectedClauseFromName maps a name like "isartor-6-6-1-t01-fail-a.pdf"
// to its target clause, e.g. "6.6.1".
func expectedClauseFromName(name string) string {
	m := isartorNameRe.FindStringSubmatch(name)
	if m == nil {
		return ""
	}
	segs := strings.Split(strings.TrimSuffix(m[1], "-"), "-")
	return strings.Join(segs, ".")
}

// issueClauses returns the violated clause for each issue, for diagnostics.
func issueClauses(issues []pdf.PDFError) []string {
	out := make([]string, 0, len(issues))
	for _, iss := range issues {
		out = append(out, iss.Check().Clause())
	}
	return out
}

// passFixtures returns every "pass" fixture path in the veraPDF corpus, or
// nil if the corpus isn't present.
func passFixtures(t *testing.T) []string {
	t.Helper()
	if _, err := os.Stat(veraDir); err != nil {
		return nil
	}
	var found []string
	filepath.WalkDir(veraDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() && strings.Contains(strings.ToLower(d.Name()), "-pass-") {
			found = append(found, path)
		}
		return nil
	})
	return found
}

// writeTempPDF writes data to a temp file named name and returns its path.
func writeTempPDF(t *testing.T, name string, data []byte) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

// assertOnePageGraph walks Root -> Pages -> Kids[0] and returns the resolved
// Page dict, failing the test on any structural mismatch.
func assertOnePageGraph(t *testing.T, graph pdf.PDFValue) pdf.PDFDict {
	t.Helper()
	root, ok := graph.(pdf.PDFDict).Entries["Root"].(pdf.PDFDict)
	if !ok {
		t.Fatalf("Root did not resolve to a dict")
	}
	if !pdf.EqualPDFValue(root.Entries["Type"], pdf.PDFName{Value: "Catalog"}) {
		t.Fatalf("Root/Type = %v, want /Catalog", root.Entries["Type"])
	}

	pages, ok := root.Entries["Pages"].(pdf.PDFDict)
	if !ok {
		t.Fatalf("Root/Pages did not resolve to a dict")
	}
	kids, ok := pages.Entries["Kids"].(pdf.PDFArray)
	if !ok || len(kids) != 1 {
		t.Fatalf("Pages/Kids = %v, want a 1-element array", pages.Entries["Kids"])
	}
	page, ok := kids[0].(pdf.PDFDict)
	if !ok {
		t.Fatalf("Kids[0] did not resolve to a dict")
	}
	if !pdf.EqualPDFValue(page.Entries["Type"], pdf.PDFName{Value: "Page"}) {
		t.Fatalf("Kids[0]/Type = %v, want /Page", page.Entries["Type"])
	}
	return page
}

// assertContentStream decodes page's /Contents stream and checks it matches want.
func assertContentStream(t *testing.T, page pdf.PDFDict, want string) {
	t.Helper()
	contents, ok := page.Entries["Contents"].(pdf.PDFDict)
	if !ok || !contents.HasStream {
		t.Fatalf("Page/Contents did not resolve to a stream dict")
	}
	data, err := pdf.DecodeStream(contents)
	if err != nil {
		t.Fatalf("DecodeStream(Contents): %v", err)
	}
	if string(data) != want {
		t.Errorf("content stream = %q, want %q", data, want)
	}
}
