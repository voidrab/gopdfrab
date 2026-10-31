package fidelity

import (
	"image"

	"github.com/voidrab/gopdfrab/internal/content"
	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/raster"
)

// fidelity tuning. The comparison runs at a low DPI: gross change and blanking
// are what matter, not sub-pixel accuracy.
const (
	// DPI is the resolution the gate renders both sides at.
	DPI           = 72
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
// opaque, such as setting a zero opacity to 1.
func (f PageFidelity) Overpainted() bool {
	return f.Covered >= overpaintArea
}

// BlankedPages returns the pages of report that came out blank, in ascending
// order. Compare already walks the pages in order, so this only filters. Nil
// when nothing was lost, so a caller can test it with len.
func BlankedPages(report []PageFidelity) []int {
	return pagesWhere(report, PageFidelity.Blanked)
}

// OverpaintedPages is BlankedPages for the pages something was painted over.
func OverpaintedPages(report []PageFidelity) []int {
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

// Summary holds the measurements of one rendered page that a comparison
// needs: its ink fraction, the mean luminance of each cell of the comparison
// grid, and the grayscale samples the similarity score is taken from. A Summary
// is about 36 KiB, far smaller than the page it was measured from, so a
// comparison holds summaries rather than renders.
type Summary struct {
	rendered bool
	ink      float64
	cells    [fidelityGrid * fidelityGrid]float64
	gray     [fidelityGrid * fidelityGrid]float64
}

// summarize measures a rendered page and lets go of it.
func summarize(img *image.RGBA) Summary {
	if img == nil {
		return Summary{}
	}
	return Summary{rendered: true, ink: inkFraction(img), cells: cellMeans(img), gray: grayGrid(img)}
}

// Compare builds the per-page report from two lists of measured pages. An
// input page that did not render is skipped, since it has no baseline. An
// output page that did not render gets no output ink and zero similarity, so
// an inked input page is reported as blanked.
func Compare(in, out []Summary) []PageFidelity {
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

// Summarize rasterizes every page of a resolved trailer in page order, the
// order the verifier numbers pages in, and keeps only the measurements, so one
// page is in memory at a time rather than a whole document twice over. A page
// that fails to render is left unrendered rather than aborting.
func Summarize(trailer pdf.PDFDict, dpi int) []Summary {
	root, _ := trailer.Entries.Get("Root").(pdf.PDFDict)
	pages, ok := root.Entries.Get("Pages").(pdf.PDFDict)
	if !ok {
		return nil
	}
	// The input is measured as a viewer shows it, with the layers it turns
	// off hidden; the conversion removes their content.
	oc := content.NewOptionalContent(root)
	var out []Summary
	pdf.WalkPages(pages, func(page, resources pdf.PDFDict, mediaBox [4]float64, _ int) {
		var s Summary
		if img, _, err := raster.RenderPage(page, resources, mediaBox, dpi, oc); err == nil {
			s = summarize(img)
		}
		out = append(out, s)
	})
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

// gridSimilarity compares two grids of samples taken by grayGrid. Because
// grayGrid samples every render on the same fixed grid, differing pixel
// dimensions do not matter. It returns 1 minus the mean per-cell grayscale
// difference, normalized to [0,1].
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
