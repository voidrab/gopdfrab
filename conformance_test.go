// Conformance suites over the committed corpora (corpustest.All). Each file is
// checked against the clause its filename targets, and against veraPDF.

package gopdfrab

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/voidrab/gopdfrab/internal/corpustest"
)

// TestConformanceCorpora verifies every file of every committed corpus
// (corpustest.All) through Verify with the corpus's profile. A fail file must
// be reported non-conformant by a check whose clause under the corpus's level
// matches the clause its name targets, and a pass file must validate cleanly.
func TestConformanceCorpora(t *testing.T) {
	for _, c := range corpustest.All {
		t.Run(c.Name, func(t *testing.T) {
			if !c.Present() {
				t.Skipf("%s not present", c.Dir)
			}
			runConformanceCorpus(t, c)
		})
	}
}

func runConformanceCorpus(t *testing.T, c corpustest.Corpus) {
	level := c.Level()
	var (
		passClean, passRejected                 int
		failCaught, failCorrect, failFalseNeg   int
		failWrongClause, openErrFail, unmatched int
		uncovered                               int
	)
	for _, path := range c.Files() {
		t.Run(c.Rel(path), func(t *testing.T) {
			expected, wantFail := c.Expect(path)
			if expected == "" {
				unmatched++
				t.Errorf("name does not follow the corpus's convention")
				return
			}
			if wantFail && !c.Covers(expected) {
				uncovered++
				t.Skipf("clause %s is not covered yet", expected)
			}

			doc, err := Open(path)
			if err != nil {
				if wantFail {
					failCaught++
					openErrFail++
					t.Logf("caught at Open (no clause mapping): %v", err)
					return
				}
				passRejected++
				t.Errorf("Open failed on conformant file: %v", err)
				return
			}
			defer doc.Close()

			res, verr := doc.Verify(c.Profile)
			switch {
			case !wantFail && verr != nil:
				passRejected++
				t.Errorf("Verify returned error on conformant file: %v", verr)
			case !wantFail && !res.Valid:
				passRejected++
				t.Errorf("false positive: conformant file reported non-conformant; issues: %v",
					corpustest.Clauses(res.Issues, level))
			case !wantFail:
				passClean++
			case verr != nil:
				failCaught++
				t.Logf("Verify returned error (treated as caught): %v", verr)
			case res.Valid:
				failFalseNeg++
				t.Errorf("false negative: expected non-conformant (clause %s) but Valid=true", expected)
			case corpustest.AnyMatches(res.Issues, level, expected):
				failCaught++
				failCorrect++
			default:
				failCaught++
				failWrongClause++
				t.Errorf("caught but by wrong clause: expected %s, got %v",
					expected, corpustest.Clauses(res.Issues, level))
			}
		})
	}

	t.Logf("%s scoreboard: pass: clean=%d rejected=%d | fail: caught=%d correctClause=%d falseNeg=%d wrongClause=%d openErr=%d uncovered=%d | unmatched names=%d",
		c.Name, passClean, passRejected,
		failCaught, failCorrect, failFalseNeg, failWrongClause, openErrFail, uncovered, unmatched)
}

// differentialDeviations lists files where gopdfrab's verdict deliberately
// differs from the veraPDF binary's. Each key is the corpus name and the
// corpus-relative path, joined by a slash; each value is the justification.
// Every entry must explain why gopdfrab is right (or at least defensible); an
// undocumented disagreement fails the test.
var differentialDeviations = map[string]string{}

// TestDifferentialVeraPDFCorpora runs the bundled veraPDF binary over each
// committed conformance corpus, in one batch per corpus with the corpus's
// flavour. It diffs each file's verdict against gopdfrab's under the corpus's
// default profile. The comparison is with the veraPDF implementation, not with
// the filename expectations that TestConformanceCorpora asserts. A verdict
// disagreement is a gopdfrab bug unless it is a documented deviation in
// differentialDeviations. Clause sets are diffed as diagnostics only, since
// check granularity legitimately differs between implementations.
func TestDifferentialVeraPDFCorpora(t *testing.T) {
	if testing.Short() {
		t.Skip("differential cross-check skipped in short mode")
	}
	if _, err := os.Stat(veraPDFBin); err != nil {
		t.Skipf("veraPDF reference verifier not available: %v", err)
	}

	for _, c := range corpustest.All {
		if !c.Present() {
			continue
		}
		dir := c.Path()
		level := c.Level()
		rep, err := runVeraPDF(c.Flavour, "--recurse", dir)
		if err != nil {
			t.Fatalf("veraPDF batch over %s: %v", c.Dir, err)
		}
		if len(rep.Jobs.Job) == 0 {
			t.Fatalf("veraPDF batch over %s returned no jobs", c.Dir)
		}

		for _, job := range rep.Jobs.Job {
			rel := c.Rel(job.Item.Name)
			t.Run(c.Name+"/"+rel, func(t *testing.T) {
				if job.Report == nil {
					t.Logf("veraPDF produced no verdict; skipping")
					return
				}
				if expected, wantFail := c.Expect(rel); wantFail && !c.Covers(expected) {
					t.Skipf("clause %s is not covered yet", expected)
				}
				res, verr := Verify(filepath.Join(dir, filepath.FromSlash(rel)), c.Default)
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
					if why, ok := differentialDeviations[c.Name+"/"+rel]; ok {
						t.Logf("documented deviation (gopdfrab valid=%v, veraPDF compliant=%v): %s",
							res.Valid, job.Report.IsCompliant, why)
						return
					}
					t.Errorf("verdict disagrees with veraPDF: gopdfrab valid=%v (clauses %v), veraPDF compliant=%v (clauses %v)",
						res.Valid, corpustest.Clauses(res.Issues, level), job.Report.IsCompliant, sortedClauses(veraClauses))
					return
				}

				gopClauses := map[string]bool{}
				for _, clause := range corpustest.Clauses(res.Issues, level) {
					gopClauses[clause] = true
				}
				if match, onlyGop, onlyVera := clauseSetMatches(gopClauses, veraClauses); !match {
					t.Logf("clause sets differ (verdict agrees): only gopdfrab=%v, only veraPDF=%v",
						onlyGop, onlyVera)
				}
			})
		}
	}
}
