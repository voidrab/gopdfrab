package convert

import (
	"context"
	"image"
	"runtime"
	"sort"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/raster"
	"github.com/voidrab/gopdfrab/internal/verify"
	"github.com/voidrab/gopdfrab/internal/writer"
)

// maxPagePasses bounds the page-by-page raster passes of the backstop, each of
// which verifies the whole graph. A document that needs more flattens every page.
const maxPagePasses = 8

// backstop is the run's last-resort remediation: it rasterizes the residual
// pages so that a resolvable graph always converts. Only fixer-addressable
// issues trigger it; structural violations (no registered fixer) are fixed by
// construction by the writer and need no rasterization. It keeps the run's
// result and clean-graph state current, as the fix loop does.
//
// If the fix loop stopped at its iteration limit, its last fix pass was never
// verified and the result is stale. backstop therefore verifies the graph
// first, so that rasterization does not flatten pages that are already
// conformant.
func (r *run) backstop() error {
	if !r.graphClean {
		r.verifyGraph()
	}
	if !r.residual(false) {
		return nil
	}
	if err := r.ctx.Err(); err != nil {
		return err
	}
	// The verifier reports a shared object once, on the first page it finds it
	// on, so flattening that page can expose the same object on a later one.
	// Those pages are flattened in turn, which leaves the pages that never used
	// it as they are. A font no fixer can embed takes all its pages in one pass.
	flattened := map[int]bool{}
	for range maxPagePasses {
		drops, rasterized, changed := applyRasterFallback(r.ctx, r.doc, &r.trailer, r.withFixers(), flattened, r.o.dpi())
		if !changed {
			break
		}
		// A cancelled pass rasterized only some pages; the run is abandoned.
		if err := r.ctx.Err(); err != nil {
			return err
		}
		r.recordRaster(drops, rasterized)
		if !r.residual(false) {
			break
		}
	}
	if r.residual(true) {
		if drops, rasterized, changed := flattenAllPages(r.ctx, &r.trailer, flattened, r.o.dpi()); changed {
			if err := r.ctx.Err(); err != nil {
				return err
			}
			r.recordRaster(drops, rasterized)
		}
	}
	return nil
}

// residual reports whether the last verify left an issue that a raster pass can
// repair. docWide selects the document-level rule of hasFixableIssue.
func (r *run) residual(docWide bool) bool {
	return !r.cr.Result.Valid && hasFixableIssue(r.cr.Result.Issues, r.fixers, docWide)
}

// withFixers returns the last verify's issues that have a fixer. The writer
// repairs the others by construction, so they are no reason to flatten the page
// they name.
func (r *run) withFixers() []pdf.PDFError {
	var issues []pdf.PDFError
	for _, iss := range r.cr.Result.Issues {
		if _, ok := r.fixers[iss.Check()]; ok {
			issues = append(issues, iss)
		}
	}
	return issues
}

// recordRaster records a completed raster pass and verifies the graph it
// changed.
func (r *run) recordRaster(drops []RasterDrop, rasterized []int) {
	r.cr.RasterDrops = append(r.cr.RasterDrops, drops...)
	r.cr.addRasterizedPages(rasterized)
	r.cr.Iterations++
	r.graphClean = false
	r.verifyGraph()
}

// applyRasterFallback rebuilds every page carrying a residual issue as a flat
// raster image (flattenPageToImage), the last-resort remediation for content
// no targeted fixer could repair. An issue that refers to a font reported as
// unusable flattens the pages showing that font instead of the page the issue
// was reported on. A page in flattened was rebuilt by an earlier pass and is
// left alone, and the pages of this pass are added to it. Page numbers in
// issues align with the graph's page order, since both come from the same
// Root/Pages/Kids walk.
func applyRasterFallback(ctx context.Context, doc *pdf.Reader, trailer *pdf.PDFDict, issues []pdf.PDFError, flattened map[int]bool, dpi int) ([]RasterDrop, []int, bool) {
	pages := orderedPages(*trailer)
	var nums []int
	mark := func(pageNum int) {
		if pageNum > 0 && !flattened[pageNum] {
			nums = append(nums, pageNum)
			flattened[pageNum] = true
		}
	}
	showing := pagesShowingUnusableFonts(doc, *trailer, pages, issues)
	for _, iss := range issues {
		if ref, ok := iss.ObjectRef(); ok && len(showing[ref.ObjNum]) > 0 {
			for _, pageNum := range showing[ref.ObjNum] {
				mark(pageNum)
			}
			continue
		}
		mark(iss.Page())
	}
	var flagged []pageTarget
	var pageNums []int
	sort.Ints(nums)
	for _, pageNum := range nums {
		if i := pageNum - 1; i >= 0 && i < len(pages) {
			flagged = append(flagged, pages[i])
			pageNums = append(pageNums, pageNum)
		}
	}
	return flattenPagesParallel(ctx, flagged, pageNums, dpi)
}

// pagesShowingUnusableFonts maps the object number of each font that issues
// report as unusable (not embedded, or with a damaged program) to the numbers
// of the pages that show it with visible text.
func pagesShowingUnusableFonts(doc *pdf.Reader, trailer pdf.PDFDict, pages []pageTarget, issues []pdf.PDFError) map[int][]int {
	font := pdf.Checks.Font
	wanted := map[int]bool{}
	for _, iss := range issues {
		if c := iss.Check(); c == font.SimpleNotEmbedded || c == font.CIDNotEmbedded || c == font.InvalidProgram {
			if ref, ok := iss.ObjectRef(); ok {
				wanted[ref.ObjNum] = true
			}
		}
	}
	if len(wanted) == 0 {
		return nil
	}
	fonts := map[uintptr]int{}
	walkDicts(trailer, map[uintptr]bool{}, func(d pdf.PDFDict) {
		if ref, ok := d.Ref(); ok && wanted[ref.ObjNum] {
			fonts[pdf.ValuePointer(d.Entries)] = ref.ObjNum
		}
	})
	showing := map[int][]int{}
	for i, page := range pages {
		visible := verify.VisibleFonts(page.dict, verify.NewContext(doc))
		for ptr, num := range fonts {
			if visible[ptr] {
				showing[num] = append(showing[num], i+1)
			}
		}
	}
	return showing
}

// flattenAllPages rasterizes each page not in flattened. It is the final
// backstop for residuals that applyRasterFallback can't target --
// document-level violations with no page number, or anything its page-by-page
// pass left behind.
func flattenAllPages(ctx context.Context, trailer *pdf.PDFDict, flattened map[int]bool, dpi int) ([]RasterDrop, []int, bool) {
	var pages []pageTarget
	var pageNums []int
	for i, page := range orderedPages(*trailer) {
		if !flattened[i+1] {
			pages = append(pages, page)
			pageNums = append(pageNums, i+1)
		}
	}
	return flattenPagesParallel(ctx, pages, pageNums, dpi)
}

// flattenPagesParallel rasterizes distinct pages on a bounded worker pool;
// each render mutates only its own page dict while reading the shared graph,
// the same access pattern transparencyFlattener's workers rely on. pageNums
// aligns with pages (the 1-based page number of each) for the drop report.
func flattenPagesParallel(ctx context.Context, pages []pageTarget, pageNums []int, dpi int) ([]RasterDrop, []int, bool) {
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
	pdf.ParallelFor(ctx, len(unique), runtime.NumCPU(), func(i int) {
		p := unique[i]
		dropLists[i], changedFlags[i] = flattenPageToImage(p.dict, p.resources, p.mediaBox, dpi)
	})

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

// flattenPageToImage rasterizes page (RenderPage) and rebuilds it in place
// as a single flat Image XObject painted by a fresh, minimal content
// stream, replacing /Resources and /Contents and dropping /Group and
// /Rotate (a flattened raster has no remaining rotation to apply). The raster
// fallback uses it, and so does the transparency flattener when /Group sits
// directly on the Page dict with no narrower Form XObject to target. A render
// failure (e.g. an unresolvable graph or an unsupported image codec) leaves
// page untouched, reporting no change rather than erroring the whole Convert.
func flattenPageToImage(page pdf.PDFDict, resources pdf.PDFDict, mediaBox [4]float64, dpi int) ([]string, bool) {
	// The optional content fixer (6.1.13) runs before the transparency
	// flattener (6.4) and the raster fallback, and removes the hidden layers,
	// so no optional content is passed.
	canvas, drops, err := raster.RenderPage(page, resources, mediaBox, dpi, nil)
	if err != nil {
		return nil, false
	}

	img := pdf.NewPDFDict()
	img.Entries.Set("Type", pdf.PDFName{Value: "XObject"})
	img.Entries.Set("Subtype", pdf.PDFName{Value: "Image"})
	img.Entries.Set("Width", pdf.PDFInteger(canvas.Bounds().Dx()))
	img.Entries.Set("Height", pdf.PDFInteger(canvas.Bounds().Dy()))
	img.Entries.Set("BitsPerComponent", pdf.PDFInteger(8))
	img.Entries.Set("ColorSpace", pdf.PDFName{Value: "DeviceRGB"})
	setStreamRGBFlate(&img, canvas)

	xobjects := pdf.NewPDFDict()
	xobjects.Entries.Set("Im0", img)
	csDict := pdf.NewPDFDict()
	csDict.Entries.Set("DefaultRGB", iccBasedColourSpace(3, srgbICCProfile))
	pageResources := pdf.NewPDFDict()
	pageResources.Entries.Set("XObject", xobjects)
	pageResources.Entries.Set("ColorSpace", csDict)

	w, h := mediaBox[2]-mediaBox[0], mediaBox[3]-mediaBox[1]
	ops := []writer.ContentOp{
		{Op: "q"},
		{Op: "cm", Operands: []pdf.PDFValue{
			pdf.PDFReal(w), pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFReal(h),
			pdf.PDFReal(mediaBox[0]), pdf.PDFReal(mediaBox[1]),
		}},
		{Op: "Do", Operands: []pdf.PDFValue{pdf.PDFName{Value: "Im0"}}},
		{Op: "Q"},
	}
	// Cannot fail: ops holds only operators with integer, real and name
	// operands, which the content writer always serialises.
	data, _ := writer.WriteContentStream(ops)
	contents := pdf.NewPDFDict()
	writer.SetStreamFlate(&contents, data)

	page.Entries.Del("Group")
	page.Entries.Del("Rotate")
	page.Entries.Set("Resources", pageResources)
	page.Entries.Set("Contents", contents)
	return drops, true
}

// setStreamRGBFlate stores canvas as a FlateDecode DeviceRGB stream, packing
// each row's RGB triples on the fly so no whole-raster RGB buffer is allocated
// (one reused row buffer per call instead). The same tight 8-bit row-major
// packing packRGBSamples produces.
func setStreamRGBFlate(d *pdf.PDFDict, canvas *image.RGBA) {
	bounds := canvas.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	rowRGB := make([]byte, w*3)
	writer.SetStreamFlateRows(d, h, func(i int) []byte {
		off := canvas.PixOffset(bounds.Min.X, bounds.Min.Y+i)
		src := canvas.Pix[off : off+w*4 : off+w*4]
		for x, j := 0, 0; x < len(src); x, j = x+4, j+3 {
			rowRGB[j], rowRGB[j+1], rowRGB[j+2] = src[x], src[x+1], src[x+2]
		}
		return rowRGB
	})
}

// packRGBSamples packs canvas's pixels as tightly-packed 8-bit RGB triples
// (row-major, no padding needed since DeviceRGB/8bpc rows are always a
// whole number of bytes), the sample format Image XObject expects.
func packRGBSamples(canvas *image.RGBA) []byte {
	bounds := canvas.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	out := make([]byte, w*h*3)
	o := 0
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		row := canvas.Pix[canvas.PixOffset(bounds.Min.X, y):]
		for i := 0; i < w*4; i += 4 {
			out[o], out[o+1], out[o+2] = row[i], row[i+1], row[i+2]
			o += 3
		}
	}
	return out
}
