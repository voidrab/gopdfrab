// Package-level pipeline for converting an arbitrary PDF into a PDF/A-1b
// rewrite:
//
//	PDF -> pre-emptive fixups -> [serialize -> verify -> targeted fixups]* -> raster last resort -> output
package convert

import (
	"bytes"
	"fmt"
	"os"
	"runtime"
	"sync"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/verify"
	"github.com/voidrab/gopdfrab/internal/writer"
)

const maxConvertIterations = 4

type ConvertResult struct {
	Output     []byte
	Result     pdf.Result
	Iterations int
}

// Residual returns the issues remaining in r.Output that Convert was unable
// to fix automatically.
func (r ConvertResult) Residual() []pdf.PDFError {
	return r.Result.Issues
}

// Convert reads the PDF at path and attempts to produce a PDF/A-1b
// conformant rewrite. It always returns the best attempt it produced,
// even if some violations remain.
func Convert(path string, p *pdf.Profile) (ConvertResult, error) {
	doc, err := pdf.Open(path)
	if err != nil {
		return ConvertResult{}, fmt.Errorf("convert: %w", err)
	}
	defer doc.Close()
	return Run(doc, p)
}

// ConvertBytes is Convert for an in-memory PDF.
func ConvertBytes(data []byte, p *pdf.Profile) (ConvertResult, error) {
	doc, err := pdf.OpenBytes(data)
	if err != nil {
		return ConvertResult{}, fmt.Errorf("convert: %w", err)
	}
	defer doc.Close()
	return Run(doc, p)
}

// ConvertFileResult is one path's outcome from ConvertAll.
type ConvertFileResult struct {
	Path   string
	Result ConvertResult
	Err    error
}

// ConvertAll opens, converts, and closes a batch of files concurrently,
// mirroring verify.VerifyAll's worker-pool pattern.
func ConvertAll(paths []string, p *pdf.Profile) []ConvertFileResult {
	results := make([]ConvertFileResult, len(paths))

	workers := min(runtime.NumCPU(), len(paths))
	if workers < 1 {
		return results
	}

	jobs := make(chan int)
	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			for i := range jobs {
				results[i] = convertFile(paths[i], p)
			}
		}()
	}
	for i := range paths {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	return results
}

// convertFile opens, converts, and closes a single file.
func convertFile(path string, p *pdf.Profile) ConvertFileResult {
	cr, err := Convert(path, p)
	return ConvertFileResult{Path: path, Result: cr, Err: err}
}

// Run converts an already-open document, the shared implementation behind
// Convert/ConvertBytes and the facade's (*Document).Convert.
func Run(doc *pdf.Reader, p *pdf.Profile) (ConvertResult, error) {
	graph, err := doc.ResolveGraph()
	if err != nil {
		res, verr := verify.Verify(doc, p)
		if verr != nil {
			return ConvertResult{}, fmt.Errorf("convert: %w", err)
		}
		return ConvertResult{Result: res}, nil
	}
	trailer, ok := graph.(pdf.PDFDict)
	if !ok {
		return ConvertResult{}, fmt.Errorf("convert: resolved graph is not a dictionary")
	}

	if err := applyPreemptiveFixups(&trailer); err != nil {
		return ConvertResult{}, fmt.Errorf("convert: pre-emptive fixups: %w", err)
	}

	var (
		cr         ConvertResult
		prevCounts map[pdf.Check]int
	)

	for iter := 1; iter <= maxConvertIterations; iter++ {
		cr.Iterations = iter

		var buf bytes.Buffer
		if err := writer.WriteDocument(&buf, trailer); err != nil {
			return ConvertResult{}, fmt.Errorf("convert: %w", err)
		}
		cr.Output = buf.Bytes()

		result, verr := verifyBytes(cr.Output, p)
		if verr != nil {
			return ConvertResult{}, fmt.Errorf("convert: %w", verr)
		}
		cr.Result = result

		if result.Valid {
			return cr, nil
		}

		counts := violationCounts(result.Issues)
		if sameMultiset(counts, prevCounts) {
			break // no progress since last iteration
		}
		prevCounts = counts

		changed := false
		// Per-dict-local fixers (batchDictFixer) share one graph walk this
		// pass instead of each walking the whole graph; everything else runs
		// its own Fix as before.
		var visitors []func(pdf.PDFDict)
		batched := map[Fixer]bool{}
		for c := range counts {
			fixer, ok := fixerRegistry[c]
			if !ok {
				continue
			}
			if bf, isBatch := fixer.(batchDictFixer); isBatch {
				if batched[fixer] {
					continue
				}
				batched[fixer] = true
				if visit, ok := bf.prepare(&trailer, &changed); ok {
					visitors = append(visitors, visit)
				}
				continue
			}
			ch, err := fixer.Fix(&trailer, result.IssuesForCheck(c))
			if err != nil {
				return ConvertResult{}, fmt.Errorf("convert: fixer for check %q: %w", c.Name(), err)
			}
			if ch {
				changed = true
			}
		}
		if len(visitors) > 0 {
			walkDicts(trailer, map[uintptr]bool{}, func(d pdf.PDFDict) {
				for _, visit := range visitors {
					visit(d)
				}
			})
		}
		if !changed {
			break
		}
	}

	// Last-resort backstop: rasterize residual pages so a resolvable graph
	// always converts. First try only the pages a residual names (preserving
	// every other page's text/vectors); if anything still remains -- including
	// document-level violations no page number is attached to -- flatten the
	// whole document, which leaves no font/content-stream/transparency
	// construct for a check to flag.
	if !cr.Result.Valid {
		if applyRasterFallback(&trailer, cr.Result.Issues) {
			cr.Iterations++
			if err := serializeAndVerify(trailer, &cr, p); err != nil {
				return ConvertResult{}, fmt.Errorf("convert: %w", err)
			}
		}
		if !cr.Result.Valid && flattenAllPages(&trailer) {
			cr.Iterations++
			if err := serializeAndVerify(trailer, &cr, p); err != nil {
				return ConvertResult{}, fmt.Errorf("convert: %w", err)
			}
		}
	}

	return cr, nil
}

// serializeAndVerify writes trailer to cr.Output and re-verifies it,
// updating cr.Output and cr.Result in place.
func serializeAndVerify(trailer pdf.PDFDict, cr *ConvertResult, p *pdf.Profile) error {
	var buf bytes.Buffer
	if err := writer.WriteDocument(&buf, trailer); err != nil {
		return err
	}
	cr.Output = buf.Bytes()
	result, err := verifyBytes(cr.Output, p)
	if err != nil {
		return err
	}
	cr.Result = result
	return nil
}

// applyRasterFallback rebuilds every page carrying a residual issue as a flat
// raster image (flattenPageToImage), the last-resort remediation for content
// no targeted fixer could repair. Page numbers in issues align with the
// graph's page order, since both come from the same Root/Pages/Kids walk.
func applyRasterFallback(trailer *pdf.PDFDict, issues []pdf.PDFError) bool {
	pages := orderedPages(*trailer)
	flag := map[int]bool{}
	for _, iss := range issues {
		if iss.Page() > 0 {
			flag[iss.Page()] = true
		}
	}
	changed := false
	for pageNum := range flag {
		i := pageNum - 1
		if i < 0 || i >= len(pages) {
			continue
		}
		p := pages[i]
		if flattenPageToImage(p.dict, p.resources, p.mediaBox) {
			changed = true
		}
	}
	return changed
}

// flattenAllPages rasterizes every page, the final backstop for residuals that
// applyRasterFallback can't target -- document-level violations with no page
// number, or anything its page-by-page pass left behind.
func flattenAllPages(trailer *pdf.PDFDict) bool {
	changed := false
	for _, p := range orderedPages(*trailer) {
		if flattenPageToImage(p.dict, p.resources, p.mediaBox) {
			changed = true
		}
	}
	return changed
}

func verifyBytes(data []byte, p *pdf.Profile) (pdf.Result, error) {
	doc, err := pdf.OpenBytes(data)
	if err != nil {
		return pdf.Result{}, err
	}
	defer doc.Close()
	return verify.Verify(doc, p)
}

func writeTempFile(pattern string, data []byte) (path string, cleanup func(), err error) {
	tmp, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", nil, err
	}
	cleanup = func() { os.Remove(tmp.Name()) }

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		cleanup()
		return "", nil, err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return "", nil, err
	}
	return tmp.Name(), cleanup, nil
}

// violationCounts tallies how many times each Check is violated, used to
// detect whether a fixup pass made any progress.
func violationCounts(issues []pdf.PDFError) map[pdf.Check]int {
	counts := map[pdf.Check]int{}
	for _, iss := range issues {
		counts[iss.Check()]++
	}
	return counts
}

// sameMultiset reports whether a and b record exactly the same violation
// counts per
func sameMultiset(a, b map[pdf.Check]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
