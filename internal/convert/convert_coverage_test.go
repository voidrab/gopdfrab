package convert

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfa"
	"github.com/voidrab/gopdfrab/internal/pdfgen"
	"github.com/voidrab/gopdfrab/internal/verify"
	"github.com/voidrab/gopdfrab/internal/writer"
)

// onePageTrailer builds a minimal in-heap one-page document graph.
func onePageTrailer() pdf.PDFDict {
	page := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type":     pdf.PDFName{Value: "Page"},
		"Contents": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{}), HasStream: true, RawStream: []byte("1 0 0 rg 0 0 10 10 re f")},
		"MediaBox": pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFInteger(10), pdf.PDFInteger(10)},
	})}
	pages := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Pages"},
		"Kids": pdf.PDFArray{page},
	})}
	root := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type":  pdf.PDFName{Value: "Catalog"},
		"Pages": pages,
	})}
	return pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Root": root})}
}

// openTrailer serializes trailer and reopens it as a Reader.
func openTrailer(t *testing.T, trailer pdf.PDFDict) *pdf.Reader {
	t.Helper()
	var buf bytes.Buffer
	if _, err := writer.WriteDocumentIndexed(&buf, trailer, 0); err != nil {
		t.Fatalf("WriteDocumentIndexed: %v", err)
	}
	doc, err := pdf.OpenBytes(buf.Bytes())
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	t.Cleanup(func() { doc.Close() })
	return doc
}

// TestRasterBackstopFlattensAllPages drives the document-wide flatten branch:
// a residual fixable issue with no page attribution can't be targeted by the
// page-by-page raster pass, so the backstop flattens every page.
func TestRasterBackstopFlattensAllPages(t *testing.T) {
	trailer := onePageTrailer()
	doc := openTrailer(t, trailer)

	c := pdf.Checks.Colour.OutputIntentNotArray
	cr := &ConvertResult{Result: pdf.Result{
		Valid:  false,
		Issues: []pdf.PDFError{pdf.NewError(c, []error{errors.New("synthetic residual")}, 0, nil)},
	}}
	fixers := map[pdf.Check]Fixer{c: nil}
	var lastParts verify.Parts
	graphClean := true // the synthetic issues stand for a verify of this graph

	if err := rasterBackstop(context.Background(), doc, &trailer, cr, pdf.PDFA1B, a1b(), fixers, &lastParts, &graphClean, defaultRasterDPI, 0); err != nil {
		t.Fatalf("rasterBackstop: %v", err)
	}
	if cr.Iterations != 1 {
		t.Errorf("Iterations = %d, want 1 (one flatten-all verify)", cr.Iterations)
	}
	if !graphClean {
		t.Error("graphClean = false after the backstop's verify")
	}
	page := trailer.Entries.Get("Root").(pdf.PDFDict).Entries.Get("Pages").(pdf.PDFDict).Entries.Get("Kids").(pdf.PDFArray)[0].(pdf.PDFDict)
	res, ok := page.Entries.Get("Resources").(pdf.PDFDict)
	if !ok {
		t.Fatal("page was not flattened: no Resources dict")
	}
	if xo, ok := res.Entries.Get("XObject").(pdf.PDFDict); !ok || xo.Entries.Get("Im0") == nil {
		t.Error("page was not flattened: Resources/XObject/Im0 missing")
	}
}

// countdownCtx reports cancellation from its (n+1)th Err call on, so a test
// can cancel at each successive check of a run in turn.
type countdownCtx struct {
	context.Context
	left atomic.Int64
}

func newCountdownCtx(n int64) *countdownCtx {
	c := &countdownCtx{Context: context.Background()}
	c.left.Store(n)
	return c
}

func (c *countdownCtx) Err() error {
	if c.left.Add(-1) < 0 {
		return context.Canceled
	}
	return nil
}

// TestRasterBackstopCancelledMidPass covers both raster blocks' post-pass
// cancellation checks: a context cancelled while pages were being rasterized
// abandons the run before re-verifying, even though the graph changed. Each
// countdown cancels at a later check -- inside the pass, where it simply
// changes nothing, or after it; some countdown must land on the check right
// after the pass.
func TestRasterBackstopCancelledMidPass(t *testing.T) {
	c := pdf.Checks.Colour.OutputIntentNotArray
	fixers := map[pdf.Check]Fixer{c: nil}
	for name, page := range map[string]int{"pageTargeted": 1, "docWide": 0} {
		t.Run(name, func(t *testing.T) {
			for n := int64(1); n < 64; n++ {
				trailer := onePageTrailer()
				cr := &ConvertResult{Result: pdf.Result{
					Valid:  false,
					Issues: []pdf.PDFError{pdf.NewError(c, []error{errors.New("synthetic")}, page, nil)},
				}}
				doc := openTrailer(t, trailer)
				var lastParts verify.Parts
				graphClean := true
				err := rasterBackstop(newCountdownCtx(n), doc, &trailer, cr, pdf.PDFA1B, a1b(), fixers, &lastParts, &graphClean, defaultRasterDPI, 0)
				if err == nil && cr.Iterations > 0 {
					t.Fatalf("countdown %d: ran to completion without seeing the cancellation", n)
				}
				if err == nil {
					continue // the pass itself saw the cancellation and changed nothing
				}
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("countdown %d: err = %v, want context.Canceled", n, err)
				}
				pg := trailer.Entries.Get("Root").(pdf.PDFDict).Entries.Get("Pages").(pdf.PDFDict).Entries.Get("Kids").(pdf.PDFArray)[0].(pdf.PDFDict)
				if _, rastered := pg.Entries.Get("Resources").(pdf.PDFDict); rastered {
					if cr.Iterations != 0 || !graphClean {
						t.Fatalf("countdown %d: a cancelled pass still counted an iteration (%d) or dirtied the graph", n, cr.Iterations)
					}
					return
				}
			}
			t.Fatal("no countdown cancelled right after a completed raster pass")
		})
	}
}

// TestRunContextCancelledBeforeOutput pins Run's last check: a cancellation
// that lands after the fix loop and backstop, but before the output is
// serialized, still fails the run. A document already valid under pdf.PDF
// polls a fixed number of times; cancelling on the last poll must fail it,
// and one poll later it must succeed.
func TestRunContextCancelledBeforeOutput(t *testing.T) {
	data := buildOnePageDoc(t, func(_, _, _ pdf.PDFDict) {})
	run := func(ctx context.Context) (ConvertResult, error) {
		doc, err := pdf.OpenBytes(data)
		if err != nil {
			t.Fatal(err)
		}
		defer doc.Close()
		return RunContext(ctx, doc, pdf.PDF, Options{})
	}
	const budget = 1 << 20
	counter := newCountdownCtx(budget)
	cr, err := run(counter)
	if err != nil || !cr.Result.Valid {
		t.Fatalf("uncancelled run: err=%v valid=%v", err, cr.Result.Valid)
	}
	cr.Close()
	polls := budget - counter.left.Load()

	if _, err := run(newCountdownCtx(polls - 1)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled on the last of %d polls: err = %v, want context.Canceled", polls, err)
	}
	cr, err = run(newCountdownCtx(polls))
	if err != nil {
		t.Fatalf("cancelled after the last poll: err = %v, want success", err)
	}
	cr.Close()
}

// TestRasterBackstopSkipsUnfixableIssues pins the no-op guard.
func TestRasterBackstopSkipsUnfixableIssues(t *testing.T) {
	trailer := onePageTrailer()
	cr := &ConvertResult{Result: pdf.Result{
		Valid:  false,
		Issues: []pdf.PDFError{pdf.NewError(pdf.Checks.Colour.OutputIntentNotArray, []error{errors.New("x")}, 0, nil)},
	}}
	var lastParts verify.Parts
	graphClean := true
	// No fixer registered for the issue's check: nothing to do.
	if err := rasterBackstop(context.Background(), nil, &trailer, cr, pdf.PDFA1B, a1b(), map[pdf.Check]Fixer{}, &lastParts, &graphClean, defaultRasterDPI, 0); err != nil {
		t.Fatalf("rasterBackstop: %v", err)
	}
	if cr.Iterations != 0 || !graphClean {
		t.Errorf("backstop acted on an unfixable issue: iterations=%d graphClean=%v", cr.Iterations, graphClean)
	}
}

// TestSerializeAndVerifyRejectsBadProfile covers both final-verify paths'
// error returns (merged/clean and full/dirty) via the nil-profile guard.
func TestSerializeAndVerifyRejectsBadProfile(t *testing.T) {
	for _, clean := range []bool{true, false} {
		cr := &ConvertResult{}
		err := serializeAndVerify(nil, onePageTrailer(), cr, nil, verify.Parts{}, clean, 0)
		if err == nil {
			t.Errorf("serializeAndVerify(nil profile, graphClean=%v) did not error", clean)
		}
	}
}

// TestRunRejectsUndefinedProfile covers the in-heap verify error path in Run.
func TestRunRejectsUndefinedProfile(t *testing.T) {
	doc := openTrailer(t, onePageTrailer())
	_, err := Run(doc, &pdf.Profile{Level: pdf.Undefined}, Options{})
	if err == nil || !strings.Contains(err.Error(), "convert:") {
		t.Errorf("Run(Undefined profile) err = %v, want a wrapped convert error", err)
	}
}

// TestRunWrapsSerializeError covers Run's final-serialize error path: a
// graph value the writer cannot serialize survives verification and the fix
// loop untouched, then fails WriteDocumentIndexed.
func TestRunWrapsSerializeError(t *testing.T) {
	doc := openTrailer(t, onePageTrailer())
	g, err := doc.ResolveGraph()
	if err != nil {
		t.Fatalf("ResolveGraph: %v", err)
	}
	trailer := g.(pdf.PDFDict)
	root := trailer.Entries.Get("Root").(pdf.PDFDict)
	root.Entries.Set("Bogus", struct{ X int }{1})

	_, err = Run(doc, pdf.PDFA1B, Options{})
	if err == nil || !strings.Contains(err.Error(), "unsupported value type") {
		t.Errorf("Run over an unserializable graph err = %v, want an unsupported-value-type error", err)
	}
}

// TestApplyPreemptiveFixupsAfterFixupError covers the after-walk phase's
// error propagation.
func TestApplyPreemptiveFixupsAfterFixupError(t *testing.T) {
	old := preemptiveAfterFixups
	t.Cleanup(func() { preemptiveAfterFixups = old })
	preemptiveAfterFixups = append(slices.Clone(old), func(*pdf.PDFDict, *pdf.Reader, pdfa.Spec) error {
		return errors.New("after fixup failed")
	})

	doc := openTrailer(t, onePageTrailer())
	g, err := doc.ResolveGraph()
	if err != nil {
		t.Fatalf("ResolveGraph: %v", err)
	}
	trailer := g.(pdf.PDFDict)
	if err := applyPreemptiveFixups(&trailer, doc, a1b()); err == nil || !strings.Contains(err.Error(), "after fixup failed") {
		t.Errorf("applyPreemptiveFixups err = %v, want the after-fixup failure", err)
	}
}

// TestPromoteEmptyGlyphsInFontGuards drives the visitor's guard cascade with
// synthetic dicts: wrong subtype, missing descriptor, missing/streamless
// FontFile2, and an undecodable program all leave the dict untouched.
func TestPromoteEmptyGlyphsInFontGuards(t *testing.T) {
	notCID := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Subtype": pdf.PDFName{Value: "TrueType"}})}
	promoteEmptyGlyphsInFont(notCID)

	noDesc := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Subtype": pdf.PDFName{Value: "CIDFontType2"}})}
	promoteEmptyGlyphsInFont(noDesc)

	noFF := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Subtype":        pdf.PDFName{Value: "CIDFontType2"},
		"FontDescriptor": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{})},
	})}
	promoteEmptyGlyphsInFont(noFF)

	streamless := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Subtype": pdf.PDFName{Value: "CIDFontType2"},
		"FontDescriptor": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"FontFile2": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{})},
		})},
	})}
	promoteEmptyGlyphsInFont(streamless)

	badFF := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Filter": pdf.PDFName{Value: "NoSuchFilter"},
	}), HasStream: true, RawStream: []byte("junk")}
	undecodable := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Subtype": pdf.PDFName{Value: "CIDFontType2"},
		"FontDescriptor": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"FontFile2": badFF,
		})},
	})}
	promoteEmptyGlyphsInFont(undecodable)
	if string(badFF.RawStream) != "junk" {
		t.Error("undecodable FontFile2 was rewritten")
	}
}

// TestPagesTreeArrayFixerRebalances covers the Fix wrapper and the lazily
// computed replacement object numbers: an oversized Kids array is split into
// a tree of intermediate Pages nodes.
func TestPagesTreeArrayFixerRebalances(t *testing.T) {
	kids := make(pdf.PDFArray, maxPDFArrayElements+1)
	for i := range kids {
		kids[i] = pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Type": pdf.PDFName{Value: "Page"}})}
	}
	pages := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Pages"},
		"Kids": kids,
	})}
	root := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type":  pdf.PDFName{Value: "Catalog"},
		"Pages": pages,
	})}
	trailer := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Root": root})}

	changed, err := pagesTreeArrayFixer{}.Fix(&trailer, nil)
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if !changed {
		t.Fatal("Fix reported no change for an oversized Kids array")
	}
	got, ok := pages.Entries.Get("Kids").(pdf.PDFArray)
	if !ok || len(got) > maxPDFArrayElements {
		t.Errorf("Kids not rebalanced: len = %d, want <= %d", len(got), maxPDFArrayElements)
	}

	// A conforming tree is left alone.
	changed, err = pagesTreeArrayFixer{}.Fix(&trailer, nil)
	if err != nil || changed {
		t.Errorf("second Fix = changed %v, err %v; want no-op", changed, err)
	}
}

// TestPagesTreeArrayFixerDropsOversizedStructure covers Fix's structure-drop
// branch: a struct tree holding an unsplittable oversized array is removed.
func TestPagesTreeArrayFixerDropsOversizedStructure(t *testing.T) {
	parents := make(pdf.PDFArray, maxPDFArrayElements+1)
	for i := range parents {
		parents[i] = pdf.PDFInteger(i)
	}
	st := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "StructTreeRoot"},
		"K":    parents,
	})}
	root := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type":           pdf.PDFName{Value: "Catalog"},
		"StructTreeRoot": st,
	})}
	trailer := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Root": root})}

	changed, err := pagesTreeArrayFixer{}.Fix(&trailer, nil)
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if !changed {
		t.Fatal("Fix reported no change for an oversized struct tree")
	}
	if _, still := root.Entries.Lookup("StructTreeRoot"); still {
		t.Error("StructTreeRoot was not dropped")
	}
}

// TestFlattenPagesParallelDedupesAndReports pins three properties of the
// page-level raster backstop that only show up with a mixed batch: one page
// object reachable at two positions in the tree is rasterized once, a page the
// rasterizer cannot draw is excluded from RasterizedPages instead of being
// reported as flattened, and dropped features are attributed to the right page
// number.
func TestFlattenPagesParallelDedupesAndReports(t *testing.T) {
	renderable := func(content string) pdf.PDFDict {
		return pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"Type": pdf.PDFName{Value: "Page"},
			"Contents": pdf.PDFDict{
				Entries: pdf.DictOf(map[string]pdf.PDFValue{}), HasStream: true, RawStream: []byte(content),
			},
		})}
	}

	shared := renderable("q Q")
	// An sh operator with no shading resource is unusable content: the page
	// still rasterizes, but the feature is reported dropped.
	dropping := renderable("q /Missing sh Q")
	// A zero-area media box cannot be rendered at all.
	unrenderable := renderable("q Q")

	box := [4]float64{0, 0, 20, 20}
	pages := []pageTarget{
		{dict: shared, mediaBox: box},
		{dict: dropping, mediaBox: box},
		{dict: shared, mediaBox: box}, // the same object again
		{dict: unrenderable, mediaBox: [4]float64{0, 0, 0, 0}},
	}
	drops, rasterized, changed := flattenPagesParallel(context.Background(), pages, []int{1, 2, 3, 4}, 72)

	if !changed {
		t.Fatal("changed = false, want true")
	}
	if !slices.Equal(rasterized, []int{1, 2}) {
		t.Errorf("rasterized = %v, want [1 2]: page 3 is page 1's object and page 4 does not render", rasterized)
	}
	if len(drops) != 1 || drops[0].Page != 2 || len(drops[0].Features) == 0 {
		t.Errorf("drops = %+v, want one entry for page 2", drops)
	}

	// A batch with nothing in it reports no change rather than an empty rewrite.
	if _, _, changed := flattenPagesParallel(context.Background(), nil, nil, 72); changed {
		t.Error("an empty page batch reported changed = true")
	}
}

// TestSortedChecksOrdering pins the fixer application order -- clause, then
// subclause, then name -- over the whole catalog. The order has to be total:
// it decides which fixer runs first, and any pair left to map order would make
// the converted bytes differ between runs.
//
// It also records why sortedChecks's name comparison never actually decides
// anything today: (clause, subclause) is already unique across every
// registered check, so the tiebreak is there for a check added later that
// collides. If this stops holding, the tiebreak becomes live and the assertion
// below starts exercising it.
func TestSortedChecksOrdering(t *testing.T) {
	all := pdf.AllChecks()
	counts := make(map[pdf.Check]int, len(all))
	for _, c := range all {
		counts[c] = 1
	}
	got := sortedChecks(counts)
	if len(got) != len(counts) {
		t.Fatalf("sortedChecks returned %d checks, want %d", len(got), len(counts))
	}

	type key struct {
		clause    string
		subclause int
	}
	seen := make(map[key]string, len(got))
	for i, cur := range got {
		k := key{cur.Clause(), cur.Subclause()}
		if prevName, dup := seen[k]; dup {
			t.Logf("checks %q and %q share %s/%d: the name tiebreak is live",
				prevName, cur.Name(), k.clause, k.subclause)
		}
		seen[k] = cur.Name()
		if i == 0 {
			continue
		}
		prev := got[i-1]
		switch {
		case prev.Clause() != cur.Clause():
			if prev.Clause() > cur.Clause() {
				t.Fatalf("clause out of order at %d: %q then %q", i, prev.Clause(), cur.Clause())
			}
		case prev.Subclause() != cur.Subclause():
			if prev.Subclause() > cur.Subclause() {
				t.Fatalf("subclause out of order at %d: %d then %d", i, prev.Subclause(), cur.Subclause())
			}
		case prev.Name() >= cur.Name():
			t.Errorf("name out of order within %s/%d at %d: %q then %q",
				cur.Clause(), cur.Subclause(), i, prev.Name(), cur.Name())
		}
	}

	// The order must not depend on map iteration order.
	for range 5 {
		again := sortedChecks(counts)
		for i := range got {
			if again[i] != got[i] {
				t.Fatalf("sortedChecks is not deterministic: position %d was %q, now %q",
					i, got[i].Name(), again[i].Name())
			}
		}
	}
}

// failingFixer stands in for every registered fixer so a fixer error, a
// targeted-fixer error or a mid-run cancellation can be provoked on any input
// that has issues.
type failingFixer struct {
	check pdf.Check
	err   error
	onFix func()
}

func (f failingFixer) Applies(c pdf.Check) bool { return c == f.check }
func (f failingFixer) Fix(*pdf.PDFDict, []pdf.PDFError) (bool, error) {
	if f.onFix != nil {
		f.onFix()
	}
	return false, f.err
}

// failingTargetedFixer adds the targetedFixer capability to failingFixer.
type failingTargetedFixer struct{ failingFixer }

func (f failingTargetedFixer) fixTargeted(*fixPass, []pdf.PDFError) (bool, bool, error) {
	return false, true, f.err
}

// lzwPDF is a one-page document whose content stream carries an LZWDecode
// filter -- a PDF/A-1 violation with a registered, non-preemptive fixer and a
// page attribution.
func lzwPDF() []byte {
	b := pdfgen.NewBuilder("%PDF-1.4\n")
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.Obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] /Contents 4 0 R >>")
	b.StreamObj(4, "/Filter /LZWDecode", []byte{0x80, 0x0B, 0x60, 0x50, 0x22, 0x0C, 0x0C, 0x85, 0x01})
	return b.FinishClassic("<< /Size 5 /Root 1 0 R >>")
}

// replaceAllFixers swaps every registered fixer for mk(check) until the test
// ends.
func replaceAllFixers(t *testing.T, mk func(pdf.Check) Fixer) {
	t.Helper()
	old := fixerRegistry
	fixerRegistry = map[pdf.Check]Fixer{}
	for c := range old {
		fixerRegistry[c] = mk(c)
	}
	t.Cleanup(func() { fixerRegistry = old })
}

func TestRunWrapsFixerErrors(t *testing.T) {
	boom := errors.New("fixer boom")
	for name, mk := range map[string]func(pdf.Check) Fixer{
		"Fix":         func(c pdf.Check) Fixer { return failingFixer{check: c, err: boom} },
		"fixTargeted": func(c pdf.Check) Fixer { return failingTargetedFixer{failingFixer{check: c, err: boom}} },
	} {
		t.Run(name, func(t *testing.T) {
			replaceAllFixers(t, mk)
			_, err := ConvertBytes(lzwPDF(), pdf.PDFA1B, Options{})
			if !errors.Is(err, boom) || !strings.Contains(err.Error(), "fixer for check") {
				t.Errorf("err = %v, want it to wrap the fixer error and name the check", err)
			}
		})
	}
}

func TestRunWrapsPreemptiveFixupError(t *testing.T) {
	old := preemptiveFixups
	t.Cleanup(func() { preemptiveFixups = old })
	boom := errors.New("preemptive boom")
	preemptiveFixups = append(slices.Clone(old), func(*pdf.PDFDict, *pdf.Reader, pdfa.Spec) error { return boom })

	_, err := ConvertBytes(pdfgen.PlainThreeIssue(), pdf.PDFA1B, Options{})
	if !errors.Is(err, boom) || !strings.Contains(err.Error(), "pre-emptive fixups") {
		t.Errorf("err = %v, want the wrapped pre-emptive fixup error", err)
	}
}

// stalledFixer claims to have changed the document on every pass without
// touching it, the way a fixer that cannot make headway looks to the loop.
type stalledFixer struct{ check pdf.Check }

func (f stalledFixer) Applies(c pdf.Check) bool                     { return c == f.check }
func (stalledFixer) Fix(*pdf.PDFDict, []pdf.PDFError) (bool, error) { return true, nil }

// TestRunStopsWhenFixesMakeNoProgress: a pass whose fixers report changes but
// leave the same issues behind ends the loop on the next iteration rather
// than running to the iteration cap.
func TestRunStopsWhenFixesMakeNoProgress(t *testing.T) {
	replaceAllFixers(t, func(c pdf.Check) Fixer { return stalledFixer{check: c} })

	const limit = 10
	cr, err := ConvertBytes(lzwPDF(), pdf.PDFA1B, Options{MaxIterations: limit})
	if err != nil {
		t.Fatalf("ConvertBytes: %v", err)
	}
	defer cr.Close()
	// Two loop iterations, then the raster backstop's own.
	if cr.Iterations >= limit {
		t.Errorf("Iterations = %d, want the loop to stop well before the cap of %d", cr.Iterations, limit)
	}
}

// TestRunCancelledDuringFixPassSkipsBackstop cancels the context from inside a
// fixer that changes nothing: the loop ends, and the raster backstop must
// notice the cancellation before it rasterizes anything.
func TestRunCancelledDuringFixPassSkipsBackstop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	replaceAllFixers(t, func(c pdf.Check) Fixer { return failingFixer{check: c, onFix: cancel} })

	_, err := ConvertBytesContext(ctx, lzwPDF(), pdf.PDFA1B, Options{})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

// TestRunUnresolvableGraphWithUnverifiableProfile: when the graph cannot be
// resolved and the best-effort verify fails too, the resolve error is still
// returned, with no result riding along.
func TestRunUnresolvableGraphWithUnverifiableProfile(t *testing.T) {
	cr, err := ConvertBytes(unresolvableChainPDF(), &pdf.Profile{Level: pdf.Undefined}, Options{})
	if !errors.Is(err, pdf.ErrUnresolvableGraph) {
		t.Fatalf("err = %v, want ErrUnresolvableGraph", err)
	}
	if len(cr.Result.Issues) != 0 {
		t.Errorf("Result carries %d issues, want none because the verify failed", len(cr.Result.Issues))
	}
}

// unresolvableChainPDF has a reference chain past the resolve depth cap, so its
// graph cannot be resolved.
func unresolvableChainPDF() []byte {
	const chain = 70000
	b := pdfgen.NewBuilder("%PDF-1.4\n")
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R /Deep 4 0 R >>")
	b.Obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] >>")
	for i := 4; i < 4+chain; i++ {
		b.Obj(i, fmt.Sprintf("[%d 0 R]", i+1))
	}
	b.Obj(4+chain, "42")
	return b.FinishClassic("<< /Size 5 /Root 1 0 R >>")
}

func TestSummarizeTrailerPagesOfUnresolvableOutput(t *testing.T) {
	doc, err := pdf.OpenBytes(unresolvableChainPDF())
	if err != nil {
		t.Fatal(err)
	}
	defer doc.Close()
	if got := summarizeTrailerPagesOf(doc); got != nil {
		t.Errorf("summarizeTrailerPagesOf = %v, want nil for an unresolvable graph", got)
	}
}

func TestSerializeAndVerifyFullVerifyOnDirtyGraph(t *testing.T) {
	trailer := onePageTrailer()
	doc := openTrailer(t, trailer)
	var cr ConvertResult
	if err := serializeAndVerify(doc, trailer, &cr, pdf.PDFA1B, verify.Parts{}, false, 0); err != nil {
		t.Fatal(err)
	}
	defer cr.Close()
	if cr.Result.Type != pdf.A1B {
		t.Errorf("Result.Type = %v, want A1B (full verify ran)", cr.Result.Type)
	}
	if b, err := cr.Output(); err != nil || len(b) == 0 {
		t.Errorf("Output = %d bytes, %v; want the serialized document", len(b), err)
	}
}

// TestResolveGraphRootIsAlwaysADict guards resolveTrailer's unchecked type
// assertion: a parsed Reader and a seeded one both resolve to a PDFDict.
func TestResolveGraphRootIsAlwaysADict(t *testing.T) {
	parsed, err := pdf.OpenBytes(pdfgen.PlainThreeIssue())
	if err != nil {
		t.Fatal(err)
	}
	defer parsed.Close()
	seeded := openTrailer(t, onePageTrailer())
	seeded.SeedResolvedGraph(onePageTrailer(), nil)
	for name, r := range map[string]*pdf.Reader{"parsed": parsed, "seeded": seeded} {
		for pass := 0; pass < 2; pass++ { // second pass takes the cached-graph path
			g, err := r.ResolveGraph()
			if err != nil {
				t.Fatalf("%s: ResolveGraph: %v", name, err)
			}
			if _, ok := g.(pdf.PDFDict); !ok {
				t.Fatalf("%s: ResolveGraph returned %T, want PDFDict", name, g)
			}
		}
	}
}

func TestSerializeAndVerifyFullVerifyError(t *testing.T) {
	trailer := onePageTrailer()
	doc := openTrailer(t, trailer)
	var cr ConvertResult
	err := serializeAndVerify(doc, trailer, &cr, &pdf.Profile{Level: pdf.Undefined}, verify.Parts{}, false, 0)
	if err == nil {
		t.Fatal("serializeAndVerify with an undefined profile succeeded")
	}
	if cr.backing != nil {
		t.Error("backing kept after a failed verify; the output must be released")
	}
}

// vanishingFile is a spill file that deletes itself when closed, so the sealed
// backing points at a path that no longer exists.
type vanishingFile struct {
	*os.File
	closeErr error
	vanish   bool
}

func (v vanishingFile) Close() error {
	err := v.File.Close()
	if v.vanish {
		os.Remove(v.File.Name())
	}
	if v.closeErr != nil {
		return v.closeErr
	}
	return err
}

func TestSerializeAndVerifySpillErrors(t *testing.T) {
	for name, vf := range map[string]vanishingFile{
		"finish fails": {closeErr: errors.New("close boom")},
		"reopen fails": {vanish: true},
	} {
		t.Run(name, func(t *testing.T) {
			dir := isolateTempDir(t)
			setSpillThreshold(t, 1)
			old := createTempFile
			t.Cleanup(func() { createTempFile = old })
			createTempFile = func(d, pattern string) (tempFile, error) {
				f, err := os.CreateTemp(d, pattern)
				if err != nil {
					return nil, err
				}
				v := vf
				v.File = f
				return v, nil
			}
			trailer := onePageTrailer()
			doc := openTrailer(t, trailer)
			var cr ConvertResult
			if err := serializeAndVerify(doc, trailer, &cr, pdf.PDFA1B, verify.Parts{}, false, 0); err == nil {
				cr.Close()
				t.Fatal("serializeAndVerify succeeded despite the broken spill file")
			}
			if cr.backing != nil {
				t.Error("backing kept after failure")
			}
			assertEmptyDir(t, dir)
		})
	}
}

// TestSortedChecksOrderIsTotal guards the dropped name tie-break: over the whole
// catalogue, sortedChecks must order strictly by (clause, subclause), i.e. no
// two checks share that pair.
func TestSortedChecksOrderIsTotal(t *testing.T) {
	counts := map[pdf.Check]int{}
	for _, c := range pdf.AllChecks() {
		counts[c] = 1
	}
	got := sortedChecks(counts)
	for i := 1; i < len(got); i++ {
		p, c := got[i-1], got[i]
		if p.Clause() > c.Clause() || (p.Clause() == c.Clause() && p.Subclause() >= c.Subclause()) {
			t.Fatalf("%s/%d (%s) not strictly before %s/%d (%s)", p.Clause(), p.Subclause(), p.Name(), c.Clause(), c.Subclause(), c.Name())
		}
	}
}

func TestSaveTempFileFailures(t *testing.T) {
	data := pdfgen.PlainThreeIssue()
	cr := ConvertResult{backing: &outputBacking{mem: data}}

	t.Run("no temp file", func(t *testing.T) {
		if err := cr.Save(filepath.Join(t.TempDir(), "missing-dir", "out.pdf")); err == nil {
			t.Error("Save into a missing directory succeeded")
		}
	})

	for name, ff := range map[string]faultySave{
		"chmod": {chmodErr: errors.New("chmod boom")},
		"sync":  {syncErr: errors.New("sync boom")},
		"close": {closeErr: errors.New("close boom")},
	} {
		t.Run(name, func(t *testing.T) {
			old := createSaveFile
			t.Cleanup(func() { createSaveFile = old })
			createSaveFile = func(dir, pattern string) (saveFile, error) {
				f, err := os.CreateTemp(dir, pattern)
				if err != nil {
					return nil, err
				}
				v := ff
				v.File = f
				return v, nil
			}
			dir := t.TempDir()
			path := filepath.Join(dir, "out.pdf")
			if err := cr.Save(path); err == nil {
				t.Fatal("Save succeeded despite the failing temp file")
			}
			entries, _ := os.ReadDir(dir)
			if len(entries) != 0 {
				t.Errorf("failed Save left %d file(s) behind", len(entries))
			}
		})
	}
}

// faultySave is a Save temp file with one step that fails.
type faultySave struct {
	*os.File
	chmodErr, syncErr, closeErr error
}

func (f faultySave) Chmod(m os.FileMode) error {
	if f.chmodErr != nil {
		return f.chmodErr
	}
	return f.File.Chmod(m)
}

func (f faultySave) Sync() error {
	if f.syncErr != nil {
		return f.syncErr
	}
	return f.File.Sync()
}

func (f faultySave) Close() error {
	err := f.File.Close()
	if f.closeErr != nil {
		return f.closeErr
	}
	return err
}

// TestRunCancelledBeforeFirstIteration: a context already cancelled when the
// run starts stops it at the first loop check, before any verify. Without this
// the check is only reached by chance, when a ConvertEach abort lands mid-run.
func TestRunCancelledBeforeFirstIteration(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cr, err := ConvertBytesContext(ctx, lzwPDF(), pdf.PDFA1B, Options{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if cr.Iterations != 0 {
		t.Errorf("Iterations = %d, want 0 (nothing ran)", cr.Iterations)
	}
}
