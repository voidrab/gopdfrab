package gopdfrab

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/voidrab/gopdfrab/internal/corpustest"
	"github.com/voidrab/gopdfrab/internal/pdf"
)

// retainedFeatures counts, in the PDF at path, the features PDF/A-2 keeps and
// PDF/A-1 flattens or removes.
func retainedFeatures(t *testing.T, path string) map[string]int {
	t.Helper()
	doc, err := pdf.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer doc.Close()
	graph, err := doc.ResolveGraph()
	if err != nil {
		t.Fatalf("resolve %s: %v", path, err)
	}
	counts := map[string]int{}
	seen := map[pdf.Dict]bool{}
	var walk func(v pdf.PDFValue)
	walk = func(v pdf.PDFValue) {
		switch x := v.(type) {
		case pdf.PDFArray:
			for _, item := range x {
				walk(item)
			}
		case pdf.PDFDict:
			if seen[x.Entries] {
				return
			}
			seen[x.Entries] = true
			if _, ok := x.Entries.Get("SMask").(pdf.PDFDict); ok {
				counts["SMask"]++
			}
			if bm := x.Entries.Get("BM"); bm != nil && bm != pdf.PDFValue(pdf.PDFName{Value: "Normal"}) {
				counts["BM"]++
			}
			if _, ok := x.Entries.Get("Group").(pdf.PDFDict); ok {
				counts["Group"]++
			}
			if x.Entries.Get("Type") == pdf.PDFValue(pdf.PDFName{Value: "OCG"}) {
				counts["OCG"]++
			}
			if x.Entries.Get("EF") != nil {
				counts["EF"]++
			}
			if usesJPX(x.Entries.Get("Filter")) {
				counts["JPX"]++
			}
			for _, child := range x.Entries.All() {
				walk(child)
			}
		}
	}
	walk(graph)
	return counts
}

// usesJPX reports whether a filter (/Filter) value names JPXDecode, either
// alone or in an array.
func usesJPX(filter pdf.PDFValue) bool {
	jpx := pdf.PDFValue(pdf.PDFName{Value: "JPXDecode"})
	if arr, ok := filter.(pdf.PDFArray); ok {
		return slices.Contains(arr, jpx)
	}
	return filter == jpx
}

// TestConvertLeavesConformingPart2FilesIntact verifies that converting a file
// already valid at PDF/A-2b keeps it valid, rasterizes nothing, does not blank or
// cover a page, and keeps the features the level allows.
func TestConvertLeavesConformingPart2FilesIntact(t *testing.T) {
	if testing.Short() {
		t.Skip("corpus walk skipped in short mode")
	}
	for _, f := range corpustest.AllFixtures(false) {
		if f.Corpus.Level() != pdf.A2B {
			continue
		}
		t.Run(filepath.Base(f.Path), func(t *testing.T) {
			cr, err := ConvertContext(context.Background(), f.Path, PDFA2B, Options{CheckFidelity: true})
			if err != nil {
				t.Fatal(err)
			}
			defer cr.Close()
			if !cr.Result.Valid {
				t.Errorf("no longer valid: %v", cr.Residual())
			}
			if len(cr.RasterizedPages) != 0 || len(cr.BlankedPages) != 0 || len(cr.OverpaintedPages) != 0 {
				t.Errorf("rasterized %v, blanked %v, overpainted %v", cr.RasterizedPages, cr.BlankedPages, cr.OverpaintedPages)
			}
			out := filepath.Join(t.TempDir(), "out.pdf")
			if err := cr.Save(out); err != nil {
				t.Fatal(err)
			}
			if in, got := retainedFeatures(t, f.Path), retainedFeatures(t, out); !maps.Equal(in, got) {
				t.Errorf("features changed: %v -> %v", in, got)
			}
		})
	}
}

// TestDifferentialConvertedOutputs verifies that veraPDF accepts the conversion
// of every fixture in each PDF/A-2b corpus. The package's own verifier is not
// used, because a gap in a check hides the same gap in a fixer.
func TestDifferentialConvertedOutputs(t *testing.T) {
	if testing.Short() {
		t.Skip("differential cross-check skipped in short mode")
	}
	if _, err := os.Stat(veraPDFBin); err != nil {
		t.Skipf("veraPDF reference verifier not available: %v", err)
	}
	for _, c := range corpustest.All {
		if !c.Present() || c.Level() != pdf.A2B {
			continue
		}
		dir := t.TempDir()
		for i, path := range c.Files() {
			cr, err := Convert(path, c.Default)
			if err != nil {
				t.Errorf("Convert(%s): %v", c.Rel(path), err)
				continue
			}
			if err := cr.Save(filepath.Join(dir, fmt.Sprintf("%04d-%s", i, filepath.Base(path)))); err != nil {
				t.Errorf("Save(%s): %v", c.Rel(path), err)
			}
			cr.Close()
		}
		rep, err := runVeraPDF(c.Flavour, "--recurse", dir)
		if err != nil {
			t.Fatalf("veraPDF batch over converted %s: %v", c.Name, err)
		}
		if len(rep.Jobs.Job) == 0 {
			t.Fatalf("veraPDF returned no jobs for converted %s", c.Name)
		}
		for _, job := range rep.Jobs.Job {
			if job.Report == nil || !job.Report.IsCompliant {
				t.Errorf("%s: veraPDF rejects the converted file", filepath.Base(job.Item.Name))
			}
		}
	}
}
