package convert

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/voidrab/gopdfrab/internal/fidelity"
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
	before, err := resolveTrailer(input)
	if err != nil {
		return false
	}
	report := fidelity.Compare(fidelity.Summarize(before, fidelity.DPI), summarizeTrailerPagesOf(output))
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

// clippedAtScale is a page drawn at 1/500 scale whose full-page clip needs
// coordinates in the hundreds of thousands. Clamping those to 32767 shrinks the
// clip to a corner and takes the whole page with it.
const clippedAtScale = "q 0.002 0 0 0.002 0 0 cm\n" +
	"0 0 m 100000.0 0 l 100000.0 100000.0 l 0 100000.0 l h W n\n" +
	"0 0 0 rg\n10000 10000 80000 80000 re\nf\nQ\n"

// TestConvertKeepsGeometryDrawnAtScale verifies that the conversion reaches
// conformance, with no coordinate left over the limit, without emptying the
// page it repairs.
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

// TestConvertReportsFidelity verifies that with CheckFidelity set, a normal
// conversion populates ConvertResult.Fidelity with a per-page report and
// reports no blanked page; without it, Fidelity stays nil.
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






// drawnAtZeroOpacity is a page of the kind the zero-opacity repair exists for:
// a black rectangle the size of the page, drawn at zero opacity over the
// content. It is invisible in the file as written, and making it opaque puts
// it over everything underneath.
const drawnAtZeroOpacity = "0 0 0 rg\n20 20 60 60 re\nf\n" +
	"/GS0 gs\n0 0 0 rg\n0 0 200 200 re\nf\n"

// onePageDocWithZeroAlpha is onePageDoc with a /GS0 graphics state that puts
// the fill opacity at zero.
func onePageDocWithZeroAlpha(content string) []byte {
	b := pdfgen.NewBuilder("%PDF-1.4\n")
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.Obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] "+
		"/Resources << /ExtGState << /GS0 << /Type /ExtGState /ca 0 >> >> >> /Contents 4 0 R >>")
	b.StreamObj(4, "<<", []byte(content))
	return b.FinishClassic("<< /Size 5 /Root 1 0 R >>")
}

// groupFormDoc builds a page that is one transparency group form. The form
// draws a black image, which is named in the form's own resources rather than
// the page's.
func groupFormDoc() []byte {
	b := pdfgen.NewBuilder("%PDF-1.4\n")
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.Obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] "+
		"/Resources << /XObject << /Fm0 5 0 R >> >> /Contents 4 0 R >>")
	b.StreamObj(4, "<<", []byte("/Fm0 Do\n"))
	// The image draw prevents the group from being removed without
	// rasterization.
	b.StreamObj(5, "<< /Type /XObject /Subtype /Form /BBox [0 0 200 200] "+
		"/Group << /S /Transparency >> /Resources << /XObject << /Im0 6 0 R >> "+
		"/ExtGState << /GS0 << /Type /ExtGState /ca 1 >> >> >>",
		[]byte("q /GS0 gs 160 0 0 160 20 20 cm /Im0 Do Q\n"))
	b.StreamObj(6, "<< /Type /XObject /Subtype /Image /Width 2 /Height 2 "+
		"/BitsPerComponent 8 /ColorSpace /DeviceGray", []byte{0, 0, 0, 0})
	return b.FinishClassic("<< /Size 7 /Root 1 0 R >>")
}

// TestConvertKeepsWhatAGroupFormDraws verifies that the content of a
// transparency group form survives conversion. PDF/A-1 forbids the transparency
// group, so the form is rasterized, and rasterizing it against the page's
// resources instead of its own leaves the page blank while the output is still
// conformant.
func TestConvertKeepsWhatAGroupFormDraws(t *testing.T) {
	cr, err := ConvertBytes(groupFormDoc(), pdf.PDFA1B, Options{CheckFidelity: true})
	if err != nil {
		t.Fatalf("ConvertBytes: %v", err)
	}
	defer cr.Close()
	if !cr.Result.Valid {
		t.Fatalf("output not conformant: %v", cr.Result.Issues)
	}
	if len(cr.BlankedPages) != 0 {
		t.Errorf("conversion blanked %v; the image the form draws should have been kept", cr.BlankedPages)
	}
	if len(cr.Fidelity) != 1 {
		t.Fatalf("Fidelity = %+v, want one page report", cr.Fidelity)
	}
	if pf := cr.Fidelity[0]; pf.Similarity < 0.99 {
		t.Errorf("page should render as it did before conversion: %+v", pf)
	}
}

// TestConvertDoesNotPaintOverInvisibleContent verifies that the conversion
// reaches conformance, with no opacity left below 1, without drawing what the
// file says cannot be seen.
func TestConvertDoesNotPaintOverInvisibleContent(t *testing.T) {
	cr, err := ConvertBytes(onePageDocWithZeroAlpha(drawnAtZeroOpacity), pdf.PDFA1B, Options{CheckFidelity: true})
	if err != nil {
		t.Fatalf("ConvertBytes: %v", err)
	}
	defer cr.Close()
	if !cr.Result.Valid {
		t.Fatalf("output not conformant: %v", cr.Result.Issues)
	}
	if len(cr.OverpaintedPages) != 0 {
		t.Errorf("conversion drew over %v; the invisible rectangle should have been taken out", cr.OverpaintedPages)
	}
	if len(cr.BlankedPages) != 0 {
		t.Errorf("conversion blanked %v; the visible square should have been kept", cr.BlankedPages)
	}
	if len(cr.Fidelity) != 1 {
		t.Fatalf("Fidelity = %+v, want one page report", cr.Fidelity)
	}
	if pf := cr.Fidelity[0]; pf.Similarity < 0.99 {
		t.Errorf("page should render as it did before conversion: %+v", pf)
	}
	if len(cr.RasterizedPages) != 0 {
		t.Errorf("page %v was rasterized; the repair should keep it text and vectors", cr.RasterizedPages)
	}
}

// coveringGroupFormDoc returns a one-page document that draws its own content
// and then a page-sized transparency group painting one small mark.
func coveringGroupFormDoc() []byte {
	b := pdfgen.NewBuilder("%PDF-1.4\n")
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.Obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] "+
		"/Resources << /XObject << /Fm0 5 0 R >> >> /Contents 4 0 R >>")
	b.StreamObj(4, "<<", []byte(filledPage+"/Fm0 Do\n"))
	// The translucent graphics state prevents the group from being removed
	// without rasterization.
	b.StreamObj(5, "<< /Type /XObject /Subtype /Form /BBox [0 0 200 200] "+
		"/Group << /S /Transparency >> "+
		"/Resources << /ExtGState << /GS0 << /Type /ExtGState /ca 0.99 >> >> >>",
		[]byte("q /GS0 gs 1 0 0 rg 0 0 10 10 re f Q\n"))
	return b.FinishClassic("<< /Size 6 /Root 1 0 R >>")
}

// TestConvertKeepsPageUnderAFlattenedGroup covers a group-flattening blanking
// cause: a flattened group becomes one flat image, and a flat image
// covers everything the page drew before it. What the group did not paint has
// to keep showing what is underneath.
func TestConvertKeepsPageUnderAFlattenedGroup(t *testing.T) {
	cr, err := ConvertBytes(coveringGroupFormDoc(), pdf.PDFA1B, Options{CheckFidelity: true})
	if err != nil {
		t.Fatalf("ConvertBytes: %v", err)
	}
	defer cr.Close()
	if !cr.Result.Valid {
		t.Fatalf("output not conformant: %v", cr.Result.Issues)
	}
	if len(cr.BlankedPages) != 0 {
		t.Errorf("conversion blanked %v; the group covers the page it is drawn on", cr.BlankedPages)
	}
	if len(cr.Fidelity) != 1 {
		t.Fatalf("Fidelity = %+v, want one page report", cr.Fidelity)
	}
	if pf := cr.Fidelity[0]; pf.OutputInk < pf.InputInk*0.9 {
		t.Errorf("the page lost ink under the flattened group: %+v", pf)
	}
}
