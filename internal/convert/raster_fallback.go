package convert

import (
	"context"
	"image"
	"runtime"
	"sort"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/raster"
	"github.com/voidrab/gopdfrab/internal/writer"
)

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
	if r.cr.Result.Valid || !hasFixableIssue(r.cr.Result.Issues, r.fixers, false) {
		return nil
	}
	if err := r.ctx.Err(); err != nil {
		return err
	}
	if drops, rasterized, changed := applyRasterFallback(r.ctx, &r.trailer, r.cr.Result.Issues, r.o.dpi()); changed {
		// A cancelled pass rasterized only some pages; the run is abandoned.
		if err := r.ctx.Err(); err != nil {
			return err
		}
		r.recordRaster(drops, rasterized)
	}
	if !r.cr.Result.Valid && hasFixableIssue(r.cr.Result.Issues, r.fixers, true) {
		if drops, rasterized, changed := flattenAllPages(r.ctx, &r.trailer, r.o.dpi()); changed {
			if err := r.ctx.Err(); err != nil {
				return err
			}
			r.recordRaster(drops, rasterized)
		}
	}
	return nil
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
// no targeted fixer could repair. Page numbers in issues align with the
// graph's page order, since both come from the same Root/Pages/Kids walk.
func applyRasterFallback(ctx context.Context, trailer *pdf.PDFDict, issues []pdf.PDFError, dpi int) ([]RasterDrop, []int, bool) {
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
	return flattenPagesParallel(ctx, flagged, pageNums, dpi)
}

// flattenAllPages rasterizes every page, the final backstop for residuals that
// applyRasterFallback can't target -- document-level violations with no page
// number, or anything its page-by-page pass left behind.
func flattenAllPages(ctx context.Context, trailer *pdf.PDFDict, dpi int) ([]RasterDrop, []int, bool) {
	pages := orderedPages(*trailer)
	pageNums := make([]int, len(pages))
	for i := range pages {
		pageNums[i] = i + 1
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
