package pdfrab

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const veraDir = "test documents/veraPDF/PDF_A-1b"

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

// TestVeraPDFSuite runs every PDF in the veraPDF PDF/A-1b corpus: fail files
// must be reported non-conformant (ideally by the matching clause), pass files must validate cleanly.
func TestVeraPDFSuite(t *testing.T) {
	if _, err := os.Stat(veraDir); err != nil {
		t.Skip("veraPDF suite not present")
	}

	var files []string
	err := filepath.WalkDir(veraDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(strings.ToLower(d.Name()), ".pdf") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("failed to walk veraPDF suite: %v", err)
	}

	var (
		total           int
		passClean       int // pass file, Valid==true
		passFalsePos    int // pass file, reported non-conformant (false positive)
		openErrPass     int // pass file, Open failed
		failCaught      int // fail file, non-conformant (any clause)
		failCorrect     int // fail file, non-conformant by correct clause
		failFalseNeg    int // fail file, Valid==true (false negative)
		failWrongClause int // fail file, caught but wrong clause
		openErrFail     int // fail file, Open failed (counts as caught)
	)

	for _, path := range files {
		rel, _ := filepath.Rel(veraDir, path)
		t.Run(rel, func(t *testing.T) {
			total++
			expectedClause, wantFail := veraClauseAndKind(filepath.Base(path))

			if !wantFail {
				// ── PASS file ──────────────────────────────────────────────
				doc, err := Open(path)
				if err != nil {
					openErrPass++
					t.Errorf("Open failed on conformant file: %v", err)
					return
				}
				defer doc.Close()

				res, verr := doc.Verify(PDFA_1B)
				if verr != nil {
					passFalsePos++
					t.Errorf("Verify returned error on conformant file: %v", verr)
					return
				}
				if !res.Valid {
					passFalsePos++
					t.Errorf("false positive: conformant file reported non-conformant; issues: %v",
						issueClauses(res.Issues))
					return
				}
				passClean++
				return
			}

			// ── FAIL file ──────────────────────────────────────────────────
			doc, err := Open(path)
			if err != nil {
				failCaught++
				openErrFail++
				t.Logf("caught at Open (no clause mapping): %v", err)
				return
			}
			defer doc.Close()

			res, verr := doc.Verify(PDFA_1B)
			if verr != nil {
				failCaught++
				t.Logf("Verify returned error (treated as caught): %v", verr)
				return
			}
			if res.Valid {
				failFalseNeg++
				t.Errorf("false negative: expected non-conformant (clause %s) but Valid=true",
					expectedClause)
				return
			}

			failCaught++
			for _, iss := range res.Issues {
				if clauseMatches(iss.Check().Clause(), expectedClause) {
					failCorrect++
					return
				}
			}
			failWrongClause++
			t.Errorf("caught but by wrong clause: expected %s, got %v",
				expectedClause, issueClauses(res.Issues))
		})
	}

	t.Logf("veraPDF scoreboard: total=%d | pass: clean=%d falsePositive=%d openErr=%d | fail: caught=%d correctClause=%d falseNeg=%d wrongClause=%d openErr=%d",
		total, passClean, passFalsePos, openErrPass,
		failCaught, failCorrect, failFalseNeg, failWrongClause, openErrFail)
}
