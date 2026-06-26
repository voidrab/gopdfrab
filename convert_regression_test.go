package pdfrab

import (
	"io/fs"
	"path/filepath"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// TestConvertNoResidualIssues is a regression test for all regression test documents.
func TestConvertNoResidualIssues(t *testing.T) {
	basePath := "test documents/regression"
	var paths []string

	err := filepath.WalkDir(basePath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("failed to gather test documents: %v", err)
	}

	results, err := ConvertAll(paths, pdf.PDFA_1B)
	if err != nil {
		t.Fatalf("ConvertAll batch execution failed: %v", err)
	}

	for i, cr := range results {
		filename := filepath.Base(paths[i])

		t.Run(filename, func(t *testing.T) {
			if !cr.Result.Result.Valid {
				t.Errorf("converted PDF is not valid, %d residual issues", len(cr.Result.Residual()))
			}

			for _, iss := range cr.Result.Residual() {
				t.Errorf("residual %s issue after conversion: %v", cr.Result.Result.Summary(), iss)
			}
		})
	}
}
