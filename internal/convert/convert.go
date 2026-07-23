// Package-level pipeline for converting an arbitrary PDF into a PDF/A-1b
// rewrite:
//
//	PDF -> pre-emptive fixups -> [serialize -> verify -> targeted fixups]* -> raster last resort -> output
package convert

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"runtime"
	"sort"
	"sync"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/verify"
	"github.com/voidrab/gopdfrab/internal/writer"
)

// ConvertResult implements io.WriterTo.
var _ io.WriterTo = ConvertResult{}

const (
	defaultMaxIterations = 4
	defaultRasterDPI     = 150
)

// Options tunes the conversion pipeline. A zero value selects the defaults, so
// callers set only the fields they care about. It is passed as a trailing
// variadic argument so the two-argument call form keeps the defaults.
type Options struct {
	// Password is the user or owner password for an encrypted input (nil is the
	// empty password). Ignored by Run, whose document is already open.
	Password []byte
	// MaxIterations bounds the verify/fix loop (default 4).
	MaxIterations int
	// RasterDPI is the resolution used when a page or form is rasterized as a
	// last resort or to flatten transparency (default 150).
	RasterDPI int
}

func optConfig(opts []Options) Options {
	if len(opts) > 0 {
		return opts[0]
	}
	return Options{}
}

func (o Options) iterations() int {
	if o.MaxIterations > 0 {
		return o.MaxIterations
	}
	return defaultMaxIterations
}

func (o Options) dpi() int {
	if o.RasterDPI > 0 {
		return o.RasterDPI
	}
	return defaultRasterDPI
}

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

// WriteTo writes the converted PDF to w, implementing io.WriterTo, and returns
// the number of bytes written. It errors if there is no output, which only
// happens on a ConvertResult whose Convert call itself returned an error.
func (r ConvertResult) WriteTo(w io.Writer) (int64, error) {
	if len(r.Output) == 0 {
		return 0, fmt.Errorf("convert: no output to write")
	}
	n, err := w.Write(r.Output)
	return int64(n), err
}

// Save writes the converted PDF to the given path. It returns an error if
// there is no output to save or the file cannot be written.
func (r ConvertResult) Save(path string) error {
	if len(r.Output) == 0 {
		return fmt.Errorf("convert: no output to save")
	}
	return os.WriteFile(path, r.Output, 0o644)
}

// Convert reads the PDF at path and attempts to produce a PDF/A-1b
// conformant rewrite. It always returns the best attempt it produced,
// even if some violations remain. password is the empty password when nil.
func Convert(path string, p *pdf.Profile, opts ...Options) (ConvertResult, error) {
	o := optConfig(opts)
	doc, err := pdf.OpenWithPassword(path, o.Password)
	if err != nil {
		return ConvertResult{}, fmt.Errorf("convert: %w", err)
	}
	defer doc.Close()
	return Run(doc, p, o)
}

// ConvertBytes is Convert for an in-memory PDF.
func ConvertBytes(data []byte, p *pdf.Profile, opts ...Options) (ConvertResult, error) {
	o := optConfig(opts)
	doc, err := pdf.OpenBytesWithPassword(data, o.Password)
	if err != nil {
		return ConvertResult{}, fmt.Errorf("convert: %w", err)
	}
	defer doc.Close()
	return Run(doc, p, o)
}

// ConvertAll opens, converts, and closes a batch of files concurrently.
func ConvertAll(paths []string, p *pdf.Profile, opts ...Options) ([]pdf.FileResult[ConvertResult], error) {
	results := make([]pdf.FileResult[ConvertResult], len(paths))
	o := optConfig(opts)

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
				results[i] = convertFile(paths[i], p, o)
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

func convertFile(path string, p *pdf.Profile, o Options) pdf.FileResult[ConvertResult] {
	cr, err := Convert(path, p, o)
	return pdf.FileResult[ConvertResult]{Path: path, Result: cr, Err: err}
}

// Run converts an already-open document, the shared implementation behind
// Convert/ConvertBytes and the facade's (*Document).Convert.
func Run(doc *pdf.Reader, p *pdf.Profile, opts ...Options) (ConvertResult, error) {
	o := optConfig(opts)
	graph, err := doc.ResolveGraph()
	if err != nil {
		// Per-object degradation makes this rare (pathological cases like the
		// resolve-depth cap). A best-effort verify Result still rides along,
		// but a Convert that produced no document must say so with an error,
		// never a silent empty Output.
		werr := fmt.Errorf("convert: %w: %v", pdf.ErrUnresolvableGraph, err)
		res, verr := verify.Verify(doc, p)
		if verr != nil {
			return ConvertResult{}, werr
		}
		return ConvertResult{Result: res}, werr
	}
	trailer, ok := graph.(pdf.PDFDict)
	if !ok {
		return ConvertResult{}, fmt.Errorf("convert: resolved graph is not a dictionary")
	}

	if err := applyPreemptiveFixups(&trailer, doc); err != nil {
		return ConvertResult{}, fmt.Errorf("convert: pre-emptive fixups: %w", err)
	}

	// Per-run deviceColourFixer wired to the Reader's concurrent decode cache,
	// shared with the pre-loop detectColourModelUsage scan.
	dcFixer := deviceColourFixer{decode: decoderFor(doc)}
	localFixers := buildLocalFixers(dcFixer, doc, o.dpi())

	var (
		cr         ConvertResult
		prevCounts map[pdf.Check]int

		// graphClean records whether the in-heap graph is byte-for-byte the
		// graph the most recent inHeapVerify checked -- true right after each
		// verify, false once any fixer or flattener edits it. When the run
		// ends clean, serializeAndVerify can reuse lastParts.Graph instead of
		// replaying the whole graph verification against the output bytes.
		graphClean bool
		lastParts  verify.Parts
	)

	for iter := 1; iter <= o.iterations(); iter++ {
		cr.Iterations = iter

		result, parts, objs, err := inHeapVerify(doc, trailer, p)
		if err != nil {
			return ConvertResult{}, fmt.Errorf("convert: %w", err)
		}
		cr.Result = result
		lastParts, graphClean = parts, true

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
		// pass instead of each walking the whole graph; targeted fixers jump
		// straight to the objects their issues reference; everything else runs
		// its own Fix as before. Sorted order keeps fixer application -- and
		// with it the whole conversion -- deterministic across runs.
		pass := &fixPass{trailer: &trailer, objs: objs}
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
			if tf, ok := fixer.(targetedFixer); ok {
				ch, handled, err := tf.fixTargeted(pass, cr.Result.IssuesForCheck(c))
				if err != nil {
					return ConvertResult{}, fmt.Errorf("convert: targeted fixer for check %q: %w", c.Name(), err)
				}
				if handled {
					if ch {
						changed = true
					}
					continue
				}
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
		graphClean = false
	}

	if err := rasterBackstop(doc, &trailer, &cr, p, localFixers, &lastParts, &graphClean, o.dpi()); err != nil {
		return ConvertResult{}, fmt.Errorf("convert: %w", err)
	}

	// Final serialize + verify against the actual output bytes (structural checks
	// like xref format must run on the written output, not the original reader).
	if err := serializeAndVerify(doc, trailer, &cr, p, lastParts, graphClean); err != nil {
		return ConvertResult{}, fmt.Errorf("convert: %w", err)
	}

	// Objects the reader degraded to null were serialized as null: their
	// content is lost, so the conversion must not claim success. The final
	// verify ran on the output bytes, where the loss is invisible, so the
	// degradation issues are carried over explicitly. (Recovered objects are
	// not carried: the rewrite emits a correct xref, genuinely fixing them.)
	for _, e := range doc.DegradedObjects() {
		c := e.Check()
		if p.Allows(c.Clause(), c.Subclause()) {
			cr.Result.Issues = append(cr.Result.Issues, e)
			cr.Result.Valid = false
		}
	}
	return cr, nil
}

// rasterBackstop is Run's last-resort remediation: rasterize residual pages
// so a resolvable graph always converts. Only fixer-addressable issues
// trigger it; structural violations (no registered fixer) are fixed by
// construction by the writer and do not need rasterization. It updates cr,
// lastParts, and graphClean exactly as the fix loop's verifies do.
func rasterBackstop(doc *pdf.Reader, trailer *pdf.PDFDict, cr *ConvertResult, p *pdf.Profile, localFixers map[pdf.Check]Fixer, lastParts *verify.Parts, graphClean *bool, dpi int) error {
	if cr.Result.Valid || !hasFixableIssue(cr.Result.Issues, localFixers, false) {
		return nil
	}
	if applyRasterFallback(trailer, cr.Result.Issues, dpi) {
		cr.Iterations++
		*graphClean = false
		result, parts, _, err := inHeapVerify(doc, *trailer, p)
		if err != nil {
			return err
		}
		cr.Result = result
		*lastParts, *graphClean = parts, true
	}
	if !cr.Result.Valid && hasFixableIssue(cr.Result.Issues, localFixers, true) && flattenAllPages(trailer, dpi) {
		cr.Iterations++
		*graphClean = false
		result, parts, _, err := inHeapVerify(doc, *trailer, p)
		if err != nil {
			return err
		}
		cr.Result = result
		*lastParts, *graphClean = parts, true
	}
	return nil
}

// inHeapVerify verifies the in-memory trailer graph without serializing it,
// by numbering objects and seeding the doc reader directly. It also returns
// the split issue parts (for serializeAndVerify's merged final verify) and
// the ObjNum -> object index so the fixer loop can target issues by ref;
// the index is only valid until the next renumbering.
func inHeapVerify(doc *pdf.Reader, trailer pdf.PDFDict, p *pdf.Profile) (pdf.Result, verify.Parts, map[int]pdf.PDFValue, error) {
	objs := writer.NumberObjects(trailer)
	doc.SeedResolvedGraph(trailer, objs)
	parts, err := verify.VerifyParts(doc, p)
	if err != nil {
		return pdf.Result{}, verify.Parts{}, nil, err
	}
	return verify.ResultFromIssues(p, parts.Issues()), parts, objs, nil
}

// fullFinalVerify forces serializeAndVerify's full from-scratch verify even
// when the graph is clean -- an escape hatch, and a lever for oracle tests
// that cross-check the merged path against the full one.
var fullFinalVerify = os.Getenv("GOPDFRAB_FULL_FINAL_VERIFY") == "1"

// serializeAndVerify serializes trailer and verifies the output bytes,
// updating cr.Output and cr.Result. Called exactly once at the end of Run.
// The loop Reader's stream caches carry over: the graph is the same in-heap
// one, so unchanged streams keep their decoded/tokenized results while
// rewritten streams miss on their fresh RawStream identity.
//
// When the graph is clean -- unchanged since the last inHeapVerify -- the
// graph-side checks would be a deterministic replay of that verify (the
// output reader is seeded with the very same graph and stream caches;
// TestConvertSeededVerifyMatchesFreshVerify pins the equivalence), so only
// the byte-level structural checks run against the output and lastParts
// supplies the graph verdicts. A dirty graph gets today's full verify.
func serializeAndVerify(loopDoc *pdf.Reader, trailer pdf.PDFDict, cr *ConvertResult, p *pdf.Profile, lastParts verify.Parts, graphClean bool) error {
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

	if graphClean && !fullFinalVerify {
		parts, err := verify.VerifyStructural(out, p)
		if err != nil {
			return err
		}
		parts.Graph = lastParts.Graph
		cr.Result = verify.ResultFromIssues(p, parts.Issues())
		return nil
	}

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
func buildLocalFixers(dcFixer deviceColourFixer, doc *pdf.Reader, dpi int) map[pdf.Check]Fixer {
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
		case transparencyFlattener:
			local[c] = transparencyFlattener{dpi: dpi}
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
func applyRasterFallback(trailer *pdf.PDFDict, issues []pdf.PDFError, dpi int) bool {
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
	return flattenPagesParallel(flagged, dpi)
}

// flattenAllPages rasterizes every page, the final backstop for residuals that
// applyRasterFallback can't target -- document-level violations with no page
// number, or anything its page-by-page pass left behind.
func flattenAllPages(trailer *pdf.PDFDict, dpi int) bool {
	return flattenPagesParallel(orderedPages(*trailer), dpi)
}

// flattenPagesParallel rasterizes distinct pages on a bounded worker pool;
// each render mutates only its own page dict while reading the shared graph,
// the same access pattern transparencyFlattener's workers rely on.
func flattenPagesParallel(pages []pageTarget, dpi int) bool {
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
				results[i] = flattenPageToImage(p.dict, p.resources, p.mediaBox, dpi)
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
// fixer and could plausibly be repaired by rasterization, used to gate the
// raster fallback. Object-model findings are dict-structural: flattening a
// page removes its whole subtree (so page-attributed ones justify the page
// pass), but no amount of rasterizing repairs document-level dict structure,
// so with docWide set they never count.
func hasFixableIssue(issues []pdf.PDFError, fixers map[pdf.Check]Fixer, docWide bool) bool {
	for _, iss := range issues {
		if _, ok := fixers[iss.Check()]; !ok {
			continue
		}
		if iss.Check().Clause() == pdf.ObjectModelClause && (docWide || iss.Page() == 0) {
			continue
		}
		return true
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
