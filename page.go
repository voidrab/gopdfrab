package gopdfrab

import (
	"context"
	"errors"
	"fmt"
	"image"
	"math"

	"github.com/voidrab/gopdfrab/internal/content"
	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/raster"
)

// ErrPageRange is returned by Document.Page for an index outside the document.
var ErrPageRange = errors.New("gopdfrab: page index out of range")

// pageEntry is one page of the document with the attributes it inherits.
type pageEntry struct {
	dict      pdf.PDFDict
	resources pdf.PDFDict
}

// loadPages resolves the page tree once. The caller holds d.mu.
func (d *Document) loadPages() error {
	if d.index {
		return nil
	}
	resolve := d.r.ResolveGraph
	if d.resolveGraph != nil {
		resolve = d.resolveGraph
	}
	graph, err := resolve()
	if err != nil {
		return err
	}
	trailer, _ := graph.(pdf.PDFDict)
	root, _ := trailer.Entries.Get("Root").(pdf.PDFDict)
	if tree, ok := root.Entries.Get("Pages").(pdf.PDFDict); ok {
		pdf.WalkPages(tree, func(page, res pdf.PDFDict, _ [4]float64, _ int) {
			d.pages = append(d.pages, pageEntry{dict: page, resources: res})
		})
	}
	d.root = root
	d.oc = content.NewOptionalContent(root)
	d.index = true
	return nil
}

// Pages returns the number of pages found by walking the page tree. Unlike
// PageCount it does not trust the /Count entry.
func (d *Document) Pages() (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.loadPages(); err != nil {
		return 0, err
	}
	return len(d.pages), nil
}

// Page returns the page at the zero-based index i, or ErrPageRange. The first
// call walks the page tree. The returned Page is valid until the document is
// closed.
func (d *Document) Page(i int) (*Page, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.loadPages(); err != nil {
		return nil, err
	}
	if i < 0 || i >= len(d.pages) {
		return nil, fmt.Errorf("%w: %d of %d", ErrPageRange, i, len(d.pages))
	}
	e := d.pages[i]
	g := pdf.Geometry(e.dict)
	return &Page{doc: d, index: i, entry: e, geom: g}, nil
}

// Page is one page of a Document. Its methods may be called from several
// goroutines; they serialize on the document.
type Page struct {
	doc   *Document
	index int
	entry pageEntry
	geom  pdf.PageGeometry
}

// Index returns the page's zero-based position in the document.
func (p *Page) Index() int { return p.index }

// Rotation returns the page's clockwise display rotation in degrees: 0, 90,
// 180 or 270.
func (p *Page) Rotation() int { return p.geom.Rotate }

// Size returns the displayed size in points (1/72 inch): the crop box scaled
// by the user unit and swapped for a quarter-turn rotation.
func (p *Page) Size() (width, height float64) {
	w := (p.geom.CropBox[2] - p.geom.CropBox[0]) * p.geom.UserUnit
	h := (p.geom.CropBox[3] - p.geom.CropBox[1]) * p.geom.UserUnit
	if p.geom.Rotate%180 != 0 {
		return h, w
	}
	return w, h
}

// RenderOptions controls Page.Render.
type RenderOptions struct {
	// Scale is device pixels per point: 1 is 72 dpi. Zero means 1. The
	// resolution is rounded to whole dpi, and very large pages are rendered
	// at a lower resolution to bound memory.
	Scale float64
	// Region, if not empty, limits the render to that part of the displayed
	// page, in pixels at Scale counted from the top-left corner and cut to the
	// page. The result is as large as the region. Its pixels equal those of
	// the same part of a whole-page render, but Scale is not lowered to fit the
	// pixel budget, so the cost is in proportion to the region.
	Region image.Rectangle
}

// Render rasterizes the page, rotated for display, onto an opaque white
// background. It returns ctx.Err() if ctx is done before or after the render;
// a render in progress stops soon after ctx is done.
func (p *Page) Render(ctx context.Context, opt RenderOptions) (*image.RGBA, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	scale := opt.Scale
	if scale == 0 {
		scale = 1
	}
	if scale < 0 || math.IsNaN(scale) || math.IsInf(scale, 0) {
		return nil, fmt.Errorf("gopdfrab: invalid render scale %v", scale)
	}
	dpi := max(1, int(math.Round(72*scale*p.geom.UserUnit)))

	p.doc.mu.Lock()
	var img *image.RGBA
	var err error
	if opt.Region.Empty() {
		img, _, err = raster.RenderPageContext(ctx, p.entry.dict, p.entry.resources, p.geom.CropBox, dpi, p.doc.oc, p.doc.renderCache())
	} else {
		w, h := raster.PixelSize(p.geom.CropBox, dpi)
		img, _, err = raster.RenderPageRegion(ctx, p.entry.dict, p.entry.resources, p.geom.CropBox, dpi, unrotateRegion(opt.Region, w, h, p.geom.Rotate), p.doc.oc, p.doc.renderCache())
	}
	p.doc.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return rotateClockwise(img, p.geom.Rotate), nil
}

// unrotateRegion maps region, in pixels of the page displayed rotated by deg
// clockwise, to the pixels of the unrotated page, which is w by h. It is the
// inverse of the pixel movement in rotateClockwise. The region is first cut to
// the displayed page, so a region that misses it comes out empty.
func unrotateRegion(region image.Rectangle, w, h, deg int) image.Rectangle {
	switch deg {
	case 90:
		region = region.Intersect(image.Rect(0, 0, h, w))
		return image.Rect(region.Min.Y, h-region.Max.X, region.Max.Y, h-region.Min.X)
	case 180:
		region = region.Intersect(image.Rect(0, 0, w, h))
		return image.Rect(w-region.Max.X, h-region.Max.Y, w-region.Min.X, h-region.Min.Y)
	case 270:
		region = region.Intersect(image.Rect(0, 0, h, w))
		return image.Rect(w-region.Max.Y, region.Min.X, w-region.Min.Y, region.Max.X)
	}
	return region
}

// rotateClockwise returns img turned by deg (0, 90, 180 or 270) clockwise.
func rotateClockwise(img *image.RGBA, deg int) *image.RGBA {
	if deg == 0 {
		return img
	}
	w, h := img.Rect.Dx(), img.Rect.Dy()
	ow, oh := w, h
	if deg != 180 {
		ow, oh = h, w
	}
	out := image.NewRGBA(image.Rect(0, 0, ow, oh))
	for y := range h {
		for x := range w {
			var dx, dy int
			switch deg {
			case 90:
				dx, dy = h-1-y, x
			case 180:
				dx, dy = w-1-x, h-1-y
			default:
				dx, dy = y, w-1-x
			}
			copy(out.Pix[out.PixOffset(dx, dy):][:4], img.Pix[img.PixOffset(x, y):][:4])
		}
	}
	return out
}

// renderCacheBytes bounds the decoded images a document keeps between renders.
// A viewer holds one per open document, so it is sized for the images of a few
// pages, about five A4 scans at 150 dpi, not for the whole document.
const renderCacheBytes = 64 << 20

// renderCache returns the document's cache for renders, made on first use. The
// caller holds d.mu.
func (d *Document) renderCache() *raster.Cache {
	if d.cache == nil {
		d.cache = raster.NewCache(renderCacheBytes)
	}
	return d.cache
}

// Rect is a rectangle on a displayed page, in points from its top-left corner,
// with X0 <= X1 and Y0 <= Y1.
type Rect struct{ X0, Y0, X1, Y1 float64 }

// Glyph is one character of text on a page.
type Glyph struct {
	// Rune is the character, or zero when the page's font does not say what
	// it is.
	Rune rune
	// Box is the glyph's cell on the displayed page: as wide as its advance
	// and as tall as the em square from descender to ascender.
	Box Rect
}

// Glyphs returns the text of the page, one Glyph for each character shown, in
// the order the page's content shows them, which is usually reading order but
// is not promised to be. Where the page has no text, or only text in a font
// that does not map to characters, the runes are zero. The boxes are in the
// page's displayed orientation, like Size.
func (p *Page) Glyphs(ctx context.Context) ([]Glyph, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.doc.mu.Lock()
	raw, err := raster.ExtractText(ctx, p.entry.dict, p.entry.resources, p.geom.CropBox, p.doc.oc, p.doc.renderCache())
	p.doc.mu.Unlock()
	if err != nil {
		return nil, err
	}
	w, h := p.geom.CropBox[2]-p.geom.CropBox[0], p.geom.CropBox[3]-p.geom.CropBox[1]
	out := make([]Glyph, len(raw))
	for i, g := range raw {
		out[i] = Glyph{Rune: g.R, Box: displayRect(g.Box, w, h, p.geom.Rotate, p.geom.UserUnit)}
	}
	return out, nil
}

// displayRect maps box, in points of the unrotated crop box of size w by h, to
// the page displayed rotated by deg clockwise and scaled by unit.
func displayRect(box [4]float64, w, h float64, deg int, unit float64) Rect {
	x0, y0, x1, y1 := box[0], box[1], box[2], box[3]
	var r Rect
	switch deg {
	case 90:
		r = Rect{h - y1, x0, h - y0, x1}
	case 180:
		r = Rect{w - x1, h - y1, w - x0, h - y0}
	case 270:
		r = Rect{y0, w - x1, y1, w - x0}
	default:
		r = Rect{x0, y0, x1, y1}
	}
	return Rect{r.X0 * unit, r.Y0 * unit, r.X1 * unit, r.Y1 * unit}
}
