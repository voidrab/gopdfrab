package convert

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfgen"
)

// onePageDoc builds a one-page document whose single content stream is the
// given operator string, so tests can control exactly what renders.
func onePageDoc(content string) []byte {
	b := pdfgen.NewBuilder("%PDF-1.4\n")
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.Obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Resources << >> /Contents 4 0 R >>")
	b.StreamObj(4, "<<", []byte(content))
	return b.FinishClassic("<< /Size 5 /Root 1 0 R >>")
}

func openReader(t *testing.T, data []byte) *pdf.Reader {
	t.Helper()
	r, err := pdf.OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}

// filledPage draws a large black rectangle, so the page carries real ink.
const filledPage = "0 0 0 rg\n20 20 160 160 re\nf\n"

// TestFidelityIdentical: a document compared against itself scores near-perfect
// similarity and is never flagged as blanked.
func TestFidelityIdentical(t *testing.T) {
	data := onePageDoc(filledPage)
	report, err := CompareFidelity(openReader(t, data), openReader(t, data), fidelityDPI)
	if err != nil {
		t.Fatalf("CompareFidelity: %v", err)
	}
	if len(report) != 1 {
		t.Fatalf("got %d page reports, want 1", len(report))
	}
	pf := report[0]
	if pf.Similarity < 0.99 {
		t.Errorf("identical similarity = %.3f, want >= 0.99", pf.Similarity)
	}
	if pf.Blanked() {
		t.Errorf("identical page reported blanked: %+v", pf)
	}
	if pf.InputInk < inkThreshold {
		t.Errorf("filled page InputInk = %.4f, want >= %.4f", pf.InputInk, inkThreshold)
	}
}

// TestFidelityBlankedDetected: an inked input against a blank output is flagged
// as blanked with low similarity -- the destructive failure the gate exists to
// catch.
func TestFidelityBlankedDetected(t *testing.T) {
	input := onePageDoc(filledPage)
	output := onePageDoc("q\nQ\n") // renders nothing

	report, err := CompareFidelity(openReader(t, input), openReader(t, output), fidelityDPI)
	if err != nil {
		t.Fatalf("CompareFidelity: %v", err)
	}
	if len(report) != 1 {
		t.Fatalf("got %d page reports, want 1", len(report))
	}
	pf := report[0]
	if !pf.Blanked() {
		t.Errorf("blanked page not detected: %+v", pf)
	}
	if pf.Similarity > 0.5 {
		t.Errorf("blanked similarity = %.3f, want low", pf.Similarity)
	}
}

// TestFidelityChangedNotBlanked: a page whose content moves/changes but still
// carries comparable ink is NOT flagged as blanked, even though its pixel
// similarity drops -- so legitimate changes (e.g. font substitution) don't trip
// the destructive-loss gate.
func TestFidelityChangedNotBlanked(t *testing.T) {
	input := onePageDoc("0 0 0 rg\n20 20 70 70 re\nf\n")    // rectangle bottom-left
	output := onePageDoc("0 0 0 rg\n110 110 70 70 re\nf\n") // same-size rectangle top-right

	report, err := CompareFidelity(openReader(t, input), openReader(t, output), fidelityDPI)
	if err != nil {
		t.Fatalf("CompareFidelity: %v", err)
	}
	pf := report[0]
	if pf.Blanked() {
		t.Errorf("moved-but-present content wrongly flagged blanked: %+v", pf)
	}
	if pf.OutputInk < inkThreshold {
		t.Errorf("output should still carry ink: %+v", pf)
	}
}

// TestInkFractionExtremes pins the ink metric at its bounds.
func TestInkFractionExtremes(t *testing.T) {
	white, err := CompareFidelity(openReader(t, onePageDoc("q\nQ\n")), openReader(t, onePageDoc("q\nQ\n")), fidelityDPI)
	if err != nil {
		t.Fatalf("CompareFidelity: %v", err)
	}
	if len(white) != 1 || white[0].InputInk >= inkThreshold {
		t.Errorf("blank page InputInk should be ~0, got %+v", white)
	}
	// Two blank pages are identical, so not blanked (no ink was lost).
	if white[0].Blanked() {
		t.Errorf("blank-to-blank should not be blanked: %+v", white[0])
	}
}

// TestComparePageRendersMissingRenders: the comparison has to survive pages
// the rasterizer could not draw. A page missing on the input side has no
// baseline and is skipped entirely; one missing only on the output side is
// total loss, not a skipped page, or a conversion that stopped rendering would
// report clean.
func TestComparePageRendersMissingRenders(t *testing.T) {
	inked := image.NewRGBA(image.Rect(0, 0, 10, 10))
	for i := range inked.Pix {
		inked.Pix[i] = 0
	}

	report := comparePageRenders(
		[]*image.RGBA{nil, inked, inked},
		[]*image.RGBA{inked, nil, inked},
	)
	if len(report) != 2 {
		t.Fatalf("got %d page reports, want 2 (page 1 has no baseline)", len(report))
	}
	if report[0].Page != 2 || report[0].OutputInk != 0 || report[0].Similarity != 0 {
		t.Errorf("page 2 = %+v, want a fully lost page", report[0])
	}
	if !report[0].Blanked() {
		t.Errorf("an unrenderable output page was not reported blanked: %+v", report[0])
	}
	if report[1].Page != 3 || report[1].Similarity < 0.99 {
		t.Errorf("page 3 = %+v, want an intact comparison", report[1])
	}

	// The report covers only pages present on both sides.
	if got := comparePageRenders([]*image.RGBA{inked, inked}, []*image.RGBA{inked}); len(got) != 1 {
		t.Errorf("got %d page reports for a shortened output, want 1", len(got))
	}
}

// TestFidelityMetricsOnEmptyRenders: the ink and grid metrics are fed straight
// from the rasterizer, so they must absorb a nil or zero-sized image rather
// than dividing by zero or indexing an empty pixel buffer.
func TestFidelityMetricsOnEmptyRenders(t *testing.T) {
	if got := inkFraction(nil); got != 0 {
		t.Errorf("inkFraction(nil) = %v, want 0", got)
	}
	empty := image.NewRGBA(image.Rect(0, 0, 0, 0))
	if got := inkFraction(empty); got != 0 {
		t.Errorf("inkFraction(empty) = %v, want 0", got)
	}
	if got := grayGrid(empty); got != [fidelityGrid * fidelityGrid]float64{} {
		t.Error("grayGrid(empty) returned non-zero samples")
	}
	if got := pageSimilarity(empty, empty); got != 1 {
		t.Errorf("pageSimilarity of two empty renders = %v, want 1", got)
	}
}

// TestCompareFidelityRenderErrors: a document whose graph cannot be resolved
// cannot be rendered, and the failure must name which side failed instead of
// being reported as a zero-similarity page.
func TestCompareFidelityRenderErrors(t *testing.T) {
	// A reference chain past the resolve depth cap, as in
	// TestConvertUnresolvableGraphReturnsError.
	const chain = 70000
	b := pdfgen.NewBuilder("%PDF-1.4\n")
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R /Deep 4 0 R >>")
	b.Obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] >>")
	for i := 4; i < 4+chain; i++ {
		b.Obj(i, fmt.Sprintf("[%d 0 R]", i+1))
	}
	b.Obj(4+chain, "42")
	unresolvable := b.FinishClassic("<< /Size 5 /Root 1 0 R >>")
	good := onePageDoc(filledPage)

	for _, tc := range []struct {
		side          string
		input, output []byte
	}{
		{"input", unresolvable, good},
		{"output", good, unresolvable},
	} {
		t.Run(tc.side, func(t *testing.T) {
			report, err := CompareFidelity(
				openReader(t, tc.input), openReader(t, tc.output), fidelityDPI)
			if err == nil {
				t.Fatalf("CompareFidelity succeeded on an unresolvable %s: %+v", tc.side, report)
			}
			if !strings.Contains(err.Error(), "render "+tc.side) {
				t.Errorf("err = %v, want it to name the %s side", err, tc.side)
			}
		})
	}
}

// TestConvertFidelityNoBlankedPages is the fidelity gate over both corpora:
// every "fail" fixture that converts to output must not have any page blanked
// (rendered content on input, near-empty on output). Convert is free to fix
// structure, embed fonts, and even rasterize a page -- but never to destroy
// visible content and still call the result conformant.
func TestConvertFidelityNoBlankedPages(t *testing.T) {
	if testing.Short() {
		t.Skip("corpus fidelity gate skipped in short mode")
	}
	fixtures := failFixturesByExpectedClause(t)
	if len(fixtures) == 0 {
		t.Skip("no corpora present")
	}
	// Rendering both sides of every fixture is expensive, dominated by a
	// heavy tail of large fixtures (the corpus averages ~3.6 KB but a few reach
	// megabytes). By default the gate skips inputs over maxFidelityInput, which
	// still covers ~95% of the corpus; a blanking regression is systematic (a
	// fixer or the writer dropping content) and shows up across the small
	// fixtures too. GOPDFRAB_FIDELITY_FULL=1 renders everything.
	const maxFidelityInput = 50 << 10
	full := os.Getenv("GOPDFRAB_FIDELITY_FULL") != ""
	paths := make([]string, 0, len(fixtures))
	for path := range fixtures {
		if !full {
			if info, err := os.Stat(path); err != nil || info.Size() > maxFidelityInput {
				continue
			}
		}
		paths = append(paths, path)
	}
	sort.Strings(paths)

	var checked, blanked int64
	jobs := make(chan string)
	var wg sync.WaitGroup
	for range runtime.NumCPU() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range jobs {
				if checkFidelity(t, path, &checked) {
					atomic.AddInt64(&blanked, 1)
				}
			}
		}()
	}
	for _, path := range paths {
		jobs <- path
	}
	close(jobs)
	wg.Wait()

	t.Logf("fidelity gate: %d documents rendered, %d page(s) blanked", checked, blanked)
}

// checkFidelity converts one fixture, compares input against output, and
// reports any blanked page via t.Errorf. It returns true if a page was blanked.
func checkFidelity(t *testing.T, path string, checked *int64) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	cr, err := ConvertBytes(data, pdf.PDFA1B, Options{})
	if err != nil {
		return false
	}
	defer cr.Close()
	outBytes, err := cr.Output()
	if err != nil || len(outBytes) == 0 {
		return false
	}
	input, err := pdf.OpenBytes(data)
	if err != nil {
		return false
	}
	defer input.Close()
	output, err := pdf.OpenBytes(outBytes)
	if err != nil {
		return false
	}
	defer output.Close()
	report, err := CompareFidelity(input, output, fidelityDPI)
	if err != nil {
		return false
	}
	atomic.AddInt64(checked, 1)
	found := false
	for _, pf := range report {
		if pf.Blanked() {
			found = true
			t.Errorf("%s page %d blanked by convert: inputInk=%.4f outputInk=%.4f sim=%.3f",
				filepath.Base(path), pf.Page, pf.InputInk, pf.OutputInk, pf.Similarity)
		}
	}
	return found
}

// clippedAtScale is the shape roadmap item 34 was written about, taken from a
// real presentation: the page is drawn at 1/500 scale, so a full-page clip
// needs coordinates in the hundreds of thousands. Clamping those to 32767
// shrinks the clip to a corner and takes the whole page with it.
const clippedAtScale = "q 0.002 0 0 0.002 0 0 cm\n" +
	"0 0 m 100000.0 0 l 100000.0 100000.0 l 0 100000.0 l h W n\n" +
	"0 0 0 rg\n10000 10000 80000 80000 re\nf\nQ\n"

// TestConvertKeepsGeometryDrawnAtScale is item 34's regression: the conversion
// must reach conformance -- no coordinate left over the limit -- without
// emptying the page it was repairing.
func TestConvertKeepsGeometryDrawnAtScale(t *testing.T) {
	cr, err := ConvertBytes(onePageDoc(clippedAtScale), pdf.PDFA1B, Options{CheckFidelity: true})
	if err != nil {
		t.Fatalf("ConvertBytes: %v", err)
	}
	defer cr.Close()
	if !cr.Result.Valid {
		t.Fatalf("output not conformant: %v", cr.Result.Issues)
	}
	if len(cr.BlankedPages) != 0 {
		t.Errorf("clamping blanked %v; the geometry should have been rescaled", cr.BlankedPages)
	}
	if len(cr.Fidelity) != 1 {
		t.Fatalf("Fidelity = %+v, want one page report", cr.Fidelity)
	}
	if pf := cr.Fidelity[0]; pf.Similarity < 0.99 {
		t.Errorf("rescaled page should render the same: %+v", pf)
	}
}

// TestConvertReportsFidelity: with CheckFidelity set, a normal conversion
// populates ConvertResult.Fidelity with a per-page report and reports no
// blanked page; without it, Fidelity stays nil.
func TestConvertReportsFidelity(t *testing.T) {
	data := onePageDoc(filledPage)

	off, err := ConvertBytes(data, pdf.PDFA1B, Options{})
	if err != nil {
		t.Fatalf("ConvertBytes: %v", err)
	}
	if off.Fidelity != nil {
		t.Errorf("Fidelity populated without CheckFidelity: %+v", off.Fidelity)
	}

	on, err := ConvertBytes(data, pdf.PDFA1B, Options{CheckFidelity: true})
	if err != nil {
		t.Fatalf("ConvertBytes(CheckFidelity): %v", err)
	}
	if len(on.Fidelity) != 1 {
		t.Fatalf("Fidelity = %+v, want one page report", on.Fidelity)
	}
	pf := on.Fidelity[0]
	if pf.Blanked() {
		t.Errorf("faithful conversion reported a blanked page: %+v", pf)
	}
	if pf.InputInk < inkThreshold || pf.OutputInk < inkThreshold {
		t.Errorf("both sides should carry ink: %+v", pf)
	}
	if len(on.BlankedPages) != 0 {
		t.Errorf("BlankedPages = %v on a faithful conversion, want none", on.BlankedPages)
	}
	if off.BlankedPages != nil {
		t.Errorf("BlankedPages = %v without CheckFidelity, want nil", off.BlankedPages)
	}
}

// TestBlankedPages: the page list is the Blanked() reports in page order, and
// stays nil when nothing was lost so len is enough to test it.
func TestBlankedPages(t *testing.T) {
	lost := PageFidelity{Page: 2, InputInk: 0.5, OutputInk: 0}
	kept := PageFidelity{Page: 1, InputInk: 0.5, OutputInk: 0.5, Similarity: 1}
	if got := blankedPages([]PageFidelity{kept, lost, {Page: 3, InputInk: 0.5, OutputInk: 0}}); !slices.Equal(got, []int{2, 3}) {
		t.Errorf("blankedPages = %v, want [2 3]", got)
	}
	if got := blankedPages([]PageFidelity{kept}); got != nil {
		t.Errorf("blankedPages with nothing lost = %v, want nil", got)
	}
	if got := blankedPages(nil); got != nil {
		t.Errorf("blankedPages(nil) = %v, want nil", got)
	}
}
