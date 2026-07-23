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
)

// PageFidelity reports how a converted page's rendering compares to the input's.
// Similarity is 1.0 for identical renders and approaches 0.0 as they diverge.
// InputInk and OutputInk are the fraction of non-white pixels on each side.
type PageFidelity struct {
	Page       int     // 1-based, matching PDFError.Page()
	Similarity float64 // 1.0 identical, 0.0 maximally different
	InputInk   float64 // fraction of non-white pixels in the input render
	OutputInk  float64 // fraction of non-white pixels in the output render
}

// Blanked reports whether the page lost essentially all of its visible content:
// the input had ink and the output is nearly empty. This is unambiguous data
// loss, and unlike a raw similarity threshold it does not fire on legitimate
// changes such as font substitution.
func (f PageFidelity) Blanked() bool {
	return f.InputInk >= inkThreshold && f.OutputInk < f.InputInk*blankLossRate
}

// CompareFidelity renders input and output at dpi and returns a per-page
// fidelity report for every page present in both. Pages the rasterizer cannot
// draw on the input side (nil render) are skipped, since there is no baseline
// to judge against.
func CompareFidelity(input, output *pdf.Reader, dpi int) ([]PageFidelity, error) {
	in, err := renderReaderPages(input, dpi)
	if err != nil {
		return nil, fmt.Errorf("fidelity: render input: %w", err)
	}
	out, err := renderReaderPages(output, dpi)
	if err != nil {
		return nil, fmt.Errorf("fidelity: render output: %w", err)
	}
	return comparePageRenders(in, out), nil
}

// comparePageRenders builds the per-page report from two already-rendered page
// lists. A nil input render is skipped (no baseline); a nil output render with
// an inked input counts as fully lost.
func comparePageRenders(in, out []*image.RGBA) []PageFidelity {
	n := min(len(in), len(out))
	var report []PageFidelity
	for i := range n {
		if in[i] == nil {
			continue
		}
		pf := PageFidelity{Page: i + 1, InputInk: inkFraction(in[i])}
		if out[i] == nil {
			pf.OutputInk, pf.Similarity = 0, 0
		} else {
			pf.OutputInk = inkFraction(out[i])
			pf.Similarity = pageSimilarity(in[i], out[i])
		}
		report = append(report, pf)
	}
	return report
}

// renderReaderPages resolves r's graph and rasterizes every page in order.
func renderReaderPages(r *pdf.Reader, dpi int) ([]*image.RGBA, error) {
	graph, err := r.ResolveGraph()
	if err != nil {
		return nil, err
	}
	trailer, ok := graph.(pdf.PDFDict)
	if !ok {
		return nil, fmt.Errorf("resolved graph is not a dictionary")
	}
	return renderTrailerPages(trailer, dpi), nil
}

// renderTrailerPages rasterizes every page of an already-resolved trailer in
// order. A page that fails to render is left nil rather than aborting.
func renderTrailerPages(trailer pdf.PDFDict, dpi int) []*image.RGBA {
	pages := orderedPages(trailer)
	imgs := make([]*image.RGBA, len(pages))
	for i, p := range pages {
		if img, _, err := RenderPage(p.dict, p.resources, p.mediaBox, dpi); err == nil {
			imgs[i] = img
		}
	}
	return imgs
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
	ga := grayGrid(a)
	gb := grayGrid(b)
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
