package convert

import (
	"fmt"
	"image"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// Fidelity compares the rendered appearance of a converted document against its
// input, page by page, using gopdfrab's own rasterizer on both sides. Because
// the same renderer draws both, its operator-coverage gaps are symmetric and
// cancel, so the comparison isolates what the *conversion* changed rather than
// what the renderer cannot draw. Its purpose is to catch the destructive
// failure a conformance-only metric misses: a page silently blanked or gutted
// while the result still "verifies clean".

// fidelity tuning. The comparison runs at a low DPI: gross change and blanking
// are what matter, not sub-pixel accuracy.
const (
	fidelityDPI   = 72
	fidelityGrid  = 48    // NxN sample grid for the similarity score
	inkThreshold  = 0.004 // a page with < 0.4% non-white pixels counts as blank
	blankLossRate = 0.10  // losing >90% of the input's ink is "blanked"
	// Painting over a page covers it: whole areas that were paper go solid. So
	// what counts is area gone dark, not ink gained -- a conversion that makes
	// text drawable which the input's own fonts could not draw adds a great
	// deal of ink and covers nothing, and it is the commonest thing a
	// conversion does.
	lightCell     = 200  // a cell of mostly paper, on the 0-255 luminance scale
	darkCell      = 100  // a cell with something solid over it
	overpaintArea = 0.05 // 5% of the page going from one to the other
)

// PageFidelity reports how a converted page's rendering compares to the input's.
// Similarity is 1.0 for identical renders and approaches 0.0 as they diverge.
// InputInk and OutputInk are the fraction of non-white pixels on each side.
type PageFidelity struct {
	Page       int     // 1-based, matching PDFError.Page()
	Similarity float64 // 1.0 identical, 0.0 maximally different
	InputInk   float64 // fraction of non-white pixels in the input render
	OutputInk  float64 // fraction of non-white pixels in the output render
	// Covered is how much of the page was paper on the way in and is solid on
	// the way out -- the shape of something painted over the top of it.
	Covered float64
}

// Blanked reports whether the page lost essentially all of its visible content:
// the input had ink and the output is nearly empty. This is unambiguous data
// loss, and unlike a raw similarity threshold it does not fire on legitimate
// changes such as font substitution.
func (f PageFidelity) Blanked() bool {
	return f.InputInk >= inkThreshold && f.OutputInk < f.InputInk*blankLossRate
}

// Overpainted reports the opposite loss: something was drawn over the page,
// covering what was underneath. Nothing a conversion does should cover the
// page, so this catches a repair that turns something invisible into something
// opaque -- which is what setting a zero opacity to 1 used to do.
func (f PageFidelity) Overpainted() bool {
	return f.Covered >= overpaintArea
}

// blankedPages returns the pages of report that came out blank, in ascending
// order. comparePageRenders already walks the pages in order, so this only
// filters. Nil when nothing was lost, so a caller can test it with len.
func blankedPages(report []PageFidelity) []int {
	return pagesWhere(report, PageFidelity.Blanked)
}

// overpaintedPages is blankedPages for the pages that gained ink.
func overpaintedPages(report []PageFidelity) []int {
	return pagesWhere(report, PageFidelity.Overpainted)
}

func pagesWhere(report []PageFidelity, match func(PageFidelity) bool) []int {
	var pages []int
	for _, pf := range report {
		if match(pf) {
			pages = append(pages, pf.Page)
		}
	}
	return pages
}

// CompareFidelity renders input and output at dpi and returns a per-page
// fidelity report for every page present in both. Pages the rasterizer cannot
// draw on the input side (nil render) are skipped, since there is no baseline
// to judge against.
func CompareFidelity(input, output *pdf.Reader, dpi int) ([]PageFidelity, error) {
	in, err := summarizeReaderPages(input, dpi)
	if err != nil {
		return nil, fmt.Errorf("fidelity: render input: %w", err)
	}
	out, err := summarizeReaderPages(output, dpi)
	if err != nil {
		return nil, fmt.Errorf("fidelity: render output: %w", err)
	}
	return comparePageSummaries(in, out), nil
}

// pageSummary is everything the comparison needs from one rendered page: how
// much ink it carries, how dark each part of it is, and the samples the
// similarity score is taken from. A few tens of kilobytes, where the page it
// was measured from is a few megabytes -- and a document's worth of those is
// what a comparison has to hold.
type pageSummary struct {
	rendered bool
	ink      float64
	cells    [fidelityGrid * fidelityGrid]float64
	gray     [fidelityGrid * fidelityGrid]float64
}

// summarize measures a rendered page and lets go of it.
func summarize(img *image.RGBA) pageSummary {
	if img == nil {
		return pageSummary{}
	}
	return pageSummary{rendered: true, ink: inkFraction(img), cells: cellMeans(img), gray: grayGrid(img)}
}

// comparePageSummaries builds the per-page report from two measured page
// lists. An unrendered input page is skipped (no baseline); an unrendered
// output page with an inked input counts as fully lost.
func comparePageSummaries(in, out []pageSummary) []PageFidelity {
	n := min(len(in), len(out))
	var report []PageFidelity
	for i := range n {
		if !in[i].rendered {
			continue
		}
		pf := PageFidelity{Page: i + 1, InputInk: in[i].ink}
		if !out[i].rendered {
			pf.OutputInk, pf.Similarity = 0, 0
		} else {
			pf.OutputInk = out[i].ink
			pf.Similarity = gridSimilarity(in[i].gray, out[i].gray)
			pf.Covered = coveredFraction(in[i].cells, out[i].cells)
		}
		report = append(report, pf)
	}
	return report
}

// comparePageRenders is comparePageSummaries over pages still in hand.
func comparePageRenders(in, out []*image.RGBA) []PageFidelity {
	summaries := func(imgs []*image.RGBA) []pageSummary {
		out := make([]pageSummary, len(imgs))
		for i, img := range imgs {
			out[i] = summarize(img)
		}
		return out
	}
	return comparePageSummaries(summaries(in), summaries(out))
}

// cellMeans divides img into a fidelityGrid x fidelityGrid grid and returns
// each cell's mean luminance. A mean, not a sample: text darkens a cell a
// little wherever it falls in it, and something painted over the cell darkens
// all of it, which is the difference this has to see.
func cellMeans(img *image.RGBA) [fidelityGrid * fidelityGrid]float64 {
	var means [fidelityGrid * fidelityGrid]float64
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 {
		return means
	}
	var counts [fidelityGrid * fidelityGrid]int
	for y := range h {
		row := y * fidelityGrid / h
		for x := range w {
			cell := row*fidelityGrid + x*fidelityGrid/w
			off := img.PixOffset(b.Min.X+x, b.Min.Y+y)
			r, g, bl := img.Pix[off], img.Pix[off+1], img.Pix[off+2]
			means[cell] += 0.299*float64(r) + 0.587*float64(g) + 0.114*float64(bl)
			counts[cell]++
		}
	}
	for i := range means {
		if counts[i] > 0 {
			means[i] /= float64(counts[i])
		}
	}
	return means
}

// coveredFraction is how much of the page went from paper to solid: the share
// of cells that were light before and are dark after.
func coveredFraction(before, after [fidelityGrid * fidelityGrid]float64) float64 {
	covered := 0
	for i := range before {
		if before[i] > lightCell && after[i] < darkCell {
			covered++
		}
	}
	return float64(covered) / float64(len(before))
}

// summarizeReaderPages resolves r's graph and measures every page in order.
func summarizeReaderPages(r *pdf.Reader, dpi int) ([]pageSummary, error) {
	graph, err := r.ResolveGraph()
	if err != nil {
		return nil, err
	}
	trailer, ok := graph.(pdf.PDFDict)
	if !ok {
		return nil, fmt.Errorf("resolved graph is not a dictionary")
	}
	return summarizeTrailerPages(trailer, dpi), nil
}

// summarizeTrailerPages rasterizes every page of an already-resolved trailer
// in order and keeps only the measurements, so one page is in memory at a time
// rather than a whole document twice over. A page that fails to render is left
// unrendered rather than aborting.
func summarizeTrailerPages(trailer pdf.PDFDict, dpi int) []pageSummary {
	pages := orderedPages(trailer)
	out := make([]pageSummary, len(pages))
	for i, p := range pages {
		if img, _, err := RenderPage(p.dict, p.resources, p.mediaBox, dpi); err == nil {
			out[i] = summarize(img)
		}
	}
	return out
}

// inkFraction returns the fraction of pixels that are meaningfully non-white,
// a proxy for how much visible content a page carries.
func inkFraction(img *image.RGBA) float64 {
	if img == nil {
		return 0
	}
	b := img.Bounds()
	total := b.Dx() * b.Dy()
	if total == 0 {
		return 0
	}
	ink := 0
	pix := img.Pix
	for i := 0; i+3 < len(pix); i += 4 {
		// A pixel counts as ink if any channel is clearly below white.
		if pix[i] < 245 || pix[i+1] < 245 || pix[i+2] < 245 {
			ink++
		}
	}
	return float64(ink) / float64(total)
}

// pageSimilarity samples both renders on a fixed grid (so differing pixel
// dimensions are handled) and returns 1 minus the mean per-cell grayscale
// difference, normalized to [0,1].
func pageSimilarity(a, b *image.RGBA) float64 {
	return gridSimilarity(grayGrid(a), grayGrid(b))
}

// gridSimilarity is pageSimilarity over samples already taken.
func gridSimilarity(ga, gb [fidelityGrid * fidelityGrid]float64) float64 {
	var sum float64
	for i := range ga {
		d := ga[i] - gb[i]
		if d < 0 {
			d = -d
		}
		sum += d
	}
	mean := sum / float64(len(ga))
	return 1 - mean/255
}

// grayGrid samples img at fidelityGrid x fidelityGrid evenly-spaced points and
// returns their luminance (0 black, 255 white).
func grayGrid(img *image.RGBA) [fidelityGrid * fidelityGrid]float64 {
	var g [fidelityGrid * fidelityGrid]float64
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 {
		return g
	}
	for row := 0; row < fidelityGrid; row++ {
		y := b.Min.Y + row*h/fidelityGrid
		for col := 0; col < fidelityGrid; col++ {
			x := b.Min.X + col*w/fidelityGrid
			off := img.PixOffset(x, y)
			r, gg, bb := img.Pix[off], img.Pix[off+1], img.Pix[off+2]
			g[row*fidelityGrid+col] = 0.299*float64(r) + 0.587*float64(gg) + 0.114*float64(bb)
		}
	}
	return g
}
