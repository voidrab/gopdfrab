// Package-level pipeline for converting an arbitrary PDF into a PDF/A-1b
// rewrite:
//
//	PDF -> pre-emptive fixups -> [serialize -> verify -> targeted fixups]* -> raster last resort -> output
package convert

import (
	"bytes"
	"fmt"
	"runtime"
	"sort"
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

// ConvertAll opens, converts, and closes a batch of files concurrently.
func ConvertAll(paths []string, p *pdf.Profile) ([]pdf.FileResult[ConvertResult], error) {
	results := make([]pdf.FileResult[ConvertResult], len(paths))

	workers := min(runtime.NumCPU(), len(paths))
	if workers < 1 {
		return results, nil
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

	return results, nil
}

func convertFile(path string, p *pdf.Profile) pdf.FileResult[ConvertResult] {
	cr, err := Convert(path, p)
	return pdf.FileResult[ConvertResult]{Path: path, Result: cr, Err: err}
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

	if err := applyPreemptiveFixups(&trailer, doc); err != nil {
		return ConvertResult{}, fmt.Errorf("convert: pre-emptive fixups: %w", err)
	}

	// Per-run decode cache for scanning fixers; per-run deviceColourFixer
	// instance so the cache is not shared across concurrent Convert calls.
	dcCache := make(map[pdf.StreamKey][]byte)
	dcFixer := deviceColourFixer{cache: dcCache}
	localFixers := buildLocalFixers(dcFixer, doc)

	var (
		cr         ConvertResult
		prevCounts map[pdf.Check]int
	)

	for iter := 1; iter <= maxConvertIterations; iter++ {
		cr.Iterations = iter

		result, objs, err := inHeapVerify(doc, trailer, p)
		if err != nil {
			return ConvertResult{}, fmt.Errorf("convert: %w", err)
		}
		cr.Result = result
		_ = objs

		if cr.Result.Valid {
			break
		}

		counts := violationCounts(cr.Result.Issues)
		if sameMultiset(counts, prevCounts) {
			break // no progress since last iteration
		}
		prevCounts = counts

		changed := false
		// Per-dict-local fixers (batchDictFixer) share one graph walk this
		// pass instead of each walking the whole graph; everything else runs
		// its own Fix as before. Sorted order keeps fixer application -- and
		// with it the whole conversion -- deterministic across runs.
		var visitors []func(pdf.PDFDict)
		batched := map[Fixer]bool{}
		for _, c := range sortedChecks(counts) {
			fixer, ok := localFixers[c]
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
			ch, err := fixer.Fix(&trailer, cr.Result.IssuesForCheck(c))
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
	// always converts. Only trigger when there are fixer-addressable issues;
	// structural violations (no registered fixer) are fixed by construction by
	// the writer and do not need rasterization.
	if !cr.Result.Valid && hasFixableIssue(cr.Result.Issues, localFixers) {
		if applyRasterFallback(&trailer, cr.Result.Issues) {
			cr.Iterations++
			result, _, err := inHeapVerify(doc, trailer, p)
			if err != nil {
				return ConvertResult{}, fmt.Errorf("convert: %w", err)
			}
			cr.Result = result
		}
		if !cr.Result.Valid && hasFixableIssue(cr.Result.Issues, localFixers) && flattenAllPages(&trailer) {
			cr.Iterations++
			result, _, err := inHeapVerify(doc, trailer, p)
			if err != nil {
				return ConvertResult{}, fmt.Errorf("convert: %w", err)
			}
			cr.Result = result
		}
	}

	// Final serialize + verify against the actual output bytes (structural checks
	// like xref format must run on the written output, not the original reader).
	if err := serializeAndVerify(doc, trailer, &cr, p); err != nil {
		return ConvertResult{}, fmt.Errorf("convert: %w", err)
	}
	return cr, nil
}

// inHeapVerify verifies the in-memory trailer graph without serializing it,
// by numbering objects and seeding the doc reader directly. It also returns
// the ObjNum -> object index so the fixer loop can target issues by ref;
// the index is only valid until the next renumbering.
func inHeapVerify(doc *pdf.Reader, trailer pdf.PDFDict, p *pdf.Profile) (pdf.Result, map[int]pdf.PDFValue, error) {
	objs := writer.NumberObjects(trailer)
	doc.SeedResolvedGraph(trailer, objs)
	res, err := verify.Verify(doc, p)
	return res, objs, err
}

// serializeAndVerify serializes trailer and verifies the output bytes,
// updating cr.Output and cr.Result. Called exactly once at the end of Run.
// The loop Reader's stream caches carry over: the graph is the same in-heap
// one, so unchanged streams keep their decoded/tokenized results while
// rewritten streams miss on their fresh RawStream identity.
func serializeAndVerify(loopDoc *pdf.Reader, trailer pdf.PDFDict, cr *ConvertResult, p *pdf.Profile) error {
	var buf bytes.Buffer
	order, err := writer.WriteDocumentIndexed(&buf, trailer)
	if err != nil {
		return err
	}
	cr.Output = buf.Bytes()

	out, err := pdf.OpenBytes(cr.Output)
	if err != nil {
		return err
	}
	defer out.Close()

	objs := make(map[int]pdf.PDFValue, len(order))
	for i, obj := range order {
		objs[i+1] = obj
	}
	out.AdoptStreamCaches(loopDoc)
	out.SeedResolvedGraph(trailer, objs)

	result, err := verify.Verify(out, p)
	if err != nil {
		return err
	}
	cr.Result = result
	return nil
}

// buildLocalFixers returns a per-run fixer map with run-scoped instances
// substituted for the registry singletons: the per-run dcFixer, a
// fontSubstitutionFixer carrying the run's Reader for cached usage scans,
// and an appearanceFixer carrying the run's appearance font.
func buildLocalFixers(dcFixer deviceColourFixer, doc *pdf.Reader) map[pdf.Check]Fixer {
	fontSrc := &appearanceFontSource{}
	local := make(map[pdf.Check]Fixer, len(fixerRegistry))
	for c, f := range fixerRegistry {
		switch f.(type) {
		case deviceColourFixer:
			local[c] = dcFixer
		case fontSubstitutionFixer:
			local[c] = fontSubstitutionFixer{doc: doc}
		case trueTypeEncodingFixer:
			local[c] = trueTypeEncodingFixer{doc: doc}
		case appearanceFixer:
			local[c] = appearanceFixer{fontSrc: fontSrc}
		default:
			local[c] = f
		}
	}
	return local
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
	var flagged []pageTarget
	nums := make([]int, 0, len(flag))
	for pageNum := range flag {
		nums = append(nums, pageNum)
	}
	sort.Ints(nums)
	for _, pageNum := range nums {
		if i := pageNum - 1; i >= 0 && i < len(pages) {
			flagged = append(flagged, pages[i])
		}
	}
	return flattenPagesParallel(flagged)
}

// flattenAllPages rasterizes every page, the final backstop for residuals that
// applyRasterFallback can't target -- document-level violations with no page
// number, or anything its page-by-page pass left behind.
func flattenAllPages(trailer *pdf.PDFDict) bool {
	return flattenPagesParallel(orderedPages(*trailer))
}

// flattenPagesParallel rasterizes distinct pages on a bounded worker pool;
// each render mutates only its own page dict while reading the shared graph,
// the same access pattern transparencyFlattener's workers rely on.
func flattenPagesParallel(pages []pageTarget) bool {
	seen := map[uintptr]bool{}
	var unique []pageTarget
	for _, p := range pages {
		ptr := pdf.ValuePointer(p.dict.Entries)
		if seen[ptr] {
			continue
		}
		seen[ptr] = true
		unique = append(unique, p)
	}
	if len(unique) == 0 {
		return false
	}

	results := make([]bool, len(unique))
	workers := min(runtime.NumCPU(), len(unique))
	jobs := make(chan int)
	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			for i := range jobs {
				p := unique[i]
				results[i] = flattenPageToImage(p.dict, p.resources, p.mediaBox)
			}
		}()
	}
	for i := range unique {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	changed := false
	for _, r := range results {
		changed = changed || r
	}
	return changed
}

// sortedChecks returns counts' keys ordered by clause, subclause, and name,
// giving the fixer loop a stable application order.
func sortedChecks(counts map[pdf.Check]int) []pdf.Check {
	checks := make([]pdf.Check, 0, len(counts))
	for c := range counts {
		checks = append(checks, c)
	}
	sort.Slice(checks, func(i, j int) bool {
		if checks[i].Clause() != checks[j].Clause() {
			return checks[i].Clause() < checks[j].Clause()
		}
		if checks[i].Subclause() != checks[j].Subclause() {
			return checks[i].Subclause() < checks[j].Subclause()
		}
		return checks[i].Name() < checks[j].Name()
	})
	return checks
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

// hasFixableIssue reports whether any issue in the list has a registered
// fixer, used to avoid triggering the raster fallback for purely structural
// violations that the writer fixes by construction.
func hasFixableIssue(issues []pdf.PDFError, fixers map[pdf.Check]Fixer) bool {
	for _, iss := range issues {
		if _, ok := fixers[iss.Check()]; ok {
			return true
		}
	}
	return false
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
