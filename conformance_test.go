// Conformance suites: Isartor (fail files) and veraPDF (pass + fail files),
// each asserted against the clause its filename targets.

package gopdfrab

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const isartorDir = "tests/Isartor/PDFA-1b"

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
		out = append(out, iss.Check().Clause())
	}
	return out
}

// TestIsartorSuite runs every negative PDF in the Isartor test suite and asserts
// each is rejected by the clause it is designed to test.
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

			res, verr := doc.Verify(Legacy1B)
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
				if clauseMatches(iss.Check().Clause(), expected) {
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

const veraDir = "tests/veraPDF/PDF_A-1b"

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

				res, verr := doc.Verify(PDFA1B)
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

			res, verr := doc.Verify(PDFA1B)
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

// differentialDeviations lists files where gopdfrab's verdict deliberately
// differs from the veraPDF binary's, keyed by corpus-relative path with the
// justification as the value. Every entry must explain why gopdfrab is right
// (or at least defensible); an undocumented disagreement fails the test.
var differentialDeviations = map[string]string{}

// TestDifferentialVeraPDFCorpora runs the bundled veraPDF binary over both
// committed conformance corpora in one batch each and diffs its verdict
// against gopdfrab's, file by file. This compares against veraPDF the
// implementation, not the suite's filename expectations (which
// TestVeraPDFSuite/TestIsartorSuite already assert): a verdict disagreement is
// a gopdfrab bug or a documented deviation in differentialDeviations. Clause
// sets are diffed as diagnostics only, since check granularity legitimately
// differs between implementations.
func TestDifferentialVeraPDFCorpora(t *testing.T) {
	if testing.Short() {
		t.Skip("differential cross-check skipped in short mode")
	}
	if _, err := os.Stat(veraPDFBin); err != nil {
		t.Skipf("veraPDF reference verifier not available: %v", err)
	}

	for _, dir := range []string{veraDir, isartorDir} {
		if _, err := os.Stat(dir); err != nil {
			continue
		}
		rep, err := runVeraPDF("--recurse", dir)
		if err != nil {
			t.Fatalf("veraPDF batch over %s: %v", dir, err)
		}
		if len(rep.Jobs.Job) == 0 {
			t.Fatalf("veraPDF batch over %s returned no jobs", dir)
		}

		for _, job := range rep.Jobs.Job {
			rel, err := filepath.Rel(dir, job.Item.Name)
			if err != nil {
				abs, _ := filepath.Abs(dir)
				rel, _ = filepath.Rel(abs, job.Item.Name)
			}
			t.Run(rel, func(t *testing.T) {
				if job.Report == nil {
					t.Logf("veraPDF produced no verdict; skipping")
					return
				}
				res, verr := Verify(filepath.Join(dir, rel), PDFA1B)
				if verr != nil {
					t.Fatalf("gopdfrab Verify: %v (veraPDF compliant=%v)", verr, job.Report.IsCompliant)
				}

				veraClauses := map[string]bool{}
				for _, r := range job.Report.Details.Rules {
					if r.Status == "failed" {
						veraClauses[r.Clause] = true
					}
				}

				if res.Valid != job.Report.IsCompliant {
					if why, ok := differentialDeviations[rel]; ok {
						t.Logf("documented deviation (gopdfrab valid=%v, veraPDF compliant=%v): %s",
							res.Valid, job.Report.IsCompliant, why)
						return
					}
					t.Errorf("verdict disagrees with veraPDF: gopdfrab valid=%v (clauses %v), veraPDF compliant=%v (clauses %v)",
						res.Valid, issueClauses(res.Issues), job.Report.IsCompliant, sortedClauses(veraClauses))
					return
				}

				gopClauses := map[string]bool{}
				for _, iss := range res.Issues {
					gopClauses[iss.Check().Clause()] = true
				}
				if match, onlyGop, onlyVera := clauseSetMatches(gopClauses, veraClauses); !match {
					t.Logf("clause sets differ (verdict agrees): only gopdfrab=%v, only veraPDF=%v",
						onlyGop, onlyVera)
				}
			})
		}
	}
}
