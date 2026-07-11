package gopdfrab

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestConvertMergedFinalVerifyOracle converts every corpus document and
// asserts the merged final verification (byte-level structural checks on the
// output plus the reused in-loop graph verdicts, see convert.go's
// serializeAndVerify) reports exactly what an independent from-scratch
// verify of the same output bytes reports: same verdict, same check
// multiset. This is the conformance gate for reusing graph verdicts instead
// of replaying the whole graph verification against the output.
func TestConvertMergedFinalVerifyOracle(t *testing.T) {
	if testing.Short() {
		t.Skip("corpus-wide oracle skipped in short mode")
	}

	var files []string
	for _, dir := range []string{isartorDir, veraDir} {
		if _, err := os.Stat(dir); err != nil {
			continue
		}
		filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(strings.ToLower(d.Name()), ".pdf") {
				return nil
			}
			files = append(files, path)
			return nil
		})
	}
	if len(files) == 0 {
		t.Skip("no corpora present")
	}

	for _, path := range files {
		t.Run(filepath.Base(path), func(t *testing.T) {
			t.Parallel()
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			cr, err := ConvertBytes(data, PDFA_1B)
			if err != nil || len(cr.Output) == 0 {
				t.Skipf("no convertible output (err=%v)", err)
			}

			fresh, err := VerifyBytes(cr.Output, PDFA_1B)
			if err != nil {
				t.Fatalf("fresh VerifyBytes of output: %v", err)
			}

			if cr.Result.Valid != fresh.Valid {
				t.Errorf("merged Valid=%v, fresh Valid=%v (merged %v, fresh %v)",
					cr.Result.Valid, fresh.Valid,
					checkMultiset(cr.Result.Issues), checkMultiset(fresh.Issues))
			}
			merged, freshSet := checkMultiset(cr.Result.Issues), checkMultiset(fresh.Issues)
			if fmt.Sprint(merged) != fmt.Sprint(freshSet) {
				t.Errorf("merged issue multiset %v != fresh %v", merged, freshSet)
			}
		})
	}
}

// checkMultiset returns the sorted "clause/subclause name xN" identity list
// of a result's issues.
func checkMultiset(issues []PDFError) []string {
	counts := map[string]int{}
	for _, iss := range issues {
		c := iss.Check()
		counts[fmt.Sprintf("%s/%d %s", c.Clause(), c.Subclause(), c.Name())]++
	}
	out := make([]string, 0, len(counts))
	for k, n := range counts {
		out = append(out, fmt.Sprintf("%s x%d", k, n))
	}
	sort.Strings(out)
	return out
}
