package pdfrab

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

// failFixturesByExpectedClause walks both corpora and returns every "fail"
// fixture's path paired with the clause its filename targets (see
// veraClauseAndKind / expectedClauseFromName).
func failFixturesByExpectedClause(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}

	if _, err := os.Stat(veraDir); err == nil {
		filepath.WalkDir(veraDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(strings.ToLower(d.Name()), ".pdf") {
				return nil
			}
			if clause, wantFail := veraClauseAndKind(d.Name()); wantFail && clause != "" {
				out[path] = clause
			}
			return nil
		})
	}
	if _, err := os.Stat(isartorDir); err == nil {
		filepath.WalkDir(isartorDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(strings.ToLower(d.Name()), ".pdf") {
				return nil
			}
			if clause := expectedClauseFromName(d.Name()); clause != "" {
				out[path] = clause
			}
			return nil
		})
	}
	return out
}
