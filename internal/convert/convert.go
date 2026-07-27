// Package-level pipeline for converting an arbitrary PDF into a PDF/A-1b
// rewrite:
//
//	PDF -> pre-emptive fixups -> [serialize -> verify -> targeted fixups]* -> raster last resort -> output
package convert

import (
	"context"
	"fmt"
	"image"
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

// Options tunes the conversion pipeline. The zero value selects the defaults,
// so the root package's functional options fill in only the fields the caller
// set.
type Options struct {
	// Password is the user or owner password for an encrypted input (nil is the
	// empty password). Ignored by Run, whose document is already open.
	Password []byte
	// MaxIterations bounds the verify/fix loop (default 4).
	MaxIterations int
	// RasterDPI is the resolution used when a page or form is rasterized as a
	// last resort or to flatten transparency (default 150).
	RasterDPI int
	// CheckFidelity renders the input and the converted output and populates
	// ConvertResult.Fidelity with a per-page comparison. Off by default because
	// it roughly doubles the work; use it to detect a conversion that destroyed
	// visible content while still verifying clean.
	CheckFidelity bool
	// Workers bounds the concurrency of the batch entry points ConvertAll and
	// ConvertEach. 0 selects the default (runtime.NumCPU). Ignored by the
	// single-file entry points.
	Workers int
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

func (o Options) workers() int {
	if o.Workers > 0 {
		return o.Workers
	}
	return runtime.NumCPU()
}

type ConvertResult struct {
	// backing holds the converted PDF -- in memory when small, in a temp file
	// when large (see spillWriter). It is a pointer so value copies of a
	// ConvertResult (as FileResult stores) share one backing and a single
	// idempotent Close. Read it via Output/WriteTo/Save.
	backing    *outputBacking
	Result     pdf.Result
	Iterations int
	// Fidelity is the per-page input-vs-output rendering comparison, populated
	// only when Options.CheckFidelity was set. See PageFidelity.
	Fidelity []PageFidelity
	// RasterDrops records content the raster fallback could not render when it
	// flattened a page or a transparency group (an unusable shading, an
	// undecodable inline image, a missing Type 3 glyph, a tiling pattern), so a
	// rasterized conversion does not silently lose it. Empty when nothing was
	// dropped -- including when a page was rasterized losslessly, which
	// RasterizedPages reports instead.
	RasterDrops []RasterDrop
	// RasterizedPages lists, in ascending order, the 1-based pages the raster
	// fallback rebuilt as a flat image. A page here converted only by being
	// rasterized, whether or not anything was dropped doing it, so it stopped
	// being text and vector content.
	RasterizedPages []int
}

// addRasterizedPages merges pages into RasterizedPages, kept sorted and
// deduped since the page-level fallback and the whole-document one can both
// rebuild the same page.
func (r *ConvertResult) addRasterizedPages(pages []int) {
	seen := make(map[int]bool, len(r.RasterizedPages)+len(pages))
	merged := make([]int, 0, len(r.RasterizedPages)+len(pages))
	for _, p := range append(append([]int(nil), r.RasterizedPages...), pages...) {
		if !seen[p] {
			seen[p] = true
			merged = append(merged, p)
		}
	}
	sort.Ints(merged)
	r.RasterizedPages = merged
}

// RasterDrop lists the content features the raster fallback dropped on one page.
type RasterDrop struct {
	Page     int
	Features []string
}

// Residual returns the issues remaining in the output that Convert was unable
// to fix automatically.
func (r ConvertResult) Residual() []pdf.PDFError {
	return r.Result.Issues
}

// Output returns the converted PDF bytes. A large output spilled to a temp file
// is read back here, so prefer WriteTo or Save, which stream without
// materializing a second copy. It errors if there is no output -- only on a
// ConvertResult whose Convert call itself returned an error -- or if a spill
// file cannot be read.
func (r ConvertResult) Output() ([]byte, error) {
	return r.backing.bytes()
}

// WriteTo streams the converted PDF to w, implementing io.WriterTo, and returns
// the number of bytes written. It errors if there is no output, which only
// happens on a ConvertResult whose Convert call itself returned an error.
func (r ConvertResult) WriteTo(w io.Writer) (int64, error) {
	return r.backing.writeTo(w)
}

// Save streams the converted PDF to the given path. It returns an error if
// there is no output to save or the file cannot be written.
func (r ConvertResult) Save(path string) error {
	if r.backing.len() == 0 {
		return errNoOutput
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if _, err := r.backing.writeTo(f); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Close releases the converted output, removing the spill temp file if one was
// created and dropping any in-memory bytes. It is safe on the zero value and on
// repeated calls. Callers own Close for results from Convert and ConvertAll;
// ConvertEach closes each result after its callback returns.
//
// Output, WriteTo and Save may run concurrently with each other -- each opens
// the spill file separately, sharing no read offset -- but not with Close,
// which releases what they read.
func (r ConvertResult) Close() error {
	return r.backing.close()
}

// Convert reads the PDF at path and attempts to produce a PDF/A-1b
// conformant rewrite. It always returns the best attempt it produced,
// even if some violations remain. password is the empty password when nil.
func Convert(path string, p *pdf.Profile, o Options) (ConvertResult, error) {
	return ConvertContext(context.Background(), path, p, o)
}

// ConvertContext is Convert honouring ctx cancellation.
func ConvertContext(ctx context.Context, path string, p *pdf.Profile, o Options) (ConvertResult, error) {
	doc, err := pdf.OpenWithPassword(path, o.Password)
	if err != nil {
		return ConvertResult{}, fmt.Errorf("convert: %w", err)
	}
	defer doc.Close()
	return RunContext(ctx, doc, p, o)
}

// ConvertBytes is Convert for an in-memory PDF.
func ConvertBytes(data []byte, p *pdf.Profile, o Options) (ConvertResult, error) {
	return ConvertBytesContext(context.Background(), data, p, o)
}

// ConvertBytesContext is ConvertBytes honouring ctx cancellation.
func ConvertBytesContext(ctx context.Context, data []byte, p *pdf.Profile, o Options) (ConvertResult, error) {
	doc, err := pdf.OpenBytesWithPassword(data, o.Password)
	if err != nil {
		return ConvertResult{}, fmt.Errorf("convert: %w", err)
	}
	defer doc.Close()
	return RunContext(ctx, doc, p, o)
}

// ConvertAll opens, converts, and closes a batch of files concurrently.
func ConvertAll(paths []string, p *pdf.Profile, o Options) ([]pdf.FileResult[ConvertResult], error) {
	return ConvertAllContext(context.Background(), paths, p, o)
}

// ConvertAllContext is ConvertAll honouring ctx cancellation: a cancelled ctx
// stops dispatching further files and records ctx.Err() for those not started.
// It holds every result resident and does not close them; the caller owns
// Close on each (large outputs spill to a temp file). ConvertEach streams and
// auto-closes instead when the batch is too large to keep in memory at once.
func ConvertAllContext(ctx context.Context, paths []string, p *pdf.Profile, o Options) ([]pdf.FileResult[ConvertResult], error) {
	results := make([]pdf.FileResult[ConvertResult], len(paths))
	convertEach(ctx, paths, p, o, func(i int, fr pdf.FileResult[ConvertResult]) error {
		results[i] = fr
		return nil
	})
	return results, nil
}

// ConvertEach opens, converts, and closes a batch of files concurrently,
// invoking fn on each result as it completes rather than retaining them all, so
// a large batch need not hold every output in memory at once. Each result is
// closed after its fn returns, so fn must not retain it (copy out what it needs).
func ConvertEach(paths []string, p *pdf.Profile, o Options, fn func(pdf.FileResult[ConvertResult]) error) error {
	return ConvertEachContext(context.Background(), paths, p, o, fn)
}

// ConvertEachContext is ConvertEach honouring ctx cancellation. fn is called
// once per file, serialized (never concurrently) but in completion order rather
// than the order of paths; the FileResult's Path identifies the file. If fn
// returns an error, no further files are dispatched or delivered and that error
// is returned. A cancelled ctx delivers ctx.Err() for files not yet started.
func ConvertEachContext(ctx context.Context, paths []string, p *pdf.Profile, o Options, fn func(pdf.FileResult[ConvertResult]) error) error {
	return convertEach(ctx, paths, p, o, func(_ int, fr pdf.FileResult[ConvertResult]) error {
		// The result is not retained past fn, so release its output (dropping a
		// spill temp file) as soon as fn returns.
		defer fr.Result.Close()
		return fn(fr)
	})
}

// convertEach is the shared worker engine behind ConvertAllContext and
// ConvertEachContext. It converts paths across o.workers() goroutines and calls
// sink(index, result) for each, serialized under a mutex so sink need not be
// concurrency-safe. The first sink error stops dispatch and delivery and is
// returned.
func convertEach(ctx context.Context, paths []string, p *pdf.Profile, o Options, sink func(int, pdf.FileResult[ConvertResult]) error) error {
	if len(paths) == 0 {
		return nil
	}
	workers := min(o.workers(), len(paths))

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var mu sync.Mutex
	var firstErr error
	deliver := func(i int, fr pdf.FileResult[ConvertResult]) {
		mu.Lock()
		defer mu.Unlock()
		if firstErr != nil {
			return
		}
		if err := sink(i, fr); err != nil {
			firstErr = err
			cancel()
		}
	}

	jobs := make(chan int)
	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			for i := range jobs {
				deliver(i, convertFile(ctx, paths[i], p, o))
			}
		}()
	}
	for i := range paths {
		if err := ctx.Err(); err != nil {
			deliver(i, pdf.FileResult[ConvertResult]{Path: paths[i], Err: err})
			continue
		}
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	return firstErr
}

func convertFile(ctx context.Context, path string, p *pdf.Profile, o Options) pdf.FileResult[ConvertResult] {
	cr, err := ConvertContext(ctx, path, p, o)
	return pdf.FileResult[ConvertResult]{Path: path, Result: cr, Err: err}
}

// Run converts an already-open document, the shared implementation behind
// Convert/ConvertBytes and the facade's (*Document).Convert.
func Run(doc *pdf.Reader, p *pdf.Profile, o Options) (ConvertResult, error) {
	return RunContext(context.Background(), doc, p, o)
}

// RunContext is Run honouring ctx cancellation, checked before each verify/fix
// iteration and each raster pass.
func RunContext(ctx context.Context, doc *pdf.Reader, p *pdf.Profile, o Options) (ConvertResult, error) {
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

	// Capture the input's appearance before any fixup mutates the graph, so
	// the final fidelity comparison sees the original.
	var inputRenders []*image.RGBA
	if o.CheckFidelity {
		inputRenders = renderTrailerPages(trailer, fidelityDPI)
	}

	if err := applyPreemptiveFixups(&trailer, doc); err != nil {
		return ConvertResult{}, fmt.Errorf("convert: pre-emptive fixups: %w", err)
	}

	// Per-run deviceColourFixer wired to the Reader's concurrent decode cache,
	// shared with the pre-loop detectColourModelUsage scan.
	dcFixer := deviceColourFixer{decode: decoderFor(doc)}
	// The transparency flattener is a Fixer, with no handle on the result, so
	// the drops it cannot render are collected here and folded in below.
	var formDrops []RasterDrop
	localFixers := buildLocalFixers(dcFixer, doc, o.dpi(), &formDrops)

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

		// objHint sizes the writer's discovery index. The resolved object
		// count is the first estimate; after that each pass reports what the
		// last one actually numbered. See writer.newPDFWriter.
		objHint = doc.Footprint().Objects
	)

	for iter := 1; iter <= o.iterations(); iter++ {
		if err := ctx.Err(); err != nil {
			return ConvertResult{}, fmt.Errorf("convert: %w", err)
		}
		cr.Iterations = iter

		result, parts, objs, err := inHeapVerify(doc, trailer, p, objHint)
		if err != nil {
			return ConvertResult{}, fmt.Errorf("convert: %w", err)
		}
		cr.Result = result
		lastParts, graphClean = parts, true
		objHint = len(objs)

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

	if err := rasterBackstop(ctx, doc, &trailer, &cr, p, localFixers, &lastParts, &graphClean, o.dpi(), objHint); err != nil {
		return ConvertResult{}, fmt.Errorf("convert: %w", err)
	}
	cr.RasterDrops = append(cr.RasterDrops, formDrops...)

	// Final serialize + verify against the actual output bytes (structural checks
	// like xref format must run on the written output, not the original reader).
	if err := serializeAndVerify(doc, trailer, &cr, p, lastParts, graphClean, objHint); err != nil {
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

	// The run is over, so the input's decoded and tokenized streams are dead
	// weight. They matter for a caller who keeps the Document open past
	// Convert -- the run's own peak is behind us either way.
	defer doc.ReleaseCaches()

	// Compare the converted output's appearance to the input captured above.
	if o.CheckFidelity && cr.backing.len() > 0 {
		if out, err := cr.backing.open(); err == nil {
			cr.Fidelity = comparePageRenders(inputRenders, renderTrailerPagesOf(out))
			out.Close()
		}
	}
	return cr, nil
}

// renderTrailerPagesOf resolves out's graph and renders its pages at the
// fidelity DPI, returning nil on any resolve failure (the comparison then has
// no output baseline and reports the pages as lost).
func renderTrailerPagesOf(out *pdf.Reader) []*image.RGBA {
	graph, err := out.ResolveGraph()
	if err != nil {
		return nil
	}
	trailer, ok := graph.(pdf.PDFDict)
	if !ok {
		return nil
	}
	return renderTrailerPages(trailer, fidelityDPI)
}

// rasterBackstop is Run's last-resort remediation: rasterize residual pages
// so a resolvable graph always converts. Only fixer-addressable issues
// trigger it; structural violations (no registered fixer) are fixed by
// construction by the writer and do not need rasterization. It updates cr,
// lastParts, and graphClean exactly as the fix loop's verifies do.
func rasterBackstop(ctx context.Context, doc *pdf.Reader, trailer *pdf.PDFDict, cr *ConvertResult, p *pdf.Profile, localFixers map[pdf.Check]Fixer, lastParts *verify.Parts, graphClean *bool, dpi, hint int) error {
	if cr.Result.Valid || !hasFixableIssue(cr.Result.Issues, localFixers, false) {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if drops, rasterized, changed := applyRasterFallback(trailer, cr.Result.Issues, dpi); changed {
		cr.RasterDrops = append(cr.RasterDrops, drops...)
		cr.addRasterizedPages(rasterized)
		cr.Iterations++
		*graphClean = false
		result, parts, _, err := inHeapVerify(doc, *trailer, p, hint)
		if err != nil {
			return err
		}
		cr.Result = result
		*lastParts, *graphClean = parts, true
	}
	if !cr.Result.Valid && hasFixableIssue(cr.Result.Issues, localFixers, true) {
		if drops, rasterized, changed := flattenAllPages(trailer, dpi); changed {
			cr.RasterDrops = append(cr.RasterDrops, drops...)
			cr.addRasterizedPages(rasterized)
			cr.Iterations++
			*graphClean = false
			result, parts, _, err := inHeapVerify(doc, *trailer, p, hint)
			if err != nil {
				return err
			}
			cr.Result = result
			*lastParts, *graphClean = parts, true
		}
	}
	return nil
}

// inHeapVerify verifies the in-memory trailer graph without serializing it,
// by numbering objects and seeding the doc reader directly. It also returns
// the split issue parts (for serializeAndVerify's merged final verify) and
// the ObjNum -> object index so the fixer loop can target issues by ref;
// the index is only valid until the next renumbering.
func inHeapVerify(doc *pdf.Reader, trailer pdf.PDFDict, p *pdf.Profile, hint int) (pdf.Result, verify.Parts, map[int]pdf.PDFValue, error) {
	objs := writer.NumberObjects(trailer, hint)
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
func serializeAndVerify(loopDoc *pdf.Reader, trailer pdf.PDFDict, cr *ConvertResult, p *pdf.Profile, lastParts verify.Parts, graphClean bool, hint int) error {
	var sw spillWriter
	if loopDoc != nil {
		sw.grow(int(loopDoc.Size()))
	}
	order, err := writer.WriteDocumentIndexed(&sw, trailer, hint)
	if err != nil {
		return err
	}
	backing, err := sw.finish()
	if err != nil {
		return err
	}
	cr.backing = backing

	// Open the freshly written output for the byte-level verify: mmap'd when it
	// spilled to a temp file, wrapped in place when it stayed in memory.
	out, err := backing.open()
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
func buildLocalFixers(dcFixer deviceColourFixer, doc *pdf.Reader, dpi int, formDrops *[]RasterDrop) map[pdf.Check]Fixer {
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
			local[c] = transparencyFlattener{dpi: dpi, drops: formDrops}
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
func applyRasterFallback(trailer *pdf.PDFDict, issues []pdf.PDFError, dpi int) ([]RasterDrop, []int, bool) {
	pages := orderedPages(*trailer)
	flag := map[int]bool{}
	for _, iss := range issues {
		if iss.Page() > 0 {
			flag[iss.Page()] = true
		}
	}
	var flagged []pageTarget
	var pageNums []int
	nums := make([]int, 0, len(flag))
	for pageNum := range flag {
		nums = append(nums, pageNum)
	}
	sort.Ints(nums)
	for _, pageNum := range nums {
		if i := pageNum - 1; i >= 0 && i < len(pages) {
			flagged = append(flagged, pages[i])
			pageNums = append(pageNums, pageNum)
		}
	}
	return flattenPagesParallel(flagged, pageNums, dpi)
}

// flattenAllPages rasterizes every page, the final backstop for residuals that
// applyRasterFallback can't target -- document-level violations with no page
// number, or anything its page-by-page pass left behind.
func flattenAllPages(trailer *pdf.PDFDict, dpi int) ([]RasterDrop, []int, bool) {
	pages := orderedPages(*trailer)
	pageNums := make([]int, len(pages))
	for i := range pages {
		pageNums[i] = i + 1
	}
	return flattenPagesParallel(pages, pageNums, dpi)
}

// flattenPagesParallel rasterizes distinct pages on a bounded worker pool;
// each render mutates only its own page dict while reading the shared graph,
// the same access pattern transparencyFlattener's workers rely on. pageNums
// aligns with pages (the 1-based page number of each) for the drop report.
func flattenPagesParallel(pages []pageTarget, pageNums []int, dpi int) ([]RasterDrop, []int, bool) {
	seen := map[uintptr]bool{}
	var unique []pageTarget
	var uniqueNums []int
	for i, p := range pages {
		ptr := pdf.ValuePointer(p.dict.Entries)
		if seen[ptr] {
			continue
		}
		seen[ptr] = true
		unique = append(unique, p)
		uniqueNums = append(uniqueNums, pageNums[i])
	}
	if len(unique) == 0 {
		return nil, nil, false
	}

	changedFlags := make([]bool, len(unique))
	dropLists := make([][]string, len(unique))
	workers := min(runtime.NumCPU(), len(unique))
	jobs := make(chan int)
	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			for i := range jobs {
				p := unique[i]
				dropLists[i], changedFlags[i] = flattenPageToImage(p.dict, p.resources, p.mediaBox, dpi)
			}
		}()
	}
	for i := range unique {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	changed := false
	var drops []RasterDrop
	var rasterized []int
	for i, c := range changedFlags {
		if !c {
			continue
		}
		changed = true
		rasterized = append(rasterized, uniqueNums[i])
		if len(dropLists[i]) > 0 {
			drops = append(drops, RasterDrop{Page: uniqueNums[i], Features: dropLists[i]})
		}
	}
	return drops, rasterized, changed
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
