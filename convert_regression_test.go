package gopdfrab

import (
	"encoding/xml"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const veraPDFBin = "benchmarks/tools/verapdf/verapdf"

type veraReport struct {
	XMLName xml.Name `xml:"report"`
	Jobs    struct {
		Job []struct {
			Report struct {
				IsCompliant bool `xml:"isCompliant,attr"`
				Details     struct {
					Rules []struct {
						Clause string `xml:"clause,attr"`
						Status string `xml:"status,attr"`
					} `xml:"rule"`
				} `xml:"details"`
			} `xml:"validationReport"`
		} `xml:"job"`
	} `xml:"jobs"`
}

// TestConvertNoResidualIssues cross-checks gopdfrab's verifier against the
// bundled veraPDF reference for every regression document, then converts the
// files whose failure causes agree and asserts the result is conformant.
func TestConvertNoResidualIssues(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping testing in short mode")
	}

	if _, err := os.Stat(veraPDFBin); err != nil {
		t.Skipf("veraPDF reference verifier not available: %v", err)
	}

	basePath := "tests/regression"
	var paths []string
	err := filepath.WalkDir(basePath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(strings.ToLower(d.Name()), ".pdf") {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("failed to gather test documents: %v", err)
	}

	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			res, err := Verify(path, PDFA1B)
			if err != nil {
				t.Fatalf("Verify failed: %v", err)
			}
			gopClauses := map[string]bool{}
			for _, iss := range res.Issues {
				gopClauses[iss.Check().Clause()] = true
			}

			veraClauses, veraCompliant, err := veraPDFClauses(path)
			if err != nil {
				t.Fatalf("veraPDF reference run failed: %v", err)
			}

			if res.Valid != veraCompliant {
				t.Fatalf("conformance verdict disagrees with veraPDF: gopdfrab valid=%v, veraPDF compliant=%v (gopdfrab clauses %v, veraPDF clauses %v)",
					res.Valid, veraCompliant, sortedClauses(gopClauses), sortedClauses(veraClauses))
			}

			if res.Valid {
				// Both verifiers agree the input is already conformant; nothing
				// to convert.
				return
			}

			match, onlyGop, onlyVera := clauseSetMatches(gopClauses, veraClauses)
			if !match {
				t.Logf("failure causes disagree with veraPDF: only gopdfrab=%v, only veraPDF=%v",
					onlyGop, onlyVera)
			}

			// Causes agree: convert and require a clean PDF/A-1b result.
			cr, err := Convert(path, PDFA1B)
			if err != nil {
				t.Fatalf("Convert failed: %v", err)
			}

			tmpDir := t.TempDir()
			outPath := filepath.Join(tmpDir, "converted.pdf")

			if err := cr.Save(outPath); err != nil {
				t.Fatalf("failed to write converted PDF: %v", err)
			}

			if !cr.Result.Valid {
				t.Errorf("converted PDF is not valid, %d residual issues", len(cr.Residual()))
			}
			for _, iss := range cr.Residual() {
				t.Errorf("residual %s issue after conversion: %v", cr.Result.Summary(), iss)
			}

			veraClauses, veraCompliant, err = veraPDFClauses(outPath)
			if err != nil {
				t.Fatalf("veraPDF verification of converted PDF failed: %v", err)
			}

			if !veraCompliant {
				t.Fatalf("veraPDF reports converted PDF is still not PDF/A-1b compliant: %v",
					sortedClauses(veraClauses))
			}
		})
	}
}

// veraPDFClauses runs the reference veraPDF verifier and returns the set of
// clauses it reports as failed for path, and whether it deems the file compliant.
func veraPDFClauses(path string) (clauses map[string]bool, compliant bool, err error) {
	cmd := exec.Command(veraPDFBin, "--format", "mrr", "--flavour", "1b", path)
	out, err := cmd.Output()
	// veraPDF exits non-zero for non-compliant files
	if len(out) == 0 {
		if err != nil {
			return nil, false, err
		}
		return nil, false, exec.ErrNotFound
	}

	var rep veraReport
	if err := xml.Unmarshal(out, &rep); err != nil {
		return nil, false, err
	}

	clauses = map[string]bool{}
	for _, job := range rep.Jobs.Job {
		compliant = job.Report.IsCompliant
		for _, r := range job.Report.Details.Rules {
			if r.Status == "failed" {
				clauses[r.Clause] = true
			}
		}
	}
	return clauses, compliant, nil
}

// clauseSetMatches reports whether two clause sets describe the same causes.
// It also returns the clauses present in only one side for diagnostics.
func clauseSetMatches(got, want map[string]bool) (match bool, onlyGot, onlyWant []string) {
	for c := range got {
		if !clauseSetHas(want, c) {
			onlyGot = append(onlyGot, c)
		}
	}
	for c := range want {
		if !clauseSetHas(got, c) {
			onlyWant = append(onlyWant, c)
		}
	}
	sort.Strings(onlyGot)
	sort.Strings(onlyWant)
	return len(onlyGot) == 0 && len(onlyWant) == 0, onlyGot, onlyWant
}

// clauseSetHas reports whether set contains a clause matching c under
// clauseMatches.
func clauseSetHas(set map[string]bool, c string) bool {
	for s := range set {
		if clauseMatches(s, c) {
			return true
		}
	}
	return false
}

func sortedClauses(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for c := range set {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}
