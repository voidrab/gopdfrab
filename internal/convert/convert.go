// Package-level pipeline for converting an arbitrary PDF into a PDF/A-1b
// rewrite:
//
//	PDF -> pre-emptive fixups -> [serialize -> verify -> targeted fixups]* -> raster last resort -> output
package convert

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"sort"

	"github.com/voidrab/gopdfrab/internal/fidelity"
	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfa"
	"github.com/voidrab/gopdfrab/internal/verify"
	"github.com/voidrab/gopdfrab/internal/writer"
)

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

// Run converts an already-open document, the shared implementation behind
// Convert/ConvertBytes and the facade's (*Document).Convert.
func Run(doc *pdf.Reader, p *pdf.Profile, o Options) (ConvertResult, error) {
	return RunContext(context.Background(), doc, p, o)
}

// RunContext is Run honouring ctx cancellation, checked before each verify/fix
// iteration and each raster pass.
func RunContext(ctx context.Context, doc *pdf.Reader, p *pdf.Profile, o Options) (ConvertResult, error) {
	return runWith(ctx, doc, p, o, standardRepairs)
}

// runWith is RunContext with the repairs to use.
func runWith(ctx context.Context, doc *pdf.Reader, p *pdf.Profile, o Options, rs repairs) (_ ConvertResult, err error) {
	defer pdf.RecoverAsError(&err)
	trailer, err := resolveTrailer(doc)
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

	// Capture the input's appearance before any fixup mutates the graph, so
	// the final fidelity comparison sees the original.
	var inputRenders []fidelity.Summary
	if o.CheckFidelity {
		inputRenders = fidelity.Summarize(trailer, fidelity.DPI)
	}

	spec, err := verify.LevelSpec(p)
	if err != nil {
		return ConvertResult{}, fmt.Errorf("convert: %w", err)
	}

	if err := rs.applyPreemptive(&trailer, doc, spec); err != nil {
		return ConvertResult{}, fmt.Errorf("convert: pre-emptive fixups: %w", err)
	}

	r := &run{ctx: ctx, doc: doc, p: p, o: o, spec: spec, trailer: trailer, objHint: doc.Footprint().Objects}
	r.fixers = rs.fixersFor(&fixerRun{
		ctx:       ctx,
		doc:       doc,
		spec:      &r.spec,
		dpi:       o.dpi(),
		decode:    decoderFor(doc),
		fontSrc:   &appearanceFontSource{},
		formDrops: &r.formDrops,
		lost:      &r.lost,
	})

	if err := r.fixLoop(); err != nil {
		return ConvertResult{}, fmt.Errorf("convert: %w", err)
	}
	if err := r.backstop(); err != nil {
		return ConvertResult{}, fmt.Errorf("convert: %w", err)
	}
	r.cr.RasterDrops = append(r.cr.RasterDrops, r.formDrops...)
	r.cr.LostObjects = append(r.cr.LostObjects, r.lost...)

	// A fixer's raster pass stops dispatching on cancellation without
	// reporting it, so check before committing to the output.
	if err := ctx.Err(); err != nil {
		return ConvertResult{}, fmt.Errorf("convert: %w", err)
	}

	// Final serialize + verify against the actual output bytes (structural checks
	// like xref format must run on the written output, not the original reader).
	if err := r.serializeAndVerify(); err != nil {
		return ConvertResult{}, fmt.Errorf("convert: %w", err)
	}
	cr := r.cr

	// Objects the reader degraded to null were serialized as null: their
	// content is lost. The final verify ran on the output bytes, where the
	// loss is invisible, so it is recorded here instead -- as loss, not as
	// non-conformance. The file that was written really does meet PDF/A-1b;
	// what it no longer holds is a separate fact, and one a caller wants
	// separately. (Recovered objects are not carried: the rewrite emits a
	// correct xref, genuinely fixing them.)
	for _, e := range spec.Issues(doc.DegradedObjects()) {
		if p.Has(e.Check()) {
			cr.LostObjects = append(cr.LostObjects, e)
		}
	}

	// The run is over, so the input's decoded and tokenized streams are dead
	// weight. They matter for a caller who keeps the Document open past
	// Convert -- the run's own peak is behind us either way.
	defer doc.ReleaseCaches()

	// Compare the converted output's appearance to the input captured above.
	if o.CheckFidelity && cr.backing.len() > 0 {
		if out, err := cr.backing.open(); err == nil {
			cr.Fidelity = fidelity.Compare(inputRenders, summarizeTrailerPagesOf(out))
			cr.BlankedPages = fidelity.BlankedPages(cr.Fidelity)
			cr.OverpaintedPages = fidelity.OverpaintedPages(cr.Fidelity)
			out.Close()
		}
	}
	return cr, nil
}

// run is one conversion in progress: the document and the level it is
// converted to, the graph being repaired, and the verdict on that graph.
type run struct {
	ctx     context.Context
	doc     *pdf.Reader
	p       *pdf.Profile
	o       Options
	spec    pdfa.Spec
	trailer pdf.PDFDict
	fixers  map[pdf.Check]Fixer
	cr      ConvertResult

	// graphClean records whether the in-heap graph is unchanged since the
	// most recent verifyGraph checked it. It is true right after each verify
	// and false once a fixer or flattener edits the graph. When it is true at
	// the end of the run, serializeAndVerify reuses lastParts.Graph instead of
	// verifying the graph again against the output bytes.
	graphClean bool
	lastParts  verify.Parts

	// objHint sizes the writer's discovery index. The resolved object count
	// is the first estimate; after that each fix-loop verify reports what it
	// actually numbered. See writer.newPDFWriter.
	objHint int

	// formDrops and lost collect what fixers render or carry over
	// incompletely.
	formDrops []RasterDrop
	lost      []pdf.PDFError
}

// fixLoop verifies the graph and runs a fix pass on what the verify reports,
// until the graph is valid, a pass changes nothing or makes no progress, or
// the iteration limit is reached.
func (r *run) fixLoop() error {
	var prevCounts map[pdf.Check]int
	for iter := 1; iter <= r.o.iterations(); iter++ {
		if err := r.ctx.Err(); err != nil {
			return err
		}
		r.cr.Iterations = iter

		objs := r.verifyGraph()
		r.objHint = len(objs)

		if r.cr.Result.Valid {
			return nil
		}

		counts := violationCounts(r.cr.Result.Issues)
		if sameMultiset(counts, prevCounts) {
			return nil // no progress since last iteration
		}
		prevCounts = counts

		changed, err := dispatchFixers(&fixPass{trailer: &r.trailer, objs: objs}, r.cr.Result, r.fixers)
		if err != nil {
			return err
		}
		if !changed {
			return nil
		}
		r.graphClean = false
	}
	return nil
}

// verifyGraph verifies the in-heap graph, records the verdict as the run's
// result and marks the graph clean. It returns the ObjNum -> object index
// inHeapVerify built, valid until the next renumbering.
func (r *run) verifyGraph() map[int]pdf.PDFValue {
	result, parts, objs := inHeapVerify(r.doc, r.trailer, r.p, r.spec, r.objHint)
	r.cr.Result = result
	r.lastParts, r.graphClean = parts, true
	return objs
}

// summarizeTrailerPagesOf resolves out's graph and measures its pages at the
// fidelity DPI, returning nil on any resolve failure (the comparison then has
// no output baseline and reports the pages as lost).
func summarizeTrailerPagesOf(out *pdf.Reader) []fidelity.Summary {
	trailer, err := resolveTrailer(out)
	if err != nil {
		return nil
	}
	return fidelity.Summarize(trailer, fidelity.DPI)
}

// inHeapVerify verifies the in-memory trailer graph without serializing it,
// by numbering objects and seeding the doc reader directly. It also returns
// the split issue parts (for serializeAndVerify's merged final verify) and
// the ObjNum -> object index so the fixer loop can target issues by ref;
// the index is only valid until the next renumbering.
func inHeapVerify(doc *pdf.Reader, trailer pdf.PDFDict, p *pdf.Profile, spec pdfa.Spec, hint int) (pdf.Result, verify.Parts, map[int]pdf.PDFValue) {
	objs := writer.NumberObjects(trailer, hint)
	doc.SeedResolvedGraph(trailer, objs)
	parts := verify.VerifyParts(doc, p, spec)
	return verify.ResultFromIssues(p, parts.Issues()), parts, objs
}

// fullFinalVerify forces serializeAndVerify's full from-scratch verify even
// when the graph is clean -- an escape hatch, and a lever for oracle tests
// that cross-check the merged path against the full one.
var fullFinalVerify = os.Getenv("GOPDFRAB_FULL_FINAL_VERIFY") == "1"

// serializeAndVerify serializes the graph and verifies the output bytes,
// updating the run's output and result. It is called exactly once at the end
// of Run. The output Reader adopts the stream caches of the Reader the fix
// loop used: unchanged streams keep their decoded and tokenized results, while
// rewritten streams miss because their bytes are new.
//
// If the graph is clean, unchanged since the last inHeapVerify, its graph-side
// checks would replay that verify exactly. The output Reader is seeded with the
// same graph and stream caches, and TestConvertSeededVerifyMatchesFreshVerify
// pins the equivalence. Only the byte-level structural checks then run against
// the output, and lastParts supplies the graph verdicts. A dirty graph gets the
// full verify.
func (r *run) serializeAndVerify() (err error) {
	loopDoc, trailer, cr, p := r.doc, r.trailer, &r.cr, r.p
	var sw spillWriter
	if loopDoc != nil {
		sw.grow(int(loopDoc.Size()))
	}
	order, err := writer.WriteDocumentIndexed(&sw, trailer, r.objHint)
	if err != nil {
		sw.abort()
		return err
	}
	backing, err := sw.finish()
	if err != nil {
		return err
	}
	cr.backing = backing
	// The caller discards cr on error, so release the output here rather
	// than leave a spilled temp file to the finalizer.
	defer func() {
		if err != nil {
			backing.close()
			cr.backing = nil
		}
	}()

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

	if r.graphClean && !fullFinalVerify {
		parts, err := verify.VerifyStructural(out, p)
		if err != nil {
			return err
		}
		parts.Graph = r.lastParts.Graph
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

// dispatchFixers runs one fix pass over the checks that result reports.
//
// Each fixer runs once, at its first reported check in clause order; a
// leadsItsClause fixer runs first within its clause. A fixer receives the
// issues of every reported check it covers and repairs only those.
// batchDictFixer implementations share a single graph walk, targetedFixer
// implementations visit only the objects their issues reference, and all
// other fixers run their own Fix. The fixed order makes conversion
// deterministic.
func dispatchFixers(pass *fixPass, result pdf.Result, localFixers map[pdf.Check]Fixer) (bool, error) {
	changed := false
	var visitors []func(pdf.PDFDict)
	reported := sortedChecks(violationCounts(result.Issues))
	leads := func(c pdf.Check) bool {
		l, ok := localFixers[c].(leadsItsClause)
		return ok && l.leadsItsClause()
	}
	sort.SliceStable(reported, func(i, j int) bool {
		a, b := reported[i], reported[j]
		if a.Clause() != b.Clause() {
			return a.Clause() < b.Clause()
		}
		return leads(a) && !leads(b)
	})
	dispatched := map[pdf.Check]bool{}
	for i, c := range reported {
		fixer, ok := localFixers[c]
		if !ok || dispatched[c] {
			continue
		}
		var issues []pdf.PDFError
		for _, rc := range reported[i:] {
			if fixer.Applies(rc) {
				dispatched[rc] = true
				issues = append(issues, result.IssuesForCheck(rc)...)
			}
		}
		if bf, isBatch := fixer.(batchDictFixer); isBatch {
			if visit, ok := bf.prepare(pass.trailer, issues, &changed); ok {
				visitors = append(visitors, visit)
			}
			continue
		}
		if tf, ok := fixer.(targetedFixer); ok {
			ch, handled, err := tf.fixTargeted(pass, issues)
			if err != nil {
				return false, fmt.Errorf("targeted fixer for check %q: %w", c.Name(), err)
			}
			if handled {
				if ch {
					changed = true
				}
				continue
			}
		}
		gf, ok := fixer.(graphFixer)
		if !ok {
			continue
		}
		ch, err := gf.Fix(pass.trailer, issues)
		if err != nil {
			return false, fmt.Errorf("fixer for check %q: %w", c.Name(), err)
		}
		if ch {
			changed = true
		}
	}
	if len(visitors) > 0 {
		walkDicts(*pass.trailer, map[uintptr]bool{}, func(d pdf.PDFDict) {
			for _, visit := range visitors {
				visit(d)
			}
		})
	}
	return changed, nil
}

// sortedChecks returns counts' keys ordered by Part 1 clause, then
// subclause, then ID, giving the fixer loop a stable application order. A
// Part 1 (clause, subclause) pair identifies one check, so the ID decides only
// among the rules of Part 2 alone, which have no Part 1 clause.
func sortedChecks(counts map[pdf.Check]int) []pdf.Check {
	checks := make([]pdf.Check, 0, len(counts))
	for c := range counts {
		checks = append(checks, c)
	}
	sort.Slice(checks, func(i, j int) bool {
		a, b := checks[i], checks[j]
		if a.Clause() != b.Clause() {
			return a.Clause() < b.Clause()
		}
		if a.Subclause() != b.Subclause() {
			return a.Subclause() < b.Subclause()
		}
		return a.ID() < b.ID()
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

// resolveTrailer resolves doc's object graph and returns its trailer. The
// graph root is the Reader's effective trailer, a PDFDict that resolves in
// place to itself (and SeedResolvedGraph only accepts a PDFDict), so the type
// assertion cannot fail; TestResolveGraphRootIsAlwaysADict guards that
// assumption for both a parsed and a seeded Reader.
func resolveTrailer(doc *pdf.Reader) (pdf.PDFDict, error) {
	graph, err := doc.ResolveGraph()
	if err != nil {
		return pdf.PDFDict{}, err
	}
	return graph.(pdf.PDFDict), nil
}
