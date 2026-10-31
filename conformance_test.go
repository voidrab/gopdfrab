// Conformance suites over the committed corpora (corpustest.All), each file
// asserted against the clause its filename targets and against veraPDF.

package gopdfrab

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/voidrab/gopdfrab/internal/corpustest"
)

// TestConformanceCorpora runs every file of every committed corpus
// (corpustest.All) through Verify with the corpus's profile. A fail file must
// be reported non-conformant by a check whose clause under the corpus's level
// matches the one its name targets; a pass file must validate cleanly.
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
	)
	for _, path := range c.Files() {
		t.Run(c.Rel(path), func(t *testing.T) {
			expected, wantFail := c.Expect(path)
			if expected == "" {
				unmatched++
				t.Errorf("name does not follow the corpus's convention")
				return
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

	t.Logf("%s scoreboard: pass: clean=%d rejected=%d | fail: caught=%d correctClause=%d falseNeg=%d wrongClause=%d openErr=%d | unmatched names=%d",
		c.Name, passClean, passRejected,
		failCaught, failCorrect, failFalseNeg, failWrongClause, openErrFail, unmatched)
}

// differentialDeviations lists files where gopdfrab's verdict deliberately
// differs from the veraPDF binary's, keyed by the corpus name and the
// corpus-relative path ("veraPDF-1b/6.1 File structure/x.pdf"), with the
// justification as the value. Every entry must explain why gopdfrab is right
// (or at least defensible); an undocumented disagreement fails the test.
var differentialDeviations = map[string]string{}

// TestDifferentialVeraPDFCorpora runs the bundled veraPDF binary, with each
// corpus's flavour, over every committed conformance corpus in one batch each
// and diffs its verdict against gopdfrab's under the corpus's default profile,
// file by file. This compares against veraPDF the implementation, not the
// suite's filename expectations (which TestConformanceCorpora already
// asserts): a verdict disagreement is
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
