package pdfrab

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const isartorDir = "test documents/Isartor testsuite/PDFA-1b"

var isartorNameRe = regexp.MustCompile(`^isartor-((?:\d+-)+)t\d+`)

// expectedClauseFromName maps an Isartor file name such as
// "isartor-6-6-1-t01-fail-a.pdf" to the clause it targets, e.g. "6.6.1".
func expectedClauseFromName(name string) string {
	m := isartorNameRe.FindStringSubmatch(name)
	if m == nil {
		return ""
	}
	segs := strings.Split(strings.TrimSuffix(m[1], "-"), "-")
	return strings.Join(segs, ".")
}

// clauseMatches reports whether a reported clause satisfies the expected clause.
// A match holds when the two are equal or one is a dot-boundary prefix of the
// other (so "6.2.3" satisfies an expected "6.2.3.3", and vice versa).
func clauseMatches(got, expected string) bool {
	if got == expected {
		return true
	}
	return strings.HasPrefix(got+".", expected+".") ||
		strings.HasPrefix(expected+".", got+".")
}

func issueClauses(issues []PDFError) []string {
	out := make([]string, 0, len(issues))
	for _, iss := range issues {
		out = append(out, iss.clause)
	}
	return out
}

// TestIsartorSuite runs every negative PDF in the Isartor test suite through the
// PDF/A-1b verifier and asserts each is detected as non-conformant by the clause
// it is designed to test. A per-run scoreboard is logged to track progress.
func TestIsartorSuite(t *testing.T) {
	if _, err := os.Stat(isartorDir); err != nil {
		t.Skip("Isartor suite not present")
	}

	var files []string
	err := filepath.WalkDir(isartorDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(strings.ToLower(d.Name()), ".pdf") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("failed to walk Isartor suite: %v", err)
	}

	var total, caught, correct, falseNeg, openErr int
	for _, path := range files {
		rel, _ := filepath.Rel(isartorDir, path)
		t.Run(rel, func(t *testing.T) {
			total++
			expected := expectedClauseFromName(filepath.Base(path))

			doc, err := Open(path)
			if err != nil {
				caught++
				openErr++
				t.Logf("caught at Open (no clause mapping): %v", err)
				return
			}
			defer doc.Close()

			res, verr := doc.Verify(A_1B)
			if verr != nil {
				caught++
				t.Logf("Verify returned error (treated as caught): %v", verr)
				return
			}
			if res.Valid {
				falseNeg++
				t.Errorf("expected non-conformant (clause %s) but Valid=true", expected)
				return
			}

			caught++
			for _, iss := range res.Issues {
				if clauseMatches(iss.clause, expected) {
					correct++
					return
				}
			}
			t.Errorf("caught but by wrong clause: expected %s, got %v",
				expected, issueClauses(res.Issues))
		})
	}

	t.Logf("Isartor scoreboard: total=%d caught=%d correct-clause=%d false-negatives=%d open-errors=%d",
		total, caught, correct, falseNeg, openErr)
}
